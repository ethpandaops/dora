package indexer

import (
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

var logger = logrus.StandardLogger().WithField("module", "indexer")

type Indexer struct {
	indexerCache   *indexerCache
	indexerClients []*indexerClient

	writeDb              bool
	prepopulateEpochs    uint16
	inMemoryEpochs       uint16
	epochProcessingDelay uint16
}

func NewIndexer() (*Indexer, error) {
	inMemoryEpochs := utils.Config.Indexer.InMemoryEpochs
	if inMemoryEpochs < 2 {
		inMemoryEpochs = 2
	}
	epochProcessingDelay := utils.Config.Indexer.EpochProcessingDelay
	if epochProcessingDelay < 2 {
		epochProcessingDelay = 2
	} else if epochProcessingDelay > inMemoryEpochs {
		inMemoryEpochs = epochProcessingDelay
	}
	prepopulateEpochs := utils.Config.Indexer.PrepopulateEpochs
	if prepopulateEpochs > inMemoryEpochs {
		prepopulateEpochs = inMemoryEpochs
	}

	indexer := &Indexer{
		indexerClients: make([]*indexerClient, 0),

		writeDb:              !utils.Config.Indexer.DisableIndexWriter,
		prepopulateEpochs:    prepopulateEpochs,
		inMemoryEpochs:       inMemoryEpochs,
		epochProcessingDelay: epochProcessingDelay,
	}
	indexer.indexerCache = newIndexerCache(indexer)

	return indexer, nil
}

func (indexer *Indexer) AddClient(index uint8, name string, endpoint string) {
	rpcClient, err := rpc.NewBeaconClient(endpoint)
	if err != nil {
		logger.Errorf("Error while adding client %v to indexer: %v", name, err)
		return
	}
	client := newIndexerClient(index, name, rpcClient, indexer.indexerCache)
	indexer.indexerClients = append(indexer.indexerClients, client)
}

func (indexer *Indexer) getReadyClient() *indexerClient {
	return indexer.indexerClients[0]
}

func (indexer *Indexer) GetRpcClient() *rpc.BeaconClient {
	return indexer.indexerClients[0].rpcClient
}

func (indexer *Indexer) GetLowestCachedSlot() int64 {
	return -1
}

func (indexer *Indexer) GetHeadSlot() uint64 {
	return 0
}

func (indexer *Indexer) GetCachedBlocks(slot uint64) []*BlockInfo {
	/*
		indexer.state.cacheMutex.RLock()
		defer indexer.state.cacheMutex.RUnlock()

		if slot < uint64(indexer.state.lowestCachedSlot) {
			return nil
		}
		blocks := indexer.state.cachedBlocks[slot]
		if blocks == nil {
			return nil
		}
		resBlocks := make([]*BlockInfo, len(blocks))
		copy(resBlocks, blocks)
	*/
	return nil
}

func (indexer *Indexer) GetCachedBlock(root []byte) *BlockInfo {
	/*
		indexer.state.cacheMutex.RLock()
		defer indexer.state.cacheMutex.RUnlock()

		if indexer.state.lowestCachedSlot < 0 {
			return nil
		}
		for slotIdx := int64(indexer.state.lastHeadBlock); slotIdx >= indexer.state.lowestCachedSlot; slotIdx-- {
			slot := uint64(slotIdx)
			if indexer.state.cachedBlocks[slot] != nil {
				blocks := indexer.state.cachedBlocks[slot]
				for bidx := 0; bidx < len(blocks); bidx++ {
					if bytes.Equal(blocks[bidx].Header.Data.Root, root) {
						return blocks[bidx]
					}
				}
			}
		}
	*/
	return nil
}

func (indexer *Indexer) GetCachedBlockByStateroot(stateroot []byte) *BlockInfo {
	/*
		indexer.state.cacheMutex.RLock()
		defer indexer.state.cacheMutex.RUnlock()

		if indexer.state.lowestCachedSlot < 0 {
			return nil
		}
		for slotIdx := int64(indexer.state.lastHeadBlock); slotIdx >= indexer.state.lowestCachedSlot; slotIdx-- {
			slot := uint64(slotIdx)
			if indexer.state.cachedBlocks[slot] != nil {
				blocks := indexer.state.cachedBlocks[slot]
				for bidx := 0; bidx < len(blocks); bidx++ {
					if bytes.Equal(blocks[bidx].Header.Data.Header.Message.StateRoot, stateroot) {
						return blocks[bidx]
					}
				}
			}
		}
	*/
	return nil
}

func (indexer *Indexer) GetCachedEpochStats(epoch uint64) *EpochStats {
	/*
		indexer.state.cacheMutex.RLock()
		defer indexer.state.cacheMutex.RUnlock()
		return indexer.state.epochStats[epoch]
	*/
	return nil
}

func (indexer *Indexer) GetCachedValidatorSet() *rpctypes.StandardV1StateValidatorsResponse {
	/*
		indexer.state.cacheMutex.RLock()
		defer indexer.state.cacheMutex.RUnlock()
		return indexer.state.headValidators
	*/
	return nil
}

func (indexer *Indexer) GetEpochVotes(epoch uint64) *EpochVotes {
	/*
		indexer.state.cacheMutex.RLock()
		defer indexer.state.cacheMutex.RUnlock()

		epochStats := indexer.state.epochStats[epoch]
		if epochStats == nil {
			return nil
		}

		return indexer.getEpochVotes(epoch, epochStats)
	*/
	return nil
}

func (indexer *Indexer) getEpochVotes(epoch uint64, epochStats *EpochStats) *EpochVotes {
	/*
			var firstBlock *BlockInfo
			firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
			lastSlot := firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch) - 1
		slotLoop:
			for slot := firstSlot; slot <= lastSlot; slot++ {
				if indexer.state.cachedBlocks[slot] != nil {
					blocks := indexer.state.cachedBlocks[slot]
					for bidx := 0; bidx < len(blocks); bidx++ {
						if !blocks[bidx].Orphaned {
							firstBlock = blocks[bidx]
							break slotLoop
						}
					}
				}
			}
			if firstBlock == nil {
				return nil
			}

			var targetRoot []byte
			if uint64(firstBlock.Header.Data.Header.Message.Slot) == firstSlot {
				targetRoot = firstBlock.Header.Data.Root
			} else {
				targetRoot = firstBlock.Header.Data.Header.Message.ParentRoot
			}
			return aggregateEpochVotes(indexer.state.cachedBlocks, epoch, epochStats, targetRoot, false)
	*/
	return nil
}

func (indexer *Indexer) BuildLiveEpoch(epoch uint64) *dbtypes.Epoch {
	/*
		indexer.state.cacheMutex.RLock()
		defer indexer.state.cacheMutex.RUnlock()

		epochStats := indexer.state.epochStats[epoch]
		if epochStats == nil {
			return nil
		}
		epochVotes := indexer.getEpochVotes(epoch, epochStats)
		return buildDbEpoch(epoch, indexer.state.cachedBlocks, epochStats, epochVotes, nil)
	*/
	return nil
}

func (indexer *Indexer) BuildLiveBlock(block *BlockInfo) *dbtypes.Block {
	/*
		epoch := utils.EpochOfSlot(uint64(block.Header.Data.Header.Message.Slot))
		epochStats := indexer.state.epochStats[epoch]
		return buildDbBlock(block, epochStats)
	*/
	return nil
}

/*
func (indexer *Indexer) startSynchronization(startEpoch uint64) error {
	if !indexer.writeDb {
		return nil
	}

	indexer.controlMutex.Lock()
	defer indexer.controlMutex.Unlock()

	if indexer.synchronizer == nil {
		indexer.synchronizer = newSynchronizer(indexer)
	}
	if !indexer.synchronizer.isEpochAhead(startEpoch) {
		indexer.synchronizer.startSync(startEpoch)
	}
	return nil
}

func (indexer *Indexer) pollHeadBlock() error {
	header, err := indexer.rpcClient.GetLatestBlockHead()
	if err != nil {
		return err
	}
	if bytes.Equal(header.Data.Root, indexer.state.lastHeadRoot) {
		return nil // chain head didn't proceed, block missied?
	}
	block, err := indexer.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return err
	}

	headSlot := uint64(header.Data.Header.Message.Slot)
	if indexer.state.lastHeadBlock < headSlot-1 {
		backfillSlot := indexer.state.lastHeadBlock + 1
		for backfillSlot < headSlot {
			indexer.pollBackfillBlock(backfillSlot)
			backfillSlot++
		}
	}

	epoch := utils.EpochOfSlot(headSlot)
	logger.Infof("Process latest slot %v/%v: %v", epoch, headSlot, header.Data.Root)
	indexer.processHeadEpoch(epoch, nil)
	indexer.processHeadBlock(headSlot, header, block)

	return nil
}

func (indexer *Indexer) pollBackfillBlock(slot uint64) (*BlockInfo, error) {
	header, err := indexer.rpcClient.GetBlockHeaderBySlot(slot)
	if err != nil {
		return nil, err
	}
	if header == nil {
		logger.Infof("Process missed slot %v/%v", utils.EpochOfSlot(slot), slot)
		return nil, nil
	}
	block, err := indexer.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return nil, err
	}

	epoch := utils.EpochOfSlot(uint64(header.Data.Header.Message.Slot))
	logger.Infof("Process polled slot %v/%v: %v", epoch, header.Data.Header.Message.Slot, header.Data.Root)
	indexer.processHeadEpoch(epoch, nil)
	blockInfo := indexer.processHeadBlock(slot, header, block)

	return blockInfo, nil
}

func (indexer *Indexer) pollStreamedBlock(root []byte) (*BlockInfo, error) {
	header, err := indexer.rpcClient.GetBlockHeaderByBlockroot(root)
	if err != nil {
		return nil, err
	}
	block, err := indexer.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return nil, err
	}

	slot := uint64(header.Data.Header.Message.Slot)
	if indexer.state.lastHeadBlock < slot-1 {
		backfillSlot := indexer.state.lastHeadBlock + 1
		for backfillSlot < slot {
			indexer.pollBackfillBlock(backfillSlot)
			backfillSlot++
		}
	}

	logger.Infof("Process stream slot %v/%v: %v", utils.EpochOfSlot(slot), header.Data.Header.Message.Slot, header.Data.Root)
	blockInfo := indexer.processHeadBlock(slot, header, block)

	return blockInfo, nil
}

func (indexer *Indexer) processHeadBlock(slot uint64, header *rpctypes.StandardV1BeaconHeaderResponse, block *rpctypes.StandardV2BeaconBlockResponse) *BlockInfo {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

	blockInfo := &BlockInfo{
		Header: header,
		Block:  block,
	}
	if indexer.state.cachedBlocks[slot] == nil {
		indexer.state.cachedBlocks[slot] = make([]*BlockInfo, 1)
		indexer.state.cachedBlocks[slot][0] = blockInfo
	} else {
		blocks := indexer.state.cachedBlocks[slot]
		duplicate := false
		for bidx := 0; bidx < len(blocks); bidx++ {
			if bytes.Equal(blocks[bidx].Header.Data.Root, header.Data.Root) {
				logger.Infof("Received duplicate (reorg) block %v.%v (%v)", slot, bidx, header.Data.Root)
				duplicate = true
				blockInfo = blocks[bidx]
				break
			}
		}
		if !duplicate {
			indexer.state.cachedBlocks[slot] = append(blocks, blockInfo)
		}
	}
	if indexer.state.lowestCachedSlot < 0 || int64(slot) < indexer.state.lowestCachedSlot {
		indexer.state.lowestCachedSlot = int64(slot)
	}
	if indexer.state.highestCachedSlot < 0 || int64(slot) > indexer.state.highestCachedSlot {
		indexer.state.highestCachedSlot = int64(slot)
	}

	if (indexer.state.lastHeadRoot != nil && !bytes.Equal(indexer.state.lastHeadRoot, header.Data.Header.Message.ParentRoot)) || blockInfo.Orphaned {
		// chain did not proceed as usual, check for reorg
		logger.Debugf("Unusual chain progress, check for reorg %v (%v)", slot, header.Data.Root)
		var canonicalBlock *BlockInfo = blockInfo

		// walk backwards, mark all blocks that are not the parent of canonicalBlock as orphaned
		// when we find the parent of canonicalBlock, check if it's orphaned
		// if orphaned: set block as new canonicalBlock and continue walking backwards
		// if not orphaned, finish index loop and exit (reached end of reorged blocks)
		reachedEnd := false
		for sidx := indexer.state.highestCachedSlot; sidx >= int64(indexer.state.lowestCachedSlot) && !reachedEnd; sidx-- {
			blocks := indexer.state.cachedBlocks[uint64(sidx)]
			if blocks == nil {
				continue
			}
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				if bytes.Equal(block.Header.Data.Root, canonicalBlock.Header.Data.Root) {
					if block.Orphaned {
						logger.Infof("Chain reorg: mark %v.%v as canonical (%v)", sidx, bidx, block.Header.Data.Root)
						block.Orphaned = false
					}
				} else if bytes.Equal(block.Header.Data.Root, canonicalBlock.Header.Data.Header.Message.ParentRoot) {
					if block.Orphaned {
						logger.Infof("Chain reorg: mark %v.%v as canonical (%v)", sidx, bidx, block.Header.Data.Root)
						block.Orphaned = false
						canonicalBlock = block
					} else {
						reachedEnd = true
					}
				} else {
					if !block.Orphaned {
						logger.Infof("Chain reorg: mark %v.%v as orphaned (%v)", sidx, bidx, block.Header.Data.Root)
						block.Orphaned = true
					}
				}
			}
		}
		if !reachedEnd {
			logger.Errorf("Large chain reorg detected, resync needed")
			// TODO: Start synchronization
		} else {
			reorgMinEpoch := int64(utils.EpochOfSlot(uint64(canonicalBlock.Header.Data.Header.Message.Slot)))
			if reorgMinEpoch <= indexer.state.lastProcessedEpoch {
				logger.Infof("Chain reorg touched processed epochs, reset epoch processing to %v", reorgMinEpoch-1)
				indexer.state.lastProcessedEpoch = reorgMinEpoch - 1
			}
		}
	}
	indexer.state.lastHeadBlock = slot
	indexer.state.lastHeadRoot = header.Data.Root

	return blockInfo
}

func (indexer *Indexer) processHeadEpoch(epoch uint64, dependentRoot []byte) {
	var epochAssignments *rpctypes.EpochAssignments
	if dependentRoot == nil {
		if indexer.state.epochStats[epoch] != nil {
			return
		}
		var err error
		epochAssignments, err = indexer.rpcClient.GetEpochAssignments(epoch)
		if err != nil {
			logger.Errorf("Error fetching epoch %v duties: %v", epoch, err)
			return
		}
		dependentRoot = epochAssignments.DependendRoot
	}

	epochStats, loadAssignments, loadValidators := indexer.newEpochStats(epoch, dependentRoot)

	if loadAssignments || loadValidators {
		go indexer.loadEpochStats(epoch, dependentRoot, epochStats, loadValidators)
	}
}

func (indexer *Indexer) newEpochStats(epoch uint64, dependentRoot []byte) (*EpochStats, bool, bool) {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

	if int64(epoch) < indexer.state.lastProcessedEpoch {
		return nil, false, false
	}
	oldEpochStats := indexer.state.epochStats[epoch]
	if oldEpochStats != nil && bytes.Equal(oldEpochStats.dependendRoot, dependentRoot) {
		loadAssignments := oldEpochStats.assignmentsFailed
		if loadAssignments {
			oldEpochStats.assignmentsFailed = false
			oldEpochStats.AssignmentsMutex = sync.Mutex{}
			oldEpochStats.AssignmentsMutex.Lock()
		}

		return oldEpochStats, loadAssignments, false
	}

	epochStats := &EpochStats{}
	epochStats.dependendRoot = dependentRoot
	epochStats.AssignmentsMutex.Lock()
	indexer.state.epochStats[epoch] = epochStats

	if oldEpochStats != nil {
		epochStats.Validators = oldEpochStats.Validators
	} else {
		epochStats.Validators = &EpochValidators{
			ValidatorCount:    0,
			EligibleAmount:    0,
			ValidatorBalances: make(map[uint64]uint64),
		}
		epochStats.Validators.ValidatorsReadyMutex.Lock()

	}

	return epochStats, oldEpochStats == nil, oldEpochStats == nil
}

func (indexer *Indexer) loadEpochStats(epoch uint64, dependentRoot []byte, epochStats *EpochStats, loadValidators bool) {
	if !indexer.loadEpochAssignments(epoch, dependentRoot, epochStats) {
		return
	}
	if loadValidators {
		indexer.loadEpochValidators(epoch, epochStats)
	}
}

func (indexer *Indexer) loadEpochAssignments(epoch uint64, dependentRoot []byte, epochStats *EpochStats) bool {
	defer epochStats.AssignmentsMutex.Unlock()
	logger.Infof("Epoch %v head, fetching assignments (dependend root: 0x%x)", epoch, dependentRoot)

	epochAssignments, err := indexer.rpcClient.GetEpochAssignments(epoch)
	if err != nil {
		logger.Errorf("Error fetching epoch %v duties: %v", epoch, err)
		return false
	}
	epochStats.Assignments = epochAssignments
	return true
}

func (indexer *Indexer) loadEpochValidators(epoch uint64, epochStats *EpochStats) {
	defer epochStats.Validators.ValidatorsReadyMutex.Unlock()
	logger.Infof("Epoch %v head, loading validator set (state: %v)", epoch, epochStats.Assignments.DependendState)

	// load epoch stats
	var epochValidators *rpctypes.StandardV1StateValidatorsResponse
	var err error
	if epochStats.Assignments.DependendIsGenesis {
		epochValidators, err = indexer.rpcClient.GetGenesisValidators()
	} else {
		epochValidators, err = indexer.rpcClient.GetStateValidators(epochStats.Assignments.DependendState)
	}
	if err != nil {
		logger.Errorf("Error fetching epoch %v validators: %v", epoch, err)
	} else {
		indexer.state.cacheMutex.Lock()
		if int64(epoch) > indexer.state.headValidatorsEpoch {
			indexer.state.headValidatorsEpoch = int64(epoch)
			indexer.state.headValidators = epochValidators
		}
		indexer.state.cacheMutex.Unlock()
		epochStats.Validators.ValidatorsStatsMutex.Lock()
		for idx := 0; idx < len(epochValidators.Data); idx++ {
			validator := epochValidators.Data[idx]
			epochStats.Validators.ValidatorBalances[uint64(validator.Index)] = uint64(validator.Validator.EffectiveBalance)
			if !strings.HasPrefix(validator.Status, "active") {
				continue
			}
			epochStats.Validators.ValidatorCount++
			epochStats.Validators.ValidatorBalance += uint64(validator.Balance)
			epochStats.Validators.EligibleAmount += uint64(validator.Validator.EffectiveBalance)
		}
		epochStats.Validators.ValidatorsStatsMutex.Unlock()
	}
}

func (indexer *Indexer) processIndexing() {
	// process old epochs
	currentEpoch := utils.EpochOfSlot(indexer.state.lastHeadBlock)
	maxProcessEpoch := int64(currentEpoch) - int64(indexer.epochProcessingDelay)
	for indexer.state.lastProcessedEpoch < maxProcessEpoch {
		processEpoch := indexer.state.lastProcessedEpoch + 1
		indexer.processEpoch(uint64(processEpoch))
		indexer.state.lastProcessedEpoch = processEpoch
	}
}

func (indexer *Indexer) processCacheCleanup() {
	currentEpoch := utils.EpochOfSlot(indexer.state.lastHeadBlock)
	lowestCachedSlot := indexer.state.lowestCachedSlot

	// cleanup cache
	cleanEpoch := currentEpoch - uint64(indexer.inMemoryEpochs)
	if lowestCachedSlot >= 0 && lowestCachedSlot < int64((cleanEpoch+1)*utils.Config.Chain.Config.SlotsPerEpoch) {
		indexer.state.cacheMutex.Lock()
		defer indexer.state.cacheMutex.Unlock()
		for indexer.state.lowestCachedSlot < int64((cleanEpoch+1)*utils.Config.Chain.Config.SlotsPerEpoch) {
			cacheSlot := uint64(indexer.state.lowestCachedSlot)
			if indexer.state.cachedBlocks[cacheSlot] != nil {
				logger.Debugf("Dropped cached block (epoch %v, slot %v)", utils.EpochOfSlot(cacheSlot), indexer.state.lowestCachedSlot)
				delete(indexer.state.cachedBlocks, cacheSlot)
			}
			indexer.state.lowestCachedSlot++
		}
		if indexer.state.epochStats[cleanEpoch] != nil {
			epochStats := indexer.state.epochStats[cleanEpoch]
			indexer.rpcClient.AddCachedEpochAssignments(cleanEpoch, epochStats.Assignments)
			delete(indexer.state.epochStats, cleanEpoch)
		}
	}
}

func (indexer *Indexer) processEpoch(epoch uint64) {
	indexer.state.cacheMutex.RLock()
	defer indexer.state.cacheMutex.RUnlock()

	logger.Infof("Process epoch %v", epoch)
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	epochStats := indexer.state.epochStats[epoch]

	// await full epochStats (might not be ready in some edge cases)
	epochStats.Validators.ValidatorsReadyMutex.Lock()
	epochStats.Validators.ValidatorsReadyMutex.Unlock()

	var epochTarget []byte
slotLoop:
	for slot := firstSlot; slot <= lastSlot; slot++ {
		blocks := indexer.state.cachedBlocks[slot]
		if blocks == nil {
			continue
		}
		for bidx := 0; bidx < len(blocks); bidx++ {
			block := blocks[bidx]
			if !block.Orphaned {
				if slot == firstSlot {
					epochTarget = block.Header.Data.Root
				} else {
					epochTarget = block.Header.Data.Header.Message.ParentRoot
				}
				break slotLoop
			}
		}
	}
	if epochTarget == nil {
		logger.Warnf("Counld not find epoch %v target (no block found)", epoch)
	}

	epochVotes := aggregateEpochVotes(indexer.state.cachedBlocks, epoch, epochStats, epochTarget, false)

	// save to db
	if indexer.writeDb {
		tx, err := db.WriterDb.Beginx()
		if err != nil {
			logger.Errorf("error starting db transactions: %v", err)
			return
		}
		defer tx.Rollback()

		err = persistEpochData(epoch, indexer.state.cachedBlocks, epochStats, epochVotes, tx)
		if err != nil {
			logger.Errorf("error persisting epoch data to db: %v", err)
		}

		if indexer.synchronizer == nil || !indexer.synchronizer.running {
			err = db.SetExplorerState("indexer.syncstate", &dbtypes.IndexerSyncState{
				Epoch: epoch,
			}, tx)
			if err != nil {
				logger.Errorf("error while updating sync state: %v", err)
			}
		}

		if err := tx.Commit(); err != nil {
			logger.Errorf("error committing db transaction: %v", err)
			return
		}
	}

	logger.Infof("Epoch %v stats: %v validators (%v)", epoch, epochStats.Validators.ValidatorCount, epochStats.Validators.EligibleAmount)
	logger.Infof("Epoch %v votes: target %v + %v = %v", epoch, epochVotes.currentEpoch.targetVoteAmount, epochVotes.nextEpoch.targetVoteAmount, epochVotes.currentEpoch.targetVoteAmount+epochVotes.nextEpoch.targetVoteAmount)
	logger.Infof("Epoch %v votes: head %v + %v = %v", epoch, epochVotes.currentEpoch.headVoteAmount, epochVotes.nextEpoch.headVoteAmount, epochVotes.currentEpoch.headVoteAmount+epochVotes.nextEpoch.headVoteAmount)
	logger.Infof("Epoch %v votes: total %v + %v = %v", epoch, epochVotes.currentEpoch.totalVoteAmount, epochVotes.nextEpoch.totalVoteAmount, epochVotes.currentEpoch.totalVoteAmount+epochVotes.nextEpoch.totalVoteAmount)

	for slot := firstSlot; slot <= lastSlot; slot++ {
		blocks := indexer.state.cachedBlocks[slot]
		if blocks == nil {
			continue
		}
		for bidx := 0; bidx < len(blocks); bidx++ {
			block := blocks[bidx]
			if block.Orphaned {
				logger.Infof("Epoch %v orphaned block %v.%v: %v", epoch, slot, bidx, block.Header.Data.Root)
			}
		}
	}
}
*/
