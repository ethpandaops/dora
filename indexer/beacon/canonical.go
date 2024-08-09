package beacon

import (
	"bytes"
	"math"
	"slices"
	"sort"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

const FarFutureEpoch = phase0.Epoch(math.MaxUint64)

// ChainHead represents a head block of the chain.
type ChainHead struct {
	HeadBlock              *Block      // The head block of the chain.
	AggregatedHeadVotes    phase0.Gwei // The aggregated votes of the last 2 epochs for the head block.
	LastEpochVotingPercent float64     // The voting percentage in the last epoch.
	ThisEpochVotingPercent float64     // The voting percentage in the current epoch.
}

// GetCanonicalHead returns the canonical head block of the chain.
func (indexer *Indexer) GetCanonicalHead(overrideForkId *ForkKey) *Block {
	indexer.computeCanonicalChain()

	if overrideForkId != nil && indexer.canonicalHead != nil && indexer.canonicalHead.forkId != *overrideForkId {
		chainHeads := indexer.cachedChainHeads
		chainHeadCandidates := []*ChainHead{}

		for _, chainHead := range chainHeads {
			parentForkIds := indexer.forkCache.getParentForkIds(chainHead.HeadBlock.forkId)
			isInParentIds := false
			for _, parentForkId := range parentForkIds {
				if parentForkId == *overrideForkId {
					isInParentIds = true
					break
				}
			}
			if !isInParentIds {
				continue
			}

			chainHeadCandidates = append(chainHeadCandidates, chainHead)
		}

		if len(chainHeadCandidates) > 0 {
			sort.Slice(chainHeadCandidates, func(i, j int) bool {
				if chainHeadCandidates[i].LastEpochVotingPercent != chainHeadCandidates[j].LastEpochVotingPercent {
					return chainHeadCandidates[i].LastEpochVotingPercent > chainHeadCandidates[j].LastEpochVotingPercent
				}
				if chainHeadCandidates[i].ThisEpochVotingPercent != chainHeadCandidates[j].ThisEpochVotingPercent {
					return chainHeadCandidates[i].ThisEpochVotingPercent > chainHeadCandidates[j].ThisEpochVotingPercent
				}
				return chainHeadCandidates[i].HeadBlock.Slot > chainHeadCandidates[j].HeadBlock.Slot
			})

			return chainHeadCandidates[0].HeadBlock
		}
	}

	return indexer.canonicalHead
}

// GetChainHeads returns the chain heads sorted by voting percentages.
func (indexer *Indexer) GetChainHeads() []*ChainHead {
	indexer.computeCanonicalChain()

	heads := make([]*ChainHead, len(indexer.cachedChainHeads))
	copy(heads, indexer.cachedChainHeads)
	sort.Slice(heads, func(i, j int) bool {
		if heads[i].LastEpochVotingPercent != heads[j].LastEpochVotingPercent {
			return heads[i].LastEpochVotingPercent > heads[j].LastEpochVotingPercent
		}
		if heads[i].ThisEpochVotingPercent != heads[j].ThisEpochVotingPercent {
			return heads[i].ThisEpochVotingPercent > heads[j].ThisEpochVotingPercent
		}
		return heads[i].HeadBlock.Slot > heads[j].HeadBlock.Slot
	})

	return heads
}

func (indexer *Indexer) IsCanonicalBlock(block *Block, overrideForkId *ForkKey) bool {
	canonicalHead := indexer.GetCanonicalHead(overrideForkId)
	return canonicalHead != nil && indexer.blockCache.isCanonicalBlock(block.Root, canonicalHead.Root)
}

// computeCanonicalChain computes the canonical chain and updates the indexer's state.
func (indexer *Indexer) computeCanonicalChain() bool {
	indexer.canonicalHeadMutex.Lock()
	defer indexer.canonicalHeadMutex.Unlock()

	if indexer.blockCache.latestBlock == nil {
		return false
	}

	latestBlockRoot := indexer.blockCache.latestBlock.Root
	if bytes.Equal(latestBlockRoot[:], indexer.canonicalComputation[:]) {
		return false
	}

	var headBlock *Block = nil
	var chainHeads []*ChainHead = nil
	t1 := time.Now()

	defer func() {
		indexer.canonicalHead = headBlock
		indexer.cachedChainHeads = chainHeads
		indexer.canonicalComputation = latestBlockRoot

		if headBlock == nil {
			indexer.logger.Warnf("canonical head computation failed. forks: %v, latest block: %v, time: %v ms", len(chainHeads), latestBlockRoot.String(), time.Since(t1).Milliseconds())
		} else {
			indexer.logger.Infof("canonical head computation complete. forks: %v, head: %v (%v), time: %v ms", len(chainHeads), headBlock.Slot, headBlock.Root.String(), time.Since(t1).Milliseconds())
		}
	}()

	headForks := indexer.forkCache.getForkHeads()
	if len(headForks) <= 1 {
		// no forks, just get latest block
		latestBlocks := indexer.blockCache.getLatestBlocks(1, nil)
		if len(latestBlocks) > 0 {
			headBlock = latestBlocks[0]

			forkVotes, thisEpochPercent, lastEpochPercent := indexer.aggregateForkVotes(headBlock.forkId)
			indexer.logger.Debugf(
				"fork %v votes in last 2 epochs: %v ETH (%.2f%%, %.2f%%), head: %v (%v)",
				headBlock.forkId,
				forkVotes/EtherGweiFactor,
				lastEpochPercent,
				thisEpochPercent,
				headBlock.Slot,
				headBlock.Root.String(),
			)

			chainHeads = []*ChainHead{{
				HeadBlock:              headBlock,
				AggregatedHeadVotes:    forkVotes,
				LastEpochVotingPercent: lastEpochPercent,
				ThisEpochVotingPercent: thisEpochPercent,
			}}
		}
	} else {
		// multiple forks, compare forks
		headForkVotes := map[ForkKey]phase0.Gwei{}
		chainHeads = make([]*ChainHead, 0, len(headForks))
		var bestForkVotes phase0.Gwei = 0

		for _, fork := range headForks {
			if fork.Block == nil {
				continue
			}

			forkVotes, thisEpochPercent, lastEpochPercent := indexer.aggregateForkVotes(fork.ForkId)
			headForkVotes[fork.ForkId] = forkVotes
			chainHeads = append(chainHeads, &ChainHead{
				HeadBlock:              fork.Block,
				AggregatedHeadVotes:    forkVotes,
				LastEpochVotingPercent: lastEpochPercent,
				ThisEpochVotingPercent: thisEpochPercent,
			})

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

	return true
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

			epochVotes := indexer.aggregateEpochVotes(currentEpoch, chainState, thisBlocks, thisEpochStats)
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
			epochVotes := indexer.aggregateEpochVotes(currentEpoch-1, chainState, lastBlocks, lastEpochStats)
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

// GetCanonicalValidatorSet returns the latest canonical validator set.
// If an overrideForkId is provided, the latest validator set for the fork is returned.
func (indexer *Indexer) GetCanonicalValidatorSet(overrideForkId *ForkKey) []*v1.Validator {
	chainState := indexer.consensusPool.GetChainState()

	canonicalHead := indexer.GetCanonicalHead(overrideForkId)
	if canonicalHead == nil {
		return []*v1.Validator{}
	}

	headEpoch := chainState.EpochOfSlot(canonicalHead.Slot)
	validatorSet := []*v1.Validator{}

	var epochStats *EpochStats

	for {
		epoch := chainState.EpochOfSlot(canonicalHead.Slot)
		if headEpoch-epoch > 2 {
			return validatorSet
		}

		dependentBlock := indexer.blockCache.getDependentBlock(chainState, canonicalHead, nil)
		if dependentBlock == nil {
			return validatorSet
		}
		canonicalHead = dependentBlock

		epochStats = indexer.epochCache.getEpochStats(epoch, dependentBlock.Root)
		if epochStats == nil || epochStats.dependentState == nil || epochStats.dependentState.loadingStatus != 2 {
			continue // retry previous state
		}

		break
	}

	epochStatsKey := getEpochStatsKey(epochStats.epoch, epochStats.dependentRoot)
	if cachedValSet, found := indexer.validatorSetCache.Get(epochStatsKey); found {
		return cachedValSet
	}

	validatorSet = make([]*v1.Validator, len(epochStats.dependentState.validatorList))
	for index, validator := range epochStats.dependentState.validatorList {
		state := v1.ValidatorToState(validator, &epochStats.dependentState.validatorBalances[index], epochStats.epoch, FarFutureEpoch)

		validatorSet[index] = &v1.Validator{
			Index:     phase0.ValidatorIndex(index),
			Balance:   epochStats.dependentState.validatorBalances[index],
			Status:    state,
			Validator: validator,
		}
	}

	indexer.validatorSetCache.Add(epochStatsKey, validatorSet)

	return validatorSet
}
