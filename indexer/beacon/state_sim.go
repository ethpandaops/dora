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
	// Epoch boundary layout: [direct from prev epoch's unconsumed payloads..., delayed from
	// process_builder_pending_payments at the tail]. The split point is given by
	// DelayedBuilderPaymentCount.
	delayedCount := int(sim.epochStatsValues.DelayedBuilderPaymentCount)
	directCount := len(rawBuilderWithdrawals) - delayedCount
	trackedWithdrawals := make([]trackedBuilderWithdrawal, len(rawBuilderWithdrawals))
	for i := range rawBuilderWithdrawals {
		typ := uint8(dbtypes.WithdrawalTypeBuilderPayment)
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

	// Cache lookup
	if parentBlock := sim.indexer.GetBlockByRoot(*parentRoot); parentBlock != nil {
		if !parentBlock.HasExecutionPayload() || parentBlock.isPayloadOrphaned {
			return nil
		}
		blockIndex := parentBlock.GetBlockIndex(sim.indexer.ctx)
		if blockIndex == nil || blockIndex.BuilderIndex == math.MaxUint64 {
			return nil
		}
		uid := parentBlock.BlockUID
		return &trackedBuilderWithdrawal{
			BuilderPendingWithdrawal: gloas.BuilderPendingWithdrawal{
				BuilderIndex: gloas.BuilderIndex(blockIndex.BuilderIndex),
			},
			Type:        dbtypes.WithdrawalTypeBuilderPayment,
			RefBlockUID: &uid,
		}
	}

	// Fall back to DB for pruned parents (e.g., during historical replay)
	parentSlot := db.GetSlotByRoot(sim.indexer.ctx, parentRoot[:])
	if parentSlot == nil {
		return nil
	}
	if parentSlot.BuilderIndex < 0 {
		return nil
	}
	if parentSlot.PayloadStatus != dbtypes.PayloadStatusCanonical {
		return nil
	}
	uid := parentSlot.BlockUid
	return &trackedBuilderWithdrawal{
		BuilderPendingWithdrawal: gloas.BuilderPendingWithdrawal{
			BuilderIndex: gloas.BuilderIndex(parentSlot.BuilderIndex),
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
		// Previous epoch is finalized/pruned — query DB. GetSlotsRange returns slots in
		// descending order, so iterate it in reverse to walk slots ascending (queue order).
		dbSlots := db.GetSlotsRange(sim.indexer.ctx, uint64(prevEnd-1), uint64(prevStart), false, false)
		for i := len(dbSlots) - 1; i >= 0 && resolved < directCount; i-- {
			assignedSlot := dbSlots[i]
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
	if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
		return nil
	}

	body := blockBody.Message.Body

	// process builder pending withdrawals (come first in the spec)
	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()
	// process_withdrawals drains the queue (up to MAX_WITHDRAWALS_PER_PAYLOAD-1 per spec).
	// In normal operation the queue never exceeds the limit, so we drain unconditionally.
	if len(sim.prevState.builderPendingWithdrawals) > 0 {
		sim.prevState.builderPendingWithdrawals = sim.prevState.builderPendingWithdrawals[:0]
	}

	// Stage this block's direct payment for the NEXT block's drain. In spec terms this
	// settle happens at B+1's process_parent_execution_payload; we account for it here so
	// the queue state observed by the next applyBlock matches the spec.
	if block.HasExecutionPayload() && !block.isPayloadOrphaned {
		blockIndex := block.GetBlockIndex(sim.indexer.ctx)
		if blockIndex != nil && blockIndex.BuilderIndex != math.MaxUint64 {
			uid := block.BlockUID
			sim.prevState.builderPendingWithdrawals = append(sim.prevState.builderPendingWithdrawals, trackedBuilderWithdrawal{
				BuilderPendingWithdrawal: gloas.BuilderPendingWithdrawal{
					BuilderIndex: gloas.BuilderIndex(blockIndex.BuilderIndex),
				},
				Type:        dbtypes.WithdrawalTypeBuilderPayment,
				RefBlockUID: &uid,
			})
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

	// If the immediate parent is in a previous epoch (no parent blocks were replayed),
	// applyBlock never had a chance to stage the parent's direct payment. Mirror the spec's
	// process_parent_execution_payload here so the queue matches what the current block
	// would observe at process_withdrawals time. Without this, the parent's builder payment
	// is missing from BuilderPaymentCount and the extra builder withdrawal in the block's
	// payload would fall through to the builder-sweep classification.
	if len(parentBlocks) == 0 {
		if entry := sim.resolveParentDirectEntry(block); entry != nil {
			sim.prevState.builderPendingWithdrawals = append(sim.prevState.builderPendingWithdrawals, *entry)
		}
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

// classifyBuilderPayments produces classifications for each entry in the pending queue.
// The queue layout at the time of the current block's process_withdrawals is, in spec order:
//
//	[unconsumed_direct_from_prev_epoch..., delayed_from_epoch_transition..., parent_direct]
//
// Direct entries (from settle_builder_payment) carry RefBlockUID. Delayed entries are
// resolved separately by matching builder indices to blocks with missed/orphaned payloads
// in the source epoch (current epoch - 2).
func (sim *stateSimulator) classifyBuilderPayments(block *Block, builderCount int) []builderPaymentClassification {
	payments := make([]builderPaymentClassification, builderCount)

	delayedRefs := sim.resolveDelayedPaymentRefSlots(block)
	delayedIdx := 0

	for i := 0; i < builderCount && i < len(sim.prevState.builderPendingWithdrawals); i++ {
		entry := sim.prevState.builderPendingWithdrawals[i]
		payments[i].Type = entry.Type
		if entry.Type == dbtypes.WithdrawalTypeBuilderDelayedPayment {
			if delayedIdx < len(delayedRefs) {
				payments[i].RefSlot = delayedRefs[delayedIdx]
			}
			delayedIdx++
		} else {
			payments[i].RefSlot = entry.RefBlockUID
		}
	}

	return payments
}

// resolveDelayedPaymentRefSlots resolves reference block UIDs for delayed entries in the
// pending queue, returned in queue order. Delayed payments originate from
// process_builder_pending_payments during the epoch transition, which appends payments from
// 2 epochs back in slot order. Each delayed entry corresponds to a block where the builder's
// payload was missed/orphaned (their payment didn't settle in the previous epoch).
//
// We scan the source epoch's blocks in slot order, collecting those with missed payloads,
// then match them to the delayed queue entries (also in slot-ascending order) by builder
// index in FIFO fashion.
func (sim *stateSimulator) resolveDelayedPaymentRefSlots(block *Block) []*uint64 {
	// Collect delayed entries in queue order
	delayedEntries := make([]gloas.BuilderIndex, 0)
	for _, entry := range sim.prevState.builderPendingWithdrawals {
		if entry.Type == dbtypes.WithdrawalTypeBuilderDelayedPayment {
			delayedEntries = append(delayedEntries, entry.BuilderIndex)
		}
	}
	if len(delayedEntries) == 0 {
		return nil
	}

	chainState := sim.indexer.consensusPool.GetChainState()
	blockEpoch := chainState.EpochOfSlot(block.Slot)
	if blockEpoch < 2 {
		return make([]*uint64, len(delayedEntries))
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
		// GetSlotsRange returns slots in descending order; iterate in reverse to walk
		// slots ascending so delayed entries match missed blocks in matching slot order.
		dbSlots := db.GetSlotsRange(sim.indexer.ctx, uint64(sourceEpochEndSlot-1), uint64(sourceEpochFirstSlot), false, false)
		for i := len(dbSlots) - 1; i >= 0; i-- {
			assignedSlot := dbSlots[i]
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

	// Match delayed entries to missed blocks in order. Each delayed entry's builder index
	// must match the missed block's builder index. Multiple delayed entries for the same
	// builder consume successive missed blocks for that builder.
	refs := make([]*uint64, len(delayedEntries))
	consumed := make([]bool, len(missedBlocks))
	for i, wantBuilder := range delayedEntries {
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
