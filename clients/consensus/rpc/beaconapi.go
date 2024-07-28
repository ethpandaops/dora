package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	eth2client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
)

var logger = logrus.StandardLogger().WithField("module", "rpc")

type BeaconClient struct {
	name      string
	endpoint  string
	headers   map[string]string
	clientSvc eth2client.Service
}

// NewBeaconClient is used to create a new beacon client
func NewBeaconClient(name, url string, headers map[string]string) (*BeaconClient, error) {
	client := &BeaconClient{
		name:     name,
		endpoint: url,
		headers:  headers,
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
		// TODO (when upstream PR is merged)
		// http.WithConnectionCheck(false),
		http.WithCustomSpecSupport(true),
	}

	// set extra endpoint headers
	if bc.headers != nil && len(bc.headers) > 0 {
		cliParams = append(cliParams, http.WithExtraHeaders(bc.headers))
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
		logger.WithField("client", bc.name).Debugf("RPC Error %v: %v", resp.StatusCode, data)

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
	provider, isProvider := bc.clientSvc.(eth2client.SpecProvider)
	if !isProvider {
		return nil, fmt.Errorf("get specs not supported")
	}

	result, err := provider.Spec(ctx, &api.SpecOpts{
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	return result.Data, nil
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

func (bc *BeaconClient) GetState(ctx context.Context, stateRef string) (*spec.VersionedBeaconState, error) {
	provider, isProvider := bc.clientSvc.(eth2client.BeaconStateProvider)
	if !isProvider {
		return nil, fmt.Errorf("get beacon state not supported")
	}

	result, err := provider.BeaconState(ctx, &api.BeaconStateOpts{
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

func (bc *BeaconClient) GetStateValidators(ctx context.Context, stateRef string) (map[phase0.ValidatorIndex]*v1.Validator, error) {
	provider, isProvider := bc.clientSvc.(eth2client.ValidatorsProvider)
	if !isProvider {
		return nil, fmt.Errorf("get validators not supported")
	}

	result, err := provider.Validators(ctx, &api.ValidatorsOpts{
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

func (bc *BeaconClient) GetProposerDuties(ctx context.Context, epoch uint64) ([]*v1.ProposerDuty, error) {
	provider, isProvider := bc.clientSvc.(eth2client.ProposerDutiesProvider)
	if !isProvider {
		return nil, fmt.Errorf("get beacon committees not supported")
	}

	result, err := provider.ProposerDuties(ctx, &api.ProposerDutiesOpts{
		Epoch: phase0.Epoch(epoch),
		Common: api.CommonOpts{
			Timeout: 0,
		},
	})
	if err != nil {
		return nil, err
	}

	return result.Data, nil
}

func (bc *BeaconClient) GetCommitteeDuties(ctx context.Context, stateRef string, epoch uint64) ([]*v1.BeaconCommittee, error) {
	provider, isProvider := bc.clientSvc.(eth2client.BeaconCommitteesProvider)
	if !isProvider {
		return nil, fmt.Errorf("get beacon committees not supported")
	}

	epochRef := phase0.Epoch(epoch)

	result, err := provider.BeaconCommittees(ctx, &api.BeaconCommitteesOpts{
		State: stateRef,
		Epoch: &epochRef,
		Common: api.CommonOpts{
			Timeout: 0,
		},
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
