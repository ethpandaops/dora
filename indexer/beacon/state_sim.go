package beacon

import (
	"bytes"
	"math"
	"slices"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

type stateSimulator struct {
	indexer          *Indexer
	epochStats       *EpochStats
	epochStatsValues *EpochStatsValues
	prevState        *stateSimulatorState
	validatorSet     []*phase0.Validator
}

// trackedBuilderWithdrawal pairs a builder pending withdrawal with classification
// metadata. Type identifies whether the entry is a direct or delayed payment, and
// RefBlockUID points to the source block (set during replay for direct entries; resolved
// separately for delayed entries by matching builder indices to missed-payload blocks).
type trackedBuilderWithdrawal struct {
	gloas.BuilderPendingWithdrawal
	Type        uint8 // dbtypes.WithdrawalTypeBuilderPayment or WithdrawalTypeBuilderDelayedPayment
	RefBlockUID *uint64
}

type stateSimulatorBlock struct {
	block           *Block
	payloadIncluded bool
}

// parentPayloadRef identifies the payload block a block's bid builds on - the settle
// target of the spec's process_parent_execution_payload at this block.
type parentPayloadRef struct {
	slot phase0.Slot
	root phase0.Root
}

type stateSimulatorState struct {
	epochRoot                 phase0.Root
	block                     *Block
	blockPayloadApplied       bool // applyPayload flag the last applied block was processed with
	pendingWithdrawals        []electra.PendingPartialWithdrawal
	builderPendingWithdrawals []trackedBuilderWithdrawal
	additionalWithdrawals     []phase0.ValidatorIndex
	pendingConsolidationCount uint64
	validatorMap              map[phase0.ValidatorIndex]*phase0.Validator
	blockResults              [][]uint8
}

func newStateSimulator(indexer *Indexer, epochStats *EpochStats) *stateSimulator {
	sim := &stateSimulator{
		indexer:          indexer,
		epochStats:       epochStats,
		epochStatsValues: epochStats.GetValues(false),
		prevState:        nil,
	}

	if sim.epochStatsValues == nil {
		return nil
	}

	return sim
}

// getParentBlocks returns blocks that need to be replayed before the target block.
// Uses the sim's epoch as the boundary — all blocks from the epoch start up to
// (but not including) the target block are returned.
// The epoch state is always the pre-state of the first slot (post-epoch-transition),
// so all blocks in the epoch need to be replayed.
func (sim *stateSimulator) getParentBlocks(block *Block) []*stateSimulatorBlock {
	chainState := sim.indexer.consensusPool.GetChainState()
	simEpoch := sim.epochStats.epoch
	minSlot := chainState.EpochToSlot(simEpoch)

	parentBlocks := []*stateSimulatorBlock{}
	headBlock := block

	for {
		parentBlockRoot := block.GetParentRoot()
		if parentBlockRoot == nil {
			break
		}

		parentBlock := sim.indexer.GetBlockByRoot(*parentBlockRoot)
		if parentBlock == nil {
			break
		}

		if parentBlock.Slot < minSlot {
			break
		}

		parentBlocks = append(parentBlocks, &stateSimulatorBlock{
			block: parentBlock,
		})
		block = parentBlock
	}

	if len(parentBlocks) > 0 {
		// filter out blocks with orphaned payloads
		parentBlockIndex := headBlock.GetBlockIndex(sim.indexer.ctx)
		if parentBlockIndex != nil {
			parentPayloadHash := parentBlockIndex.ExecutionParentHash
			for i := 0; i < len(parentBlocks); i++ {
				parentBlockIndex = parentBlocks[i].block.GetBlockIndex(sim.indexer.ctx)
				if parentBlockIndex == nil {
					break
				}

				if bytes.Equal(parentBlockIndex.ExecutionHash[:], parentPayloadHash[:]) {
					parentBlocks[i].payloadIncluded = true
					parentPayloadHash = parentBlockIndex.ExecutionParentHash
				}
			}
		}
	}

	slices.Reverse(parentBlocks)

	return parentBlocks
}

func (sim *stateSimulator) resetState(block *Block) *stateSimulatorState {
	pendingWithdrawals := sim.epochStatsValues.PendingWithdrawals
	if pendingWithdrawals == nil {
		pendingWithdrawals = []electra.PendingPartialWithdrawal{}
	}

	epochRoot := block.Root
	chainState := sim.indexer.consensusPool.GetChainState()
	specs := chainState.GetSpecs()
	if chainState.SlotToSlotIndex(block.Slot) == phase0.Slot(specs.SlotsPerEpoch-1) {
		parentRoot := block.GetParentRoot()
		if parentRoot != nil {
			epochRoot = *parentRoot
		}
	}

	rawBuilderWithdrawals := sim.epochStatsValues.BuilderPendingWithdrawals
	if rawBuilderWithdrawals == nil {
		rawBuilderWithdrawals = []gloas.BuilderPendingWithdrawal{}
	}
	// Epoch boundary layout: [direct from prev epoch's unconsumed payloads..., delayed from
	// process_builder_pending_payments at the tail]. The split point is given by
	// DelayedBuilderPaymentRefs.
	delayedCount := len(sim.epochStatsValues.DelayedBuilderPaymentRefs)
	directCount := len(rawBuilderWithdrawals) - delayedCount
	trackedWithdrawals := make([]trackedBuilderWithdrawal, len(rawBuilderWithdrawals))
	for i := range rawBuilderWithdrawals {
		typ := uint8(dbtypes.WithdrawalTypeBuilderUnknownPayment)
		if i >= directCount {
			typ = dbtypes.WithdrawalTypeBuilderDelayedPayment
		}
		trackedWithdrawals[i] = trackedBuilderWithdrawal{
			BuilderPendingWithdrawal: rawBuilderWithdrawals[i],
			Type:                     typ,
		}
	}

	state := &stateSimulatorState{
		block:                     nil,
		epochRoot:                 epochRoot,
		pendingWithdrawals:        pendingWithdrawals,
		builderPendingWithdrawals: trackedWithdrawals,
		pendingConsolidationCount: 0,
		additionalWithdrawals:     []phase0.ValidatorIndex{},
		validatorMap:              map[phase0.ValidatorIndex]*phase0.Validator{},
	}
	sim.prevState = state

	// get pending consolidations from state
	processedConsolidations := uint64(0)
	for _, pendingConsolidation := range sim.epochStatsValues.PendingConsolidations {
		srcValidator := sim.getValidator(pendingConsolidation.SourceIndex)
		if srcValidator == nil {
			return nil
		}

		if srcValidator.WithdrawableEpoch > sim.epochStats.epoch {
			break
		}

		processedConsolidations++
	}

	state.pendingConsolidationCount = uint64(len(sim.epochStatsValues.PendingConsolidations)) - processedConsolidations

	// get pending withdrawals from state
	state.pendingWithdrawals = sim.epochStatsValues.PendingWithdrawals

	// Resolve RefBlockUIDs for initial direct entries by matching them to
	// blocks with delivered payloads in the previous epoch (FIFO order).
	if directCount > 0 {
		sim.resolveInitialDirectRefs(state, directCount)
	}
	if delayedCount > 0 && sim.epochStats.epoch > 1 {
		delayedPaymentBlockMap := sim.resolveDelayedPaymentBlocks()

		pendingIndex := len(state.builderPendingWithdrawals) - 1
		for i := len(sim.epochStatsValues.DelayedBuilderPaymentRefs) - 1; i >= 0; i-- {
			if uid, ok := delayedPaymentBlockMap[phase0.Slot(sim.epochStatsValues.DelayedBuilderPaymentRefs[i])]; ok {
				state.builderPendingWithdrawals[pendingIndex].RefBlockUID = &uid
			}
			pendingIndex--
		}
	}

	return state
}

// resolveParentDirectEntry constructs a trackedBuilderWithdrawal for the parent of `block`
// if the parent had a delivered (canonical, non-orphaned) payload — i.e., the conditions
// under which the spec's process_parent_execution_payload would settle the parent's payment
// into BuilderPendingWithdrawals. The entry is nil if the parent is unknown, has no payload,
// was orphaned, wasn't built by a registered builder or bid zero. The returned ref identifies
// the resolved payload block (the settle target) even when no entry is created, or nil if it
// couldn't be resolved.
func (sim *stateSimulator) resolveParentDirectEntry(block *Block) (*trackedBuilderWithdrawal, *parentPayloadRef) {
	parentRoot := block.GetParentRoot()
	if parentRoot == nil {
		return nil, nil
	}

	blockIndex := block.GetBlockIndex(sim.indexer.ctx)
	if blockIndex == nil {
		return nil, nil
	}

	blockParentPayload := blockIndex.ExecutionParentHash
	if bytes.Equal(blockParentPayload[:], zeroHash[:]) {
		return nil, nil
	}

	// Cache lookup
	if parentPayloadBlocks := sim.indexer.GetBlocksByExecutionBlockHash(blockParentPayload); len(parentPayloadBlocks) > 0 {
		var parentPayloadBlock *Block
		for _, parentBlock := range parentPayloadBlocks {
			if isParent, _ := sim.indexer.GetBlockDistance(parentBlock.Root, block.Root); isParent {
				parentPayloadBlock = parentBlock
				break
			}
		}

		if parentPayloadBlock != nil {
			ref := &parentPayloadRef{
				slot: parentPayloadBlock.Slot,
				root: parentPayloadBlock.Root,
			}

			blockIndex := parentPayloadBlock.GetBlockIndex(sim.indexer.ctx)
			if blockIndex == nil || blockIndex.BuilderIndex == math.MaxUint64 || blockIndex.BidValue == 0 {
				// settle_builder_payment only appends an entry when the bid value is > 0
				return nil, ref
			}

			uid := parentPayloadBlock.BlockUID
			return &trackedBuilderWithdrawal{
				BuilderPendingWithdrawal: gloas.BuilderPendingWithdrawal{
					BuilderIndex: gloas.BuilderIndex(blockIndex.BuilderIndex),
					Amount:       phase0.Gwei(blockIndex.BidValue),
				},
				Type:        dbtypes.WithdrawalTypeBuilderPayment,
				RefBlockUID: &uid,
			}, ref
		}
	}

	// Fall back to DB for pruned parents (e.g., during historical replay)
	parentSlots := db.GetSlotsByBlockHash(sim.indexer.ctx, blockParentPayload[:])

	var parentPayloadSlot *dbtypes.Slot
	if len(parentSlots) > 1 {
		// determinate correct block by parent hash links (small hop count)
		parentRoot := block.GetParentRoot()
		if parentRoot == nil {
			return nil, nil
		}

		minSlot := parentSlots[0].Slot
		maxSlot := block.Slot - 1
		for i := 0; i < len(parentSlots); i++ {
			if parentSlots[i].Slot < minSlot {
				minSlot = parentSlots[i].Slot
			}
		}

		slots := db.GetSlotsRange(sim.indexer.ctx, uint64(maxSlot), uint64(minSlot), false, true)
		slotsMap := make(map[phase0.Root]*dbtypes.Slot)
		for _, slot := range slots {
			slotsMap[phase0.Root(slot.Block.Root)] = slot.Block
		}

	parentloop:
		for parentSlot := slotsMap[*parentRoot]; parentSlot != nil; parentSlot = slotsMap[phase0.Root(parentSlot.ParentRoot)] {
			for i := 0; i < len(parentSlots); i++ {
				if bytes.Equal(parentSlots[i].Root, parentSlot.Root[:]) {
					parentPayloadSlot = parentSlots[i]
					break parentloop
				}
			}
		}
	} else if len(parentSlots) == 1 {
		parentPayloadSlot = parentSlots[0]
	}

	if parentPayloadSlot == nil {
		return nil, nil
	}

	ref := &parentPayloadRef{
		slot: phase0.Slot(parentPayloadSlot.Slot),
		root: phase0.Root(parentPayloadSlot.Root),
	}

	if parentPayloadSlot.BuilderIndex < 0 || parentPayloadSlot.EthBidValue == 0 {
		// settle_builder_payment only appends an entry when the bid value is > 0
		return nil, ref
	}

	uid := parentPayloadSlot.BlockUid
	return &trackedBuilderWithdrawal{
		BuilderPendingWithdrawal: gloas.BuilderPendingWithdrawal{
			BuilderIndex: gloas.BuilderIndex(parentPayloadSlot.BuilderIndex),
			Amount:       phase0.Gwei(parentPayloadSlot.EthBidValue),
		},
		Type:        dbtypes.WithdrawalTypeBuilderPayment,
		RefBlockUID: &uid,
	}, ref
}

// isPreEpochSettle returns true when the resolved settle target lies before the epoch's
// dependent block. In that case the spec's gated process_withdrawals for this payload
// chain link ran at a block in a previous epoch, and its settle, drain and partial sweep
// are already reflected in the epoch state.
func (sim *stateSimulator) isPreEpochSettle(parentRef *parentPayloadRef) bool {
	if parentRef == nil {
		return false
	}

	chainState := sim.indexer.consensusPool.GetChainState()
	if parentRef.slot >= chainState.EpochToSlot(sim.epochStats.epoch) {
		return false
	}

	dependentRoot := sim.epochStats.GetDependentRoot()

	return !bytes.Equal(parentRef.root[:], dependentRoot[:])
}

// resolveInitialDirectRefs populates RefBlockUID for the first directCount entries
// in the builder pending withdrawals queue. These are direct payments from the
// previous epoch's delivered payloads, loaded from the epoch boundary state without
// source block information. We scan the previous epoch's blocks in slot order and
// match delivered payloads to queue entries by FIFO position.
func (sim *stateSimulator) resolveInitialDirectRefs(state *stateSimulatorState, directCount int) {
	if sim.epochStats.epoch == 0 {
		return
	}

	prevEpoch := sim.epochStats.epoch - 1
	type settledPayment struct {
		blockUID uint64
		amount   phase0.Gwei
	}
	settledPayments := []settledPayment{}
	dependentRoot := sim.epochStats.GetDependentRoot()
	curPayloadHash := sim.epochStatsValues.DependentExecutionHash

	_, prunedEpoch := sim.indexer.GetBlockCacheState()
	if prevEpoch >= prunedEpoch {
		// Previous epoch is in cache
		for curBlock := sim.indexer.blockCache.getBlockByRoot(dependentRoot); curBlock != nil; {
			curBlockIndex := curBlock.GetBlockIndex(sim.indexer.ctx)
			if curBlockIndex == nil {
				break
			}
			if bytes.Equal(curBlockIndex.ExecutionHash[:], curPayloadHash[:]) {
				// zero-value bids (incl. self-builds) are part of the payload chain,
				// but settle_builder_payment never queued a payment for them
				if curBlockIndex.BuilderIndex != math.MaxUint64 && curBlockIndex.BidValue > 0 {
					settledPayments = append(settledPayments, settledPayment{
						blockUID: curBlock.BlockUID,
						amount:   phase0.Gwei(curBlockIndex.BidValue),
					})
				}
				curPayloadHash = curBlockIndex.ExecutionParentHash
			}

			parentRoot := curBlock.GetParentRoot()
			if parentRoot == nil {
				break
			}
			curBlock = sim.indexer.blockCache.getBlockByRoot(*parentRoot)
		}
	} else {
		// Previous epoch is finalized/pruned — query DB. GetSlotsRange returns slots in
		// descending order, so iterate it in reverse to walk slots ascending (queue order).
		chainState := sim.indexer.consensusPool.GetChainState()
		prevStart := chainState.EpochToSlot(prevEpoch)
		prevEnd := chainState.EpochToSlot(prevEpoch + 1)

		dbSlots := db.GetSlotsRange(sim.indexer.ctx, uint64(prevEnd-1), uint64(prevStart), false, true)
		dbSlotsMap := make(map[phase0.Root]*dbtypes.Slot)
		for _, slot := range dbSlots {
			dbSlotsMap[phase0.Root(slot.Block.Root)] = slot.Block
		}

		for curSlot := dbSlotsMap[dependentRoot]; curSlot != nil; curSlot = dbSlotsMap[phase0.Root(curSlot.ParentRoot)] {
			if bytes.Equal(curSlot.EthBlockHash[:], curPayloadHash[:]) {
				if curSlot.BuilderIndex >= 0 && curSlot.EthBidValue > 0 {
					settledPayments = append(settledPayments, settledPayment{
						blockUID: curSlot.BlockUid,
						amount:   phase0.Gwei(curSlot.EthBidValue),
					})
				}
				curPayloadHash = phase0.Hash32(curSlot.EthBlockParentHash)
			}
		}
	}

	// match the first directCount withdrawals with the settled payments, starting at the end,
	// leaving unknown items at the beginning
	for i := 0; i < len(settledPayments) && i < directCount; i++ {
		uid := settledPayments[i].blockUID
		pendingIndex := directCount - i - 1

		if state.builderPendingWithdrawals[pendingIndex].Amount != settledPayments[i].amount {
			// alignment broken (e.g. bid value missing for pre-migration db rows) -
			// stop matching, remaining entries stay unknown
			break
		}

		state.builderPendingWithdrawals[pendingIndex].RefBlockUID = &uid
		state.builderPendingWithdrawals[pendingIndex].Type = dbtypes.WithdrawalTypeBuilderPayment // direct payment
	}
}

// resolveDelayedPaymentBlocks returns a map of slot to block UID for the delayed payments
func (sim *stateSimulator) resolveDelayedPaymentBlocks() map[phase0.Slot]uint64 {
	res := make(map[phase0.Slot]uint64)
	if sim.epochStats.epoch <= 1 {
		return res
	}

	prevEpoch := sim.epochStats.epoch - 1
	dependentRoot := sim.epochStats.GetDependentRoot()
	chainState := sim.indexer.consensusPool.GetChainState()

	sourceHeadRoot := dependentRoot
	dbMaxSlot := chainState.EpochToSlot(sim.epochStats.epoch)

	_, prunedEpoch := sim.indexer.GetBlockCacheState()
	if prevEpoch >= prunedEpoch {
		// Previous epoch is in cache
		if curBlock := sim.indexer.blockCache.getBlockByRoot(dependentRoot); curBlock != nil {
			for {
				if chainState.EpochOfSlot(curBlock.Slot) < prevEpoch {
					break
				}

				dbMaxSlot = curBlock.Slot

				parentRoot := curBlock.GetParentRoot()
				if parentRoot == nil {
					break
				}

				sourceHeadRoot = *parentRoot

				curBlock = sim.indexer.blockCache.getBlockByRoot(*parentRoot)
				if curBlock == nil {
					break
				}
			}
		}
	}

	// Delayed payments processed at the transition into epoch E originate from epoch E-2:
	// written to the second half of builder_pending_payments during E-2, shifted to the
	// first half at the E-1 boundary, processed by process_builder_pending_payments at
	// the E boundary. The refs are slot offsets relative to E-2's first slot.
	sourceEpoch := sim.epochStats.epoch - 2
	sourceOffset := chainState.EpochToSlot(sourceEpoch)

	if sourceEpoch >= prunedEpoch {
		for curBlock := sim.indexer.blockCache.getBlockByRoot(sourceHeadRoot); curBlock != nil; {
			if chainState.EpochOfSlot(curBlock.Slot) < sourceEpoch {
				break
			}

			res[phase0.Slot(curBlock.Slot-sourceOffset)] = curBlock.BlockUID

			parentRoot := curBlock.GetParentRoot()
			if parentRoot == nil {
				break
			}

			curBlock = sim.indexer.blockCache.getBlockByRoot(*parentRoot)
			if curBlock == nil {
				break
			}
		}
	} else {
		prevStart := chainState.EpochToSlot(sourceEpoch)
		dbSlots := db.GetBlockHeadBySlotRange(sim.indexer.ctx, uint64(prevStart), uint64(dbMaxSlot-1))
		dbSlotsMap := make(map[phase0.Root]*dbtypes.BlockHead)
		for _, slot := range dbSlots {
			dbSlotsMap[phase0.Root(slot.Root)] = slot
		}

		if prevEpoch < prunedEpoch {
			for curSlot := dbSlotsMap[dependentRoot]; curSlot != nil; curSlot = dbSlotsMap[phase0.Root(curSlot.ParentRoot)] {
				if chainState.EpochOfSlot(phase0.Slot(curSlot.Slot)) < prevEpoch {
					break
				}

				sourceHeadRoot = phase0.Root(curSlot.ParentRoot)
			}
		}

		for curSlot := dbSlotsMap[sourceHeadRoot]; curSlot != nil; curSlot = dbSlotsMap[phase0.Root(curSlot.ParentRoot)] {
			if chainState.EpochOfSlot(phase0.Slot(curSlot.Slot)) < sourceEpoch {
				break
			}

			res[phase0.Slot(curSlot.Slot)-sourceOffset] = curSlot.BlockUid
		}
	}

	return res
}

func (sim *stateSimulator) getValidator(index phase0.ValidatorIndex) *phase0.Validator {
	if validator, ok := sim.prevState.validatorMap[index]; ok {
		return validator
	}

	var validator *phase0.Validator

	if sim.validatorSet != nil && len(sim.validatorSet) > int(index) {
		valdata := sim.validatorSet[index]
		validator = &phase0.Validator{
			PublicKey:                  valdata.PublicKey,
			WithdrawalCredentials:      valdata.WithdrawalCredentials,
			EffectiveBalance:           valdata.EffectiveBalance,
			Slashed:                    valdata.Slashed,
			ActivationEligibilityEpoch: valdata.ActivationEligibilityEpoch,
			ActivationEpoch:            valdata.ActivationEpoch,
			ExitEpoch:                  valdata.ExitEpoch,
			WithdrawableEpoch:          valdata.WithdrawableEpoch,
		}
	} else {
		validator = sim.indexer.validatorCache.getValidatorByIndexAndRoot(index, sim.prevState.epochRoot)
	}

	sim.prevState.validatorMap[index] = validator
	return validator
}

func (sim *stateSimulator) applyConsolidation(consolidation *electra.ConsolidationRequest) uint8 {
	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()

	// this follows the spec logic:
	// https://github.com/ethereum/consensus-specs/blob/dev/specs/electra/beacon-chain.md#new-process_consolidation_request

	sourceIndice, sourceFound := sim.indexer.pubkeyCache.Get(consolidation.SourcePubkey)
	if !sourceFound {
		return dbtypes.ConsolidationRequestResultSrcNotFound
	}

	targetIndice, targetFound := sim.indexer.pubkeyCache.Get(consolidation.TargetPubkey)
	if !targetFound {
		return dbtypes.ConsolidationRequestResultTgtNotFound
	}

	srcValidator := sim.getValidator(sourceIndice)
	if srcValidator == nil {
		return dbtypes.ConsolidationRequestResultSrcNotFound
	}

	tgtValidator := sim.getValidator(targetIndice)
	if tgtValidator == nil {
		return dbtypes.ConsolidationRequestResultTgtNotFound
	}

	srcWithdrawalCreds := srcValidator.WithdrawalCredentials
	if srcWithdrawalCreds[0] == 0x00 {
		return dbtypes.ConsolidationRequestResultSrcInvalidCredentials
	}

	if !bytes.Equal(srcWithdrawalCreds[12:], consolidation.SourceAddress[:]) {
		return dbtypes.ConsolidationRequestResultSrcInvalidSender
	}

	tgtWithdrawalCreds := tgtValidator.WithdrawalCredentials
	if tgtWithdrawalCreds[0] == 0x00 {
		return dbtypes.ConsolidationRequestResultTgtInvalidCredentials
	}

	if srcValidator == tgtValidator {
		// self consolidation, set withdrawal credentials to 0x02
		if srcValidator.WithdrawalCredentials[0] == 0x01 {
			withdrawalCreds := make([]byte, 32)
			copy(withdrawalCreds, srcValidator.WithdrawalCredentials)
			withdrawalCreds[0] = 0x02
			srcValidator.WithdrawalCredentials = withdrawalCreds
		}
		return dbtypes.ConsolidationRequestResultSuccess
	}

	if sim.prevState.pendingConsolidationCount >= chainSpec.PendingConsolidationsLimit {
		return dbtypes.ConsolidationRequestResultQueueFull
	}

	if tgtValidator.WithdrawalCredentials[0] != 0x02 {
		return dbtypes.ConsolidationRequestResultTgtNotCompounding
	}

	// get_balance_churn_limit
	balanceChurnLimit := uint64(sim.epochStatsValues.EffectiveBalance) / chainSpec.ChurnLimitQuotient
	if chainSpec.MinPerEpochChurnLimitElectra > balanceChurnLimit {
		balanceChurnLimit = chainSpec.MinPerEpochChurnLimitElectra
	}
	balanceChurnLimit = balanceChurnLimit - (balanceChurnLimit % chainSpec.EffectiveBalanceIncrement)

	// get_activation_exit_churn_limit
	activationExitChurnLimit := balanceChurnLimit
	if chainSpec.MaxPerEpochActivationExitChurnLimit < activationExitChurnLimit {
		activationExitChurnLimit = chainSpec.MaxPerEpochActivationExitChurnLimit
	}

	// get_consolidation_churn_limit
	consolidationChurnLimit := balanceChurnLimit - activationExitChurnLimit

	// check consolidationChurnLimit
	if consolidationChurnLimit <= chainSpec.MinActivationBalance {
		return dbtypes.ConsolidationRequestResultTotalBalanceTooLow
	}

	if srcValidator.ActivationEpoch == FarFutureEpoch || srcValidator.ActivationEpoch > sim.epochStats.epoch || srcValidator.ExitEpoch < FarFutureEpoch {
		return dbtypes.ConsolidationRequestResultSrcNotActive
	}

	if tgtValidator.ActivationEpoch == FarFutureEpoch || tgtValidator.ActivationEpoch > sim.epochStats.epoch || tgtValidator.ExitEpoch < FarFutureEpoch {
		return dbtypes.ConsolidationRequestResultTgtNotActive
	}

	if sim.epochStats.epoch < srcValidator.ActivationEpoch+phase0.Epoch(chainSpec.ShardCommitteePeriod) {
		return dbtypes.ConsolidationRequestResultSrcNotOldEnough
	}

	// check pending withdrawals for source
	pendingWithdrawals := 0
	for _, pendingWithdrawal := range sim.prevState.pendingWithdrawals {
		if pendingWithdrawal.ValidatorIndex == sourceIndice {
			pendingWithdrawals++
			break
		}
	}

	if pendingWithdrawals > 0 || slices.Contains(sim.prevState.additionalWithdrawals, sourceIndice) {
		return dbtypes.ConsolidationRequestResultSrcHasPendingWithdrawal
	}

	sim.prevState.pendingConsolidationCount++
	srcValidator.ExitEpoch = FarFutureEpoch - 1 // dummy value to indicate the validator is exiting, but we don't know when exactly
	return dbtypes.ConsolidationRequestResultSuccess
}

func (sim *stateSimulator) applyWithdrawal(withdrawal *electra.WithdrawalRequest) uint8 {
	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()

	// this follows the spec logic:
	// https://github.com/ethereum/consensus-specs/blob/dev/specs/electra/beacon-chain.md#new-process_withdrawal_request

	if uint64(len(sim.prevState.pendingWithdrawals)+len(sim.prevState.additionalWithdrawals)) >= chainSpec.PendingPartialWithdrawalsLimit {
		return dbtypes.WithdrawalRequestResultQueueFull
	}

	validatorIndice, validatorFound := sim.indexer.pubkeyCache.Get(withdrawal.ValidatorPubkey)
	if !validatorFound {
		return dbtypes.WithdrawalRequestResultValidatorNotFound
	}

	validator := sim.getValidator(validatorIndice)
	if validator == nil {
		return dbtypes.WithdrawalRequestResultValidatorNotFound
	}

	srcWithdrawalCreds := validator.WithdrawalCredentials
	if srcWithdrawalCreds[0] == 0x00 {
		return dbtypes.WithdrawalRequestResultValidatorInvalidCredentials
	}

	if !bytes.Equal(srcWithdrawalCreds[12:], withdrawal.SourceAddress[:]) {
		return dbtypes.WithdrawalRequestResultValidatorInvalidSender
	}

	if validator.ActivationEpoch == FarFutureEpoch || validator.ActivationEpoch > sim.epochStats.epoch || validator.ExitEpoch < FarFutureEpoch {
		return dbtypes.WithdrawalRequestResultValidatorNotActive
	}

	if sim.epochStats.epoch < validator.ActivationEpoch+phase0.Epoch(chainSpec.ShardCommitteePeriod) {
		return dbtypes.WithdrawalRequestResultValidatorNotOldEnough
	}

	if withdrawal.Amount == 0 {
		pendingWithdrawals := 0
		for _, pendingWithdrawal := range sim.prevState.pendingWithdrawals {
			if pendingWithdrawal.ValidatorIndex == validatorIndice {
				pendingWithdrawals++
				break
			}
		}

		if pendingWithdrawals > 0 || slices.Contains(sim.prevState.additionalWithdrawals, validatorIndice) {
			return dbtypes.WithdrawalRequestResultValidatorHasPendingWithdrawal
		}

		validator.ExitEpoch = FarFutureEpoch - 1 // dummy value to indicate the validator is exiting, but we don't know when exactly
		return dbtypes.WithdrawalRequestResultSuccess
	}

	if validator.WithdrawalCredentials[0] != 0x02 {
		return dbtypes.WithdrawalRequestResultValidatorNotCompounding
	}

	if validator.EffectiveBalance < phase0.Gwei(chainSpec.MinActivationBalance) {
		return dbtypes.WithdrawalRequestResultValidatorBalanceTooLow
	}

	sim.prevState.additionalWithdrawals = append(sim.prevState.additionalWithdrawals, validatorIndice)

	return dbtypes.WithdrawalRequestResultSuccess
}

func (sim *stateSimulator) applyBlock(block *Block, applyPayload bool) [][]uint8 {
	if sim.prevState.block != nil && sim.prevState.block.Slot >= block.Slot {
		return nil
	}

	blockBody := block.GetBlock(sim.indexer.ctx)
	if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
		return nil
	}

	body := blockBody.Message.Body

	// process builder pending withdrawals (come first in the spec)
	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()

	if applyPayload {
		// resolve parent direct payment
		// When the settle target lies before the dependent block, the gated
		// process_withdrawals ran at a block in a previous epoch - its settle, drain and
		// partial sweep are already reflected in the epoch state and the spec
		// early-returns at this block (empty parent), so skip all withdrawal processing.
		entry, parentRef := sim.resolveParentDirectEntry(block)
		if !sim.isPreEpochSettle(parentRef) {
			if entry != nil {
				sim.prevState.builderPendingWithdrawals = append(sim.prevState.builderPendingWithdrawals, *entry)
			}

			// process_withdrawals drains the builder withdrawal queue (up to MAX_WITHDRAWALS_PER_PAYLOAD-1 per spec).
			if uint64(len(sim.prevState.builderPendingWithdrawals)) > chainSpec.MaxWithdrawalsPerPayload-1 {
				sim.prevState.builderPendingWithdrawals = sim.prevState.builderPendingWithdrawals[chainSpec.MaxWithdrawalsPerPayload-1:]
			} else if len(sim.prevState.builderPendingWithdrawals) > 0 {
				sim.prevState.builderPendingWithdrawals = sim.prevState.builderPendingWithdrawals[:0]
			}

			// process pending partial withdrawals
			processedWithdrawals := uint64(0)
			skippedWithdrawals := uint64(0)
			for _, pendingWithdrawal := range sim.prevState.pendingWithdrawals {
				if pendingWithdrawal.WithdrawableEpoch > sim.epochStats.epoch {
					break
				}

				srcValidator := sim.getValidator(pendingWithdrawal.ValidatorIndex)
				if srcValidator == nil {
					return nil
				}

				if srcValidator.ExitEpoch != FarFutureEpoch || srcValidator.EffectiveBalance < phase0.Gwei(chainSpec.MinActivationBalance) {
					skippedWithdrawals++
					continue
				}

				processedWithdrawals++
				if processedWithdrawals >= chainSpec.MaxPendingPartialsPerWithdrawalsSweep {
					break
				}
			}
			if processedWithdrawals+skippedWithdrawals > 0 {
				sim.prevState.pendingWithdrawals = sim.prevState.pendingWithdrawals[processedWithdrawals+skippedWithdrawals:]
			}
		}
	}

	// apply bls changes
	for _, blsChange := range body.BLSToExecutionChanges {
		validatorIndex := blsChange.Message.ValidatorIndex

		validator := sim.getValidator(validatorIndex)
		if validator == nil {
			return nil
		}

		wdcredsPrefix := []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		validator.WithdrawalCredentials = append(wdcredsPrefix, blsChange.Message.ToExecutionAddress[:]...)
	}

	// apply slashings
	for _, proposerSlashing := range body.ProposerSlashings {
		proposerIndex := proposerSlashing.SignedHeader1.Message.ProposerIndex
		validator := sim.getValidator(proposerIndex)
		if validator == nil {
			return nil
		}

		validator.Slashed = true
		validator.ExitEpoch = FarFutureEpoch - 1 // dummy value to indicate the validator is exiting, but we don't know when exactly
	}

	for _, attesterSlashing := range body.AttesterSlashings {
		if attesterSlashing == nil || attesterSlashing.Attestation1 == nil || attesterSlashing.Attestation2 == nil {
			continue
		}

		att1Indices := attesterSlashing.Attestation1.AttestingIndices
		att2Indices := attesterSlashing.Attestation2.AttestingIndices
		if att1Indices == nil || att2Indices == nil {
			continue
		}

		for _, valIdx := range utils.FindMatchingIndices(att1Indices, att2Indices) {
			validator := sim.getValidator(phase0.ValidatorIndex(valIdx))
			if validator == nil {
				return nil
			}

			validator.Slashed = true
			validator.ExitEpoch = FarFutureEpoch - 1 // dummy value to indicate the validator is exiting, but we don't know when exactly
		}
	}

	// apply voluntary exits
	for _, voluntaryExit := range body.VoluntaryExits {
		validator := sim.getValidator(voluntaryExit.Message.ValidatorIndex)
		if validator == nil {
			return nil
		}

		validator.ExitEpoch = FarFutureEpoch - 1 // dummy value to indicate the validator is exiting, but we don't know when exactly
	}

	// get execution requests
	requests := body.ExecutionRequests
	if requests == nil {
		return nil
	}

	results := make([][]uint8, 2)

	if applyPayload {
		// apply withdrawal requests
		results[0] = make([]uint8, len(requests.Withdrawals))
		for i, withdrawal := range requests.Withdrawals {
			results[0][i] = sim.applyWithdrawal(withdrawal)
		}

		// apply consolidation requests
		results[1] = make([]uint8, len(requests.Consolidations))
		for i, consolidation := range requests.Consolidations {
			results[1][i] = sim.applyConsolidation(consolidation)
		}
	}

	sim.prevState.block = block
	sim.prevState.blockPayloadApplied = applyPayload

	return results
}

func (sim *stateSimulator) replayBlockResults(block *Block) [][]uint8 {
	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()
	if chainSpec.ElectraForkEpoch == nil || sim.epochStats.epoch < phase0.Epoch(*chainSpec.ElectraForkEpoch) {
		return nil
	}

	block.blockResultsMutex.Lock()
	defer block.blockResultsMutex.Unlock()

	if len(block.blockResults) > 0 {
		return block.blockResults
	}

	parentBlocks := sim.getParentBlocks(block)

	canReuseParentState := false
	if sim.prevState != nil && sim.prevState.block != nil {
		if sim.prevState.block.Slot == block.Slot {
			canReuseParentState = bytes.Equal(sim.prevState.block.Root[:], block.Root[:])
		} else if sim.prevState.block.Slot < block.Slot {
			for _, parentBlock := range parentBlocks {
				if parentBlock.block.Slot == sim.prevState.block.Slot {
					// Only reuse if the block was applied with the same payload-inclusion
					// flag the current chain walk demands - the drained withdrawal queue
					// and applied execution requests differ otherwise.
					canReuseParentState = bytes.Equal(parentBlock.block.Root[:], sim.prevState.block.Root[:]) &&
						parentBlock.payloadIncluded == sim.prevState.blockPayloadApplied
					break
				}
			}
		}
	}

	if !canReuseParentState {
		state := sim.resetState(block)
		if state == nil {
			return nil
		}
	}

	if sim.prevState.block == block {
		return sim.prevState.blockResults
	}

	// replay parent blocks up to the current block and apply all relevant operations
	for _, parentBlock := range parentBlocks {
		sim.applyBlock(parentBlock.block, parentBlock.payloadIncluded)
	}

	// apply current block and store results
	results := sim.applyBlock(block, true)
	sim.prevState.blockResults = results
	block.blockResults = results

	return results
}

// builderPaymentClassification holds the type and reference slot for a single builder payment withdrawal.
type builderPaymentClassification struct {
	Type    uint8
	RefSlot *uint64
}

// withdrawalSimResult holds the result of simulating pending withdrawals for a block.
type withdrawalSimResult struct {
	BuilderPaymentCount uint64
	BuilderPayments     []builderPaymentClassification // one per builder payment (first BuilderPaymentCount entries)
	PartialCount        int
}

// replayWithdrawalState simulates the pending withdrawal queue for the given block
// and returns classification info for builder payments and counts of each category.
// The epoch state is always the pre-state of the first slot (post-epoch-transition),
// so all blocks from the epoch start are replayed uniformly — no first-slot special casing.
func (sim *stateSimulator) replayWithdrawalState(block *Block) *withdrawalSimResult {
	result := &withdrawalSimResult{}

	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()
	if chainSpec.MaxPendingPartialsPerWithdrawalsSweep == 0 {
		return result
	}

	// Replay state up to (but not including) target block
	parentBlocks := sim.getParentBlocks(block)
	state := sim.resetState(block)
	if state == nil {
		return result
	}
	for _, parentBlock := range parentBlocks {
		sim.applyBlock(parentBlock.block, parentBlock.payloadIncluded)
	}

	// apply parent block direct payment
	entry, parentRef := sim.resolveParentDirectEntry(block)
	carryOver := sim.isPreEpochSettle(parentRef)
	if carryOver {
		// The spec computed this block's payload withdrawals at a gated block in a
		// previous epoch (process_withdrawals early-returns on empty parents and
		// payload_expected_withdrawals carries over) - classify them with a simulator
		// of that epoch at the gated block.
		if carryOverResult := sim.replayCarryOverWithdrawalState(block, parentRef); carryOverResult != nil {
			return carryOverResult
		}
		// gated epoch state unavailable - fall through with unknown classifications
	}

	if entry != nil {
		sim.prevState.builderPendingWithdrawals = append(sim.prevState.builderPendingWithdrawals, *entry)
	}

	// Builder payment classification
	builderCount := min(uint64(len(sim.prevState.builderPendingWithdrawals)), chainSpec.MaxWithdrawalsPerPayload-1)
	result.BuilderPaymentCount = builderCount

	if builderCount > 0 {
		builderPayments := sim.prevState.builderPendingWithdrawals[:builderCount]
		result.BuilderPayments = make([]builderPaymentClassification, len(builderPayments))
		for i, payment := range builderPayments {
			if carryOver {
				// local queue doesn't reflect the actual withdrawal source state
				result.BuilderPayments[i] = builderPaymentClassification{
					Type: dbtypes.WithdrawalTypeBuilderUnknownPayment,
				}
			} else {
				result.BuilderPayments[i] = builderPaymentClassification{
					Type:    payment.Type,
					RefSlot: payment.RefBlockUID,
				}
			}
		}
	}

	// Count pending partial withdrawals
	for _, pw := range sim.prevState.pendingWithdrawals {
		if pw.WithdrawableEpoch > sim.epochStats.epoch {
			break
		}
		validator := sim.getValidator(pw.ValidatorIndex)
		if validator == nil {
			break
		}
		if validator.ExitEpoch != FarFutureEpoch || validator.EffectiveBalance < phase0.Gwei(chainSpec.MinActivationBalance) {
			continue
		}
		result.PartialCount++
		if uint64(result.PartialCount) >= chainSpec.MaxPendingPartialsPerWithdrawalsSweep {
			break
		}
	}

	return result
}

// replayCarryOverWithdrawalState classifies the withdrawals of a block whose expected
// withdrawals were computed at a gated block in a previous epoch. process_withdrawals
// early-returns on empty parents and payload_expected_withdrawals carries over, so the
// withdrawals in this block's payload equal those computed at the chain child of the
// payload block it builds on. Returns nil if the gated block or its epoch state is
// unavailable.
func (sim *stateSimulator) replayCarryOverWithdrawalState(block *Block, parentRef *parentPayloadRef) *withdrawalSimResult {
	// find the gated block: the chain child of the payload block this block builds on
	var gatedBlock *Block
	for curBlock := block; curBlock != nil; {
		parentRoot := curBlock.GetParentRoot()
		if parentRoot == nil {
			return nil
		}
		if bytes.Equal(parentRoot[:], parentRef.root[:]) {
			gatedBlock = curBlock
			break
		}
		curBlock = sim.indexer.blockCache.getBlockByRoot(*parentRoot)
	}
	if gatedBlock == nil || bytes.Equal(gatedBlock.Root[:], block.Root[:]) {
		return nil
	}

	chainState := sim.indexer.consensusPool.GetChainState()
	gatedEpoch := chainState.EpochOfSlot(gatedBlock.Slot)
	if gatedEpoch >= sim.epochStats.epoch {
		return nil
	}

	gatedEpochStats := sim.indexer.epochCache.getEpochStatsByEpochAndRoot(gatedEpoch, gatedBlock.Root)
	if gatedEpochStats == nil {
		return nil
	}

	subSim := newStateSimulator(sim.indexer, gatedEpochStats)
	if subSim == nil {
		return nil
	}

	return subSim.replayWithdrawalState(gatedBlock)
}
