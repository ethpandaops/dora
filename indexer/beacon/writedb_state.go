package beacon

import (
	"slices"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
)

func (dbw *dbWriter) getParentBlocks(block *Block) []*Block {
	chainState := dbw.indexer.consensusPool.GetChainState()
	minSlot := chainState.EpochToSlot(chainState.EpochOfSlot(block.Slot))
	parentBlocks := []*Block{}

	for {
		parentBlockRoot := block.GetParentRoot()
		if parentBlockRoot == nil {
			break
		}

		parentBlock := dbw.indexer.GetBlockByRoot(*parentBlockRoot)
		if parentBlock == nil {
			break
		}

		if parentBlock.Slot < minSlot {
			break
		}

		parentBlocks = append(parentBlocks, parentBlock)
	}

	slices.Reverse(parentBlocks)

	return parentBlocks
}

func (dbw *dbWriter) replayPendingWithdrawalsMap(epochStats *EpochStats, parentBlocks []*Block) map[phase0.ValidatorIndex]bool {
	withdrawalsMap := map[phase0.ValidatorIndex]bool{}

	chainState := dbw.indexer.consensusPool.GetChainState()
	chainSpec := chainState.GetSpecs()
	if chainSpec.ElectraForkEpoch == nil || epochStats.epoch < phase0.Epoch(*chainSpec.ElectraForkEpoch) {
		return withdrawalsMap
	}

	var pendingWithdrawals []EpochStatsPendingWithdrawals
	epochStatsValues := epochStats.GetValues(false)
	if epochStatsValues == nil || epochStatsValues.PendingWithdrawals == nil {
		pendingWithdrawals = epochStatsValues.PendingWithdrawals
	} else {
		pendingWithdrawals = []EpochStatsPendingWithdrawals{}
	}

	maxDequeuePerSweep := chainSpec.MaxPendingPartialsPerWithdrawalsSweep

	for _, block := range parentBlocks {
		// dequeue pending withdrawals
		dequeued := uint64(0)
		for _, pendingWithdrawal := range pendingWithdrawals {
			if pendingWithdrawal.Epoch > epochStats.epoch || dequeued >= maxDequeuePerSweep {
				break
			}
			dequeued++
		}
		pendingWithdrawals = pendingWithdrawals[dequeued:]

		// get additional withdrawal requests from block
		blockBody := block.GetBlock()
		if blockBody == nil {
			continue
		}

		requests, err := blockBody.ExecutionRequests()
		if err != nil {
			continue
		}

		for _, withdrawal := range requests.Withdrawals {

		}
	}

	return nil
}

type replayBlockConsolidationsState struct {
	block                 *Block
	pendingWithdrawals    []EpochStatsPendingWithdrawals
	additionalWithdrawals []phase0.ValidatorIndex
	validatorMap          map[phase0.ValidatorIndex]*phase0.Validator
}

func (dbw *dbWriter) replayBlockConsolidations(epochStats *EpochStats, block *Block, prevState *replayBlockConsolidationsState) ([]uint8, *replayBlockConsolidationsState) {
	parentBlocks := dbw.getParentBlocks(block)
	if prevState == nil || prevState.block.Slot < block.Slot {
		epochStatsValues := epochStats.GetValues(false)
		if epochStatsValues == nil {
			return nil, nil
		}

		pendingWithdrawals := epochStatsValues.PendingWithdrawals
		if pendingWithdrawals == nil {
			pendingWithdrawals = []EpochStatsPendingWithdrawals{}
		}

		prevState = &replayBlockConsolidationsState{
			block:                 parentBlocks[0],
			pendingWithdrawals:    pendingWithdrawals,
			additionalWithdrawals: []phase0.ValidatorIndex{},
			validatorMap:          map[phase0.ValidatorIndex]*phase0.Validator{},
		}
	}

	// get consolidations from block
	blockBody := block.GetBlock()
	if blockBody == nil {
		return nil, nil
	}

	blockRequests, err := blockBody.ExecutionRequests()
	if err != nil {
		return nil, nil
	}

	// get relevant indices and prepare results array
	relevantIndices := []phase0.ValidatorIndex{}
	requestResults := make([]uint8, len(blockRequests.Consolidations))
	for i, consolidation := range blockRequests.Consolidations {
		sourceIndice, sourceFound := dbw.indexer.validatorCache.getValidatorIndexByPubkey(consolidation.SourcePubkey)
		if !sourceFound {
			requestResults[i] = dbtypes.ConsolidationRequestResultSrcNotFound
			continue
		}

		targetIndice, targetFound := dbw.indexer.validatorCache.getValidatorIndexByPubkey(consolidation.TargetPubkey)
		if !targetFound {
			requestResults[i] = dbtypes.ConsolidationRequestResultTgtNotFound
			continue
		}

		relevantIndices = append(relevantIndices, sourceIndice, targetIndice)
	}

	validatorMap := map[phase0.ValidatorIndex]*phase0.Validator{}
	getValidator := func(index phase0.ValidatorIndex) *phase0.Validator {
		if validator, ok := validatorMap[index]; ok {
			return validator
		}

		validator, err := dbw.indexer.validatorCache.getValidatorByIndex(index, nil)
		if err != nil {
			return nil
		}
	}

	// replay parent blocks up to the current block and apply all operations for relevant indices
	for _, parentBlock := range parentBlocks {
		blockBody := parentBlock.GetBlock()
		if blockBody == nil {
			return nil, nil
		}

		// get bls changes
		blsChanges, err := blockBody.BLSToExecutionChanges()
		if err != nil {
			return nil, nil
		}

		for _, blsChange := range blsChanges {
			validatorIndex := blsChange.Message.ValidatorIndex
			if slices.Contains(relevantIndices, validatorIndex) {
				prevState.validatorMap[validatorIndex] = &blsChange.Message.Validator
			}

			if blsChange.ValidatorIndex == prevState.validatorMap[blsChange.ValidatorIndex].Index {
				prevState.validatorMap[blsChange.ValidatorIndex].WithdrawalCredentials = blsChange.WithdrawalCredentials
			}
		}

		// get execution requests
		requests, err := blockBody.ExecutionRequests()
		requests, err := blockBody.ExecutionRequests()
		if err != nil {
			return nil
		}
	}

	return prevState

}
