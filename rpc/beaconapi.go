package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/lru"
	logger "github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type BeaconClient struct {
	endpoint            string
	assignmentsCache    *lru.Cache[uint64, *rpctypes.EpochAssignments]
	assignmentsCacheMux sync.Mutex
}

// NewBeaconClient is used to create a new beacon client
func NewBeaconClient(endpoint string, assignmentsCacheSize int) (*BeaconClient, error) {
	if assignmentsCacheSize < 10 {
		assignmentsCacheSize = 10
	}
	client := &BeaconClient{
		endpoint:         endpoint,
		assignmentsCache: lru.NewCache[uint64, *rpctypes.EpochAssignments](assignmentsCacheSize),
	}

	return client, nil
}

var errNotFound = errors.New("not found 404")

func (bc *BeaconClient) get(url string) ([]byte, error) {
	//t0 := time.Now()
	//defer func() { fmt.Println("RPC GET: ", url, time.Since(t0)) }()
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

func (bc *BeaconClient) getJson(url string, returnValue interface{}) error {
	//t0 := time.Now()
	//defer func() { fmt.Println("RPC GET (json): ", url, time.Since(t0)) }()
	client := &http.Client{Timeout: time.Second * 120}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return errNotFound
		}
		data, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("url: %v, error-response: %s", url, data)
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
	resp, err := bc.get(fmt.Sprintf("%s/eth/v1/beacon/blocks/0x%x", bc.endpoint, blockroot))
	if err != nil {
		return nil, fmt.Errorf("error retrieving block body for 0x%x: %v", blockroot, err)
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

func (bc *BeaconClient) GetEpochAssignments(epoch uint64) (*rpctypes.EpochAssignments, error) {
	currentEpoch := utils.TimeToEpoch(time.Now())
	// don't cache current & last epoch as these might change due to reorgs
	// the most recent epoch assignments are cached in the indexer anyway
	cachable := epoch < uint64(currentEpoch)-1
	if cachable {
		bc.assignmentsCacheMux.Lock()
		cachedValue, found := bc.assignmentsCache.Get(epoch)
		bc.assignmentsCacheMux.Unlock()
		if found {
			return cachedValue, nil
		}
	}

	epochAssignments, err := bc.getEpochAssignments(epoch)
	if cachable && epochAssignments != nil && err == nil {
		bc.assignmentsCacheMux.Lock()
		bc.assignmentsCache.Add(epoch, epochAssignments)
		bc.assignmentsCacheMux.Unlock()
	}
	return epochAssignments, err
}

func (bc *BeaconClient) AddCachedEpochAssignments(epoch uint64, epochAssignments *rpctypes.EpochAssignments) {
	bc.assignmentsCacheMux.Lock()
	bc.assignmentsCache.Add(epoch, epochAssignments)
	bc.assignmentsCacheMux.Unlock()
}

// GetEpochAssignments will get the epoch assignments from Lighthouse RPC api
func (bc *BeaconClient) getEpochAssignments(epoch uint64) (*rpctypes.EpochAssignments, error) {
	parsedProposerResponse, err := bc.GetProposerDuties(epoch)
	if err != nil {
		return nil, err
	}

	// fetch the block root that the proposer data is dependent on
	parsedHeader, err := bc.GetBlockHeaderByBlockroot(parsedProposerResponse.DependentRoot)
	if err != nil {
		return nil, err
	}
	depStateRoot := parsedHeader.Data.Header.Message.StateRoot

	// Now use the state root to make a consistent committee query
	var parsedCommittees rpctypes.StandardV1CommitteesResponse
	err = bc.getJson(fmt.Sprintf("%s/eth/v1/beacon/states/%s/committees?epoch=%d", bc.endpoint, depStateRoot, epoch), &parsedCommittees)
	if err != nil {
		return nil, fmt.Errorf("error retrieving committees data: %w", err)
	}
	assignments := &rpctypes.EpochAssignments{
		DependendRoot:       parsedProposerResponse.DependentRoot,
		DependendState:      depStateRoot,
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
		syncCommitteeState := fmt.Sprintf("%s", depStateRoot)
		if epoch == utils.Config.Chain.Config.AltairForkEpoch {
			syncCommitteeState = fmt.Sprintf("%d", utils.Config.Chain.Config.AltairForkEpoch*utils.Config.Chain.Config.SlotsPerEpoch)
		}

		var parsedSyncCommittees rpctypes.StandardV1SyncCommitteesResponse
		err := bc.getJson(fmt.Sprintf("%s/eth/v1/beacon/states/%s/sync_committees?epoch=%d", bc.endpoint, syncCommitteeState, epoch), &parsedSyncCommittees)
		if err != nil {
			return nil, fmt.Errorf("error retrieving sync_committees for epoch %v (state: %v): %w", epoch, syncCommitteeState, err)
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
	var parsedResponse rpctypes.StandardV1StateValidatorsResponse
	err := bc.getJson(fmt.Sprintf("%s/eth/v1/beacon/states/0x%x/validators", bc.endpoint, stateroot), &parsedResponse)
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
