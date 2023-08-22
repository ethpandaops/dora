package indexer

import (
	"time"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

func (cache *indexerCache) runCacheLoop() {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Uncaught panic in runCacheLoop subroutine: %v", err)
		}
	}()

	for {
		select {
		case <-cache.triggerChan:
		case <-time.After(30 * time.Second):
		}
		logger.Debugf("Run indexer cache logic")
		err := cache.runCacheLogic()
		if err != nil {
			logger.Errorf("Indexer cache error: %v, retrying in 10 sec...", err)
			time.Sleep(10 * time.Second)
		}
	}
}

func (cache *indexerCache) runCacheLogic() error {
	if cache.highestSlot < 0 {
		return nil
	}

	var cleanupEpoch int64
	if cache.indexer.writeDb {
		if cache.finalizedEpoch > 0 && cache.processedEpoch == -2 {
			syncState := dbtypes.IndexerSyncState{}
			_, err := db.GetExplorerState("indexer.syncstate", &syncState)
			if err != nil {
				cache.processedEpoch = -1
			} else {
				cache.processedEpoch = int64(syncState.Epoch)
			}

			if cache.processedEpoch < cache.finalizedEpoch {
				var syncStartEpoch uint64
				if cache.processedEpoch < 0 {
					syncStartEpoch = 0
				} else {
					syncStartEpoch = uint64(cache.processedEpoch)
				}
				cache.startSynchronizer(syncStartEpoch)
				cache.processedEpoch = cache.finalizedEpoch
			}
		}

		logger.Debugf("check finalized processing %v < %v", cache.processedEpoch, cache.finalizedEpoch)
		if cache.processedEpoch < cache.finalizedEpoch {
			// process finalized epochs
			err := cache.processFinalizedEpochs()
			if err != nil {
				return err
			}
		}

		if cache.lowestSlot >= 0 && int64(utils.EpochOfSlot(uint64(cache.lowestSlot))) < cache.processedEpoch {
			// process cached blocks in already processed epochs (duplicates or new orphaned blocks)
			err := cache.processOrphanedBlocks(cache.processedEpoch)
			if err != nil {
				return err
			}
		}

		if cache.persistEpoch < cache.processedEpoch {
			// process cache persistence
			err := cache.processCachePersistence()
			if err != nil {
				return err
			}
			cache.persistEpoch = cache.processedEpoch
		}
		cleanupEpoch = cache.processedEpoch
	} else {
		cleanupEpoch = cache.finalizedEpoch
	}

	if cache.cleanupEpoch < cleanupEpoch {
		// process cache persistence
		err := cache.processCacheCleanup(cleanupEpoch)
		if err != nil {
			return err
		}
		cache.cleanupEpoch = cleanupEpoch
	}

	return nil
}

func (cache *indexerCache) processFinalizedEpochs() error {
	for cache.processedEpoch < cache.finalizedEpoch {
		processEpoch := uint64(cache.processedEpoch + 1)
		err := cache.processFinalizedEpoch(processEpoch)
		if err != nil {
			return err
		}
		cache.processedEpoch = int64(processEpoch)
	}
	return nil
}

func (cache *indexerCache) processFinalizedEpoch(epoch uint64) error {
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	firstBlock := cache.getFirstCanonicalBlock(epoch, nil)
	var epochTarget []byte
	var epochDependentRoot []byte
	if firstBlock == nil {
		logger.Warnf("Counld not find epoch %v target (no block found)", epoch)
	} else {
		if firstBlock.slot == firstSlot {
			epochTarget = firstBlock.root
		} else {
			epochTarget = firstBlock.header.Message.ParentRoot
		}
		epochDependentRoot = firstBlock.header.Message.ParentRoot
	}
	logger.Infof("Processing finalized epoch %v:  target: 0x%x, dependent: 0x%x", epoch, epochTarget, epochDependentRoot)

	// get epoch stats
	epochStats, isNewStats := cache.createOrGetEpochStats(epoch, epochDependentRoot)
	if isNewStats {
		logger.Warnf("Loading epoch stats during finalized epoch %v processing.", epoch)
	}
	epochStats.dutiesMutex.RLock()
	defer epochStats.dutiesMutex.RUnlock()
	epochStats.validatorsMutex.RLock()
	defer epochStats.validatorsMutex.RUnlock()

	// get canonical blocks
	canonicalMap := cache.getCanonicalBlockMap(epoch, nil)
	// append next epoch blocks (needed for vote aggregation)
	for slot, block := range cache.getCanonicalBlockMap(epoch+1, nil) {
		canonicalMap[slot] = block
	}

	// calculate votes
	epochVotes := aggregateEpochVotes(canonicalMap, epoch, epochStats, epochTarget, false)

	if epochStats.validatorStats != nil {
		logger.Infof("Epoch %v stats: %v validators (%v)", epoch, epochStats.validatorStats.ValidatorCount, epochStats.validatorStats.EligibleAmount)
	}
	logger.Infof("Epoch %v votes: target %v + %v = %v", epoch, epochVotes.currentEpoch.targetVoteAmount, epochVotes.nextEpoch.targetVoteAmount, epochVotes.currentEpoch.targetVoteAmount+epochVotes.nextEpoch.targetVoteAmount)
	logger.Infof("Epoch %v votes: head %v + %v = %v", epoch, epochVotes.currentEpoch.headVoteAmount, epochVotes.nextEpoch.headVoteAmount, epochVotes.currentEpoch.headVoteAmount+epochVotes.nextEpoch.headVoteAmount)
	logger.Infof("Epoch %v votes: total %v + %v = %v", epoch, epochVotes.currentEpoch.totalVoteAmount, epochVotes.nextEpoch.totalVoteAmount, epochVotes.currentEpoch.totalVoteAmount+epochVotes.nextEpoch.totalVoteAmount)

	// store canonical blocks to db and remove from cache
	tx, err := db.WriterDb.Beginx()
	if err != nil {
		logger.Errorf("error starting db transactions: %v", err)
		return err
	}
	defer tx.Rollback()

	err = persistEpochData(epoch, canonicalMap, epochStats, epochVotes, tx)
	if err != nil {
		logger.Errorf("error persisting epoch data to db: %v", err)
	}

	if cache.synchronizer == nil || !cache.synchronizer.running {
		err = db.SetExplorerState("indexer.syncstate", &dbtypes.IndexerSyncState{
			Epoch: epoch,
		}, tx)
		if err != nil {
			logger.Errorf("error while updating sync state: %v", err)
		}
	}

	if err := tx.Commit(); err != nil {
		logger.Errorf("error committing db transaction: %v", err)
		return err
	}

	// remove canonical blocks from cache
	for slot, block := range canonicalMap {
		if utils.EpochOfSlot(slot) == epoch {
			cache.removeCachedBlock(block)
		}
	}

	return nil
}

func (cache *indexerCache) processOrphanedBlocks(processedEpoch int64) error {
	cachedBlocks := map[string]*indexerCacheBlock{}
	orphanedBlocks := map[string]*indexerCacheBlock{}
	blockRoots := [][]byte{}
	cache.cacheMutex.RLock()
	for slot, blocks := range cache.slotMap {
		if int64(utils.EpochOfSlot(slot)) <= processedEpoch {
			for _, block := range blocks {
				cachedBlocks[string(block.root)] = block
				orphanedBlocks[string(block.root)] = block
				blockRoots = append(blockRoots, block.root)
			}
		}
	}
	cache.cacheMutex.RUnlock()

	logger.Infof("Processing %v non-canonical blocks (epoch <= %v)", len(cachedBlocks), processedEpoch)
	if len(cachedBlocks) == 0 {
		return nil
	}

	// check if blocks are already in db
	for _, blockRef := range db.GetBlockOrphanedRefs(blockRoots) {
		if blockRef.Orphaned {
			logger.Debugf("Processed duplicate orphaned block: 0x%x", blockRef.Root)
		} else {
			logger.Warnf("Processed duplicate canonical block in orphaned handler: 0x%x", blockRef.Root)
		}
		delete(orphanedBlocks, string(blockRef.Root))
	}

	// save orphaned blocks to db
	tx, err := db.WriterDb.Beginx()
	if err != nil {
		logger.Errorf("error starting db transactions: %v", err)
		return err
	}
	defer tx.Rollback()

	for _, block := range orphanedBlocks {
		dbBlock := buildDbBlock(block, cache.getEpochStats(utils.EpochOfSlot(block.slot), nil))
		dbBlock.Orphaned = true
		db.InsertBlock(dbBlock, tx)
		db.InsertOrphanedBlock(block.buildOrphanedBlock(), tx)
	}

	if err := tx.Commit(); err != nil {
		logger.Errorf("error committing db transaction: %v", err)
		return err
	}

	// remove blocks from cache
	for _, block := range cachedBlocks {
		cache.removeCachedBlock(block)
	}

	return nil
}

func (cache *indexerCache) processCachePersistence() error {
	logger.Infof("Processing cache persistence")

	// TODO
	return nil
}

func (cache *indexerCache) processCacheCleanup(processedEpoch int64) error {
	cachedBlocks := map[string]*indexerCacheBlock{}
	clearStats := []*EpochStats{}
	cache.cacheMutex.RLock()
	for slot, blocks := range cache.slotMap {
		if int64(utils.EpochOfSlot(slot)) <= processedEpoch {
			for _, block := range blocks {
				cachedBlocks[string(block.root)] = block
			}
		}
	}
	cache.cacheMutex.RUnlock()
	cache.epochStatsMutex.RLock()
	for epoch, stats := range cache.epochStatsMap {
		if int64(epoch) <= processedEpoch {
			clearStats = append(clearStats, stats...)
		}
	}
	cache.epochStatsMutex.RUnlock()

	logger.Infof("Cache cleanup: remove %v blocks, %v epoch stats", len(cachedBlocks), len(clearStats))
	if len(cachedBlocks) > 0 {
		// remove blocks from cache
		for _, block := range cachedBlocks {
			cache.removeCachedBlock(block)
		}
	}

	if len(clearStats) > 0 {
		// remove blocks from cache
		for _, stats := range clearStats {
			cache.removeEpochStats(stats)
		}
	}

	return nil
}
