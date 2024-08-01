package beacon

import (
	"fmt"
	"sort"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/jmoiron/sqlx"
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

		if dependentBlock := indexer.blockCache.getDependentBlock(chainState, block, nil); dependentBlock != nil {
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

func (indexer *Indexer) processFinalityEvent(finalityEvent *v1.Finality) error {
	// first wait 5 seconds for other clients to process the finality checkpoint
	time.Sleep(5 * time.Second)

	indexer.logger.Infof("process finality event (epoch: %v, root: %v)", finalityEvent.Finalized.Epoch, finalityEvent.Finalized.Root.String())
	startSynchronizer := false

	for finalizeEpoch := indexer.lastFinalizedEpoch; finalizeEpoch < finalityEvent.Finalized.Epoch; finalizeEpoch++ {
		readyClients := indexer.GetReadyClientsByCheckpoint(finalityEvent.Finalized.Root)
		retryCount := 5

		for {
			if len(readyClients) > 0 {
				break
			}

			if retryCount > 0 {
				indexer.logger.Warnf("no ready clients for epoch %v finalization, retrying in 5 sec...", finalizeEpoch)
				time.Sleep(5 * time.Second)
				retryCount--
			} else {
				indexer.logger.Warnf("no ready clients for epoch %v finalization", finalizeEpoch)
				break
			}
		}

		retryCount = len(readyClients)
		if retryCount == 0 {
			indexer.logger.Infof("need synchronization! epoch %v, reason: no clients", finalizeEpoch)
			startSynchronizer = true
			indexer.lastFinalizedEpoch = finalizeEpoch + 1
			break
		}

		clientIdx := 0

		indexer.logger.Infof("finalizing epoch %v", finalizeEpoch)
		var finalizationError error
		for retry := 0; retry < retryCount; retry++ {
			lastTry := retry == retryCount-1
			client := readyClients[clientIdx%len(readyClients)]

			canRetry, err := indexer.finalizeEpoch(finalizeEpoch, finalityEvent.Justified.Root, client, lastTry)
			finalizationError = err
			if err != nil {
				if !lastTry && canRetry {
					indexer.logger.WithError(finalizationError).Warnf("failed finalizing epoch %v, retrying...", finalizeEpoch)
					time.Sleep(10 * time.Second)
				} else {
					indexer.logger.WithError(finalizationError).Errorf("failed finalizing epoch %v", finalizeEpoch)
				}

				if canRetry {
					continue
				} else {
					break
				}
			} else if canRetry {
				// finalization processing is not complete, still needs resync
				indexer.logger.Infof("need synchronization! epoch %v, reason: incomplete", finalizeEpoch)
				startSynchronizer = true
			}

			break
		}

		if finalizationError != nil {
			indexer.logger.Infof("need synchronization! epoch %v, reason: error", finalizeEpoch)
			startSynchronizer = true
		}
	}

	if startSynchronizer {
		indexer.logger.Infof("need synchronization!")
	}

	return nil
}

func (indexer *Indexer) finalizeEpoch(epoch phase0.Epoch, justifiedRoot phase0.Root, client *Client, lastTry bool) (bool, error) {
	epochBlocks := indexer.blockCache.getEpochBlocks(epoch)
	nextEpochBlocks := indexer.blockCache.getEpochBlocks(epoch + 1)
	chainState := indexer.consensusPool.GetChainState()

	canonicalBlocks := []*Block{}
	orphanedBlocks := []*Block{}
	nextEpochCanonicalBlocks := []*Block{}

	var dependentRoot phase0.Root

	for _, block := range epochBlocks {
		// restore block body from db as we gonna use it a lot for the epoch & voting aggregations
		// block is wiped from cache after processing anyway, so no need to prune it again
		block.unpruneBlockBody()

		if indexer.blockCache.isCanonicalBlock(block.Root, justifiedRoot) {
			if _, err := block.EnsureBlock(func() (*spec.VersionedSignedBeaconBlock, error) {
				return client.loadBlock(block.Root)
			}); err != nil {
				client.logger.Warnf("failed loading finalized block body %v (%v): %v", block.Slot, block.Root.String(), err)
			}

			if block.block == nil {
				return true, fmt.Errorf("missing block body for canonical block %v (%v)", block.Slot, block.Root.String())
			}
			canonicalBlocks = append(canonicalBlocks, block)
		} else {
			if block.isInFinalizedDb {
				// orphaned block which is already in db, ignore
				continue
			}
			if block.block == nil {
				indexer.logger.Warnf("missing block body for orphaned block %v (%v)", block.Slot, block.Root.String())
				continue
			}

			orphanedBlocks = append(orphanedBlocks, block)
		}
	}

	for _, block := range nextEpochBlocks {
		if indexer.blockCache.isCanonicalBlock(block.Root, justifiedRoot) {
			block.unpruneBlockBody()
			nextEpochCanonicalBlocks = append(nextEpochCanonicalBlocks, block)
		}
	}

	// sort by slot, all aggregations expect blocks in ascending order
	sort.Slice(canonicalBlocks, func(i, j int) bool {
		return canonicalBlocks[i].Slot < canonicalBlocks[j].Slot
	})
	sort.Slice(nextEpochCanonicalBlocks, func(i, j int) bool {
		return nextEpochCanonicalBlocks[i].Slot < nextEpochCanonicalBlocks[j].Slot
	})

	// check if first canonical block is really the first block of the epoch
	// clients do backfilling, so we only need to check if the first block
	if len(canonicalBlocks) > 0 {
		// check if first blocks parent is from parent epoch
		firstBlock := canonicalBlocks[0]
		isValid := false

		dependentBlock := indexer.blockCache.getDependentBlock(chainState, firstBlock, client)
		if dependentBlock != nil {
			dependentRoot = dependentBlock.Root
			isValid = chainState.EpochOfSlot(dependentBlock.Slot) < chainState.EpochOfSlot(firstBlock.Slot)
		} else {
			depRoot := firstBlock.GetParentRoot()
			if depRoot != nil {
				dependentRoot = *depRoot
				dependentHead, _ := client.loadHeader(*depRoot)
				isValid = dependentHead != nil && chainState.EpochOfSlot(dependentHead.Message.Slot) < chainState.EpochOfSlot(firstBlock.Slot)
			}
		}

		if !isValid {
			return false, fmt.Errorf("first canonical block %v (%v) is not the first block of epoch %v", firstBlock.Slot, firstBlock.Root.String(), epoch)
		}
	} else {
		// check if there's really no canonical block in the epoch
		canonicalBlock := indexer.blockCache.getBlockByRoot(justifiedRoot)
		for {
			if canonicalBlock == nil {
				return false, fmt.Errorf("missing blocks between epoch %v and the finalized checkpoint", epoch)
			}

			blockEpoch := chainState.EpochOfSlot(canonicalBlock.Slot)
			if blockEpoch == epoch {
				return false, fmt.Errorf("missing blocks in epoch %v", epoch)
			}

			if chainState.EpochOfSlot(canonicalBlock.Slot) < epoch {
				// we've walked back to the previous epoch without finding any canonical block for this epoch
				// so there's no canonical block in this epoch
				dependentRoot = canonicalBlock.Root
				break
			}

			parentRoot := canonicalBlock.GetParentRoot()
			if parentRoot == nil {
				return false, fmt.Errorf("missing blocks between epoch %v and the finalized checkpoint", epoch)
			}

			canonicalBlock = indexer.blockCache.getBlockByRoot(*parentRoot)
			if canonicalBlock == nil {
				blockHead := db.GetBlockHeadByRoot((*parentRoot)[:])
				if blockHead != nil {
					canonicalBlock = newBlock(indexer.dynSsz, phase0.Root(blockHead.Root), phase0.Slot(blockHead.Slot))
					canonicalBlock.isInFinalizedDb = true
					parentRootVal := phase0.Root(blockHead.ParentRoot)
					canonicalBlock.parentRoot = &parentRootVal
				}
			}
			if canonicalBlock == nil {
				dependentHead, _ := client.loadHeader(*parentRoot)

				if dependentHead != nil {
					canonicalBlock = newBlock(indexer.dynSsz, phase0.Root(*parentRoot), phase0.Slot(dependentHead.Message.Slot))
					canonicalBlock.isInFinalizedDb = true
					parentRootVal := phase0.Root(dependentHead.Message.ParentRoot)
					canonicalBlock.parentRoot = &parentRootVal
				}
			}
		}
	}

	// get epoch stats
	var epochStatsValues *EpochStatsValues
	var epochVotes *EpochVotes

	epochStats := indexer.epochCache.getEpochStats(epoch, dependentRoot)
	if epochStats != nil {
		epochStatsValues = epochStats.GetValues(chainState)
	}

	if epochStatsValues == nil {
		if !lastTry { // do not error on last try, we can at least persist the canonical and orphaned blocks even without epoch stats
			return false, fmt.Errorf("missing epoch stats values for epoch %v", epoch)
		}
	} else {
		// compute votes for canonical blocks
		votingBlocks := make([]*Block, len(canonicalBlocks)+len(nextEpochCanonicalBlocks))
		copy(votingBlocks, canonicalBlocks)
		copy(votingBlocks[len(canonicalBlocks):], nextEpochCanonicalBlocks)
		epochVotes = indexer.aggregateEpochVotes(chainState, votingBlocks, epochStats)
		if epochVotes == nil && !lastTry {
			return false, fmt.Errorf("failed computing votes for epoch %v", epoch)
		}
	}

	// persist to db
	deleteBeforeSlot := chainState.EpochToSlot(epoch + 1)
	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		// persist canonical epoch data
		if err := indexer.dbWriter.persistEpochData(tx, epoch, canonicalBlocks, epochStats, epochVotes); err != nil {
			return fmt.Errorf("failed persisting epoch data for epoch %v: %v", epoch, err)
		}

		// persist orphaned blocks
		orphanedForkId := ForkKey(1)
		for _, block := range orphanedBlocks {
			dependentBlock := indexer.blockCache.getDependentBlock(chainState, block, client)
			epochStats := indexer.epochCache.getEpochStats(epoch, dependentBlock.Root)

			if err := indexer.dbWriter.persistBlockData(tx, block, epochStats, nil, true, &orphanedForkId); err != nil {
				return fmt.Errorf("failed persisting orphaned slot %v (%v): %v", block.Slot, block.Root.String(), err)
			}

			orphanedBlock, err := block.buildOrphanedBlock()
			if err != nil {
				return fmt.Errorf("failed building orphaned block %v (%v): %v", block.Slot, block.Root.String(), err)
			}

			if err := db.InsertOrphanedBlock(orphanedBlock, tx); err != nil {
				return fmt.Errorf("failed persisting orphaned slot %v (%v): %v", block.Slot, block.Root.String(), err)
			}
		}

		// persist sync committee assignments
		if err := indexer.dbWriter.persistSyncAssignments(tx, epoch, epochStats); err != nil {
			return fmt.Errorf("error persisting sync committee assignments to db: %v", err)
		}

		// delete unfinalized duties before epoch
		if err := db.DeleteUnfinalizedDutiesBefore(uint64(epoch+1), tx); err != nil {
			return fmt.Errorf("failed deleting unfinalized duties <= epoch %v: %v", epoch, err)
		}

		// delete unfinalized duties before epoch
		if err := db.DeleteUnfinalizedBlocksBefore(uint64(deleteBeforeSlot), tx); err != nil {
			return fmt.Errorf("failed deleting unfinalized duties < slot %v: %v", deleteBeforeSlot, err)
		}

		// delete unfinalized duties before epoch
		if err := db.DeleteUnfinalizedEpochsBefore(uint64(epoch+1), tx); err != nil {
			return fmt.Errorf("failed deleting unfinalized duties <= epoch %v: %v", epoch, err)
		}

		// delete unfinalized forks before epoch
		if err := db.DeleteUnfinalizedForks(uint64(deleteBeforeSlot), tx); err != nil {
			return fmt.Errorf("failed deleting unfinalized forks < slot %v: %v", deleteBeforeSlot, err)
		}

		return nil
	})
	if err != nil {
		return false, fmt.Errorf("failed persisting epoch %v data: %v", epoch, err)
	}

	indexer.lastFinalizedEpoch = epoch + 1
	indexer.logger.Infof("completed epoch %v finalization", epoch)
	indexer.logger.Infof("epoch %v blocks: %v canonical, %v orphaned", epoch, len(canonicalBlocks), len(orphanedBlocks))
	if epochStatsValues != nil {
		indexer.logger.Infof("epoch %v stats: %v validators (%v ETH)", epoch, epochStatsValues.ActiveValidators, epochStatsValues.EffectiveBalance/EtherGweiFactor)
		indexer.logger.Infof(
			"epoch %v votes: target %v + %v = %v ETH (%.2f%%)",
			epoch,
			epochVotes.CurrentEpoch.TargetVoteAmount/EtherGweiFactor,
			epochVotes.NextEpoch.TargetVoteAmount/EtherGweiFactor,
			(epochVotes.CurrentEpoch.TargetVoteAmount+epochVotes.NextEpoch.TargetVoteAmount)/EtherGweiFactor,
			float64(epochVotes.CurrentEpoch.TargetVoteAmount)*100/float64(epochVotes.CurrentEpoch.TargetVoteAmount+epochVotes.NextEpoch.TargetVoteAmount),
		)
		indexer.logger.Infof(
			"epoch %v votes: head %v + %v = %v ETH (%.2f%%)",
			epoch,
			epochVotes.CurrentEpoch.HeadVoteAmount/EtherGweiFactor,
			epochVotes.NextEpoch.HeadVoteAmount/EtherGweiFactor,
			(epochVotes.CurrentEpoch.HeadVoteAmount+epochVotes.NextEpoch.HeadVoteAmount)/EtherGweiFactor,
			float64(epochVotes.CurrentEpoch.HeadVoteAmount)*100/float64(epochVotes.CurrentEpoch.HeadVoteAmount+epochVotes.NextEpoch.HeadVoteAmount),
		)
		indexer.logger.Infof(
			"epoch %v votes: total %v + %v = %v ETH (%.2f%%)",
			epoch,
			epochVotes.CurrentEpoch.TotalVoteAmount/EtherGweiFactor,
			epochVotes.NextEpoch.TotalVoteAmount/EtherGweiFactor,
			(epochVotes.CurrentEpoch.TotalVoteAmount+epochVotes.NextEpoch.TotalVoteAmount)/EtherGweiFactor,
			float64(epochVotes.CurrentEpoch.TotalVoteAmount)*100/float64(epochVotes.CurrentEpoch.TotalVoteAmount+epochVotes.NextEpoch.TotalVoteAmount),
		)
	}

	// sleep 500 ms to give running UI threads time to fetch data from cache
	time.Sleep(500 * time.Millisecond)

	// clean epoch stats
	indexer.epochCache.removeEpochStatsByEpoch(epoch)

	// clean block cache
	for _, block := range canonicalBlocks {
		indexer.blockCache.removeBlock(block)
	}
	for _, block := range orphanedBlocks {
		indexer.blockCache.removeBlock(block)
	}

	// clean fork cache
	indexer.forkCache.setFinalizedEpoch(deleteBeforeSlot, justifiedRoot)
	for _, fork := range indexer.forkCache.getForksBefore(deleteBeforeSlot) {
		indexer.forkCache.removeFork(fork.forkId)
	}

	return (epochStatsValues == nil || epochVotes == nil), nil
}
