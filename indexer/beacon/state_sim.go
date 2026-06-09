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

type stateSimulatorState struct {
	epochRoot                 phase0.Root
	block                     *Block
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
	// DelayedBuilderPaymentCount.
	delayedCount := int(sim.epochStatsValues.DelayedBuilderPaymentCount)
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
	if delayedCount > 0 {
		sim.resolveInitialDelayedRefs(state, delayedCount)
	}

	return state
}

// resolveParentDirectEntry constructs a trackedBuilderWithdrawal for the parent of `block`
// if the parent had a delivered (canonical, non-orphaned) payload — i.e., the conditions
// under which the spec's process_parent_execution_payload would settle the parent's payment
// into BuilderPendingWithdrawals. Returns nil if the parent is unknown, has no payload, was
// orphaned, or wasn't built by a registered builder.
func (sim *stateSimulator) resolveParentDirectEntry(block *Block) *trackedBuilderWithdrawal {
	parentRoot := block.GetParentRoot()
	if parentRoot == nil {
		return nil
	}

	blockIndex := block.GetBlockIndex(sim.indexer.ctx)
	if blockIndex == nil {
		return nil
	}

	blockParentPayload := blockIndex.ExecutionParentHash
	if bytes.Equal(blockParentPayload[:], zeroHash[:]) {
		return nil
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
			blockIndex := parentPayloadBlock.GetBlockIndex(sim.indexer.ctx)
			if blockIndex == nil || blockIndex.BuilderIndex == math.MaxUint64 {
				return nil
			}

			uid := parentPayloadBlock.BlockUID
			return &trackedBuilderWithdrawal{
				BuilderPendingWithdrawal: gloas.BuilderPendingWithdrawal{
					BuilderIndex: gloas.BuilderIndex(blockIndex.BuilderIndex),
				},
				Type:        dbtypes.WithdrawalTypeBuilderPayment,
				RefBlockUID: &uid,
			}
		}
	}

	// Fall back to DB for pruned parents (e.g., during historical replay)
	parentSlots := db.GetSlotsByBlockHash(sim.indexer.ctx, blockParentPayload[:])

	var parentPayloadSlot *dbtypes.Slot
	if len(parentSlots) > 1 {
		// determinate correct block by parent hash links (small hop count)
		parentRoot := block.GetParentRoot()
		if parentRoot == nil {
			return nil
		}

		minSlot := parentSlots[0].Slot
		maxSlot := block.Slot - 1
		for i := 0; i < len(parentSlots); i++ {
			if parentSlots[i].Slot < minSlot {
				minSlot = parentSlots[i].Slot
			}
		}

		slots := db.GetSlotsRange(sim.indexer.ctx, uint64(minSlot), uint64(maxSlot), false, true)
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
		return nil
	}
	if parentPayloadSlot.BuilderIndex < 0 {
		return nil
	}

	uid := parentPayloadSlot.BlockUid
	return &trackedBuilderWithdrawal{
		BuilderPendingWithdrawal: gloas.BuilderPendingWithdrawal{
			BuilderIndex: gloas.BuilderIndex(parentPayloadSlot.BuilderIndex),
		},
		Type:        dbtypes.WithdrawalTypeBuilderPayment,
		RefBlockUID: &uid,
	}
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
	blockUIDsWithCanonicalPayload := []uint64{}
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
				blockUIDsWithCanonicalPayload = append(blockUIDsWithCanonicalPayload, curBlock.BlockUID)
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
				blockUIDsWithCanonicalPayload = append(blockUIDsWithCanonicalPayload, curSlot.BlockUid)
				curPayloadHash = phase0.Hash32(curSlot.EthBlockParentHash)
			}
		}
	}

	// match the first directCount withdrawals with blocksWithCanonicalPayload, starting at the end, leaving unknown items at the beginning
	for i := 0; i < len(blockUIDsWithCanonicalPayload) && i < directCount; i++ {
		uid := blockUIDsWithCanonicalPayload[i]
		pendingIndex := directCount - i - 1

		state.builderPendingWithdrawals[pendingIndex].RefBlockUID = &uid
		state.builderPendingWithdrawals[pendingIndex].Type = dbtypes.WithdrawalTypeBuilderPayment // direct payment
	}
}

// resolveInitialDelayedRefs populates RefBlockUID for the delayed payment entries
// in the builder pending withdrawals queue. These are delayed payments from epoch
// n-2 missed payloads, appended to the queue by the state transition
func (sim *stateSimulator) resolveInitialDelayedRefs(state *stateSimulatorState, delayedCount int) {
	if sim.epochStats.epoch <= 1 {
		return
	}

	prevEpoch := sim.epochStats.epoch - 1
	dependentRoot := sim.epochStats.GetDependentRoot()
	chainState := sim.indexer.consensusPool.GetChainState()

	sourceHeadRoot := dependentRoot
	sourcePayloadHash := sim.epochStatsValues.DependentExecutionHash
	dbMaxSlot := chainState.EpochToSlot(sim.epochStats.epoch)

	_, prunedEpoch := sim.indexer.GetBlockCacheState()
	if prevEpoch >= prunedEpoch {
		// Previous epoch is in cache
		if curBlock := sim.indexer.blockCache.getBlockByRoot(dependentRoot); curBlock != nil {
			for {
				if chainState.EpochOfSlot(curBlock.Slot) < prevEpoch {
					break
				}

				if index := curBlock.GetBlockIndex(sim.indexer.ctx); index != nil {
					sourcePayloadHash = index.ExecutionParentHash
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

	type blockWithMissedPayload struct {
		slot    phase0.Slot
		builder gloas.BuilderIndex
	}
	blocksWithMissedPayload := []blockWithMissedPayload{}

	sourceEpoch := prevEpoch - 2
	if sourceEpoch >= prunedEpoch {
		for curBlock := sim.indexer.blockCache.getBlockByRoot(sourceHeadRoot); curBlock != nil; {
			if chainState.EpochOfSlot(curBlock.Slot) < sourceEpoch {
				break
			}

			blockIndex := curBlock.GetBlockIndex(sim.indexer.ctx)
			if blockIndex == nil {
				break
			}
			if !bytes.Equal(blockIndex.ExecutionHash[:], sourcePayloadHash[:]) && blockIndex.BuilderIndex != math.MaxUint64 {
				blocksWithMissedPayload = append(blocksWithMissedPayload, blockWithMissedPayload{
					slot:    curBlock.Slot,
					builder: gloas.BuilderIndex(blockIndex.BuilderIndex),
				})
			}

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
		dbSlots := db.GetSlotsRange(sim.indexer.ctx, uint64(dbMaxSlot-1), uint64(prevStart), false, true)
		dbSlotsMap := make(map[phase0.Root]*dbtypes.Slot)
		for _, slot := range dbSlots {
			dbSlotsMap[phase0.Root(slot.Block.Root)] = slot.Block
		}

		if prevEpoch < prunedEpoch {
			for curSlot := dbSlotsMap[dependentRoot]; curSlot != nil; curSlot = dbSlotsMap[phase0.Root(curSlot.ParentRoot)] {
				if chainState.EpochOfSlot(phase0.Slot(curSlot.Slot)) < prevEpoch {
					break
				}

				if bytes.Equal(curSlot.EthBlockHash[:], sourcePayloadHash[:]) {
					sourcePayloadHash = phase0.Hash32(curSlot.EthBlockParentHash)
				}
				sourceHeadRoot = phase0.Root(curSlot.ParentRoot)
			}
		}

		for curSlot := dbSlotsMap[sourceHeadRoot]; curSlot != nil; curSlot = dbSlotsMap[phase0.Root(curSlot.ParentRoot)] {
			if chainState.EpochOfSlot(phase0.Slot(curSlot.Slot)) < sourceEpoch {
				break
			}

			if !bytes.Equal(curSlot.EthBlockHash[:], sourcePayloadHash[:]) && curSlot.BuilderIndex != math.MaxInt64 {
				blocksWithMissedPayload = append(blocksWithMissedPayload, blockWithMissedPayload{
					slot:    phase0.Slot(curSlot.Slot),
					builder: gloas.BuilderIndex(curSlot.BuilderIndex),
				})
			}

			if bytes.Equal(curSlot.EthBlockHash[:], sourcePayloadHash[:]) {
				sourcePayloadHash = phase0.Hash32(curSlot.EthBlockParentHash)
			}
		}

	}

	// match the delayed payments with blocksWithMissedPayload, starting at the end, leaving unknown items at the beginning
	pendingIndex := len(state.builderPendingWithdrawals) - 1

	for i := 0; i < len(blocksWithMissedPayload) && i < delayedCount; i++ {
		if state.builderPendingWithdrawals[pendingIndex].BuilderIndex != blocksWithMissedPayload[i].builder {
			continue
		}

		uid := uint64(blocksWithMissedPayload[i].slot)
		state.builderPendingWithdrawals[pendingIndex].RefBlockUID = &uid
		pendingIndex--
	}
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
		if entry := sim.resolveParentDirectEntry(block); entry != nil {
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
					canReuseParentState = bytes.Equal(parentBlock.block.Root[:], sim.prevState.block.Root[:])
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
	if entry := sim.resolveParentDirectEntry(block); entry != nil {
		sim.prevState.builderPendingWithdrawals = append(sim.prevState.builderPendingWithdrawals, *entry)
	}

	// Builder payment classification
	builderCount := min(uint64(len(sim.prevState.builderPendingWithdrawals)), chainSpec.MaxWithdrawalsPerPayload-1)
	result.BuilderPaymentCount = builderCount

	if builderCount > 0 {
		builderPayments := sim.prevState.builderPendingWithdrawals[:builderCount]
		result.BuilderPayments = make([]builderPaymentClassification, len(builderPayments))
		for i, payment := range builderPayments {
			result.BuilderPayments[i] = builderPaymentClassification{
				Type:    payment.Type,
				RefSlot: payment.RefBlockUID,
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
