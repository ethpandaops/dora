package indexer

import (
	"bytes"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

var logger = logrus.StandardLogger().WithField("module", "indexer")

type Indexer struct {
	rpcClient    *rpc.BeaconClient
	controlMutex sync.Mutex
	runMutex     sync.Mutex
	running      bool
	state        indexerState
}

type indexerState struct {
	lastHeadPoll       uint64
	lastHeadBlock      uint64
	lastHeadRoot       []byte
	lastFinalizedBlock uint64
	cacheMutex         sync.Mutex
	cachedBlocks       map[uint64][]*BlockInfo
	epochStats         map[uint64]*EpochStats
	lowestCachedSlot   uint64
	lastProcessedEpoch uint64
}

type EpochStats struct {
	statsMutex        sync.Mutex
	validatorCount    uint64
	eligibleAmount    uint64
	assignments       *rpctypes.EpochAssignments
	validatorBalances map[uint64]uint64
}

func NewIndexer(rpcClient *rpc.BeaconClient) (*Indexer, error) {
	return &Indexer{
		rpcClient: rpcClient,
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

func (indexer *Indexer) runIndexer() {
	indexer.runMutex.Lock()
	defer indexer.runMutex.Unlock()

	chainConfig := utils.Config.Chain.Config
	genesisTime := time.Unix(int64(utils.Config.Chain.GenesisTimestamp), 0)

	if now := time.Now(); now.Compare(genesisTime) > 0 {
		currentEpoch := utils.TimeToEpoch(time.Now())
		if currentEpoch > 2 {
			indexer.state.lastHeadBlock = uint64((currentEpoch-1)*int64(chainConfig.SlotsPerEpoch)) - 1
			indexer.state.lastProcessedEpoch = uint64(currentEpoch - 2)
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
	// TODO

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
		case <-time.After(30 * time.Second):
			err := indexer.pollHeadBlock()
			if err != nil {
				logger.Errorf("Indexer Error while polling latest head: %v", err)
			}
		}

		now := time.Now()
		indexer.processIndexing()
		logger.Infof("indexer loop processing time: %v ms", time.Now().Sub(now).Milliseconds())
	}
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
			validatorCount:    0,
			eligibleAmount:    0,
			assignments:       epochAssignments,
			validatorBalances: make(map[uint64]uint64),
		}
		epochStats.statsMutex.Lock()
		defer epochStats.statsMutex.Unlock()
		indexer.state.epochStats[epoch] = &epochStats
		indexer.state.cacheMutex.Unlock()

		// load epoch stats
		epochValidators, err := indexer.rpcClient.GetStateValidators(header.Data.Header.Message.StateRoot)
		if err != nil {
			logger.Errorf("Error fetching epoch %v/%v validators: %v", epoch, slot, err)
		} else {

			for idx := 0; idx < len(epochValidators.Data); idx++ {
				validator := epochValidators.Data[idx]
				epochStats.validatorBalances[uint64(validator.Index)] = uint64(validator.Validator.EffectiveBalance)
				if validator.Status != "active_ongoing" {
					continue
				}
				epochStats.validatorCount++
				epochStats.eligibleAmount += uint64(validator.Validator.EffectiveBalance)
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
		header: header,
		block:  block,
	}
	if indexer.state.cachedBlocks[slot] == nil {
		indexer.state.cachedBlocks[slot] = make([]*BlockInfo, 1)
		indexer.state.cachedBlocks[slot][0] = blockInfo
	} else {
		indexer.state.cachedBlocks[slot] = append(indexer.state.cachedBlocks[slot], blockInfo)
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
				if bytes.Equal(block.header.Data.Root, header.Data.Root) {
					continue
				}
				if bytes.Equal(block.header.Data.Root, header.Data.Header.Message.ParentRoot) {
					reorgBaseSlot = sidx
					reorgBaseIndex = bidx
					reorgBaseBlock = block
				} else {
					logger.Infof("Chain reorg: mark %v.%v as orphaned (%v)", sidx, bidx, block.header.Data.Root)
					block.orphaned = true
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
			logger.Infof("Chain reorg detected, skipped %v slots", (slot - reorgBaseSlot - 1))

			orphanedBlock := reorgBaseBlock
			orphanedSlot := reorgBaseSlot
			orphanedIndex := reorgBaseIndex
		mainLoop:
			for orphanedBlock.orphaned {
				// reorg back to a block we've previously marked as orphaned
				// walk backwards and fix orphaned flags
				orphanedBlock.orphaned = false
				logger.Infof("Chain reorg: mark %v.%v as canonical (%v) (complex)", orphanedSlot, orphanedIndex, orphanedBlock.header.Data.Root)

				for sidx := reorgBaseSlot - 1; sidx >= indexer.state.lowestCachedSlot; sidx-- {
					blocks := indexer.state.cachedBlocks[sidx]
					if blocks == nil {
						continue
					}
					for bidx := 0; bidx < len(blocks); bidx++ {
						block := blocks[bidx]

						if bytes.Equal(block.header.Data.Root, orphanedBlock.header.Data.Header.Message.ParentRoot) {
							if !block.orphaned {
								// reached end of reorg range
								break mainLoop
							}

							block.orphaned = false
							orphanedBlock = block
							orphanedSlot = sidx
							orphanedIndex = bidx
						} else {
							logger.Infof("Chain reorg: mark %v.%v as orphaned (%v) (complex)", sidx, bidx, block.header.Data.Root)
							block.orphaned = true
						}
					}
				}

				if utils.EpochOfSlot(orphanedSlot) != epoch {
					isEpochHead = true
				}
			}
		}

		if resyncNeeded {
			logger.Errorf("Large chain reorg detected, resync needed")
		}
	}
	indexer.state.lastHeadRoot = header.Data.Root

	return blockInfo, isEpochHead
}

func (indexer *Indexer) processIndexing() {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

	currentEpoch := utils.EpochOfSlot(indexer.state.lastHeadBlock)
	processEpoch := currentEpoch - 2

	// process old epochs
	if indexer.state.lastProcessedEpoch < processEpoch {
		indexer.processEpoch(processEpoch)
		indexer.state.lastProcessedEpoch = processEpoch

		if indexer.state.epochStats[processEpoch] != nil {
			delete(indexer.state.epochStats, processEpoch)
		}
	}

	// cleanup cache
	for indexer.state.lowestCachedSlot < (currentEpoch-1)*utils.Config.Chain.Config.SlotsPerEpoch {
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
			if !block.orphaned {
				if slot == firstSlot {
					epochTarget = block.header.Data.Root
				} else {
					epochTarget = block.header.Data.Header.Message.ParentRoot
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

	logger.Infof("Epoch %v stats: %v validators (%v)", epoch, epochStats.validatorCount, epochStats.eligibleAmount)
	logger.Infof("Epoch %v votes: target %v + %v = %v", epoch, epochVotes.currentEpoch.targetVoteAmount, epochVotes.nextEpoch.targetVoteAmount, epochVotes.currentEpoch.targetVoteAmount+epochVotes.nextEpoch.targetVoteAmount)
	logger.Infof("Epoch %v votes: head %v + %v = %v", epoch, epochVotes.currentEpoch.headVoteAmount, epochVotes.nextEpoch.headVoteAmount, epochVotes.currentEpoch.headVoteAmount+epochVotes.nextEpoch.headVoteAmount)
	logger.Infof("Epoch %v votes: total %v + %v = %v", epoch, epochVotes.currentEpoch.totalVoteAmount, epochVotes.nextEpoch.totalVoteAmount, epochVotes.currentEpoch.totalVoteAmount+epochVotes.nextEpoch.totalVoteAmount)
}
