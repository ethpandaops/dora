package beacon

import (
	"fmt"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

func (indexer *Indexer) precalcNextEpochStats(epoch phase0.Epoch) error {
	chainState := indexer.consensusPool.GetChainState()
	canonicalHead := indexer.GetCanonicalHead(nil)
	if canonicalHead == nil {
		return fmt.Errorf("canonical head not found")
	}

	dependentBlock := canonicalHead

	for {
		if chainState.EpochOfSlot(dependentBlock.Slot) < epoch {
			break
		}

		parentRoot := dependentBlock.GetParentRoot()
		if parentRoot == nil {
			return fmt.Errorf("parent block not found for head block %v", dependentBlock.Root.String())
		}

		dependentBlock = indexer.blockCache.getBlockByRoot(*parentRoot)
		if dependentBlock == nil {
			return fmt.Errorf("parent block %v not found", parentRoot.String())
		}
	}

	// precompute epoch stats for the epoch if we have the parent epoch stats ready
	epochStats := indexer.epochCache.createOrGetEpochStats(epoch, dependentBlock.Root)
	if !epochStats.ready {
		var parentDependentBlock *Block
		if chainState.EpochOfSlot(dependentBlock.Slot) == epoch-1 {
			parentDependentBlock = indexer.blockCache.getDependentBlock(chainState, dependentBlock, nil)
		} else {
			parentDependentBlock = dependentBlock
		}

		if parentDependentBlock == nil {
			indexer.logger.Warnf("failed precomputing epoch %v stats: parent epoch dependent block not found for head block %v", epoch, dependentBlock.Root.String())
		} else if parentEpochStats := indexer.epochCache.getEpochStats(epoch-1, parentDependentBlock.Root); parentEpochStats == nil {
			indexer.logger.Warnf("failed precomputing epoch %v stats: parent epoch stats (%v) not found", epoch, parentDependentBlock.Root.String())
		} else if err := epochStats.precomputeFromParentState(indexer, parentEpochStats); err != nil {
			indexer.logger.Warnf("failed precomputing epoch %v stats: %v", epoch, err)
		}
	}

	return nil
}
