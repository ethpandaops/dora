package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	eth2client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/altair"
	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/fulu"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"

	"github.com/ethpandaops/dora/clients/sshtunnel"
	"github.com/ethpandaops/dora/utils"
)

type BeaconClient struct {
	name       string
	endpoint   string
	headers    map[string]string
	sshtunnel  *sshtunnel.SSHTunnel
	disableSSZ bool
	clientSvc  eth2client.Service
	logger     logrus.FieldLogger
}

// NewBeaconClient is used to create a new beacon client
func NewBeaconClient(name, endpoint string, headers map[string]string, sshcfg *sshtunnel.SshConfig, disableSSZ bool, logger logrus.FieldLogger) (*BeaconClient, error) {
	client := &BeaconClient{
		name:       name,
		endpoint:   endpoint,
		headers:    headers,
		disableSSZ: disableSSZ,
		logger:     logger,
	}

	if sshcfg != nil {
		// create ssh tunnel to remote host
		sshPort := 0
		if sshcfg.Port != "" {
			sshPort, _ = strconv.Atoi(sshcfg.Port)
		}
		if sshPort == 0 {
			sshPort = 22
		}
		sshEndpoint := fmt.Sprintf("%v@%v:%v", sshcfg.User, sshcfg.Host, sshPort)
		var sshAuth ssh.AuthMethod
		if sshcfg.Keyfile != "" {
			var err error
			sshAuth, err = sshtunnel.PrivateKeyFile(sshcfg.Keyfile)
			if err != nil {
				return nil, fmt.Errorf("could not load ssh keyfile: %w", err)
			}
		} else {
			sshAuth = ssh.Password(sshcfg.Password)
		}

		// get tunnel target from endpoint url
		endpointUrl, _ := url.Parse(endpoint)
		tunTarget := endpointUrl.Host
		if endpointUrl.Port() != "" {
			tunTarget = fmt.Sprintf("%v:%v", tunTarget, endpointUrl.Port())
		} else {
			tunTargetPort := 80
			if endpointUrl.Scheme == "https:" {
				tunTargetPort = 443
			}
			tunTarget = fmt.Sprintf("%v:%v", tunTarget, tunTargetPort)
		}

		client.sshtunnel = sshtunnel.NewSSHTunnel(sshEndpoint, sshAuth, tunTarget)
		client.sshtunnel.Log = logger.WithField("sshtun", sshcfg.Host)
		err := client.sshtunnel.Start()
		if err != nil {
			return nil, fmt.Errorf("could not start ssh tunnel: %w", err)
		}

		// override endpoint to use local tunnel end
		endpointUrl.Host = fmt.Sprintf("localhost:%v", client.sshtunnel.Local.Port)

		client.endpoint = endpointUrl.String()
	}

	return client, nil
}

func (bc *BeaconClient) Initialize(ctx context.Context) error {
	if bc.clientSvc != nil {
		return nil
	}

	cliParams := []http.Parameter{
		http.WithAddress(bc.endpoint),
		http.WithTimeout(10 * time.Minute),
		http.WithLogLevel(zerolog.Disabled),
		http.WithCustomSpecSupport(true),
	}

	// set extra endpoint headers
	if len(bc.headers) > 0 {
		cliParams = append(cliParams, http.WithExtraHeaders(bc.headers))
	}

	if bc.disableSSZ {
		cliParams = append(cliParams, http.WithEnforceJSON(true))
	}

	clientSvc, err := http.New(ctx, cliParams...)
	if err != nil {
		return err
	}

	bc.clientSvc = clientSvc

	return nil
}

func (bc *BeaconClient) getJSON(ctx context.Context, requrl string, returnValue interface{}) error {
	logurl := getRedactedURL(requrl)

	req, err := nethttp.NewRequestWithContext(ctx, "GET", requrl, nethttp.NoBody)
	if err != nil {
		return err
	}

	for headerKey, headerVal := range bc.headers {
		req.Header.Set(headerKey, headerVal)
	}

	client := &nethttp.Client{Timeout: time.Second * 300}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != nethttp.StatusOK {
		if resp.StatusCode == nethttp.StatusNotFound {
			return fmt.Errorf("not found")
		}

		data, _ := io.ReadAll(resp.Body)
		bc.logger.Debugf("RPC Error %v: %v", resp.StatusCode, data)

		return fmt.Errorf("url: %v, error-response: %s", logurl, data)
	}

	dec := json.NewDecoder(resp.Body)

	err = dec.Decode(&returnValue)
	if err != nil {
		return fmt.Errorf("error parsing json response: %v", err)
	}

	return nil
}

func (bc *BeaconClient) postJSON(ctx context.Context, requrl string, postData, returnValue interface{}) error {
	logurl := getRedactedURL(requrl)

	postDataBytes, err := json.Marshal(postData)
	if err != nil {
		return fmt.Errorf("error encoding json request: %v", err)
	}

	reader := bytes.NewReader(postDataBytes)
	req, err := nethttp.NewRequestWithContext(ctx, "POST", requrl, reader)

	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	for headerKey, headerVal := range bc.headers {
		req.Header.Set(headerKey, headerVal)
	}

	client := &nethttp.Client{Timeout: time.Second * 300}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != nethttp.StatusOK {
		if resp.StatusCode == nethttp.StatusNotFound {
			return fmt.Errorf("not found")
		}

		data, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("url: %v, error-response: %s", logurl, data)
	}

	if returnValue != nil {
		dec := json.NewDecoder(resp.Body)

		err = dec.Decode(&returnValue)
		if err != nil {
			return fmt.Errorf("error parsing json response: %v", err)
		}
	}

	return nil
}

func (bc *BeaconClient) GetGenesis(ctx context.Context) (*v1.Genesis, error) {
	provider, isProvider := bc.clientSvc.(eth2client.GenesisProvider)
	if !isProvider {
		return nil, fmt.Errorf("get genesis not supported")
	}

	result, err := provider.Genesis(ctx, &api.GenesisOpts{
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	return result.Data, nil
}

func (bc *BeaconClient) GetNodeSyncing(ctx context.Context) (*v1.SyncState, error) {
	provider, isProvider := bc.clientSvc.(eth2client.NodeSyncingProvider)
	if !isProvider {
		return nil, fmt.Errorf("get node syncing not supported")
	}

	result, err := provider.NodeSyncing(ctx, &api.NodeSyncingOpts{
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	return result.Data, nil
}

func (bc *BeaconClient) GetNodeSyncStatus(ctx context.Context) (*SyncStatus, error) {
	syncState, err := bc.GetNodeSyncing(ctx)
	if err != nil {
		return nil, err
	}

	syncStatus := NewSyncStatus(syncState)

	return &syncStatus, nil
}

type apiNodeVersion struct {
	Data struct {
		Version string `json:"version"`
	} `json:"data"`
}

func (bc *BeaconClient) GetNodeVersion(ctx context.Context) (string, error) {
	var nodeVersion apiNodeVersion

	err := bc.getJSON(ctx, fmt.Sprintf("%s/eth/v1/node/version", bc.endpoint), &nodeVersion)
	if err != nil {
		return "", fmt.Errorf("error retrieving node version: %v", err)
	}

	return nodeVersion.Data.Version, nil
}

func (bc *BeaconClient) GetConfigSpecs(ctx context.Context) (map[string]interface{}, error) {
	specs := map[string]interface{}{}

	err := bc.getJSON(ctx, fmt.Sprintf("%s/eth/v1/config/spec", bc.endpoint), &specs)
	if err != nil {
		return nil, fmt.Errorf("error retrieving specs: %v", err)
	}

	if specs["data"] == nil {
		return nil, fmt.Errorf("specs data is nil")
	}

	parsedSpecs := utils.ParseSpecMap(specs["data"].(map[string]interface{}))

	return parsedSpecs, nil
}

func (bc *BeaconClient) GetLatestBlockHead(ctx context.Context) (*v1.BeaconBlockHeader, error) {
	provider, isProvider := bc.clientSvc.(eth2client.BeaconBlockHeadersProvider)
	if !isProvider {
		return nil, fmt.Errorf("get beacon block headers not supported")
	}

	result, err := provider.BeaconBlockHeader(ctx, &api.BeaconBlockHeaderOpts{
		Block: "head",
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	return result.Data, nil
}

func (bc *BeaconClient) GetFinalityCheckpoints(ctx context.Context) (*v1.Finality, error) {
	provider, isProvider := bc.clientSvc.(eth2client.FinalityProvider)
	if !isProvider {
		return nil, fmt.Errorf("get finality not supported")
	}

	result, err := provider.Finality(ctx, &api.FinalityOpts{
		State: "head",
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	return result.Data, nil
}

func (bc *BeaconClient) GetBlockHeaderByBlockroot(ctx context.Context, blockroot phase0.Root) (*v1.BeaconBlockHeader, error) {
	provider, isProvider := bc.clientSvc.(eth2client.BeaconBlockHeadersProvider)
	if !isProvider {
		return nil, fmt.Errorf("get beacon block headers not supported")
	}

	result, err := provider.BeaconBlockHeader(ctx, &api.BeaconBlockHeaderOpts{
		Block: fmt.Sprintf("0x%x", blockroot),
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	return result.Data, nil
}

func (bc *BeaconClient) GetBlockHeaderBySlot(ctx context.Context, slot phase0.Slot) (*v1.BeaconBlockHeader, error) {
	provider, isProvider := bc.clientSvc.(eth2client.BeaconBlockHeadersProvider)
	if !isProvider {
		return nil, fmt.Errorf("get beacon block headers not supported")
	}

	result, err := provider.BeaconBlockHeader(ctx, &api.BeaconBlockHeaderOpts{
		Block: fmt.Sprintf("%d", slot),
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "GET failed with status 404") {
			return nil, nil
		}
		return nil, err
	}

	return result.Data, nil
}

func (bc *BeaconClient) GetBlockBodyByBlockroot(ctx context.Context, blockroot phase0.Root) (*spec.VersionedSignedBeaconBlock, error) {
	provider, isProvider := bc.clientSvc.(eth2client.SignedBeaconBlockProvider)
	if !isProvider {
		return nil, fmt.Errorf("get signed beacon block not supported")
	}

	result, err := provider.SignedBeaconBlock(ctx, &api.SignedBeaconBlockOpts{
		Block: fmt.Sprintf("0x%x", blockroot),
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "GET failed with status 404") {
			return nil, nil
		}

		return nil, err
	}

	return result.Data, nil
}

// GetState fetches a beacon state using streaming decoding to avoid buffering
// the full response (200MB+) in memory. Both SSZ and JSON responses are decoded
// directly from the HTTP response body.
func (bc *BeaconClient) GetState(ctx context.Context, stateRef string) (*spec.VersionedBeaconState, error) {
	reqURL := fmt.Sprintf("%s/eth/v2/debug/beacon/states/%s", bc.endpoint, stateRef)

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create beacon state request: %w", err)
	}

	for headerKey, headerVal := range bc.headers {
		req.Header.Set(headerKey, headerVal)
	}

	if bc.disableSSZ {
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Accept", "application/octet-stream;q=1,application/json;q=0.9")
	}

	client := &nethttp.Client{Timeout: 10 * time.Minute}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request beacon state: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != nethttp.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to load beacon state (status %s): %s", resp.Status, string(data))
	}

	// Parse consensus version from Eth-Consensus-Version header.
	consensusVersion, err := parseConsensusVersionHeader(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse consensus version: %w", err)
	}

	state := &spec.VersionedBeaconState{
		Version: consensusVersion,
	}

	// Determine content type from response header.
	contentType := resp.Header.Get("Content-Type")
	isSSZ := strings.HasPrefix(contentType, "application/octet-stream")

	if isSSZ {
		err = bc.streamDecodeStateSSZ(ctx, resp, state)
	} else {
		err = streamDecodeStateJSON(resp.Body, state)
	}

	if err != nil {
		return nil, err
	}

	return state, nil
}

// parseConsensusVersionHeader extracts the consensus version from the
// Eth-Consensus-Version HTTP response header.
func parseConsensusVersionHeader(resp *nethttp.Response) (spec.DataVersion, error) {
	versionHeader := resp.Header.Get("Eth-Consensus-Version")
	if versionHeader == "" {
		return spec.DataVersionUnknown, fmt.Errorf("missing Eth-Consensus-Version header")
	}

	version, err := spec.DataVersionFromString(versionHeader)
	if err != nil {
		return spec.DataVersionUnknown, fmt.Errorf("unrecognized consensus version %q: %w", versionHeader, err)
	}

	return version, nil
}

// streamDecodeStateSSZ decodes a beacon state from an SSZ-encoded HTTP response
// body using streaming deserialization via dynamic-ssz.
func (bc *BeaconClient) streamDecodeStateSSZ(ctx context.Context, resp *nethttp.Response, state *spec.VersionedBeaconState) error {
	// Fetch spec data for dynamic SSZ decoding.
	specProvider, isProvider := bc.clientSvc.(eth2client.SpecProvider)
	if !isProvider {
		return fmt.Errorf("spec provider not available for dynamic SSZ decoding")
	}

	specs, err := specProvider.Spec(ctx, &api.SpecOpts{})
	if err != nil {
		return fmt.Errorf("failed to fetch specs for SSZ decoding: %w", err)
	}

	ds := dynssz.NewDynSsz(specs.Data, dynssz.WithLogCb(func(format string, args ...any) {
		bc.logger.Infof(format, args...)
	}), dynssz.WithVerbose())
	sszSize := int(resp.ContentLength) // -1 if unknown

	switch state.Version {
	case spec.DataVersionPhase0:
		state.Phase0 = &phase0.BeaconState{}
		err = ds.UnmarshalSSZReader(state.Phase0, resp.Body, sszSize)
	case spec.DataVersionAltair:
		state.Altair = &altair.BeaconState{}
		err = ds.UnmarshalSSZReader(state.Altair, resp.Body, sszSize)
	case spec.DataVersionBellatrix:
		state.Bellatrix = &bellatrix.BeaconState{}
		err = ds.UnmarshalSSZReader(state.Bellatrix, resp.Body, sszSize)
	case spec.DataVersionCapella:
		state.Capella = &capella.BeaconState{}
		err = ds.UnmarshalSSZReader(state.Capella, resp.Body, sszSize)
	case spec.DataVersionDeneb:
		state.Deneb = &deneb.BeaconState{}
		err = ds.UnmarshalSSZReader(state.Deneb, resp.Body, sszSize)
	case spec.DataVersionElectra:
		state.Electra = &electra.BeaconState{}
		err = ds.UnmarshalSSZReader(state.Electra, resp.Body, sszSize)
	case spec.DataVersionFulu:
		state.Fulu = &fulu.BeaconState{}
		err = ds.UnmarshalSSZReader(state.Fulu, resp.Body, sszSize)
	default:
		return fmt.Errorf("unsupported SSZ state version: %s", state.Version)
	}

	if err != nil {
		return fmt.Errorf("failed to decode %s beacon state from SSZ: %w", state.Version, err)
	}

	return nil
}

// streamDecodeStateJSON decodes a beacon state from a JSON-encoded HTTP response
// body using streaming JSON decoding. The response has the standard beacon API
// envelope: {"version":"...","data":{...}}.
// The decoder streams from the reader and populates the target struct directly,
// avoiding an intermediate raw-bytes buffer for the data field.
func streamDecodeStateJSON(body io.Reader, state *spec.VersionedBeaconState) error {
	var err error

	switch state.Version {
	case spec.DataVersionPhase0:
		state.Phase0 = &phase0.BeaconState{}
		err = decodeJSONEnvelope(body, state.Phase0)
	case spec.DataVersionAltair:
		state.Altair = &altair.BeaconState{}
		err = decodeJSONEnvelope(body, state.Altair)
	case spec.DataVersionBellatrix:
		state.Bellatrix = &bellatrix.BeaconState{}
		err = decodeJSONEnvelope(body, state.Bellatrix)
	case spec.DataVersionCapella:
		state.Capella = &capella.BeaconState{}
		err = decodeJSONEnvelope(body, state.Capella)
	case spec.DataVersionDeneb:
		state.Deneb = &deneb.BeaconState{}
		err = decodeJSONEnvelope(body, state.Deneb)
	case spec.DataVersionElectra:
		state.Electra = &electra.BeaconState{}
		err = decodeJSONEnvelope(body, state.Electra)
	case spec.DataVersionFulu:
		state.Fulu = &fulu.BeaconState{}
		err = decodeJSONEnvelope(body, state.Fulu)
	default:
		return fmt.Errorf("unsupported JSON state version: %s", state.Version)
	}

	if err != nil {
		return fmt.Errorf("failed to decode %s beacon state from JSON: %w", state.Version, err)
	}

	return nil
}

// decodeJSONEnvelope decodes a beacon API JSON envelope {"data": ...} directly
// into the target struct via streaming decoder, avoiding buffering the raw JSON.
func decodeJSONEnvelope[T any](body io.Reader, target T) error {
	envelope := struct {
		Data T `json:"data"`
	}{
		Data: target,
	}

	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		return fmt.Errorf("failed to decode beacon state JSON: %w", err)
	}

	return nil
}

func (bc *BeaconClient) GetBlobSidecarsByBlockroot(ctx context.Context, blockroot []byte) ([]*deneb.BlobSidecar, error) {
	provider, isProvider := bc.clientSvc.(eth2client.BlobSidecarsProvider)
	if !isProvider {
		return nil, fmt.Errorf("get beacon block blobs not supported")
	}
	result, err := provider.BlobSidecars(ctx, &api.BlobSidecarsOpts{
		Block: fmt.Sprintf("0x%x", blockroot),
	})
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (bc *BeaconClient) GetBlobsByBlockroot(ctx context.Context, blockroot []byte) ([]*deneb.Blob, error) {
	provider, isProvider := bc.clientSvc.(eth2client.BlobsProvider)
	if !isProvider {
		return nil, fmt.Errorf("get beacon block blobs not supported")
	}
	result, err := provider.Blobs(ctx, &api.BlobsOpts{
		Block: fmt.Sprintf("0x%x", blockroot),
	})
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (bc *BeaconClient) GetForkState(ctx context.Context, stateRef string) (*phase0.Fork, error) {
	provider, isProvider := bc.clientSvc.(eth2client.ForkProvider)
	if !isProvider {
		return nil, fmt.Errorf("get fork not supported")
	}

	result, err := provider.Fork(ctx, &api.ForkOpts{
		State: stateRef,
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	return result.Data, nil
}

func (bc *BeaconClient) GetNodePeers(ctx context.Context) ([]*v1.Peer, error) {
	provider, isProvider := bc.clientSvc.(eth2client.NodePeersProvider)
	if !isProvider {
		return nil, fmt.Errorf("get peers not supported")
	}
	result, err := provider.NodePeers(ctx, &api.NodePeersOpts{State: []string{"connected"}})
	if err != nil {
		return nil, err
	}

	// Temporary workaround to filter out peers that are not connected (https://github.com/grandinetech/grandine/issues/46)
	filteredPeers := make([]*v1.Peer, 0)
	for _, peer := range result.Data {
		if peer.State == "connected" {
			filteredPeers = append(filteredPeers, peer)
		}
	}

	return filteredPeers, nil
}

func (bc *BeaconClient) GetNodeIdentity(ctx context.Context) (*NodeIdentity, error) {
	response := struct {
		Data *NodeIdentity `json:"data"`
	}{}

	err := bc.getJSON(ctx, fmt.Sprintf("%s/eth/v1/node/identity", bc.endpoint), &response)
	if err != nil {
		return nil, fmt.Errorf("error retrieving node identity: %v", err)
	}
	return response.Data, nil
}

func (bc *BeaconClient) SubmitBLSToExecutionChanges(ctx context.Context, blsChanges []*capella.SignedBLSToExecutionChange) error {
	submitter, isOk := bc.clientSvc.(eth2client.BLSToExecutionChangesSubmitter)
	if !isOk {
		return fmt.Errorf("submit bls to execution changes not supported")
	}

	err := submitter.SubmitBLSToExecutionChanges(ctx, blsChanges)
	if err != nil {
		return err
	}

	return nil
}

func (bc *BeaconClient) SubmitVoluntaryExits(ctx context.Context, exit *phase0.SignedVoluntaryExit) error {
	submitter, isOk := bc.clientSvc.(eth2client.VoluntaryExitSubmitter)
	if !isOk {
		return fmt.Errorf("submit voluntary exit not supported")
	}

	err := submitter.SubmitVoluntaryExit(ctx, exit)
	if err != nil {
		return err
	}

	return nil
}

func (bc *BeaconClient) SubmitAttesterSlashing(ctx context.Context, slashing *phase0.AttesterSlashing) error {
	err := bc.postJSON(ctx, fmt.Sprintf("%s/eth/v1/beacon/pool/attester_slashings", bc.endpoint), slashing, nil)
	if err != nil {
		return err
	}

	return nil
}

func (bc *BeaconClient) SubmitProposerSlashing(ctx context.Context, slashing *phase0.ProposerSlashing) error {
	err := bc.postJSON(ctx, fmt.Sprintf("%s/eth/v1/beacon/pool/proposer_slashings", bc.endpoint), slashing, nil)
	if err != nil {
		return err
	}

	return nil
}
