package indexer

import (
	"bytes"
	"errors"
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
	cachedBlocks       map[uint64]*BlockInfo
	epochStats         map[uint64]*EpochStats
	lowestCachedSlot   uint64
	lastProcessedEpoch uint64
}

type EpochStats struct {
	validatorCount uint64
	eligibleAmount uint64
	assignments    *rpctypes.EpochAssignments
	validators     map[uint64]*rpctypes.Validator
}

func NewIndexer(rpcClient *rpc.BeaconClient) (*Indexer, error) {
	return &Indexer{
		rpcClient: rpcClient,
		state: indexerState{
			cachedBlocks: make(map[uint64]*BlockInfo),
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
		var sleepDelay time.Duration = 0

		// check head
		now := time.Now()
		nextHeadTime := genesisTime.Add(time.Duration(indexer.state.lastHeadPoll+1) * time.Duration(chainConfig.SecondsPerSlot) * time.Second)
		tout := nextHeadTime.Sub(now) + ((time.Duration(chainConfig.SecondsPerSlot) - 2) * time.Second) // always poll 2 secs before slot end, so we should catch catch late blocks
		if tout <= 0 {
			indexer.state.lastHeadPoll = utils.TimeToSlot(uint64(now.Unix()))
			tout += time.Duration(chainConfig.SecondsPerSlot) * time.Second
			logger.Infof("Poll chain head (slot %v)", indexer.state.lastHeadPoll)
			err := indexer.pollHeadBlock()
			if err != nil {
				logger.Errorf("Indexer Error while polling latest head: %v", err)
			}
		}
		if sleepDelay == 0 || tout < sleepDelay {
			sleepDelay = tout
		}

		indexer.processIndexing()

		processingTime := time.Now().Sub(now)
		logger.Infof("processing time: %v ms,  sleep: %v ms", processingTime.Milliseconds(), sleepDelay.Milliseconds()-processingTime.Milliseconds())
		if sleepDelay > processingTime {
			time.Sleep(sleepDelay - processingTime)
		}
	}
}

func (indexer *Indexer) pollHeadBlock() error {
	header, err := indexer.rpcClient.GetLatestBlockHead()
	if err != nil {
		return err
	}
	if uint64(header.Data.Header.Message.Slot) == indexer.state.lastHeadBlock {
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

	logger.Infof("Process latest  slot %v/%v: %v", utils.EpochOfSlot(headSlot), headSlot, header.Data.Root)
	indexer.addBlockInfo(headSlot, header, block)

	return nil
}

func (indexer *Indexer) pollBackfillBlock(slot uint64) (*BlockInfo, error) {
	header, err := indexer.rpcClient.GetBlockHeaderBySlot(slot)
	if err != nil {
		return nil, err
	}
	if header == nil {
		logger.Infof("Process missed  slot %v/%v", utils.EpochOfSlot(slot), slot)
		return nil, nil
	}
	block, err := indexer.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return nil, err
	}

	logger.Infof("Process delayed slot %v/%v: %v", utils.EpochOfSlot(uint64(header.Data.Header.Message.Slot)), header.Data.Header.Message.Slot, header.Data.Root)
	blockInfo := indexer.addBlockInfo(slot, header, block)

	return blockInfo, nil
}

func (indexer *Indexer) addBlockInfo(slot uint64, header *rpctypes.StandardV1BeaconHeaderResponse, block *rpctypes.StandardV2BeaconBlockResponse) *BlockInfo {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

	blockInfo := &BlockInfo{
		header: header,
		block:  block,
	}
	indexer.state.cachedBlocks[slot] = blockInfo
	if indexer.state.lowestCachedSlot == 0 || slot < indexer.state.lowestCachedSlot {
		indexer.state.lowestCachedSlot = slot
	}

	epoch := utils.EpochOfSlot(slot)
	isEpochHead := utils.EpochOfSlot(indexer.state.lastHeadBlock) != epoch
	indexer.state.lastHeadBlock = slot

	// check for chain reorgs
	parentRoot := utils.MustParseHex(header.Data.Header.Message.ParentRoot)
	if indexer.state.lastHeadRoot != nil && !bytes.Equal(indexer.state.lastHeadRoot, parentRoot) {
		// reorg detected
		var reorgBaseBlock *BlockInfo
		var reorgBaseSlot uint64
		for sidx := slot - 2; sidx >= indexer.state.lowestCachedSlot; sidx-- {
			block := indexer.state.cachedBlocks[sidx]
			if block != nil && block.header.Data.Root == header.Data.Header.Message.ParentRoot {
				reorgBaseSlot = sidx
				reorgBaseBlock = block
			}
		}

		if utils.EpochOfSlot(reorgBaseSlot) != epoch {
			isEpochHead = true
		}

		resyncNeeded := false
		if reorgBaseBlock == nil {
			// reorg with > 64 blocks??
			// resync needed
			resyncNeeded = true
		} else {
			logger.Infof("Chain reorg detected, skipped %v slots", (slot - reorgBaseSlot - 1))

			if reorgBaseBlock.orphanded {
				// reorg back to a chain we've previously marked as orphanded
				orphandedBlock := reorgBaseBlock
				orphandedSlot := reorgBaseSlot - 1
				orphandedBlock.orphanded = false
				for ; orphandedSlot >= indexer.state.lowestCachedSlot; orphandedSlot-- {
					block := indexer.state.cachedBlocks[orphandedSlot]
					if block == nil {
						resyncNeeded = true
						logger.Errorf("Chain reorg, but can't find canonical chain in cache")
						break
					}
					if block.header.Data.Root == orphandedBlock.header.Data.Header.Message.ParentRoot {
						if !block.orphanded {
							break
						}
						logger.Infof("Chain reorg: mark %v as canonical (complex)", block.header.Data.Header.Message.Slot)
						block.orphanded = false
					} else {
						logger.Infof("Chain reorg: mark %v as orphanded (complex)", block.header.Data.Header.Message.Slot)
						block.orphanded = true
					}
				}

				if utils.EpochOfSlot(orphandedSlot) != epoch {
					isEpochHead = true
				}
			}

			for sidx := reorgBaseSlot + 1; sidx < slot; sidx++ {
				block := indexer.state.cachedBlocks[sidx]
				if block != nil {
					logger.Infof("Chain reorg: mark %v as orphanded", block.header.Data.Header.Message.Slot)
					block.orphanded = true
				}
			}
		}

		if resyncNeeded {
			logger.Errorf("Large chain reorg detected, resync needed")
		}
	}
	indexer.state.lastHeadRoot = utils.MustParseHex(header.Data.Root)

	// check for new epoch
	if isEpochHead {
		logger.Infof("Epoch %v head, fetching assingments & validator stats", epoch)

		// load epoch assingments
		epochAssignments, err := indexer.rpcClient.GetEpochAssignments(epoch)
		if err != nil {
			logger.Errorf("Error fetching epoch %v duties: %v", epoch, err)
		}

		// load epoch stats
		epochValidators, err := indexer.rpcClient.GetStateValidators(utils.MustParseHex(header.Data.Header.Message.StateRoot))
		if err != nil {
			logger.Errorf("Error fetching epoch %v/%v validators: %v", epoch, slot, err)
		} else {
			epochStats := EpochStats{
				validatorCount: 0,
				eligibleAmount: 0,
				assignments:    epochAssignments,
				validators:     make(map[uint64]*rpctypes.Validator),
			}
			for idx := 0; idx < len(epochValidators.Data); idx++ {
				validator := epochValidators.Data[idx]
				epochStats.validators[uint64(validator.Index)] = &validator.Validator
				if validator.Status != "active_ongoing" {
					continue
				}
				epochStats.validatorCount++
				epochStats.eligibleAmount += uint64(validator.Validator.EffectiveBalance)
			}
			indexer.state.epochStats[epoch] = &epochStats
		}
	}

	blockInfo.processAggregations()
	return blockInfo
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

	var epochTarget string = ""
	for slot := firstSlot; slot <= lastSlot; slot++ {
		block := indexer.state.cachedBlocks[slot]
		if block != nil && !block.orphanded {
			if slot == firstSlot {
				epochTarget = block.header.Data.Root
			} else {
				epochTarget = block.header.Data.Header.Message.ParentRoot
			}
			break
		}
	}
	if epochTarget == "" {
		logger.Errorf("Error fetching epoch %v target block (no block found)", epoch)
		return
	}

	epochVotes := aggregateEpochVotes(indexer.state.cachedBlocks, epoch, epochStats, epochTarget, false)

	logger.Infof("Epoch %v stats: %v validators (%v)", epoch, epochStats.validatorCount, epochStats.eligibleAmount)
	logger.Infof("Epoch %v votes: target %v + %v = %v", epoch, epochVotes.currentEpoch.targetVoteAmount, epochVotes.nextEpoch.targetVoteAmount, epochVotes.currentEpoch.targetVoteAmount+epochVotes.nextEpoch.targetVoteAmount)
	logger.Infof("Epoch %v votes: head %v + %v = %v", epoch, epochVotes.currentEpoch.headVoteAmount, epochVotes.nextEpoch.headVoteAmount, epochVotes.currentEpoch.headVoteAmount+epochVotes.nextEpoch.headVoteAmount)
	logger.Infof("Epoch %v votes: total %v + %v = %v", epoch, epochVotes.currentEpoch.totalVoteAmount, epochVotes.nextEpoch.totalVoteAmount, epochVotes.currentEpoch.totalVoteAmount+epochVotes.nextEpoch.totalVoteAmount)
}
