package indexer

import (
	"errors"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type Indexer struct {
	rpcClient    *rpc.BeaconClient
	controlMutex sync.Mutex
	runMutex     sync.Mutex
	running      bool
	state        IndexerState
}

type IndexerState struct {
	lastHeadPoll       uint64
	lastHeadBlock      uint64
	lastFinalizedBlock uint64
	cacheMutex         sync.Mutex
	cachedBlocks       map[uint64]*BlockInfo
	lowestCachedSlot   uint64
	lastProcessedEpoch uint64
}

func NewIndexer(rpcClient *rpc.BeaconClient) (*Indexer, error) {
	return &Indexer{
		rpcClient: rpcClient,
		state: IndexerState{
			cachedBlocks: make(map[uint64]*BlockInfo),
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
			logrus.Infof("[Indexer] Poll chain head (slot %v)", indexer.state.lastHeadPoll)
			err := indexer.pollHeadBlock()
			if err != nil {
				logrus.Errorf("Indexer Error while polling latest head: %v", err)
			}
		}
		if sleepDelay == 0 || tout < sleepDelay {
			sleepDelay = tout
		}

		indexer.processIndexing()

		processingTime := time.Now().Sub(now)
		logrus.Infof("[Indexer] processing time: %v ms,  sleep: %v ms", processingTime.Milliseconds(), sleepDelay.Milliseconds()-processingTime.Milliseconds())
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

	logrus.Infof("[Indexer] Process  latest  slot %v/%v: %v", utils.EpochOfSlot(headSlot), headSlot, header.Data.Root)
	indexer.state.lastHeadBlock = headSlot
	indexer.addBlockInfo(headSlot, header, block)

	return nil
}

func (indexer *Indexer) pollBackfillBlock(slot uint64) (*BlockInfo, error) {
	header, err := indexer.rpcClient.GetBlockHeaderBySlot(slot)
	if err != nil {
		return nil, err
	}
	block, err := indexer.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return nil, err
	}

	logrus.Infof("[Indexer] Process backfill slot %v/%v: %v", utils.EpochOfSlot(uint64(header.Data.Header.Message.Slot)), header.Data.Header.Message.Slot, header.Data.Root)
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
	blockInfo.processAggregations()
	return blockInfo
}

func (indexer *Indexer) processIndexing() {
	indexer.state.cacheMutex.Lock()
	defer indexer.state.cacheMutex.Unlock()

	// process old epochs
	currentEpoch := utils.EpochOfSlot(indexer.state.lastHeadBlock)
	if indexer.state.lastProcessedEpoch < currentEpoch-2 {
		indexer.processEpoch(currentEpoch - 2)
		indexer.state.lastProcessedEpoch = currentEpoch - 2
	}

	// cleanup cache
	for indexer.state.lowestCachedSlot < (currentEpoch-1)*utils.Config.Chain.Config.SlotsPerEpoch {
		if indexer.state.cachedBlocks[indexer.state.lowestCachedSlot] != nil {
			logrus.Debugf("[Indexer] Dropped cached block (epoch %v, slot %v)", utils.EpochOfSlot(indexer.state.lowestCachedSlot), indexer.state.lowestCachedSlot)
			delete(indexer.state.cachedBlocks, indexer.state.lowestCachedSlot)
		}
		indexer.state.lowestCachedSlot++
	}
}

func (indexer *Indexer) processEpoch(epoch uint64) {
	logrus.Infof("[Indexer] Process epoch %v", epoch)
	// TODO: Process epoch aggregations and save to DB

}
