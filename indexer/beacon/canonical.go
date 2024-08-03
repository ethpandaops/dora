package beacon

import (
	"bytes"
	"slices"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// GetCanonicalHead returns the canonical head block of the beacon chain.
func (indexer *Indexer) GetCanonicalHead() *Block {
	indexer.canonicalHeadMutex.Lock()
	defer indexer.canonicalHeadMutex.Unlock()

	latestBlockRoot := indexer.blockCache.latestBlock.Root
	if indexer.blockCache.latestBlock == nil || bytes.Equal(latestBlockRoot[:], indexer.canonicalComputation[:]) {
		return indexer.canonicalHead
	}

	var headBlock *Block = nil
	t1 := time.Now()

	defer func() {
		indexer.canonicalHead = headBlock
		indexer.canonicalComputation = latestBlockRoot

		if headBlock == nil {
			indexer.logger.Warnf("canonical head computation failed. latest block: %v, time: %v ms", latestBlockRoot.String(), time.Since(t1).Milliseconds())
		} else {
			indexer.logger.Infof("canonical head computation complete. block: %v (%v), time: %v ms", headBlock.Slot, headBlock.Root.String(), time.Since(t1).Milliseconds())
		}
	}()

	headForks := indexer.forkCache.getForkHeads()
	if len(headForks) <= 1 {
		// no forks, just get latest block
		latestBlocks := indexer.blockCache.getLatestBlocks(1, nil)
		if len(latestBlocks) > 0 {
			headBlock = latestBlocks[0]
		}
	} else {
		// multiple forks, compare forks
		headForkVotes := map[ForkKey]phase0.Gwei{}
		var bestForkVotes phase0.Gwei = 0

		for _, fork := range headForks {
			if fork.Block == nil {
				continue
			}

			forkVotes, thisEpochPercent, lastEpochPercent := indexer.aggregateForkVotes(fork.ForkId)
			headForkVotes[fork.ForkId] = forkVotes

			if forkVotes > 0 {
				indexer.logger.Infof(
					"fork %v votes in last 2 epochs: %v ETH (%.2f%%, %.2f%%), head: %v (%v)",
					fork.ForkId,
					forkVotes/EtherGweiFactor,
					lastEpochPercent,
					thisEpochPercent,
					fork.Block.Slot,
					fork.Block.Root.String(),
				)
			}

			if forkVotes > bestForkVotes || headBlock == nil {
				bestForkVotes = forkVotes
				headBlock = fork.Block
			} else if forkVotes == bestForkVotes && headBlock.Slot < fork.Block.Slot {
				headBlock = fork.Block
			}
		}
	}

	return headBlock
}

// aggregateForkVotes aggregates the votes for a given fork.
func (indexer *Indexer) aggregateForkVotes(forkId ForkKey) (totalVotes phase0.Gwei, thisEpochPercent float64, lastEpochPercent float64) {
	chainState := indexer.consensusPool.GetChainState()
	specs := chainState.GetSpecs()
	currentEpoch := chainState.CurrentEpoch()
	minAggregateEpoch := currentEpoch
	if minAggregateEpoch > 1 {
		minAggregateEpoch -= 1
	} else {
		minAggregateEpoch = 0
	}
	minAggregateSlot := chainState.EpochStartSlot(minAggregateEpoch)

	fork := indexer.forkCache.getForkById(forkId)
	if fork != nil && fork.headBlock != nil && fork.headBlock.Slot < minAggregateSlot {
		// fork head block is outside aggregation range
		// very old fork, skip aggregation and return 0
		return
	}

	// get all blocks for given fork (and its parents) from the last 2 epochs
	lastBlocks := []*Block{}
	lastSlot := phase0.Slot(0)
	thisForkId := forkId
	for {
		for _, block := range indexer.blockCache.getLatestBlocks(2*specs.SlotsPerEpoch, &thisForkId) {
			lastSlot = block.Slot
			if block.Slot < minAggregateSlot {
				break
			}
			lastBlocks = append(lastBlocks, block)
		}

		if lastSlot < minAggregateSlot {
			break
		}

		fork := indexer.forkCache.getForkById(thisForkId)
		if fork == nil {
			break
		}

		thisForkId = fork.parentFork
	}

	if len(lastBlocks) == 0 {
		return
	}

	// already sorted descending by getLatestBlocks, reverse to ascending for aggregation
	lastBlock := lastBlocks[0]
	slices.Reverse(lastBlocks)

	// aggregate votes for last & current epoch
	if chainState.EpochOfSlot(lastBlock.Slot) == currentEpoch {
		thisEpochDependent := indexer.blockCache.getDependentBlock(chainState, lastBlock, nil)
		if thisEpochDependent == nil {
			return
		}
		lastBlock = thisEpochDependent

		thisEpochStats := indexer.epochCache.getEpochStats(currentEpoch, thisEpochDependent.Root)
		if thisEpochStats != nil {
			thisBlocks := []*Block{}
			for _, block := range lastBlocks {
				if chainState.EpochOfSlot(block.Slot) == currentEpoch {
					thisBlocks = append(thisBlocks, block)
				}
			}

			epochVotes := indexer.aggregateEpochVotes(chainState, thisBlocks, thisEpochStats)
			if epochVotes.AmountIsCount {
				totalVotes += epochVotes.CurrentEpoch.TargetVoteAmount * 32 * EtherGweiFactor
			} else {
				totalVotes += epochVotes.CurrentEpoch.TargetVoteAmount
			}
			thisEpochPercent = epochVotes.TargetVotePercent
		}
	}

	if chainState.EpochOfSlot(lastBlock.Slot)+1 == currentEpoch {
		lastEpochDependent := indexer.blockCache.getDependentBlock(chainState, lastBlock, nil)
		if lastEpochDependent == nil {
			return
		}

		lastEpochStats := indexer.epochCache.getEpochStats(currentEpoch-1, lastEpochDependent.Root)
		if lastEpochStats != nil {
			epochVotes := indexer.aggregateEpochVotes(chainState, lastBlocks, lastEpochStats)
			if epochVotes.AmountIsCount {
				totalVotes += (epochVotes.CurrentEpoch.TargetVoteAmount + epochVotes.NextEpoch.TargetVoteAmount) * 32 * EtherGweiFactor
			} else {
				totalVotes += epochVotes.CurrentEpoch.TargetVoteAmount + epochVotes.NextEpoch.TargetVoteAmount
			}
			lastEpochPercent = epochVotes.TargetVotePercent
		}
	}

	return
}
