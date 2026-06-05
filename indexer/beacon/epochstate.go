package beacon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/indexer/beacon/statetransition"
	"github.com/ethpandaops/dora/statecache"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// epochState represents a beacon state which a epoch status depends on.
type epochState struct {
	slotRoot    phase0.Root
	stateRoot   phase0.Root
	targetEpoch phase0.Epoch // the epoch this state is being prepared for

	loadingCancel  context.CancelFunc
	loadingStatus  uint8
	retryCount     uint64
	readyChanMutex sync.Mutex
	readyChan      chan bool
	highPriority   bool

	stateSlot                  phase0.Slot
	sourceBlockUid             uint64 // block UID of the source block (before epoch transition)
	validatorBalances          []phase0.Gwei
	builderBalances            []phase0.Gwei
	randaoMixes                []phase0.Root
	depositIndex               uint64
	syncCommittee              []phase0.ValidatorIndex
	depositBalanceToConsume    phase0.Gwei
	pendingDeposits            []*electra.PendingDeposit
	pendingPartialWithdrawals  []*electra.PendingPartialWithdrawal
	builderPendingWithdrawals  []*gloas.BuilderPendingWithdrawal
	delayedBuilderPaymentCount uint32 // number of delayed payments at the tail of builderPendingWithdrawals
	pendingConsolidations      []*electra.PendingConsolidation
	proposerLookahead          []phase0.ValidatorIndex
}

// newEpochState creates a new epochState instance with the root of the state to be loaded.
func newEpochState(slotRoot phase0.Root, targetEpoch phase0.Epoch) *epochState {
	return &epochState{
		slotRoot:    slotRoot,
		targetEpoch: targetEpoch,
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
func (s *epochState) loadState(ctx context.Context, client *Client, cache *epochCache) (*all.BeaconState, error) {
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

	var beaconBlock *all.SignedBeaconBlock

	block := client.indexer.blockCache.getBlockByRoot(s.slotRoot)
	if block != nil {
		beaconBlock = block.AwaitBlock(ctx, beaconHeaderRequestTimeout)
	}

	if beaconBlock == nil {
		loaded, err := LoadBeaconBlock(ctx, client, s.slotRoot)
		if err != nil {
			return nil, err
		}

		beaconBlock = loaded
	}

	if beaconBlock != nil && beaconBlock.Message != nil {
		s.stateRoot = beaconBlock.Message.StateRoot
	}

	specs := client.indexer.consensusPool.GetChainState().GetSpecs()

	// Save the source block UID before epoch transition (needed for ref slot of
	// direct builder payments from the parent epoch's last block).
	if block != nil {
		s.sourceBlockUid = block.BlockUID
	} else if beaconBlock != nil && beaconBlock.Message != nil {
		s.sourceBlockUid = uint64(beaconBlock.Message.Slot) << 16
	}

	// Try loading from state cache first (post-epoch-transition state).
	var resState *all.BeaconState
	sc := client.indexer.stateCache
	if sc != nil && sc.Check(s.slotRoot, s.targetEpoch) {
		resState = sc.Load(s.slotRoot, s.targetEpoch)
		if resState != nil {
			client.logger.Infof("loaded epoch %v state from cache (dep: %v)", s.targetEpoch, s.slotRoot.String())
		}
	}

	// Try replaying from parent epoch's cached state + blocks. This is much
	// cheaper than loading the full state from the beacon API (which can be
	// hundreds of MB on mainnet). On any failure, falls through to API load.
	if resState == nil && sc != nil && s.targetEpoch > 0 {
		if replayed := s.tryReplayFromParentState(ctx, client, block, beaconBlock, specs, sc); replayed != nil {
			resState = replayed
		}
	}

	if resState == nil {
		// Fall back to loading the full state from the beacon API.
		apiStart := time.Now()
		loaded, err := LoadBeaconState(ctx, client, s.stateRoot)
		if err != nil {
			return nil, err
		}
		resState = loaded
		apiLoadDur := time.Since(apiStart)

		// For Fulu+: apply epoch transition to advance the state from the post-block state
		// of the parent epoch's last block to the pre-state of the target epoch.
		// Skip for genesis (epoch 0) — the genesis state is already the correct pre-state.
		var epochTransitionDur time.Duration
		if resState.Version >= spec.DataVersionFulu && s.targetEpoch > 0 {
			epochStart := time.Now()
			var transitionInfo statetransition.TransitionInfo
			if err := statetransition.NewStateTransition(specs, client.indexer.dynSsz).PrepareEpochPreState(resState, s.targetEpoch, &transitionInfo); err != nil {
				return nil, fmt.Errorf("error applying epoch transition for epoch %v: %w", s.targetEpoch, err)
			}
			epochTransitionDur = time.Since(epochStart)
			s.delayedBuilderPaymentCount = transitionInfo.DelayedBuilderPayments
		}

		client.logger.Infof("loaded epoch %v state from beacon API in %v + epoch transition %v",
			s.targetEpoch, apiLoadDur.Round(time.Millisecond), epochTransitionDur.Round(time.Millisecond))

		// Store in state cache for future use.
		if sc != nil {
			if err := sc.Store(s.slotRoot, s.targetEpoch, resState); err != nil {
				client.logger.Warnf("failed to cache state for epoch %v: %v", s.targetEpoch, err)
			}
		}
	}

	if err := s.processState(resState, cache); err != nil {
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
func (s *epochState) processState(state *all.BeaconState, cache *epochCache) error {
	if state == nil {
		return fmt.Errorf("nil state for %v", s.slotRoot.String())
	}

	s.stateSlot = state.Slot
	dependentRoot := s.slotRoot

	// validators/balances default to the on-chain registry. For recent (unfinalized)
	// epoch states we extend them with the validators the chain will create from the
	// pending_deposits queue, at their exact future indices. New slices are built —
	// state.Validators/Balances must not be mutated, as the same state object is
	// advanced for sibling epochs and stored in the state cache.
	realValidatorCount := len(state.Validators)
	validators := state.Validators
	balances := state.Balances

	if cache != nil && state.Version >= spec.DataVersionElectra && len(state.PendingDeposits) > 0 {
		chainState := cache.indexer.consensusPool.GetChainState()
		finalizedEpoch, _ := chainState.GetFinalizedCheckpoint()
		if chainState.EpochOfSlot(state.Slot) > finalizedEpoch {
			specs := chainState.GetSpecs()
			var gloasForkEpoch *phase0.Epoch
			if specs.GloasForkEpoch != nil {
				e := phase0.Epoch(*specs.GloasForkEpoch)
				gloasForkEpoch = &e
			}
			projectedValidators, projectedBalances := cache.indexer.pendingValidators.project(state.Validators, state.PendingDeposits, pendingProjectionInput{
				genesisForkVersion:         specs.GenesisForkVersion,
				currentEpoch:               chainState.EpochOfSlot(state.Slot),
				depositBalanceToConsume:    state.DepositBalanceToConsume,
				maxPendingDepositsPerEpoch: specs.MaxPendingDepositsPerEpoch,
				gloasForkEpoch:             gloasForkEpoch,
				churnLimit:                 chainState.GetActivationExitChurnLimit,
			})
			if len(projectedValidators) > 0 {
				validators = make([]*phase0.Validator, 0, realValidatorCount+len(projectedValidators))
				validators = append(validators, state.Validators...)
				validators = append(validators, projectedValidators...)

				balances = make([]phase0.Gwei, 0, realValidatorCount+len(projectedBalances))
				balances = append(balances, state.Balances...)
				balances = append(balances, projectedBalances...)
			}
		}
	}

	if cache != nil {
		cache.indexer.validatorCache.updateValidatorSet(state.Slot, dependentRoot, validators)
	}

	// Process builder set for Gloas+
	if state.Version >= spec.DataVersionGloas {
		if cache != nil {
			cache.indexer.builderCache.updateBuilderSet(state.Slot, dependentRoot, state.Builders)
		}

		builderBalances := make([]phase0.Gwei, len(state.Builders))
		for i, builder := range state.Builders {
			builderBalances[i] = builder.Balance
		}
		s.builderBalances = builderBalances
	}

	validatorPubkeyMap := make(map[phase0.BLSPubKey]phase0.ValidatorIndex, len(state.Validators))
	for i, v := range state.Validators {
		validatorPubkeyMap[v.PublicKey] = phase0.ValidatorIndex(i)
	}

	s.validatorBalances = balances
	s.randaoMixes = state.RANDAOMixes
	s.depositIndex = state.ETH1DepositIndex

	if state.Version >= spec.DataVersionAltair {
		var pubkeys []phase0.BLSPubKey
		if state.CurrentSyncCommittee != nil {
			pubkeys = state.CurrentSyncCommittee.Pubkeys
		}

		syncCommittee := make([]phase0.ValidatorIndex, len(pubkeys))
		for i, v := range pubkeys {
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
		s.depositBalanceToConsume = state.DepositBalanceToConsume
		s.pendingDeposits = state.PendingDeposits
		s.pendingPartialWithdrawals = state.PendingPartialWithdrawals
		s.pendingConsolidations = state.PendingConsolidations
	}

	if state.Version >= spec.DataVersionGloas {
		s.builderPendingWithdrawals = state.BuilderPendingWithdrawals
	}

	s.proposerLookahead = state.ProposerLookahead

	return nil
}

// tryReplayFromParentState attempts to reconstruct the dependent block's post-state
// by loading the parent epoch's pre-state from cache and replaying all parent epoch
// blocks using the state transition. Returns the post-epoch-transition state ready
// for the target epoch, or nil if replay is not possible or verification fails.
// On any failure (missing inputs, ApplyBlock error, HTR mismatch, epoch transition
// error) the function returns nil and the caller falls back to loading the state
// from the beacon API.
func (s *epochState) tryReplayFromParentState(
	ctx context.Context,
	client *Client,
	depBlock *Block,
	depBeaconBlock *all.SignedBeaconBlock,
	specs *consensus.ChainSpec,
	sc *statecache.StateCache,
) *all.BeaconState {
	if depBlock == nil || depBeaconBlock == nil {
		return nil
	}

	parentEpoch := s.targetEpoch - 1
	slotsPerEpoch := specs.SlotsPerEpoch

	// Walk back from depBlock to find the dependent root for the parent epoch
	// (the last block before parentEpoch's first slot).
	parentEpochFirstSlot := phase0.Slot(uint64(parentEpoch) * slotsPerEpoch)
	walkBlock := depBlock
	for walkBlock != nil && walkBlock.Slot >= parentEpochFirstSlot {
		parentRoot := walkBlock.GetParentRoot()
		if parentRoot == nil {
			return nil
		}
		walkBlock = client.indexer.blockCache.getBlockByRoot(*parentRoot)
	}
	if walkBlock == nil {
		return nil
	}
	parentDepRoot := walkBlock.Root

	// Parent epoch's pre-state must be in cache.
	if !sc.Check(parentDepRoot, parentEpoch) {
		return nil
	}
	parentState := sc.Load(parentDepRoot, parentEpoch)
	if parentState == nil {
		return nil
	}

	// Skip replay across fork boundaries — the state version must match the
	// dependent block's version (fork upgrades during state transition are not
	// yet implemented).
	if depBeaconBlock.Version != parentState.Version {
		return nil
	}

	// Collect all blocks in the parent epoch in slot order.
	var epochBlocks []*Block
	walkBlock = depBlock
	for walkBlock != nil && walkBlock.Slot >= parentEpochFirstSlot {
		epochBlocks = append(epochBlocks, walkBlock)
		parentRoot := walkBlock.GetParentRoot()
		if parentRoot == nil {
			break
		}
		walkBlock = client.indexer.blockCache.getBlockByRoot(*parentRoot)
	}
	for i, j := 0, len(epochBlocks)-1; i < j; i, j = i+1, j-1 {
		epochBlocks[i], epochBlocks[j] = epochBlocks[j], epochBlocks[i]
	}

	// Reusable state transition; caches persist across all blocks in the epoch
	// and the trailing epoch transition.
	st := statetransition.NewStateTransition(specs, client.indexer.dynSsz)

	// prevStateRoot is the verified post-block HTR from the previous iteration —
	// the same value as the next block's pre-state HTR — passed as a hint to
	// skip the expensive HTR computation in the first process_slot.
	var prevStateRoot phase0.Root

	replayStart := time.Now()
	var blockApplyTotal time.Duration
	for _, blk := range epochBlocks {
		beaconBlock := blk.GetBlock(ctx)
		if beaconBlock == nil || beaconBlock.Message == nil {
			return nil
		}

		blockStart := time.Now()
		if err := st.ApplyBlockWithStateRoot(parentState, beaconBlock, prevStateRoot); err != nil {
			client.logger.Warnf("replay: ApplyBlock failed at slot %v: %v", blk.Slot, err)
			return nil
		}

		// Verify post-block state root matches the block header. In Gloas, block
		// processing now includes processing the parent payload's requests via
		// block.body.parent_execution_requests, so the post-block HTR already
		// reflects everything — no separate payload application step.
		expectedStateRoot := beaconBlock.Message.StateRoot
		gotRootBytes, htrErr := client.indexer.dynSsz.HashTreeRoot(parentState)
		gotStateRoot := phase0.Root(gotRootBytes)
		if htrErr != nil {
			client.logger.Warnf("replay: HTR failed at slot %v: %v", blk.Slot, htrErr)
			return nil
		}
		if gotStateRoot != expectedStateRoot {
			client.logger.Warnf("replay: state root mismatch at slot %v (got %v, expected %v), falling back to API",
				blk.Slot, gotStateRoot.String(), expectedStateRoot.String())
			return nil
		}
		prevStateRoot = gotStateRoot
		blockApplyTotal += time.Since(blockStart)
	}
	blockReplayDur := time.Since(replayStart)

	// Apply epoch transition to advance the state from the post-block state of
	// the parent epoch's last block to the pre-state of the target epoch.
	var epochTransitionDur time.Duration
	if parentState.Version >= spec.DataVersionFulu {
		epochStart := time.Now()
		var transitionInfo statetransition.TransitionInfo
		if err := st.PrepareEpochPreState(parentState, s.targetEpoch, &transitionInfo); err != nil {
			client.logger.Warnf("replay: epoch transition failed for epoch %v: %v", s.targetEpoch, err)
			return nil
		}
		epochTransitionDur = time.Since(epochStart)
		s.delayedBuilderPaymentCount = transitionInfo.DelayedBuilderPayments
	}

	client.logger.Infof(
		"replayed epoch %v: %d blocks in %v (apply %v) + epoch transition %v",
		parentEpoch, len(epochBlocks),
		blockReplayDur.Round(time.Millisecond),
		blockApplyTotal.Round(time.Millisecond),
		epochTransitionDur.Round(time.Millisecond),
	)

	// Cache the post-epoch-transition state for the target epoch.
	if err := sc.Store(s.slotRoot, s.targetEpoch, parentState); err != nil {
		client.logger.Warnf("failed to cache replayed state for epoch %v: %v", s.targetEpoch, err)
	}

	return parentState
}
