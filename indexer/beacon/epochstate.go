package beacon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// epochState represents a beacon state which a epoch status depends on.
type epochState struct {
	slotRoot  phase0.Root
	stateRoot phase0.Root

	loadingCancel  context.CancelFunc
	loadingStatus  uint8
	retryCount     uint64
	readyChanMutex sync.Mutex
	readyChan      chan bool
	highPriority   bool

	stateSlot                 phase0.Slot
	validatorBalances         []phase0.Gwei
	builderBalances           []phase0.Gwei
	randaoMixes               []phase0.Root
	depositIndex              uint64
	syncCommittee             []phase0.ValidatorIndex
	depositBalanceToConsume   phase0.Gwei
	pendingDeposits           []*electra.PendingDeposit
	pendingPartialWithdrawals []*electra.PendingPartialWithdrawal
	pendingConsolidations     []*electra.PendingConsolidation
	proposerLookahead         []phase0.ValidatorIndex
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
	s.readyChanMutex.Lock()
	if s.readyChan != nil {
		close(s.readyChan)
		s.readyChan = nil
	}
	s.readyChanMutex.Unlock()
}

func (s *epochState) awaitStateLoaded(ctx context.Context, timeout time.Duration) bool {
	s.readyChanMutex.Lock()
	if s.readyChan == nil && s.loadingStatus != 2 {
		s.readyChan = make(chan bool)
	}
	s.readyChanMutex.Unlock()

	timeoutTime := time.Now().Add(timeout)
	for {
		if s.loadingStatus == 2 {
			return true
		}
		if s.retryCount > 10 {
			return false
		}

		select {
		case <-s.readyChan:
			return true
		case <-time.After(time.Until(timeoutTime)):
			return false
		case <-ctx.Done():
			return false
		case <-time.After(5 * time.Second):
		}
	}
}

// loadState loads the state for the epoch from the client.
func (s *epochState) loadState(ctx context.Context, client *Client, cache *epochCache) (*spec.VersionedBeaconState, error) {
	if s.loadingStatus > 0 {
		return nil, fmt.Errorf("already loading")
	}

	s.loadingStatus = 1

	ctx, cancel := context.WithTimeout(ctx, beaconStateRequestTimeout+(beaconHeaderRequestTimeout*2))
	s.loadingCancel = cancel
	defer func() {
		s.loadingCancel = nil
		cancel()

		if s.loadingStatus == 1 {
			s.loadingStatus = 0
			s.retryCount++
		}
	}()

	var beaconBlock *spec.VersionedSignedBeaconBlock

	block := client.indexer.blockCache.getBlockByRoot(s.slotRoot)
	if block != nil {
		beaconBlock = block.AwaitBlock(ctx, beaconHeaderRequestTimeout)
	}

	if beaconBlock == nil {
		var err error
		beaconBlock, err = LoadBeaconBlock(ctx, client, s.slotRoot)
		if err != nil {
			return nil, err
		}
	}

	if beaconBlock != nil {
		slot, _ := beaconBlock.Slot()
		client.logger.Infof("loading state for block root %v (slot %v)", s.slotRoot.String(), slot)

		var err error
		s.stateRoot, err = beaconBlock.StateRoot()
		if err != nil {
			return nil, fmt.Errorf("error getting state root from beacon block %v: %v", s.slotRoot.String(), err)
		}
	}

	resState, err := LoadBeaconState(ctx, client, s.stateRoot)
	if err != nil {
		return nil, err
	}

	err = s.processState(resState, beaconBlock, cache)
	if err != nil {
		return nil, err
	}

	s.readyChanMutex.Lock()
	defer s.readyChanMutex.Unlock()
	if s.readyChan != nil {
		close(s.readyChan)
		s.readyChan = nil
	}

	s.loadingStatus = 2
	return resState, nil
}

// processState processes the state and updates the epochState instance.
// the function extracts and unifies all relevant information from the beacon state, so the full beacon state can be dropped from memory afterwards.
func (s *epochState) processState(state *spec.VersionedBeaconState, beaconBlock *spec.VersionedSignedBeaconBlock, cache *epochCache) error {
	slot, err := state.Slot()
	if err != nil {
		return fmt.Errorf("error getting slot from state %v: %v", s.slotRoot.String(), err)
	}

	s.stateSlot = slot

	dependentRoot := s.slotRoot
	if state.Version >= spec.DataVersionFulu {
		blockRoots, err := getStateBlockRoots(state)
		if err != nil {
			return fmt.Errorf("error getting block roots from state %v: %v", s.slotRoot.String(), err)
		}

		specs := cache.indexer.consensusPool.GetChainState().GetSpecs()
		dependentRoot = blockRoots[slot%phase0.Slot(specs.SlotsPerHistoricalRoot)]
	}

	validatorList, err := state.Validators()
	if err != nil {
		return fmt.Errorf("error getting validators from state %v: %v", s.slotRoot.String(), err)
	}

	if cache != nil {
		cache.indexer.validatorCache.updateValidatorSet(slot, dependentRoot, validatorList)
	}

	// Process builder set for Gloas
	if state.Version >= spec.DataVersionGloas && state.Gloas != nil {
		if cache != nil {
			cache.indexer.builderCache.updateBuilderSet(slot, dependentRoot, state.Gloas.Builders)
		}

		// Extract builder balances
		builderBalances := make([]phase0.Gwei, len(state.Gloas.Builders))
		for i, builder := range state.Gloas.Builders {
			builderBalances[i] = builder.Balance
		}
		s.builderBalances = builderBalances
	}

	validatorPubkeyMap := make(map[phase0.BLSPubKey]phase0.ValidatorIndex)
	for i, v := range validatorList {
		validatorPubkeyMap[v.PublicKey] = phase0.ValidatorIndex(i)
	}

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

	if state.Version >= spec.DataVersionFulu {
		// subtract the deposit indexes from the current block
		blockRequests, err := beaconBlock.ExecutionRequests()
		if err != nil {
			return fmt.Errorf("error getting execution requests from block %v: %v", s.slotRoot.String(), err)
		}

		if len(blockRequests.Deposits) > 0 {
			s.depositIndex = blockRequests.Deposits[0].Index
		} else {
			s.depositIndex = getStateDepositIndex(state)
		}
	} else {
		s.depositIndex = getStateDepositIndex(state)
	}

	if state.Version >= spec.DataVersionAltair {
		currentSyncCommittee, err := getStateCurrentSyncCommittee(state)
		if err != nil {
			return fmt.Errorf("error getting current sync committee from state %v: %v", s.slotRoot.String(), err)
		}

		syncCommittee := make([]phase0.ValidatorIndex, len(currentSyncCommittee))
		for i, v := range currentSyncCommittee {
			syncCommittee[i] = validatorPubkeyMap[v]
		}
		if cache != nil {
			syncCommittee = cache.getOrUpdateSyncCommittee(syncCommittee)
		}
		s.syncCommittee = syncCommittee
	} else {
		s.syncCommittee = []phase0.ValidatorIndex{}
	}

	if state.Version >= spec.DataVersionElectra {
		depositBalanceToConsume, err := getStateDepositBalanceToConsume(state)
		if err != nil {
			return fmt.Errorf("error getting deposit balance to consume from state %v: %v", s.slotRoot.String(), err)
		}
		s.depositBalanceToConsume = depositBalanceToConsume

		pendingDeposits, err := getStatePendingDeposits(state)
		if err != nil {
			return fmt.Errorf("error getting pending deposit indices from state %v: %v", s.slotRoot.String(), err)
		}
		s.pendingDeposits = pendingDeposits

		pendingPartialWithdrawals, err := getStatePendingWithdrawals(state)
		if err != nil {
			return fmt.Errorf("error getting pending withdrawal indices from state %v: %v", s.slotRoot.String(), err)
		}
		s.pendingPartialWithdrawals = pendingPartialWithdrawals

		pendingConsolidations, err := getStatePendingConsolidations(state)
		if err != nil {
			return fmt.Errorf("error getting pending consolidation indices from state %v: %v", s.slotRoot.String(), err)
		}

		// apply epoch transition to get remaining pending consolidations
		s.pendingConsolidations = pendingConsolidations
	}

	proposerLookahead, _ := getStateProposerLookahead(state)
	s.proposerLookahead = proposerLookahead

	return nil
}
