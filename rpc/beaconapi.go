package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"time"

	logger "github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type BeaconClient struct {
	endpoint string
}

// NewBeaconClient is used to create a new beacon client
func NewBeaconClient(endpoint string) (*BeaconClient, error) {
	client := &BeaconClient{
		endpoint: endpoint,
	}

	return client, nil
}

var errNotFound = errors.New("not found 404")

func (bc *BeaconClient) get(url string) ([]byte, error) {
	// t0 := time.Now()
	// defer func() { fmt.Println(url, time.Since(t0)) }()
	client := &http.Client{Timeout: time.Second * 120}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, errNotFound
		}
		return nil, fmt.Errorf("url: %v, error-response: %s", url, data)
	}

	return data, err
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

func (bc *BeaconClient) GetBlockBodyByBlockroot(blockroot string) (*rpctypes.StandardV2BeaconBlockResponse, error) {
	resp, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/blocks/%s", bc.endpoint, blockroot))
	if err != nil {
		return nil, fmt.Errorf("error retrieving block body for %s: %v", blockroot, err)
	}

	var parsedResponse rpctypes.StandardV2BeaconBlockResponse
	err = json.Unmarshal(resp, &parsedResponse)
	if err != nil {
		logger.Errorf("error parsing block body for %s: %v", blockroot, err)
		return nil, fmt.Errorf("error parsing block body for %s: %v", blockroot, err)
	}

	return &parsedResponse, nil
}

func (bc *BeaconClient) GetProposerDuties(epoch uint64) (*rpctypes.StandardV1ProposerDutiesResponse, error) {
	proposerResp, err := bc.get(fmt.Sprintf("%s/eth/v1/validator/duties/proposer/%d", bc.endpoint, epoch))
	if err != nil {
		return nil, fmt.Errorf("error retrieving proposer duties: %v", err)
	}
	var parsedProposerResponse rpctypes.StandardV1ProposerDutiesResponse
	err = json.Unmarshal(proposerResp, &parsedProposerResponse)
	if err != nil {
		return nil, fmt.Errorf("error parsing proposer duties: %v", err)
	}
	return &parsedProposerResponse, nil
}

// GetEpochAssignments will get the epoch assignments from Lighthouse RPC api
func (bc *BeaconClient) GetEpochAssignments(epoch uint64) (*rpctypes.EpochAssignments, error) {
	parsedProposerResponse, err := bc.GetProposerDuties(epoch)
	if err != nil {
		return nil, err
	}

	// fetch the block root that the proposer data is dependent on
	parsedHeader, err := bc.GetBlockHeaderByBlockroot(utils.MustParseHex(parsedProposerResponse.DependentRoot))
	if err != nil {
		return nil, err
	}
	depStateRoot := parsedHeader.Data.Header.Message.StateRoot

	// Now use the state root to make a consistent committee query
	committeesResp, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/states/%s/committees?epoch=%d", bc.endpoint, depStateRoot, epoch))
	if err != nil {
		return nil, fmt.Errorf("error retrieving committees data: %w", err)
	}
	var parsedCommittees rpctypes.StandardV1CommitteesResponse
	err = json.Unmarshal(committeesResp, &parsedCommittees)
	if err != nil {
		return nil, fmt.Errorf("error parsing committees data: %w", err)
	}

	assignments := &rpctypes.EpochAssignments{
		ProposerAssignments: make(map[uint64]uint64),
		AttestorAssignments: make(map[string][]uint64),
	}

	// proposer duties
	for _, duty := range parsedProposerResponse.Data {
		assignments.ProposerAssignments[uint64(duty.Slot)] = uint64(duty.ValidatorIndex)
	}

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

	if epoch >= utils.Config.Chain.Config.AltairForkEpoch {
		syncCommitteeState := depStateRoot
		if epoch == utils.Config.Chain.Config.AltairForkEpoch {
			syncCommitteeState = fmt.Sprintf("%d", utils.Config.Chain.Config.AltairForkEpoch*utils.Config.Chain.Config.SlotsPerEpoch)
		}

		syncCommitteesResp, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/states/%s/sync_committees?epoch=%d", bc.endpoint, syncCommitteeState, epoch))
		if err != nil {
			return nil, fmt.Errorf("error retrieving sync_committees for epoch %v (state: %v): %w", epoch, syncCommitteeState, err)
		}
		var parsedSyncCommittees rpctypes.StandardV1SyncCommitteesResponse
		err = json.Unmarshal(syncCommitteesResp, &parsedSyncCommittees)
		if err != nil {
			return nil, fmt.Errorf("error parsing sync_committees data for epoch %v (state: %v): %w", epoch, syncCommitteeState, err)
		}
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

	return assignments, nil
}

func (bc *BeaconClient) GetStateValidators(stateroot []byte) (*rpctypes.StandardV1StateValidatorsResponse, error) {
	resp, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/states/0x%x/validators", bc.endpoint, stateroot))
	if err != nil {
		return nil, fmt.Errorf("error retrieving state validators: %v", err)
	}
	var parsedResponse rpctypes.StandardV1StateValidatorsResponse
	err = json.Unmarshal(resp, &parsedResponse)
	if err != nil {
		return nil, fmt.Errorf("error parsing state validators: %v", err)
	}
	return &parsedResponse, nil
}
