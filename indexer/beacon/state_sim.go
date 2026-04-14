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

// trackedBuilderWithdrawal pairs a builder pending withdrawal with the optional
// BlockUID of the block whose payload delivery created it. Entries loaded from
// the epoch boundary state have RefBlockUID == nil; entries added during replay
// carry the source block's UID.
type trackedBuilderWithdrawal struct {
	gloas.BuilderPendingWithdrawal
	RefBlockUID *uint64
}

type stateSimulatorState struct {
	epochRoot                 phase0.Root
	block                     *Block
	pendingWithdrawals        []electra.PendingPartialWithdrawal
	builderPendingWithdrawals []trackedBuilderWithdrawal
	builderDelayedCount       uint32 // how many entries in builderPendingWithdrawals are delayed/quorum payments
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
func (sim *stateSimulator) getParentBlocks(block *Block) []*Block {
	chainState := sim.indexer.consensusPool.GetChainState()
	simEpoch := sim.epochStats.epoch
	minSlot := chainState.EpochToSlot(simEpoch)

	parentBlocks := []*Block{}

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

		parentBlocks = append(parentBlocks, parentBlock)
		block = parentBlock
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
	trackedWithdrawals := make([]trackedBuilderWithdrawal, len(rawBuilderWithdrawals))
	for i := range rawBuilderWithdrawals {
		trackedWithdrawals[i] = trackedBuilderWithdrawal{BuilderPendingWithdrawal: rawBuilderWithdrawals[i]}
	}

	state := &stateSimulatorState{
		block:                     nil,
		epochRoot:                 epochRoot,
		pendingWithdrawals:        pendingWithdrawals,
		builderPendingWithdrawals: trackedWithdrawals,
		builderDelayedCount:       sim.epochStatsValues.DelayedBuilderPaymentCount, // delayed payments from epoch transition are at the tail
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
	directCount := len(trackedWithdrawals) - int(state.builderDelayedCount)
	if directCount > 0 {
		sim.resolveInitialDirectRefs(state, directCount)
	}

	return state
}

// resolveInitialDirectRefs populates RefBlockUID for the first directCount entries
// in the builder pending withdrawals queue. These are direct payments from the
// previous epoch's delivered payloads, loaded from the epoch boundary state without
// source block information. We scan the previous epoch's blocks in slot order and
// match delivered payloads to queue entries by FIFO position.
func (sim *stateSimulator) resolveInitialDirectRefs(state *stateSimulatorState, directCount int) {
	chainState := sim.indexer.consensusPool.GetChainState()

	if sim.epochStats.epoch == 0 {
		return
	}
	prevEpoch := sim.epochStats.epoch - 1
	prevStart := chainState.EpochToSlot(prevEpoch)
	prevEnd := chainState.EpochToSlot(prevEpoch + 1)

	resolved := 0
	_, prunedEpoch := sim.indexer.GetBlockCacheState()
	if prevEpoch >= prunedEpoch {
		// Previous epoch is in cache
		for slot := prevStart; slot < prevEnd && resolved < directCount; slot++ {
			blocks := sim.indexer.GetBlocksBySlot(slot)
			for _, b := range blocks {
				if b.HasExecutionPayload() && !b.isPayloadOrphaned {
					uid := b.BlockUID
					state.builderPendingWithdrawals[resolved].RefBlockUID = &uid
					resolved++
					if resolved >= directCount {
						break
					}
				}
			}
		}
	} else {
		// Previous epoch is finalized/pruned — query DB
		dbSlots := db.GetSlotsRange(sim.indexer.ctx, uint64(prevEnd-1), uint64(prevStart), false, false)
		for _, assignedSlot := range dbSlots {
			if resolved >= directCount {
				break
			}
			if assignedSlot.Block == nil {
				continue
			}
			if assignedSlot.Block.PayloadStatus == dbtypes.PayloadStatusCanonical {
				uid := assignedSlot.Block.BlockUid
				state.builderPendingWithdrawals[resolved].RefBlockUID = &uid
				resolved++
			}
		}
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

func (sim *stateSimulator) applyBlock(block *Block) [][]uint8 {
	if sim.prevState.block != nil && sim.prevState.block.Slot >= block.Slot {
		return nil
	}

	blockBody := block.GetBlock(sim.indexer.ctx)
	if blockBody == nil {
		return nil
	}

	// process builder pending withdrawals (come first in the spec)
	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()
	processedBuilderWithdrawals := uint32(len(sim.prevState.builderPendingWithdrawals))
	if processedBuilderWithdrawals > 0 {
		// Delayed entries are at the tail. When consuming N entries from the front,
		// the delayed count decreases by however many delayed entries were in that batch.
		// directCount = total - delayed; consumed from front = min(total, processed).
		// If we consume all: delayed consumed = delayed count.
		// If we consume partial: delayed consumed = max(0, processed - (total - delayed)).
		directCount := uint32(0)
		if processedBuilderWithdrawals > sim.prevState.builderDelayedCount {
			directCount = processedBuilderWithdrawals - sim.prevState.builderDelayedCount
		}
		delayedConsumed := processedBuilderWithdrawals - directCount
		if delayedConsumed > sim.prevState.builderDelayedCount {
			delayedConsumed = sim.prevState.builderDelayedCount
		}
		sim.prevState.builderDelayedCount -= delayedConsumed
		sim.prevState.builderPendingWithdrawals = sim.prevState.builderPendingWithdrawals[processedBuilderWithdrawals:]
	}

	// After processing withdrawals, check if this block has a full payload (direct payment added to queue)
	if block.HasExecutionPayload() && !block.isPayloadOrphaned {
		blockIndex := block.GetBlockIndex(sim.indexer.ctx)
		if blockIndex != nil && blockIndex.BuilderIndex != math.MaxUint64 {
			uid := block.BlockUID
			sim.prevState.builderPendingWithdrawals = append(sim.prevState.builderPendingWithdrawals, trackedBuilderWithdrawal{
				BuilderPendingWithdrawal: gloas.BuilderPendingWithdrawal{
					BuilderIndex: gloas.BuilderIndex(blockIndex.BuilderIndex),
				},
				RefBlockUID: &uid,
			})
			// builderDelayedCount stays the same — the new entry is direct, not delayed
		}
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

	// apply bls changes
	blsChanges, err := blockBody.BLSToExecutionChanges()
	if err != nil {
		return nil
	}
	for _, blsChange := range blsChanges {
		validatorIndex := blsChange.Message.ValidatorIndex

		validator := sim.getValidator(validatorIndex)
		if validator == nil {
			return nil
		}

		wdcredsPrefix := []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		validator.WithdrawalCredentials = append(wdcredsPrefix, blsChange.Message.ToExecutionAddress[:]...)
	}

	// apply slashings
	proposerSlashing, err := blockBody.ProposerSlashings()
	if err != nil {
		return nil
	}
	for _, proposerSlashing := range proposerSlashing {
		proposerIndex := proposerSlashing.SignedHeader1.Message.ProposerIndex
		validator := sim.getValidator(proposerIndex)
		if validator == nil {
			return nil
		}

		validator.Slashed = true
		validator.ExitEpoch = FarFutureEpoch - 1 // dummy value to indicate the validator is exiting, but we don't know when exactly
	}

	attesterSlashing, err := blockBody.AttesterSlashings()
	if err != nil {
		return nil
	}
	for _, attesterSlashing := range attesterSlashing {
		att1, _ := attesterSlashing.Attestation1()
		att2, _ := attesterSlashing.Attestation2()
		if att1 == nil || att2 == nil {
			continue
		}

		att1AttestingIndices, _ := att1.AttestingIndices()
		att2AttestingIndices, _ := att2.AttestingIndices()
		if att1AttestingIndices == nil || att2AttestingIndices == nil {
			continue
		}

		for _, valIdx := range utils.FindMatchingIndices(att1AttestingIndices, att2AttestingIndices) {
			validator := sim.getValidator(phase0.ValidatorIndex(valIdx))
			if validator == nil {
				return nil
			}

			validator.Slashed = true
			validator.ExitEpoch = FarFutureEpoch - 1 // dummy value to indicate the validator is exiting, but we don't know when exactly
		}
	}

	// apply voluntary exits
	voluntaryExits, err := blockBody.VoluntaryExits()
	if err != nil {
		return nil
	}
	for _, voluntaryExit := range voluntaryExits {
		validator := sim.getValidator(voluntaryExit.Message.ValidatorIndex)
		if validator == nil {
			return nil
		}

		validator.ExitEpoch = FarFutureEpoch - 1 // dummy value to indicate the validator is exiting, but we don't know when exactly
	}

	// get execution requests
	requests, err := blockBody.ExecutionRequests()
	if err != nil {
		return nil
	}

	results := make([][]uint8, 2)

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
				if parentBlock.Slot == sim.prevState.block.Slot {
					canReuseParentState = bytes.Equal(parentBlock.Root[:], sim.prevState.block.Root[:])
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
		sim.applyBlock(parentBlock)
	}

	// apply current block and store results
	results := sim.applyBlock(block)
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
	BuilderPaymentCount int
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
		sim.applyBlock(parentBlock)
	}

	// Builder payment classification
	builderCount := len(sim.prevState.builderPendingWithdrawals)
	result.BuilderPaymentCount = builderCount

	if builderCount > 0 {
		result.BuilderPayments = sim.classifyBuilderPayments(block, builderCount)
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

// classifyBuilderPayments determines the type and reference slot for each builder payment
// in the pending queue. The epoch pre-state queue layout is:
//
//	[direct_from_prev_payloads..., delayed_0, ..., delayed_N]
//
// Direct entries (from delivered payloads) are at the front — the payload transition
// (process_execution_payload) runs before the epoch transition. Delayed entries
// (from process_builder_pending_payments during the epoch transition) are at the tail.
// The number of delayed entries is known from DelayedBuilderPaymentCount.
//
// During block replay, each block consumes all entries from the front and may append
// a new direct entry at the back if it has a delivered payload.
func (sim *stateSimulator) classifyBuilderPayments(block *Block, builderCount int) []builderPaymentClassification {
	delayedCount := sim.prevState.builderDelayedCount

	payments := make([]builderPaymentClassification, builderCount)

	// Delayed entries are at the tail: positions [builderCount - delayedCount, builderCount)
	delayedStart := builderCount - int(delayedCount)

	// Resolve delayed entries by matching against blocks with missed/orphaned payloads
	// from the source epoch (2 epochs back). The delayed entries are generated in slot
	// order by process_builder_pending_payments, so we collect all candidate blocks
	// and assign them to delayed entries in order.
	delayedRefs := sim.resolveDelayedPaymentRefSlots(builderCount, block)

	for i := range payments {
		if i >= delayedStart && delayedStart >= 0 {
			// Delayed entry (tail of queue, from epoch transition)
			payments[i].Type = dbtypes.WithdrawalTypeBuilderDelayedPayment
			delayedOff := i - delayedStart
			if delayedOff < len(delayedRefs) {
				payments[i].RefSlot = delayedRefs[delayedOff]
			}
		} else {
			// Direct entry (from a delivered payload)
			payments[i].Type = dbtypes.WithdrawalTypeBuilderPayment
			// Use the tracked source block UID if available (set during replay).
			if i < len(sim.prevState.builderPendingWithdrawals) {
				payments[i].RefSlot = sim.prevState.builderPendingWithdrawals[i].RefBlockUID
			}
		}
	}

	return payments
}

// resolveDelayedPaymentRefSlots resolves reference block UIDs for all delayed entries
// in the builder pending withdrawals queue. Delayed payments originate from
// process_builder_pending_payments during the epoch transition, which processes
// BuilderPendingPayments entries from 2 epochs ago in slot order. Each delayed
// entry corresponds to a block where the builder's payload was missed/orphaned.
//
// We scan the source epoch's blocks in slot order, collecting those with missed
// payloads, and assign them to delayed entries in FIFO order.
func (sim *stateSimulator) resolveDelayedPaymentRefSlots(builderCount int, block *Block) []*uint64 {
	delayedCount := int(sim.prevState.builderDelayedCount)
	if delayedCount == 0 {
		return nil
	}

	chainState := sim.indexer.consensusPool.GetChainState()
	blockEpoch := chainState.EpochOfSlot(block.Slot)
	if blockEpoch < 2 {
		return make([]*uint64, delayedCount)
	}

	// Delayed payments from epoch K-2 are evaluated at epoch K boundary
	sourceEpoch := blockEpoch - 2
	sourceEpochFirstSlot := chainState.EpochToSlot(sourceEpoch)
	sourceEpochEndSlot := chainState.EpochToSlot(sourceEpoch + 1)

	// Collect all blocks with missed/orphaned payloads from the source epoch, in slot order.
	type missedBlock struct {
		builderIndex uint64
		blockUID     uint64
	}
	var missedBlocks []missedBlock

	_, prunedEpoch := sim.indexer.GetBlockCacheState()
	if sourceEpoch >= prunedEpoch {
		for slot := sourceEpochFirstSlot; slot < sourceEpochEndSlot; slot++ {
			blocks := sim.indexer.GetBlocksBySlot(slot)
			for _, b := range blocks {
				blockIndex := b.GetBlockIndex(sim.indexer.ctx)
				if blockIndex == nil || blockIndex.BuilderIndex == math.MaxUint64 {
					continue
				}
				if !b.HasExecutionPayload() || b.isPayloadOrphaned {
					missedBlocks = append(missedBlocks, missedBlock{
						builderIndex: blockIndex.BuilderIndex,
						blockUID:     b.BlockUID,
					})
				}
			}
		}
	} else {
		dbSlots := db.GetSlotsRange(sim.indexer.ctx, uint64(sourceEpochEndSlot-1), uint64(sourceEpochFirstSlot), false, false)
		for _, assignedSlot := range dbSlots {
			if assignedSlot.Block == nil {
				continue
			}
			if assignedSlot.Block.BuilderIndex < 0 {
				continue
			}
			if assignedSlot.Block.PayloadStatus == dbtypes.PayloadStatusMissing || assignedSlot.Block.PayloadStatus == dbtypes.PayloadStatusOrphaned {
				missedBlocks = append(missedBlocks, missedBlock{
					builderIndex: uint64(assignedSlot.Block.BuilderIndex),
					blockUID:     assignedSlot.Block.BlockUid,
				})
			}
		}
	}

	// Match delayed entries to missed blocks in order. Each delayed entry's builder
	// index must match the missed block's builder index. Multiple delayed entries for
	// the same builder consume successive missed blocks for that builder.
	delayedStart := builderCount - delayedCount
	refs := make([]*uint64, delayedCount)
	consumed := make([]bool, len(missedBlocks))

	for i := range delayedCount {
		queueIdx := delayedStart + i
		if queueIdx < 0 || queueIdx >= len(sim.prevState.builderPendingWithdrawals) {
			continue
		}
		wantBuilder := sim.prevState.builderPendingWithdrawals[queueIdx].BuilderIndex
		for j, mb := range missedBlocks {
			if consumed[j] {
				continue
			}
			if mb.builderIndex == uint64(wantBuilder) {
				uid := mb.blockUID
				refs[i] = &uid
				consumed[j] = true
				break
			}
		}
	}

	return refs
}
