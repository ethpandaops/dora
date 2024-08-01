package beacon

import (
	"context"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// epochState represents a beacon state which a epoch status depends on.
type epochState struct {
	slotRoot  phase0.Root
	stateRoot phase0.Root

	loadingCancel context.CancelFunc
	loadingStatus uint8
	retryCount    uint64

	validatorList     []*phase0.Validator
	validatorBalances []phase0.Gwei
	randaoMixes       []phase0.Root
	depositIndex      uint64
	syncCommittee     []phase0.ValidatorIndex
}

// newEpochState creates a new epochState instance with the root of the state to be loaded.
func newEpochState(slotRoot phase0.Root) *epochState {
	return &epochState{
		slotRoot: slotRoot,
	}
}

// dispose cancels the loading process if it is in progress.
func (s *epochState) dispose() {
	if s.loadingCancel != nil {
		s.loadingCancel()
	}
}

// loadState loads the state for the epoch from the client.
func (s *epochState) loadState(client *Client, cache *epochCache) error {
	if s.loadingStatus > 0 {
		return fmt.Errorf("already loading")
	}

	s.loadingStatus = 1
	client.logger.Debugf("loading state for slot %v", s.slotRoot.String())

	ctx, cancel := context.WithTimeout(client.client.GetContext(), BeaconStateRequestTimeout)
	s.loadingCancel = cancel
	defer func() {
		s.loadingCancel = nil
		cancel()

		if s.loadingStatus == 1 {
			s.loadingStatus = 0
			s.retryCount++
		}
	}()

	var blockHeader *phase0.SignedBeaconBlockHeader

	block := client.indexer.blockCache.getBlockByRoot(s.slotRoot)
	if block != nil {
		blockHeader = block.AwaitHeader(ctx, BeaconHeaderRequestTimeout)
	}

	if blockHeader == nil {
		var err error
		blockHeader, err = client.loadHeader(s.slotRoot)
		if err != nil {
			return err
		}
	}

	s.stateRoot = blockHeader.Message.StateRoot

	resState, err := client.client.GetRPCClient().GetState(ctx, fmt.Sprintf("0x%x", blockHeader.Message.StateRoot[:]))
	if err != nil {
		return err
	}

	err = s.processState(resState, cache)
	if err != nil {
		return err
	}

	s.loadingStatus = 2
	return nil
}

// processState processes the state and updates the epochState instance.
// the function extracts and unifies all relevant information from the beacon state, so the full beacon state can be dropped from memory afterwards.
func (s *epochState) processState(state *spec.VersionedBeaconState, cache *epochCache) error {
	validatorList, err := state.Validators()
	if err != nil {
		return fmt.Errorf("error getting validators from state %v: %v", s.slotRoot.String(), err)
	}

	unifiedValidatorList := make([]*phase0.Validator, len(validatorList))
	validatorPubkeyMap := map[phase0.BLSPubKey]phase0.ValidatorIndex{}
	for i, v := range validatorList {
		unifiedValidatorList[i] = cache.getOrCreateValidator(phase0.ValidatorIndex(i), v)
		validatorPubkeyMap[v.PublicKey] = phase0.ValidatorIndex(i)
	}

	s.validatorList = unifiedValidatorList

	validatorBalances, err := state.ValidatorBalances()
	if err != nil {
		return fmt.Errorf("error getting validator balances from state %v: %v", s.slotRoot.String(), err)
	}

	s.validatorBalances = validatorBalances

	randaoMixes, err := getStateRandaoMixes(state)
	if err != nil {
		return fmt.Errorf("error getting randao mixes from state %v: %v", s.slotRoot.String(), err)
	}

	s.randaoMixes = randaoMixes
	s.depositIndex = getStateDepositIndex(state)

	currentSyncCommittee, err := getStateCurrentSyncCommittee(state)
	if err != nil {
		return fmt.Errorf("error getting current sync committee from state %v: %v", s.slotRoot.String(), err)
	}

	syncCommittee := make([]phase0.ValidatorIndex, len(currentSyncCommittee))
	for i, v := range currentSyncCommittee {
		syncCommittee[i] = validatorPubkeyMap[v]
	}
	s.syncCommittee = cache.getOrUpdateSyncCommittee(syncCommittee)

	return nil
}
