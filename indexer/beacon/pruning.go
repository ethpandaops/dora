package beacon

import (
	"sort"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

func (indexer *Indexer) processCachePruning() error {
	indexer.logger.Infof("process pruning!")

	chainState := indexer.consensusPool.GetChainState()
	minInMemorySlot := indexer.getMinInMemorySlot()
	pruningBlocks := indexer.blockCache.getPruningBlocks(minInMemorySlot)

	sort.Slice(pruningBlocks, func(i, j int) bool {
		return pruningBlocks[i].Slot < pruningBlocks[j].Slot
	})

	// group by epoch
	var epochBlocks []*Block
	var epoch phase0.Epoch

	for _, block := range pruningBlocks {
		if epochBlocks == nil || chainState.EpochOfSlot(block.Slot) != epoch {
			if epochBlocks != nil {
				if err := indexer.pruneEpoch(epoch, epochBlocks); err != nil {
					return err
				}
			}

			epoch = chainState.EpochOfSlot(block.Slot)
			epochBlocks = []*Block{}
		}

		epochBlocks = append(epochBlocks, block)
	}

	if epochBlocks != nil {
		if err := indexer.pruneEpoch(epoch, epochBlocks); err != nil {
			return err
		}
	}

	return nil
}

func (indexer *Indexer) pruneEpoch(epoch phase0.Epoch, pruneBlocks []*Block) error {
	// group blocks by dependent roots and process each group independently
	dependentGroups := map[phase0.Root][]*Block{}
	chainState := indexer.consensusPool.GetChainState()

	for _, block := range pruneBlocks {
		var dependendRoot phase0.Root

		if dependentBlock := indexer.blockCache.getDependentBlock(chainState, block); dependentBlock != nil {
			dependendRoot = dependentBlock.Root
		}

		if dependentGroups[dependendRoot] == nil {
			dependentGroups[dependendRoot] = []*Block{block}
		} else {
			dependentGroups[dependendRoot] = append(dependentGroups[dependendRoot], block)
		}
	}

	// process each group
	/*
		for dependentRoot, blocks := range dependentGroups {
			epochStats := indexer.epochCache.getEpochStats(epoch, dependentRoot)
			epochStatsValues := epochStats.GetValues()



		}
	*/
	return nil

}
