package beacon

import (
	"bytes"
	"slices"

	"github.com/attestantio/go-eth2-client/spec/electra"
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
	pendingWithdrawals        []EpochStatsPendingWithdrawals
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

func (sim *stateSimulator) getParentBlocks(block *Block) []*Block {
	chainState := sim.indexer.consensusPool.GetChainState()
	minSlot := chainState.EpochToSlot(chainState.EpochOfSlot(block.Slot))
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
		pendingWithdrawals = []EpochStatsPendingWithdrawals{}
	}

	state := &stateSimulatorState{
		block:                     nil,
		epochRoot:                 block.Root,
		pendingWithdrawals:        pendingWithdrawals,
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

	if !bytes.Equal(tgtWithdrawalCreds[12:], consolidation.SourceAddress[:]) {
		return dbtypes.ConsolidationRequestResultTgtInvalidSender
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

	blockBody := block.GetBlock()
	if blockBody == nil {
		return nil
	}

	// process pending withdrawals
	chainState := sim.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()
	processedWithdrawals := uint64(0)
	skippedWithdrawals := uint64(0)
	for _, pendingWithdrawal := range sim.prevState.pendingWithdrawals {
		if pendingWithdrawal.Epoch > sim.epochStats.epoch {
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
