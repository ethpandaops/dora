package beacon

import (
	"bytes"
	"math"
	"slices"

	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
)

type stateSimulator struct {
	indexer          *Indexer
	epochStats       *EpochStats
	epochStatsValues *EpochStatsValues
	prevState        *stateSimulatorState
	validatorSet     []*phase0.Validator
}

type stateSimulatorState struct {
	epochRoot                 phase0.Root
	block                     *Block
	pendingWithdrawals        []electra.PendingPartialWithdrawal
	builderPendingWithdrawals []gloas.BuilderPendingWithdrawal
	builderDelayedCount       int // how many entries in builderPendingWithdrawals are delayed/quorum payments
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
// Uses the sim's epoch as the boundary.
//
// In Fulu+, the epoch state is the post-state of the first block of the epoch,
// so the first block must be excluded (its effects are already in the state).
// The boundary is set to firstSlot+1 to exclude it.
//
// In pre-Fulu, the epoch state is from the last block of the previous epoch,
// so all blocks in the current epoch need to be replayed (boundary = firstSlot).
func (sim *stateSimulator) getParentBlocks(block *Block) []*Block {
	chainState := sim.indexer.consensusPool.GetChainState()
	simEpoch := sim.epochStats.epoch
	minSlot := chainState.EpochToSlot(simEpoch)

	// In Fulu+, skip the first block of the sim's epoch (state is its post-state)
	if chainState.IsFuluEnabled(simEpoch) {
		minSlot++
	}

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

// getSimForBlock returns the correct stateSimulator for a given block.
// In Fulu+, the first block of an epoch needs the previous epoch's sim,
// since the current epoch state is the post-state of that first block.
// For all other blocks, the current sim is returned unchanged.
func (sim *stateSimulator) getSimForBlock(block *Block) *stateSimulator {
	chainState := sim.indexer.consensusPool.GetChainState()
	blockEpoch := chainState.EpochOfSlot(block.Slot)

	if !chainState.IsFuluEnabled(blockEpoch) || blockEpoch == 0 {
		return sim
	}

	epochFirstSlot := chainState.EpochToSlot(blockEpoch)
	if block.Slot != epochFirstSlot {
		return sim
	}

	// First block of a Fulu+ epoch: need previous epoch's sim
	prevEpoch := blockEpoch - 1
	prevEpochStats := sim.indexer.epochCache.getEpochStatsByEpochAndRoot(prevEpoch, block.Root)
	if prevEpochStats == nil {
		if parentRoot := block.GetParentRoot(); parentRoot != nil {
			prevEpochStats = sim.indexer.epochCache.getEpochStatsByEpochAndRoot(prevEpoch, *parentRoot)
		}
	}
	if prevEpochStats != nil {
		prevSim := newStateSimulator(sim.indexer, prevEpochStats)
		if prevSim != nil {
			return prevSim
		}
	}

	return sim // fallback: can't get prev epoch, use current (may be imprecise)
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

	builderPendingWithdrawals := sim.epochStatsValues.BuilderPendingWithdrawals
	if builderPendingWithdrawals == nil {
		builderPendingWithdrawals = []gloas.BuilderPendingWithdrawal{}
	}

	state := &stateSimulatorState{
		block:                     nil,
		epochRoot:                 epochRoot,
		pendingWithdrawals:        pendingWithdrawals,
		builderPendingWithdrawals: builderPendingWithdrawals,
		builderDelayedCount:       len(builderPendingWithdrawals), // all entries from epoch state are delayed/quorum payments
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

	return state
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
	processedBuilderWithdrawals := len(sim.prevState.builderPendingWithdrawals)
	if processedBuilderWithdrawals > 0 {
		// Track how many delayed entries were consumed
		delayedConsumed := processedBuilderWithdrawals
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
			// Block has a delivered payload from a builder — direct payment will be appended
			// We don't know the exact BuilderPendingWithdrawal details here, but we track
			// that a non-delayed entry was added to the queue
			sim.prevState.builderPendingWithdrawals = append(sim.prevState.builderPendingWithdrawals, gloas.BuilderPendingWithdrawal{
				BuilderIndex: gloas.BuilderIndex(blockIndex.BuilderIndex),
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
	// Use the correct sim for this block (handles Fulu first-block-of-epoch case)
	sim = sim.getSimForBlock(block)

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
// This handles the Fulu epoch state nuances:
//   - The Fulu state (post-block-1) has only remaining delayed entries (no direct from block 1)
//   - process_execution_payload adds direct entries to the BACK (not in state_root)
//   - For block 1: queue = [carry_over_direct?, delayed_0, ..., delayed_D]
//   - For block 2+: queue = [remaining_delayed..., direct_from_prev_block?]
func (sim *stateSimulator) replayWithdrawalState(block *Block) *withdrawalSimResult {
	result := &withdrawalSimResult{}

	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()
	if chainSpec.MaxPendingPartialsPerWithdrawalsSweep == 0 {
		return result
	}

	// Use the correct sim for this block (handles Fulu first-block-of-epoch case)
	sim = sim.getSimForBlock(block)

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
// in the pending queue. Handles carry-over detection, delayed vs direct classification,
// and the fallback guard for unreliable detection.
func (sim *stateSimulator) classifyBuilderPayments(block *Block, builderCount int) []builderPaymentClassification {
	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()
	blockEpoch := chainState.EpochOfSlot(block.Slot)
	epochFirstSlot := chainState.EpochToSlot(blockEpoch)
	isFirstBlock := block.Slot == epochFirstSlot

	delayedCount := sim.prevState.builderDelayedCount

	// Validate detection reliability for Fulu
	hasCarryOver := false
	fallback := false
	var lastPrevEpochPayloadSlot phase0.Slot

	if chainState.IsFuluEnabled(blockEpoch) && blockEpoch > 0 {
		prevEpoch := blockEpoch - 1
		prevEpochFirstSlot := chainState.EpochToSlot(prevEpoch)
		currentEpochFirstSlot := chainState.EpochToSlot(blockEpoch)

		// Count payloads in previous epoch and find the last one
		payloadCount := 0
		for slot := prevEpochFirstSlot; slot < currentEpochFirstSlot; slot++ {
			blocks := sim.indexer.GetBlocksBySlot(slot)
			for _, b := range blocks {
				if b.HasExecutionPayload() && !b.isPayloadOrphaned {
					payloadCount++
					lastPrevEpochPayloadSlot = slot
				}
			}
		}

		// Guard: if delayed count exceeds prev epoch drain capacity, detection is unreliable.
		// Each block can process up to MAX_WITHDRAWALS_PER_PAYLOAD - 1 builder withdrawals,
		// but a delivered payload adds 1 direct payment back. Net drain = (max - 1) - 1 per payload.
		netDrainPerPayload := int(chainSpec.MaxWithdrawalsPerPayload) - 2
		if netDrainPerPayload < 1 {
			netDrainPerPayload = 1
		}
		if delayedCount > payloadCount*netDrainPerPayload {
			fallback = true
		}

		// For block 1: check carry-over direct from last payload of prev epoch
		if isFirstBlock && lastPrevEpochPayloadSlot > 0 {
			blocks := sim.indexer.GetBlocksBySlot(lastPrevEpochPayloadSlot)
			for _, b := range blocks {
				blockIndex := b.GetBlockIndex(sim.indexer.ctx)
				if blockIndex != nil && blockIndex.BuilderIndex != math.MaxUint64 {
					hasCarryOver = true
				}
			}
		}
	}

	// Build classifications
	payments := make([]builderPaymentClassification, builderCount)

	if fallback {
		for i := range payments {
			payments[i] = builderPaymentClassification{Type: dbtypes.WithdrawalTypeBuilderPayment}
		}
		return payments
	}

	// Get previous block slot for direct payment ref slot
	var prevBlockSlot phase0.Slot
	if sim.prevState.block != nil {
		prevBlockSlot = sim.prevState.block.Slot
	}

	for i := range payments {
		if isFirstBlock {
			// Block 1: [carry_over_direct?, delayed_0, ..., delayed_D]
			if hasCarryOver && i == 0 {
				// First entry = carry-over direct from prev epoch's last payload
				payments[i].Type = dbtypes.WithdrawalTypeBuilderPayment
				if lastPrevEpochPayloadSlot > 0 {
					slot := uint64(lastPrevEpochPayloadSlot)
					payments[i].RefSlot = &slot
				}
			} else {
				// Remaining = delayed
				delayedIdx := i
				if hasCarryOver {
					delayedIdx = i - 1
				}
				payments[i].Type = dbtypes.WithdrawalTypeBuilderDelayedPayment
				payments[i].RefSlot = sim.resolveDelayedPaymentRefSlot(delayedIdx, block)
			}
		} else {
			// Block 2+: [remaining_delayed..., direct_from_prev_block?]
			if i < delayedCount {
				// Delayed entry (front of queue)
				payments[i].Type = dbtypes.WithdrawalTypeBuilderDelayedPayment
				payments[i].RefSlot = sim.resolveDelayedPaymentRefSlot(i, block)
			} else {
				// Direct entry (back of queue)
				payments[i].Type = dbtypes.WithdrawalTypeBuilderPayment
				if prevBlockSlot > 0 {
					slot := uint64(prevBlockSlot)
					payments[i].RefSlot = &slot
				}
			}
		}
	}

	return payments
}

// resolveDelayedPaymentRefSlot finds the slot a delayed builder payment is for
// by matching the builder index against blocks with missed payloads from the previous epoch.
func (sim *stateSimulator) resolveDelayedPaymentRefSlot(delayedIdx int, block *Block) *uint64 {
	if delayedIdx < 0 || delayedIdx >= len(sim.prevState.builderPendingWithdrawals) {
		return nil
	}

	builderIndex := sim.prevState.builderPendingWithdrawals[delayedIdx].BuilderIndex
	chainState := sim.indexer.consensusPool.GetChainState()
	blockEpoch := chainState.EpochOfSlot(block.Slot)
	if blockEpoch == 0 {
		return nil
	}

	prevEpoch := blockEpoch - 1
	prevEpochFirstSlot := chainState.EpochToSlot(prevEpoch)
	currentEpochFirstSlot := chainState.EpochToSlot(blockEpoch)

	for slot := prevEpochFirstSlot; slot < currentEpochFirstSlot; slot++ {
		blocks := sim.indexer.GetBlocksBySlot(slot)
		for _, b := range blocks {
			blockIndex := b.GetBlockIndex(sim.indexer.ctx)
			if blockIndex == nil {
				continue
			}
			if blockIndex.BuilderIndex == uint64(builderIndex) && (!b.HasExecutionPayload() || b.isPayloadOrphaned) {
				s := uint64(slot)
				return &s
			}
		}
	}

	return nil
}
