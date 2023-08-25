package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

var logger = logrus.StandardLogger().WithField("module", "rpc")

type BeaconClient struct {
	name     string
	endpoint string
	headers  map[string]string
}

// NewBeaconClient is used to create a new beacon client
func NewBeaconClient(endpoint string, name string, headers map[string]string) (*BeaconClient, error) {
	client := &BeaconClient{
		name:     name,
		endpoint: endpoint,
		headers:  headers,
	}

	return client, nil
}

var errNotFound = errors.New("not found 404")

func (bc *BeaconClient) get(requrl string) ([]byte, error) {
	logurl := utils.GetRedactedUrl(requrl)
	t0 := time.Now()
	defer func() {
		logger.WithField("client", bc.name).Debugf("RPC GET call (byte): %v [%v ms]", logurl, time.Since(t0).Milliseconds())
	}()

	req, err := http.NewRequest("GET", requrl, nil)
	if err != nil {
		return nil, err
	}
	for headerKey, headerVal := range bc.headers {
		req.Header.Set(headerKey, headerVal)
	}

	client := &http.Client{Timeout: time.Second * 120}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, errNotFound
		}
		return nil, fmt.Errorf("url: %v, error-response: %s", logurl, data)
	}

	return data, err
}

func (bc *BeaconClient) getJson(requrl string, returnValue interface{}) error {
	logurl := utils.GetRedactedUrl(requrl)
	t0 := time.Now()
	defer func() {
		logger.WithField("client", bc.name).Debugf("RPC GET call (json): %v [%v ms]", logurl, time.Since(t0).Milliseconds())
	}()

	req, err := http.NewRequest("GET", requrl, nil)
	if err != nil {
		return err
	}
	for headerKey, headerVal := range bc.headers {
		req.Header.Set(headerKey, headerVal)
	}

	client := &http.Client{Timeout: time.Second * 120}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return errNotFound
		}
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("url: %v, error-response: %s", logurl, data)
	}

	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&returnValue)
	if err != nil {
		return fmt.Errorf("error parsing json response: %v", err)
	}

	return nil
}

func (bc *BeaconClient) postJson(requrl string, postData interface{}, returnValue interface{}) error {
	logurl := utils.GetRedactedUrl(requrl)
	t0 := time.Now()
	defer func() {
		logger.WithField("client", bc.name).Debugf("RPC POST call (json): %v [%v ms]", logurl, time.Since(t0).Milliseconds())
	}()

	postDataBytes, err := json.Marshal(postData)
	if err != nil {
		return fmt.Errorf("error encoding json request: %v", err)
	}
	reader := bytes.NewReader(postDataBytes)
	req, err := http.NewRequest("POST", requrl, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for headerKey, headerVal := range bc.headers {
		req.Header.Set(headerKey, headerVal)
	}

	client := &http.Client{Timeout: time.Second * 120}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return errNotFound
		}
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("url: %v, error-response: %s", logurl, data)
	}

	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&returnValue)
	if err != nil {
		return fmt.Errorf("error parsing json response: %v", err)
	}

	return nil
}

func (bc *BeaconClient) GetGenesis() (*rpctypes.StandardV1GenesisResponse, error) {
	resGenesis, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/genesis", bc.endpoint))
	if err != nil {
		if err == errNotFound {
			// no block found
			return nil, nil
		}
		return nil, fmt.Errorf("error retrieving genesis: %v", err)
	}

	var parsedGenesis rpctypes.StandardV1GenesisResponse
	err = json.Unmarshal(resGenesis, &parsedGenesis)
	if err != nil {
		return nil, fmt.Errorf("error parsing genesis response: %v", err)
	}
	return &parsedGenesis, nil
}

func (bc *BeaconClient) GetNodeSyncing() (*rpctypes.StandardV1NodeSyncingResponse, error) {
	resGenesis, err := bc.get(fmt.Sprintf("%s/eth/v1/node/syncing", bc.endpoint))
	if err != nil {
		if err == errNotFound {
			// no block found
			return nil, nil
		}
		return nil, fmt.Errorf("error retrieving syncing status: %v", err)
	}

	var parsedSyncingStatus rpctypes.StandardV1NodeSyncingResponse
	err = json.Unmarshal(resGenesis, &parsedSyncingStatus)
	if err != nil {
		return nil, fmt.Errorf("error parsing syncing status response: %v", err)
	}
	return &parsedSyncingStatus, nil
}

func (bc *BeaconClient) GetNodeVersion() (*rpctypes.StandardV1NodeVersionResponse, error) {
	var parsedRsp rpctypes.StandardV1NodeVersionResponse
	err := bc.getJson(fmt.Sprintf("%s/eth/v1/node/version", bc.endpoint), &parsedRsp)
	if err != nil {
		if err == errNotFound {
			// no block found
			return nil, nil
		}
		return nil, fmt.Errorf("error retrieving node version: %v", err)
	}
	return &parsedRsp, nil
}

func (bc *BeaconClient) GetLatestBlockHead() (*rpctypes.StandardV1BeaconHeaderResponse, error) {
	resHeaders, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/headers/head", bc.endpoint))
	if err != nil {
		if err == errNotFound {
			// no block found
			return nil, nil
		}
		return nil, fmt.Errorf("error retrieving latest block header: %v", err)
	}

	var parsedHeaders rpctypes.StandardV1BeaconHeaderResponse
	err = json.Unmarshal(resHeaders, &parsedHeaders)
	if err != nil {
		return nil, fmt.Errorf("error parsing response for latest block header: %v", err)
	}
	return &parsedHeaders, nil
}

func (bc *BeaconClient) GetFinalizedBlockHead() (*rpctypes.StandardV1BeaconHeaderResponse, error) {
	resHeaders, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/headers/finalized", bc.endpoint))
	if err != nil {
		if err == errNotFound {
			// no block found
			return nil, nil
		}
		return nil, fmt.Errorf("error retrieving finalized block header: %v", err)
	}

	var parsedHeaders rpctypes.StandardV1BeaconHeaderResponse
	err = json.Unmarshal(resHeaders, &parsedHeaders)
	if err != nil {
		return nil, fmt.Errorf("error parsing header-response for finalized block: %v", err)
	}
	return &parsedHeaders, nil
}

func (bc *BeaconClient) GetBlockHeaderByBlockroot(blockroot []byte) (*rpctypes.StandardV1BeaconHeaderResponse, error) {
	resHeaders, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/headers/0x%x", bc.endpoint, blockroot))
	if err != nil {
		if err == errNotFound {
			// no block found
			return nil, nil
		}
		return nil, fmt.Errorf("error retrieving headers for blockroot 0x%x: %v", blockroot, err)
	}

	var parsedHeaders rpctypes.StandardV1BeaconHeaderResponse
	err = json.Unmarshal(resHeaders, &parsedHeaders)
	if err != nil {
		return nil, fmt.Errorf("error parsing header-response for blockroot 0x%x: %v", blockroot, err)
	}
	return &parsedHeaders, nil
}

func (bc *BeaconClient) GetBlockHeaderBySlot(slot uint64) (*rpctypes.StandardV1BeaconHeaderResponse, error) {
	resHeaders, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/headers/%d", bc.endpoint, slot))
	if err != nil {
		if err == errNotFound {
			// no block found
			return nil, nil
		}
		return nil, fmt.Errorf("error retrieving headers at slot %v: %v", slot, err)
	}

	var parsedHeaders rpctypes.StandardV1BeaconHeaderResponse
	err = json.Unmarshal(resHeaders, &parsedHeaders)
	if err != nil {
		return nil, fmt.Errorf("error parsing header-response for slot %v: %v", slot, err)
	}
	return &parsedHeaders, nil
}

func (bc *BeaconClient) GetBlockBodyByBlockroot(blockroot []byte) (*rpctypes.StandardV2BeaconBlockResponse, error) {
	resp, err := bc.get(fmt.Sprintf("%s/eth/v2/beacon/blocks/0x%x", bc.endpoint, blockroot))
	disperr := err
	if err != nil {
		resp, err = bc.get(fmt.Sprintf("%s/eth/v1/beacon/blocks/0x%x", bc.endpoint, blockroot))
	}
	if err != nil {
		return nil, fmt.Errorf("error retrieving block body for 0x%x: %v", blockroot, disperr)
	}

	var parsedResponse rpctypes.StandardV2BeaconBlockResponse
	err = json.Unmarshal(resp, &parsedResponse)
	if err != nil {
		logger.Errorf("error parsing block body for 0x%x: %v", blockroot, err)
		return nil, fmt.Errorf("error parsing block body for 0x%x: %v", blockroot, err)
	}

	return &parsedResponse, nil
}

func (bc *BeaconClient) GetProposerDuties(epoch uint64) (*rpctypes.StandardV1ProposerDutiesResponse, error) {
	if utils.Config.Chain.WhiskForkEpoch != nil && epoch >= *utils.Config.Chain.WhiskForkEpoch {
		// whisk activated - cannot fetch proposer duties
		return nil, nil
	}
	var parsedProposerResponse rpctypes.StandardV1ProposerDutiesResponse
	proposerResp, err := bc.get(fmt.Sprintf("%s/eth/v1/validator/duties/proposer/%d", bc.endpoint, epoch))
	if err != nil {
		return nil, fmt.Errorf("error retrieving proposer duties: %v", err)
	}
	err = json.Unmarshal(proposerResp, &parsedProposerResponse)
	if err != nil {
		return nil, fmt.Errorf("error parsing proposer duties: %v", err)
	}
	return &parsedProposerResponse, nil
}

func (bc *BeaconClient) GetCommitteeDuties(stateRef string, epoch uint64) (*rpctypes.StandardV1CommitteesResponse, error) {
	var parsedCommittees rpctypes.StandardV1CommitteesResponse
	err := bc.getJson(fmt.Sprintf("%s/eth/v1/beacon/states/%s/committees?epoch=%d", bc.endpoint, stateRef, epoch), &parsedCommittees)
	if err != nil {
		return nil, fmt.Errorf("error loading committee duties: %v", err)
	}
	return &parsedCommittees, nil
}

func (bc *BeaconClient) GetSyncCommitteeDuties(stateRef string, epoch uint64) (*rpctypes.StandardV1SyncCommitteesResponse, error) {
	if epoch < utils.Config.Chain.Config.AltairForkEpoch {
		return nil, fmt.Errorf("cannot get sync committee duties for epoch before altair: %v", epoch)
	}
	var parsedSyncCommittees rpctypes.StandardV1SyncCommitteesResponse
	err := bc.getJson(fmt.Sprintf("%s/eth/v1/beacon/states/%s/sync_committees?epoch=%d", bc.endpoint, stateRef, epoch), &parsedSyncCommittees)
	if err != nil {
		return nil, fmt.Errorf("error loading sync committee duties: %v", err)
	}
	return &parsedSyncCommittees, nil
}

// GetEpochAssignments will get the epoch assignments from Lighthouse RPC api
func (bc *BeaconClient) GetEpochAssignments(epoch uint64, dependendRoot []byte) (*rpctypes.EpochAssignments, error) {
	parsedProposerResponse, err := bc.GetProposerDuties(epoch)
	if err != nil {
		return nil, err
	}

	if parsedProposerResponse != nil {
		dependendRoot = parsedProposerResponse.DependentRoot
	}
	if dependendRoot == nil {
		return nil, fmt.Errorf("couldn't find dependent root for epoch %v", epoch)
	}

	var depStateRoot string
	// fetch the block root that the proposer data is dependent on
	parsedHeader, err := bc.GetBlockHeaderByBlockroot(dependendRoot)
	if err != nil {
		return nil, err
	}
	depStateRoot = parsedHeader.Data.Header.Message.StateRoot.String()
	if epoch == 0 {
		depStateRoot = "genesis"
	}

	assignments := &rpctypes.EpochAssignments{
		DependendRoot:       dependendRoot,
		DependendStateRef:   depStateRoot,
		ProposerAssignments: make(map[uint64]uint64),
		AttestorAssignments: make(map[string][]uint64),
	}

	// proposer duties
	if utils.Config.Chain.WhiskForkEpoch != nil && epoch >= *utils.Config.Chain.WhiskForkEpoch {
		firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
		lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
		for slot := firstSlot; slot <= lastSlot; slot++ {
			assignments.ProposerAssignments[slot] = math.MaxInt64
		}
	} else if parsedProposerResponse != nil {
		for _, duty := range parsedProposerResponse.Data {
			assignments.ProposerAssignments[uint64(duty.Slot)] = uint64(duty.ValidatorIndex)
		}
	}

	// Now use the state root to make a consistent committee query
	parsedCommittees, err := bc.GetCommitteeDuties(depStateRoot, epoch)
	if err != nil {
		logger.Errorf("error retrieving committees data: %v", err)
	} else {
		// attester duties
		for _, committee := range parsedCommittees.Data {
			for i, valIndex := range committee.Validators {
				valIndexU64, err := strconv.ParseUint(valIndex, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("epoch %d committee %d index %d has bad validator index %q", epoch, committee.Index, i, valIndex)
				}
				k := fmt.Sprintf("%v-%v", uint64(committee.Slot), uint64(committee.Index))
				if assignments.AttestorAssignments[k] == nil {
					assignments.AttestorAssignments[k] = make([]uint64, 0)
				}
				assignments.AttestorAssignments[k] = append(assignments.AttestorAssignments[k], valIndexU64)
			}
		}
	}

	if epoch >= utils.Config.Chain.Config.AltairForkEpoch {
		syncCommitteeState := depStateRoot
		if epoch > 0 && epoch == utils.Config.Chain.Config.AltairForkEpoch {
			syncCommitteeState = fmt.Sprintf("%d", utils.Config.Chain.Config.AltairForkEpoch*utils.Config.Chain.Config.SlotsPerEpoch)
		}
		parsedSyncCommittees, err := bc.GetSyncCommitteeDuties(syncCommitteeState, epoch)
		if err != nil {
			logger.Errorf("error retrieving sync_committees for epoch %v (state: %v): %v", epoch, syncCommitteeState, err)
		} else {
			assignments.SyncAssignments = make([]uint64, len(parsedSyncCommittees.Data.Validators))

			// sync committee duties
			for i, valIndexStr := range parsedSyncCommittees.Data.Validators {
				valIndexU64, err := strconv.ParseUint(valIndexStr, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("in sync_committee for epoch %d validator %d has bad validator index: %q", epoch, i, valIndexStr)
				}
				assignments.SyncAssignments[i] = valIndexU64
			}
		}
	}

	return assignments, nil
}

func (bc *BeaconClient) GetStateValidators(stateRef string) (*rpctypes.StandardV1StateValidatorsResponse, error) {
	var parsedResponse rpctypes.StandardV1StateValidatorsResponse
	err := bc.getJson(fmt.Sprintf("%s/eth/v1/beacon/states/%v/validators", bc.endpoint, stateRef), &parsedResponse)
	if err != nil {
		return nil, fmt.Errorf("error retrieving state validators: %v", err)
	}
	return &parsedResponse, nil
}

func (bc *BeaconClient) GetGenesisValidators() (*rpctypes.StandardV1StateValidatorsResponse, error) {
	var parsedResponse rpctypes.StandardV1StateValidatorsResponse
	err := bc.getJson(fmt.Sprintf("%s/eth/v1/beacon/states/genesis/validators", bc.endpoint), &parsedResponse)
	if err != nil {
		return nil, fmt.Errorf("error retrieving state validators: %v", err)
	}
	return &parsedResponse, nil
}

func (bc *BeaconClient) GetBlobSidecarsByBlockroot(blockroot []byte) (*rpctypes.StandardV1BlobSidecarsResponse, error) {
	resp, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/blob_sidecars/0x%x", bc.endpoint, blockroot))
	if err != nil {
		return nil, fmt.Errorf("error retrieving blob sidecars for 0x%x: %v", blockroot, err)
	}

	var parsedResponse rpctypes.StandardV1BlobSidecarsResponse
	err = json.Unmarshal(resp, &parsedResponse)
	if err != nil {
		logger.Errorf("error parsing blob sidecars for 0x%x: %v", blockroot, err)
		return nil, fmt.Errorf("error parsing blob sidecars for 0x%x: %v", blockroot, err)
	}

	return &parsedResponse, nil
}
