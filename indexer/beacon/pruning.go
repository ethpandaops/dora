package beacon

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
	"github.com/mashingan/smapping"
)

func (indexer *Indexer) runCachePruning() error {
	chainState := indexer.consensusPool.GetChainState()

	pruneToEpoch := chainState.CurrentEpoch()
	if pruneToEpoch >= phase0.Epoch(indexer.inMemoryEpochs) {
		pruneToEpoch -= phase0.Epoch(indexer.inMemoryEpochs)
	} else {
		pruneToEpoch = 0
	}

	if pruneToEpoch < indexer.lastFinalizedEpoch {
		pruneToEpoch = indexer.lastFinalizedEpoch
	}

	// process all epochs that are not yet pruned and can be pruned
	for pruneEpoch := indexer.lastPrunedEpoch; pruneEpoch < pruneToEpoch; pruneEpoch++ {
		if err := indexer.processEpochPruning(pruneEpoch); err != nil {
			return fmt.Errorf("failed pruning epoch %d: %v", pruneEpoch, err)
		}
	}

	// process all remaining blocks in cache
	if err := indexer.processCachePruning(); err != nil {
		return fmt.Errorf("failed pruning cache: %v", err)
	}

	return nil
}

func (cache *forkCache) updatePruningState(tx *sqlx.Tx, epoch phase0.Epoch) error {
	err := db.SetExplorerState("indexer.prunestate", &dbtypes.IndexerPruneState{
		Epoch: uint64(epoch),
	}, tx)
	if err != nil {
		return fmt.Errorf("error while updating pruning state: %v", err)
	}
	return nil
}

type pruningEpochData struct {
	dependentRoot phase0.Root
	chainHead     *Block
	chain         []*Block
	epochStats    *EpochStats
	epochVotes    *EpochVotes
}

func (indexer *Indexer) processEpochPruning(pruneEpoch phase0.Epoch) error {
	chainState := indexer.consensusPool.GetChainState()
	indexer.logger.Infof("process epoch %d pruning", pruneEpoch)

	// get all blocks from this epoch and sort by slot (aggregations expect blocks in ascending order)
	pruningBlocks := indexer.blockCache.getEpochBlocks(pruneEpoch)
	sort.Slice(pruningBlocks, func(i, j int) bool {
		return pruningBlocks[i].Slot < pruningBlocks[j].Slot
	})

	nextEpochBlocks := indexer.blockCache.getEpochBlocks(pruneEpoch + 1)
	sort.Slice(nextEpochBlocks, func(i, j int) bool {
		return nextEpochBlocks[i].Slot < nextEpochBlocks[j].Slot
	})

	// group blocks by dependent roots and process each group independently
	dependentGroups := map[phase0.Root][]*Block{}
	pruningBlockRoots := [][]byte{}
	for _, block := range pruningBlocks {
		pruningBlockRoots = append(pruningBlockRoots, block.Root[:])

		var dependentRoot phase0.Root
		client := indexer.GetReadyClientByBlockRoot(block.Root)
		if client == nil {
			seenBy := block.GetSeenBy()
			if len(seenBy) > 0 {
				client = seenBy[0]
			}
		}
		if dependentBlock := indexer.blockCache.getDependentBlock(chainState, block, client); dependentBlock != nil {
			dependentRoot = dependentBlock.Root
		}

		if dependentGroups[dependentRoot] == nil {
			dependentGroups[dependentRoot] = []*Block{block}
		} else {
			dependentGroups[dependentRoot] = append(dependentGroups[dependentRoot], block)
		}
	}

	// process each group and generate epoch aggregations
	epochData := []*pruningEpochData{}
	for dependentRoot, blocks := range dependentGroups {
		epochStats := indexer.epochCache.getEpochStats(pruneEpoch, dependentRoot)

		// ensure epoch stats are loaded
		// if the state is not yet loaded, we set it to high priority and wait for it to be loaded
		if !epochStats.ready && epochStats.dependentState != nil && epochStats.dependentState.loadingStatus != 2 && epochStats.dependentState.retryCount < 10 {
			indexer.logger.Infof("epoch %d state (%v) not yet loaded, waiting for state to be loaded", pruneEpoch, dependentRoot.String())
			epochStats.dependentState.highPriority = true
			loaded := epochStats.dependentState.awaitStateLoaded(context.Background(), beaconStateRequestTimeout)
			if loaded {
				// wait for async duty computation to be completed
				time.Sleep(500 * time.Millisecond)
			}
		}

		// get all chain heads from the list of blocks
		chainHeads := map[phase0.Root]*Block{}
		for _, block := range blocks {
			parentRoot := block.GetParentRoot()
			if parentRoot != nil {
				delete(chainHeads, *parentRoot)
			}

			chainHeads[block.Root] = block
		}

		// reconstruct all chains from the chain heads
		chainBlocks := map[*Block][]*Block{}
		for _, chainHead := range chainHeads {
			chain := []*Block{}

			for _, block := range blocks {
				if indexer.blockCache.isCanonicalBlock(block.Root, chainHead.Root) {
					chain = append(chain, block)
				}
			}

			chainBlocks[chainHead] = chain
		}

		// generate epoch aggregations for each chain
		for chainHead, chain := range chainBlocks {
			nextBlocks := []*Block{}
			nextParentRoot := chainHead.Root
			for _, block := range nextEpochBlocks {
				parentRoot := block.GetParentRoot()
				if parentRoot != nil && bytes.Equal((*parentRoot)[:], nextParentRoot[:]) {
					nextBlocks = append(nextBlocks, block)
					nextParentRoot = block.Root
				}
			}

			// compute votes for canonical blocks
			votingBlocks := make([]*Block, len(chain)+len(nextBlocks))
			copy(votingBlocks, chain)
			copy(votingBlocks[len(chain):], nextBlocks)
			epochVotes := indexer.aggregateEpochVotes(chainState, votingBlocks, epochStats)

			epochData = append(epochData, &pruningEpochData{
				dependentRoot: dependentRoot,
				chainHead:     chainHead,
				chain:         chain,
				epochStats:    epochStats,
				epochVotes:    epochVotes,
			})
		}
	}

	// persist data in db
	db.RunDBTransaction(func(tx *sqlx.Tx) error {
		persistedBlocks := map[phase0.Root]bool{}

		for _, epochData := range epochData {
			dbEpoch := indexer.dbWriter.buildDbEpoch(pruneEpoch, epochData.chain, epochData.epochStats, epochData.epochVotes, func(block *Block, depositIndex *uint64) {
				if persistedBlocks[block.Root] {
					return
				}

				dbBlock, err := indexer.dbWriter.persistBlockData(tx, block, epochData.epochStats, depositIndex, false, nil)
				if err != nil {
					indexer.logger.Errorf("error persisting pruned slot %v: %v", block.Root.String(), err)
				}

				block.cachedDbBlock = dbBlock
			})

			mapped := smapping.MapTags(dbEpoch, "db")

			dbUnfinalizedEpoch := dbtypes.UnfinalizedEpoch{}
			err := smapping.FillStructByTags(&dbUnfinalizedEpoch, mapped, "db")
			if err != nil {
				indexer.logger.Errorf("mapper failed copying epoch to unfinalized epoch: %v", err)
				continue
			}

			dbUnfinalizedEpoch.DependentRoot = epochData.dependentRoot[:]
			dbUnfinalizedEpoch.EpochHeadRoot = epochData.chainHead.Root[:]
			dbUnfinalizedEpoch.EpochHeadForkId = uint64(epochData.chainHead.forkId)

			if epochData.epochStats != nil {
				if epochData.epochStats.prunedEpochAggregations == nil {
					epochData.epochStats.prunedEpochAggregations = []*dbtypes.UnfinalizedEpoch{}
				}

				epochData.epochStats.prunedEpochAggregations = append(epochData.epochStats.prunedEpochAggregations, &dbUnfinalizedEpoch)
			}

			err = db.InsertUnfinalizedEpoch(&dbUnfinalizedEpoch, tx)
			if err != nil {
				indexer.logger.Errorf("error persisting unfinalized epoch %v: %v", dbUnfinalizedEpoch.Epoch, err)
			}
		}

		err := db.UpdateUnfinalizedBlockStatus(pruningBlockRoots, dbtypes.UnfinalizedBlockStatusPruned, tx)
		if err != nil {
			indexer.logger.Errorf("error updating block status to pruned: %v", err)
		}

		err = indexer.forkCache.updatePruningState(tx, pruneEpoch+1)
		if err != nil {
			return fmt.Errorf("error while updating prune state: %v", err)
		}

		return nil
	})

	indexer.lastPrunedEpoch = pruneEpoch + 1
	indexer.logger.Infof("pruned epoch %d with %v blocks", pruneEpoch, len(pruningBlocks))

	// sleep 500 ms to give running UI threads time to fetch data from cache
	time.Sleep(500 * time.Millisecond)

	// remove bodies from all pruned blocks in cache
	for _, block := range pruningBlocks {
		block.isInFinalizedDb = true
		block.processingStatus = dbtypes.UnfinalizedBlockStatusPruned
		block.block = nil
	}

	return nil
}

type pruningBlockData struct {
	block      *Block
	epochStats *EpochStats
}

func (indexer *Indexer) processCachePruning() error {
	// process all remaining old blocks in cache (additional orphaned blocks from earlier pruned epochs)
	// we simply add those blocks as orphaned to the database. don't recalculate pruned epoch stats with them.
	chainState := indexer.consensusPool.GetChainState()
	minInMemoryEpoch := indexer.getMinInMemoryEpoch()
	minInMemorySlot := indexer.getMinInMemorySlot()
	pruningBlocks := indexer.blockCache.getPruningBlocks(minInMemorySlot)

	sort.Slice(pruningBlocks, func(i, j int) bool {
		return pruningBlocks[i].Slot < pruningBlocks[j].Slot
	})

	pruningData := []*pruningBlockData{}
	for _, block := range pruningBlocks {
		if block.isInFinalizedDb {
			continue
		}

		var dependentRoot phase0.Root
		client := indexer.GetReadyClientByBlockRoot(block.Root)
		if client == nil {
			seenBy := block.GetSeenBy()
			if len(seenBy) > 0 {
				client = seenBy[0]
			}
		}
		if dependentBlock := indexer.blockCache.getDependentBlock(chainState, block, client); dependentBlock != nil {
			dependentRoot = dependentBlock.Root
		}

		epochStats := indexer.epochCache.getEpochStats(chainState.EpochOfSlot(block.Slot), dependentRoot)

		pruningData = append(pruningData, &pruningBlockData{
			block:      block,
			epochStats: epochStats,
		})
	}

	if len(pruningData) > 0 {
		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			for _, pruneBlock := range pruningData {
				dbBlock, err := indexer.dbWriter.persistBlockData(tx, pruneBlock.block, pruneBlock.epochStats, nil, true, nil)
				if err != nil {
					indexer.logger.Errorf("error persisting old pruned slot %v: %v", pruneBlock.block.Root.String(), err)
				}

				pruneBlock.block.cachedDbBlock = dbBlock
			}

			return nil
		})
		if err != nil {
			indexer.logger.Errorf("error persisting old pruned blocks: %v", err)
		}

		// sleep 500 ms to give running UI threads time to fetch data from cache
		time.Sleep(500 * time.Millisecond)
	}

	// remove bodies from all pruned blocks in cache
	for _, pruneBlock := range pruningData {
		pruneBlock.block.isInFinalizedDb = true
		pruneBlock.block.processingStatus = dbtypes.UnfinalizedBlockStatusPruned
		pruneBlock.block.block = nil
	}

	// clean up epoch stats cache
	prunedEpochStats := 0
	for _, epochStats := range indexer.epochCache.getEpochStatsBeforeEpoch(minInMemoryEpoch) {
		if epochStats.dependentState != nil {
			epochStats.dependentState = nil
		}

		if epochStats.ready && epochStats.prunedValues == nil {
			epochStats.pruneValues()
		}
	}

	prunedEpochStates := indexer.epochCache.removeUnreferencedEpochStates()

	indexer.logger.Infof("cache pruning complete! pruned %v blocks, %v epoch stats and %v epoch states", len(pruningData), prunedEpochStats, prunedEpochStates)

	return nil
}
