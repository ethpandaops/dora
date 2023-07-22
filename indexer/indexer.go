package indexer

import (
	"bytes"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

var logger = logrus.StandardLogger().WithField("module", "indexer")

type Indexer struct {
	rpcClient            *rpc.BeaconClient
	controlMutex         sync.Mutex
	runMutex             sync.Mutex
	running              bool
	writeDb              bool
	inMemoryEpochs       uint16
	epochProcessingDelay uint16
	state                indexerState
	synchronizer         *synchronizerState
}

type indexerState struct {
	lastHeadBlock      uint64
	lastHeadRoot       []byte
	lastFinalizedBlock uint64
	cacheMutex         sync.Mutex
	cachedBlocks       map[uint64][]*BlockInfo
	epochStats         map[uint64]*EpochStats
	lowestCachedSlot   uint64
	lastProcessedEpoch uint64
}

type indexerSyncState struct {
	Epoch uint64 `json:"epoch"`
}

type EpochStats struct {
	StatsMutex        sync.Mutex
	ValidatorCount    uint64
	EligibleAmount    uint64
	Assignments       *rpctypes.EpochAssignments
	ValidatorBalances map[uint64]uint64
}

func NewIndexer(rpcClient *rpc.BeaconClient, inMemoryEpochs uint16, epochProcessingDelay uint16, writeDb bool) (*Indexer, error) {
	return &Indexer{
		rpcClient:            rpcClient,
		writeDb:              writeDb,
		inMemoryEpochs:       inMemoryEpochs,
		epochProcessingDelay: epochProcessingDelay,
		state: indexerState{
			cachedBlocks: make(map[uint64][]*BlockInfo),
			epochStats:   make(map[uint64]*EpochStats),
		},
	}, nil
}

func (indexer *Indexer) Start() error {
	indexer.controlMutex.Lock()
	defer indexer.controlMutex.Unlock()

	if indexer.running {
		return errors.New("indexer already running")
	}
	indexer.running = true

	go indexer.runIndexer()

	return nil
}

func (indexer *Indexer) GetLowestCachedSlot() uint64 {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()
	return indexer.state.lowestCachedSlot
}

func (indexer *Indexer) GetHeadSlot() uint64 {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()
	return indexer.state.lastHeadBlock
}

func (indexer *Indexer) GetCachedBlocks(slot uint64) []*BlockInfo {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

	if slot < indexer.state.lowestCachedSlot {
		return nil
	}
	blocks := indexer.state.cachedBlocks[slot]
	if blocks == nil {
		return nil
	}
	resBlocks := make([]*BlockInfo, len(blocks))
	copy(resBlocks, blocks)
	return resBlocks
}

func (indexer *Indexer) GetCachedEpochStats(epoch uint64) *EpochStats {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()
	return indexer.state.epochStats[epoch]
}

func (indexer *Indexer) BuildLiveEpoch(epoch uint64) *dbtypes.Epoch {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

	epochStats := indexer.state.epochStats[epoch]
	if epochStats == nil {
		return nil
	}

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
	epochVotes := aggregateEpochVotes(indexer.state.cachedBlocks, epoch, epochStats, targetRoot, false)

	totalSyncAssigned := 0
	totalSyncVoted := 0
	dbEpoch := dbtypes.Epoch{
		Epoch:          epoch,
		ValidatorCount: epochStats.ValidatorCount,
		Eligible:       epochStats.EligibleAmount,
		VotedTarget:    epochVotes.currentEpoch.targetVoteAmount + epochVotes.nextEpoch.targetVoteAmount,
		VotedHead:      epochVotes.currentEpoch.headVoteAmount + epochVotes.nextEpoch.headVoteAmount,
		VotedTotal:     epochVotes.currentEpoch.totalVoteAmount + epochVotes.nextEpoch.totalVoteAmount,
	}

	// aggregate blocks
	for slot := firstSlot; slot <= lastSlot; slot++ {
		blocks := indexer.state.cachedBlocks[slot]
		if blocks == nil {
			continue
		}
		for bidx := 0; bidx < len(blocks); bidx++ {
			block := blocks[bidx]
			if block.Orphaned {
				dbEpoch.OrphanedCount++
				continue
			}

			dbEpoch.BlockCount++
			dbEpoch.AttestationCount += uint64(len(block.Block.Data.Message.Body.Attestations))
			dbEpoch.DepositCount += uint64(len(block.Block.Data.Message.Body.Deposits))
			dbEpoch.ExitCount += uint64(len(block.Block.Data.Message.Body.VoluntaryExits))
			dbEpoch.AttesterSlashingCount += uint64(len(block.Block.Data.Message.Body.AttesterSlashings))
			dbEpoch.ProposerSlashingCount += uint64(len(block.Block.Data.Message.Body.ProposerSlashings))
			dbEpoch.BLSChangeCount += uint64(len(block.Block.Data.Message.Body.SignedBLSToExecutionChange))

			syncAggregate := block.Block.Data.Message.Body.SyncAggregate
			syncAssignments := epochStats.Assignments.SyncAssignments
			if syncAggregate != nil && syncAssignments != nil {
				votedCount := 0
				assignedCount := len(syncAssignments)
				for i := 0; i < assignedCount; i++ {
					if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
						votedCount++
					}
				}
				totalSyncAssigned += assignedCount
				totalSyncVoted += votedCount
			}

			if executionPayload := block.Block.Data.Message.Body.ExecutionPayload; executionPayload != nil {
				dbEpoch.EthTransactionCount += uint64(len(executionPayload.Transactions))
			}
		}
	}

	return &dbEpoch
}

func (indexer *Indexer) BuildLiveBlock(block *BlockInfo) *dbtypes.Block {
	dbBlock := dbtypes.Block{
		Root:                  block.Header.Data.Root,
		Slot:                  uint64(block.Header.Data.Header.Message.Slot),
		ParentRoot:            block.Header.Data.Header.Message.ParentRoot,
		StateRoot:             block.Header.Data.Header.Message.StateRoot,
		Orphaned:              block.Orphaned,
		Proposer:              uint64(block.Block.Data.Message.ProposerIndex),
		Graffiti:              block.Block.Data.Message.Body.Graffiti,
		AttestationCount:      uint64(len(block.Block.Data.Message.Body.Attestations)),
		DepositCount:          uint64(len(block.Block.Data.Message.Body.Deposits)),
		ExitCount:             uint64(len(block.Block.Data.Message.Body.VoluntaryExits)),
		AttesterSlashingCount: uint64(len(block.Block.Data.Message.Body.AttesterSlashings)),
		ProposerSlashingCount: uint64(len(block.Block.Data.Message.Body.ProposerSlashings)),
		BLSChangeCount:        uint64(len(block.Block.Data.Message.Body.SignedBLSToExecutionChange)),
	}

	epoch := utils.EpochOfSlot(uint64(block.Header.Data.Header.Message.Slot))
	epochStats := indexer.state.epochStats[epoch]
	syncAggregate := block.Block.Data.Message.Body.SyncAggregate
	syncAssignments := epochStats.Assignments.SyncAssignments
	if syncAggregate != nil && syncAssignments != nil {
		votedCount := 0
		assignedCount := len(syncAssignments)
		for i := 0; i < assignedCount; i++ {
			if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
				votedCount++
			}
		}
		dbBlock.SyncParticipation = float32(votedCount) / float32(assignedCount)
	}

	if executionPayload := block.Block.Data.Message.Body.ExecutionPayload; executionPayload != nil {
		dbBlock.EthTransactionCount = uint64(len(executionPayload.Transactions))
		dbBlock.EthBlockNumber = uint64(executionPayload.BlockNumber)
		dbBlock.EthBlockHash = executionPayload.BlockHash
	}

	return &dbBlock
}

func (indexer *Indexer) runIndexer() {
	indexer.runMutex.Lock()
	defer indexer.runMutex.Unlock()

	chainConfig := utils.Config.Chain.Config
	genesisTime := time.Unix(int64(utils.Config.Chain.GenesisTimestamp), 0)

	if now := time.Now(); now.Compare(genesisTime) > 0 {
		currentEpoch := utils.TimeToEpoch(time.Now())
		if currentEpoch > int64(indexer.inMemoryEpochs) {
			indexer.state.lastHeadBlock = uint64((currentEpoch-int64(indexer.inMemoryEpochs)+1)*int64(chainConfig.SlotsPerEpoch)) - 1
		}
		if currentEpoch > int64(indexer.epochProcessingDelay) {
			indexer.state.lastProcessedEpoch = uint64(currentEpoch - int64(indexer.epochProcessingDelay))
		}
	}

	// fill indexer cache
	err := indexer.pollHeadBlock()
	if err != nil {
		logger.Errorf("Indexer Error while polling latest head: %v", err)
	}

	// start block stream
	blockStream := indexer.rpcClient.NewBlockStream()
	defer blockStream.Close()

	// check if we need to start a sync job (last synced epoch < lastProcessedEpoch)
	if indexer.writeDb {
		syncState := indexerSyncState{}
		db.GetExplorerState("indexer.syncstate", &syncState)
		if syncState.Epoch < indexer.state.lastProcessedEpoch {
			indexer.startSynchronization(syncState.Epoch)
		}
	}

	// run indexer loop
	for {
		indexer.controlMutex.Lock()
		isRunning := indexer.running
		indexer.controlMutex.Unlock()
		if !isRunning {
			break
		}

		select {
		case blockEvt := <-blockStream.BlockChan:
			indexer.pollStreamedBlock(blockEvt.Block)
		case <-blockStream.CloseChan:
			logger.Warnf("Indexer lost connection to beacon event stream. Reconnection in 5 sec")
			time.Sleep(5 * time.Second)
			blockStream.Start()
			err := indexer.pollHeadBlock()
			if err != nil {
				logger.Errorf("Indexer Error while polling latest head: %v", err)
			}
		case <-time.After(30 * time.Second):
			err := indexer.pollHeadBlock()
			if err != nil {
				logger.Errorf("Indexer Error while polling latest head: %v", err)
			}
		}

		//now := time.Now()
		indexer.processIndexing()
		//logger.Infof("indexer loop processing time: %v ms", time.Now().Sub(now).Milliseconds())
	}
}

func (indexer *Indexer) startSynchronization(startEpoch uint64) error {
	if !indexer.writeDb {
		return nil
	}

	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

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

	logger.Infof("Process latest slot %v/%v: %v", utils.EpochOfSlot(headSlot), headSlot, header.Data.Root)
	indexer.processBlock(headSlot, header, block)

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

	logger.Infof("Process polled slot %v/%v: %v", utils.EpochOfSlot(uint64(header.Data.Header.Message.Slot)), header.Data.Header.Message.Slot, header.Data.Root)
	blockInfo := indexer.processBlock(slot, header, block)

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
	blockInfo := indexer.processBlock(slot, header, block)

	return blockInfo, nil
}

func (indexer *Indexer) processBlock(slot uint64, header *rpctypes.StandardV1BeaconHeaderResponse, block *rpctypes.StandardV2BeaconBlockResponse) *BlockInfo {
	blockInfo, isEpochHead := indexer.addBlockInfo(slot, header, block)

	if isEpochHead {
		epoch := utils.EpochOfSlot(slot)
		logger.Infof("Epoch %v head, fetching assingments & validator stats", epoch)

		// load epoch assingments
		epochAssignments, err := indexer.rpcClient.GetEpochAssignments(epoch)
		if err != nil {
			logger.Errorf("Error fetching epoch %v duties: %v", epoch, err)
		}

		indexer.state.cacheMutex.Lock()
		epochStats := EpochStats{
			ValidatorCount:    0,
			EligibleAmount:    0,
			Assignments:       epochAssignments,
			ValidatorBalances: make(map[uint64]uint64),
		}
		epochStats.StatsMutex.Lock()
		defer epochStats.StatsMutex.Unlock()
		indexer.state.epochStats[epoch] = &epochStats
		indexer.state.cacheMutex.Unlock()

		// load epoch stats
		epochValidators, err := indexer.rpcClient.GetStateValidators(header.Data.Header.Message.StateRoot)
		if err != nil {
			logger.Errorf("Error fetching epoch %v/%v validators: %v", epoch, slot, err)
		} else {

			for idx := 0; idx < len(epochValidators.Data); idx++ {
				validator := epochValidators.Data[idx]
				epochStats.ValidatorBalances[uint64(validator.Index)] = uint64(validator.Validator.EffectiveBalance)
				if validator.Status != "active_ongoing" {
					continue
				}
				epochStats.ValidatorCount++
				epochStats.EligibleAmount += uint64(validator.Validator.EffectiveBalance)
			}

		}

		defer runtime.GC() // free memory used to parse the validators state - this might be hundreds of MBs
	}

	indexer.state.cacheMutex.Lock()
	indexer.state.lastHeadBlock = slot
	indexer.state.cacheMutex.Unlock()

	return blockInfo
}

func (indexer *Indexer) addBlockInfo(slot uint64, header *rpctypes.StandardV1BeaconHeaderResponse, block *rpctypes.StandardV2BeaconBlockResponse) (*BlockInfo, bool) {
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
		for bidx := 0; bidx < len(blocks); bidx++ {
			if bytes.Equal(blocks[bidx].Header.Data.Root, header.Data.Root) {
				logger.Infof("Skip duplicate block %v.%v (%v)", slot, bidx, header.Data.Root)
				return nil, false // block already present - skip
			}
		}
		indexer.state.cachedBlocks[slot] = append(blocks, blockInfo)
	}

	if indexer.state.lowestCachedSlot == 0 || slot < indexer.state.lowestCachedSlot {
		indexer.state.lowestCachedSlot = slot
	}

	epoch := utils.EpochOfSlot(slot)
	isEpochHead := utils.EpochOfSlot(indexer.state.lastHeadBlock) != epoch

	// check for chain reorgs
	if indexer.state.lastHeadRoot != nil && !bytes.Equal(indexer.state.lastHeadRoot, header.Data.Header.Message.ParentRoot) {
		// reorg detected
		var reorgBaseBlock *BlockInfo
		var reorgBaseSlot uint64
		var reorgBaseIndex int
		for sidx := slot; sidx >= indexer.state.lowestCachedSlot; sidx-- {
			blocks := indexer.state.cachedBlocks[sidx]
			if blocks == nil {
				continue
			}
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				if bytes.Equal(block.Header.Data.Root, header.Data.Root) {
					continue
				}
				if bytes.Equal(block.Header.Data.Root, header.Data.Header.Message.ParentRoot) {
					reorgBaseSlot = sidx
					reorgBaseIndex = bidx
					reorgBaseBlock = block
				} else {
					logger.Infof("Chain reorg: mark %v.%v as orphaned (%v)", sidx, bidx, block.Header.Data.Root)
					block.Orphaned = true
				}
			}
			if reorgBaseBlock != nil {
				break
			}
		}

		resyncNeeded := false
		if reorgBaseBlock == nil {
			// reorg with > 2 epochs length or we missed a block somehow
			// resync needed
			resyncNeeded = true
		} else {
			orphanedBlock := reorgBaseBlock
			orphanedSlot := reorgBaseSlot
			orphanedIndex := reorgBaseIndex
			for orphanedBlock.Orphaned {
				// reorg back to a block we've previously marked as orphaned
				// walk backwards and fix orphaned flags
				orphanedBlock.Orphaned = false
				logger.Infof("Chain reorg: mark %v.%v as canonical (%v)", orphanedSlot, orphanedIndex, orphanedBlock.Header.Data.Root)

				foundReorgBase := false
				for sidx := reorgBaseSlot - 1; sidx >= indexer.state.lowestCachedSlot; sidx-- {
					blocks := indexer.state.cachedBlocks[sidx]
					if blocks == nil {
						continue
					}
					for bidx := 0; bidx < len(blocks); bidx++ {
						block := blocks[bidx]

						if bytes.Equal(block.Header.Data.Root, orphanedBlock.Header.Data.Header.Message.ParentRoot) {
							if !block.Orphaned {
								// reached end of reorg range
								foundReorgBase = true
							}

							orphanedBlock = block
							orphanedSlot = sidx
							orphanedIndex = bidx
						} else {
							logger.Infof("Chain reorg: mark %v.%v as orphaned (%v)", sidx, bidx, block.Header.Data.Root)
							block.Orphaned = true
						}
					}
					if foundReorgBase {
						break
					}
				}

				if !foundReorgBase {
					resyncNeeded = true
				}
				if utils.EpochOfSlot(orphanedSlot) != epoch {
					isEpochHead = true
				}
			}
		}

		if resyncNeeded {
			logger.Errorf("Large chain reorg detected, resync needed")
			// TODO: Drop all unfinalized & resync
		}
	}
	indexer.state.lastHeadRoot = header.Data.Root

	return blockInfo, isEpochHead
}

func (indexer *Indexer) processIndexing() {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

	currentEpoch := utils.EpochOfSlot(indexer.state.lastHeadBlock)
	processEpoch := currentEpoch - uint64(indexer.epochProcessingDelay)

	// process old epochs
	if indexer.state.lastProcessedEpoch < processEpoch {
		indexer.processEpoch(processEpoch)
		indexer.state.lastProcessedEpoch = processEpoch

		if indexer.state.epochStats[processEpoch] != nil {
			delete(indexer.state.epochStats, processEpoch)
		}
	}

	// cleanup cache
	for indexer.state.lowestCachedSlot < (currentEpoch-uint64(indexer.inMemoryEpochs)+1)*utils.Config.Chain.Config.SlotsPerEpoch {
		if indexer.state.cachedBlocks[indexer.state.lowestCachedSlot] != nil {
			logger.Debugf("Dropped cached block (epoch %v, slot %v)", utils.EpochOfSlot(indexer.state.lowestCachedSlot), indexer.state.lowestCachedSlot)
			delete(indexer.state.cachedBlocks, indexer.state.lowestCachedSlot)
		}
		indexer.state.lowestCachedSlot++
	}
}

func (indexer *Indexer) processEpoch(epoch uint64) {
	logger.Infof("Process epoch %v", epoch)
	// TODO: Process epoch aggregations and save to DB
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	epochStats := indexer.state.epochStats[epoch]

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
		logger.Errorf("Error fetching epoch %v target block (no block found)", epoch)
		return
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
			err = db.SetExplorerState("indexer.syncstate", &indexerSyncState{
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

	logger.Infof("Epoch %v stats: %v validators (%v)", epoch, epochStats.ValidatorCount, epochStats.EligibleAmount)
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
