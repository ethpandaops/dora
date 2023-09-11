package indexer

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type IndexerClient struct {
	clientIdx          uint8
	clientName         string
	rpcClient          *rpc.BeaconClient
	skipValidators     bool
	archive            bool
	priority           int
	versionStr         string
	indexerCache       *indexerCache
	cacheMutex         sync.RWMutex
	lastStreamEvent    time.Time
	isSynchronizing    bool
	syncDistance       uint64
	isConnected        bool
	retryCounter       uint64
	lastHeadSlot       int64
	lastHeadRoot       []byte
	lastEpochStats     int64
	lastFinalizedEpoch int64
	lastFinalizedRoot  []byte
	lastJustifiedEpoch int64
	lastJustifiedRoot  []byte
}

func newIndexerClient(clientIdx uint8, clientName string, rpcClient *rpc.BeaconClient, indexerCache *indexerCache, archive bool, priority int, skipValidators bool) *IndexerClient {
	client := IndexerClient{
		clientIdx:          clientIdx,
		clientName:         clientName,
		rpcClient:          rpcClient,
		skipValidators:     skipValidators,
		archive:            archive,
		priority:           priority,
		indexerCache:       indexerCache,
		lastHeadSlot:       -1,
		lastEpochStats:     -1,
		lastFinalizedEpoch: -1,
		lastJustifiedEpoch: -1,
	}
	go client.runIndexerClientLoop()
	return &client
}

func (client *IndexerClient) GetIndex() uint8 {
	return client.clientIdx
}

func (client *IndexerClient) GetName() string {
	return client.clientName
}

func (client *IndexerClient) GetVersion() string {
	return client.versionStr
}

func (client *IndexerClient) GetRpcClient() *rpc.BeaconClient {
	return client.rpcClient
}

func (client *IndexerClient) GetLastHead() (int64, []byte) {
	client.cacheMutex.RLock()
	defer client.cacheMutex.RUnlock()
	return client.lastHeadSlot, client.lastHeadRoot
}

func (client *IndexerClient) GetStatus() string {
	if client.isSynchronizing {
		return "synchronizing"
	} else if !client.isConnected {
		return "disconnected"
	} else {
		return "ready"
	}
}

func (client *IndexerClient) runIndexerClientLoop() {
	defer utils.HandleSubroutinePanic("runIndexerClientLoop")

	for {
		err := client.checkIndexerClient()

		if err == nil {
			genesisTime := time.Unix(int64(utils.Config.Chain.GenesisTimestamp), 0)
			genesisSince := time.Since(genesisTime)
			waitTime := 0
			if genesisSince < 0 {
				// preload genesis validator set
				if !client.skipValidators {
					epochStats, _ := client.indexerCache.createOrGetEpochStats(0, nil)
					epochStats.loadValidatorStats(client, "genesis")
				}

				waitTime = int(time.Since(genesisTime).Abs().Seconds())
				if waitTime > 600 {
					waitTime = 600
				}
				logger.WithField("client", client.clientName).Infof("waiting for genesis (%v secs)", waitTime)

				time.Sleep(time.Duration(waitTime) * time.Second)
				continue
			}

			err = client.runIndexerClient()
		}
		if err == nil {
			return
		}

		client.retryCounter++
		waitTime := 10
		if client.retryCounter > 10 {
			waitTime = 300
		} else if client.retryCounter > 5 {
			waitTime = 60
		}

		logger.WithField("client", client.clientName).Warnf("indexer client error: %v, retrying in %v sec...", err, waitTime)
		time.Sleep(time.Duration(waitTime) * time.Second)
	}
}

func (client *IndexerClient) checkIndexerClient() error {
	// get node version
	nodeVersion, err := client.rpcClient.GetNodeVersion()
	if err != nil {
		return fmt.Errorf("error while fetching node version: %v", err)
	}
	if nodeVersion != nil {
		client.versionStr = nodeVersion.Data.Version
	}

	// check genesis
	genesis, err := client.rpcClient.GetGenesis()
	if err != nil {
		return fmt.Errorf("error while fetching genesis: %v", err)
	}
	if genesis == nil {
		return fmt.Errorf("no genesis block found")
	}
	genesisTime := uint64(genesis.Data.GenesisTime)
	if genesisTime != utils.Config.Chain.GenesisTimestamp {
		return fmt.Errorf("genesis time from RPC does not match the genesis time from explorer configuration")
	}
	if genesis.Data.GenesisForkVersion.String() != utils.Config.Chain.Config.GenesisForkVersion {
		return fmt.Errorf("genesis fork version from RPC does not match the genesis fork version explorer configuration")
	}
	client.indexerCache.setGenesis(genesis)

	// check syncronization state
	syncStatus, err := client.rpcClient.GetNodeSyncing()
	if err != nil {
		return fmt.Errorf("error while fetching synchronization status: %v", err)
	}
	if syncStatus == nil {
		return fmt.Errorf("could not get synchronization status")
	}
	client.isSynchronizing = syncStatus.Data.IsSyncing || syncStatus.Data.IsOptimistic
	client.syncDistance = uint64(syncStatus.Data.SyncDistance)

	return nil
}

func (client *IndexerClient) runIndexerClient() error {
	// get latest header
	latestHeader, err := client.rpcClient.GetLatestBlockHead()
	if err != nil {
		return fmt.Errorf("could not get latest header: %v", err)
	}
	if latestHeader == nil {
		return fmt.Errorf("could not find latest header")
	}
	client.setHeadBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))

	// check latest header / sync status
	if client.isSynchronizing {
		return fmt.Errorf("beacon node is synchronizing")
	}
	if client.indexerCache.finalizedEpoch >= 0 && utils.EpochOfSlot(uint64(latestHeader.Data.Header.Message.Slot)) <= uint64(client.indexerCache.finalizedEpoch) {
		return fmt.Errorf("client is far behind - head is before synchronized checkpoint")
	}

	// get finalized header
	finalizedSlot, err := client.refreshFinalityCheckpoints()
	if err != nil {
		logger.WithField("client", client.clientName).Warnf("could not get finalized header: %v", err)
	}

	logger.WithField("client", client.clientName).Debugf("endpoint %v ready: %v ", client.clientName, client.versionStr)
	client.retryCounter = 0

	// start event stream
	blockStream := client.rpcClient.NewBlockStream(rpc.StreamBlockEvent | rpc.StreamFinalizedEvent)
	defer blockStream.Close()

	// prefill cache
	err = client.prefillCache(finalizedSlot, latestHeader)
	if err != nil {
		return err
	}

	// set finalized head and trigger epoch processing / synchronization
	client.indexerCache.setFinalizedHead(client.lastFinalizedEpoch, client.lastFinalizedRoot, client.lastJustifiedEpoch, client.lastJustifiedRoot)

	// process events
	client.lastStreamEvent = time.Now()
	for {
		var eventTimeout time.Duration = time.Since(client.lastStreamEvent)
		if eventTimeout > 30*time.Second {
			eventTimeout = 0
		} else {
			eventTimeout = 30*time.Second - eventTimeout
		}
		select {
		case evt := <-blockStream.EventChan:
			now := time.Now()
			switch evt.Event {
			case rpc.StreamBlockEvent:
				client.processBlockEvent(evt.Data.(*rpctypes.StandardV1StreamedBlockEvent))
			case rpc.StreamFinalizedEvent:
				client.processFinalizedEvent(evt.Data.(*rpctypes.StandardV1StreamedFinalizedCheckpointEvent))
			}
			logger.WithField("client", client.clientName).Tracef("event (%v) processing time: %v ms", evt.Event, time.Since(now).Milliseconds())
			client.lastStreamEvent = time.Now()
		case ready := <-blockStream.ReadyChan:
			if client.isConnected != ready {
				client.isConnected = ready
				if ready {
					logger.WithField("client", client.clientName).Debug("RPC event stream connected")
				} else {
					logger.WithField("client", client.clientName).Debug("RPC event stream disconnected")
				}
			}
		case <-time.After(eventTimeout):
			logger.WithField("client", client.clientName).Debug("no head event since 30 secs, polling chain head")
			err := client.pollLatestBlocks()
			if err != nil {
				client.isConnected = false
				return err
			}
			client.lastStreamEvent = time.Now()
		}

		currentEpoch := utils.TimeToEpoch(time.Now())
		if currentEpoch > client.lastEpochStats {
			// ensure latest epoch stats are loaded for chain of this client
			client.ensureEpochStats(uint64(currentEpoch), client.lastHeadRoot)
		}
	}
}

func (client *IndexerClient) refreshFinalityCheckpoints() (uint64, error) {
	finalizedCheckpoints, err := client.rpcClient.GetFinalityCheckpoints()
	if err != nil {
		return 0, err
	}
	var finalizedSlot uint64
	client.cacheMutex.Lock()
	defer client.cacheMutex.Unlock()
	finalizedSlot = uint64(finalizedCheckpoints.Data.Finalized.Epoch) * utils.Config.Chain.Config.SlotsPerEpoch
	client.lastFinalizedEpoch = int64(finalizedCheckpoints.Data.Finalized.Epoch) - 1
	client.lastFinalizedRoot = finalizedCheckpoints.Data.Finalized.Root
	client.lastJustifiedEpoch = int64(finalizedCheckpoints.Data.CurrentJustified.Epoch) - 1
	client.lastJustifiedRoot = finalizedCheckpoints.Data.CurrentJustified.Root

	return finalizedSlot, nil
}

func (client *IndexerClient) prefillCache(finalizedSlot uint64, latestHeader *rpctypes.StandardV1BeaconHeaderResponse) error {
	currentBlock, isNewBlock := client.indexerCache.createOrGetCachedBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))
	if isNewBlock {
		logger.WithField("client", client.clientName).Infof("received block %v:%v [0x%x] warmup, head", utils.EpochOfSlot(uint64(client.lastHeadSlot)), client.lastHeadSlot, client.lastHeadRoot)
	} else {
		logger.WithField("client", client.clientName).Debugf("received known block %v:%v [0x%x] warmup, head", utils.EpochOfSlot(uint64(client.lastHeadSlot)), client.lastHeadSlot, client.lastHeadRoot)
	}
	client.ensureBlock(currentBlock, &latestHeader.Data.Header)
	client.setHeadBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))

	// walk backwards and load all blocks until we reach a finalized epoch
	parentRoot := []byte(currentBlock.header.Message.ParentRoot)
	for {
		finalizedCheckpoint := (client.indexerCache.finalizedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
		if finalizedCheckpoint > int64(finalizedSlot) {
			finalizedSlot = uint64(finalizedCheckpoint)
		}

		var parentHead *rpctypes.SignedBeaconBlockHeader
		parentBlock := client.indexerCache.getCachedBlock(parentRoot)
		if parentBlock != nil {
			parentBlock.mutex.RLock()
			parentHead = parentBlock.header
			parentBlock.mutex.RUnlock()
		}
		if parentHead == nil {
			headerRsp, err := client.rpcClient.GetBlockHeaderByBlockroot(parentRoot)
			if err != nil {
				return fmt.Errorf("could not load parent header: %v", err)
			}
			if headerRsp == nil {
				return fmt.Errorf("could not find parent header 0x%x", parentRoot)
			}
			parentHead = &headerRsp.Data.Header
		}
		parentSlot := uint64(parentHead.Message.Slot)
		var isNewBlock bool
		if parentBlock == nil {
			parentBlock, isNewBlock = client.indexerCache.createOrGetCachedBlock(parentRoot, parentSlot)
		}
		if isNewBlock {
			logger.WithField("client", client.clientName).Infof("received block %v:%v [0x%x] warmup", utils.EpochOfSlot(parentSlot), parentSlot, parentRoot)
		} else {
			logger.WithField("client", client.clientName).Debugf("received known block %v:%v [0x%x] warmup", utils.EpochOfSlot(parentSlot), parentSlot, parentRoot)
		}
		client.ensureBlock(parentBlock, parentHead)

		if parentSlot <= finalizedSlot {
			logger.WithField("client", client.clientName).Debugf("prefill cache: reached finalized slot %v:%v [0x%x]", utils.EpochOfSlot(parentSlot), parentSlot, parentRoot)
			break
		}
		if parentSlot == 0 {
			logger.WithField("client", client.clientName).Debugf("prefill cache: reached gensis slot [0x%x]", parentRoot)
			break
		}
		parentRoot = parentHead.Message.ParentRoot
	}

	// ensure epoch stats
	var firstEpoch uint64
	if finalizedSlot == 0 {
		firstEpoch = 0
	} else {
		firstEpoch = utils.EpochOfSlot(finalizedSlot)
	}
	currentEpoch := utils.EpochOfSlot(currentBlock.Slot)
	for epoch := firstEpoch; epoch <= currentEpoch; epoch++ {
		client.ensureEpochStats(epoch, currentBlock.Root)
	}

	return nil
}

func (client *IndexerClient) ensureBlock(block *CacheBlock, header *rpctypes.SignedBeaconBlockHeader) error {
	// ensure the cached block is loaded (header & block body), load missing parts
	block.mutex.Lock()
	defer block.mutex.Unlock()
	if block.header == nil {
		if header == nil {
			headerRsp, err := client.rpcClient.GetBlockHeaderByBlockroot(block.Root)
			if err != nil {
				logger.WithField("client", client.clientName).Warnf("ensure block %v [0x%x] failed (header): %v", block.Slot, block.Root, err)
				return err
			}
			header = &headerRsp.Data.Header
		}
		block.header = header
	}
	if block.block == nil && !block.isInDb {
		blockRsp, err := client.rpcClient.GetBlockBodyByBlockroot(block.Root)
		if err != nil {
			logger.WithField("client", client.clientName).Warnf("ensure block %v [0x%x] failed (block): %v", block.Slot, block.Root, err)
			return err
		}
		if utils.EpochOfSlot(block.Slot) >= utils.Config.Chain.Config.BellatrixForkEpoch && blockRsp.Data.Message.Body.ExecutionPayload == nil {
			logger.WithField("client", client.clientName).Warnf("ensure block %v [0x%x] failed (block): execution payload missing for post-bellatix block", block.Slot, block.Root)
			return err
		}
		block.block = &blockRsp.Data
	}
	// set seen flag
	clientFlag := uint64(1) << client.clientIdx
	block.seenBy |= clientFlag
	return nil
}

func (client *IndexerClient) pollLatestBlocks() error {
	// get latest header
	latestHeader, err := client.rpcClient.GetLatestBlockHead()
	if err != nil {
		return fmt.Errorf("could not get latest header: %v", err)
	}
	if latestHeader == nil {
		return fmt.Errorf("could not find latest header")
	}
	client.setHeadBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))

	currentBlock, isNewBlock := client.indexerCache.createOrGetCachedBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))
	if isNewBlock {
		logger.WithField("client", client.clientName).Infof("received block %v:%v [0x%x] polled, head", utils.EpochOfSlot(uint64(client.lastHeadSlot)), client.lastHeadSlot, client.lastHeadRoot)
	} else {
		logger.WithField("client", client.clientName).Debugf("received known block %v:%v [0x%x] polled, head", utils.EpochOfSlot(uint64(client.lastHeadSlot)), client.lastHeadSlot, client.lastHeadRoot)
	}
	err = client.ensureBlock(currentBlock, &latestHeader.Data.Header)
	if err != nil {
		return err
	}
	err = client.ensureParentBlocks(currentBlock)
	if err != nil {
		return err
	}
	return nil
}

func (client *IndexerClient) ensureParentBlocks(currentBlock *CacheBlock) error {
	// walk backwards and load all blocks until we reach a block that is marked as seen by this client or is smaller than finalized
	parentRoot := []byte(currentBlock.header.Message.ParentRoot)
	for {
		var parentHead *rpctypes.SignedBeaconBlockHeader
		parentBlock := client.indexerCache.getCachedBlock(parentRoot)
		if parentBlock != nil {
			parentBlock.mutex.RLock()
			parentHead = parentBlock.header
			// check if already marked as seen by this client
			clientFlag := uint64(1) << client.clientIdx
			isSeen := parentBlock.seenBy&clientFlag > 0
			parentBlock.mutex.RUnlock()
			if isSeen {
				break
			}
		}
		if parentHead == nil {
			headerRsp, err := client.rpcClient.GetBlockHeaderByBlockroot(parentRoot)
			if err != nil {
				return fmt.Errorf("could not load parent header [0x%x]: %v", parentRoot, err)
			}
			if headerRsp == nil {
				return fmt.Errorf("could not find parent header [0x%x]", parentRoot)
			}
			parentHead = &headerRsp.Data.Header
		}
		parentSlot := uint64(parentHead.Message.Slot)
		isNewBlock := false
		if parentBlock == nil {
			parentBlock, isNewBlock = client.indexerCache.createOrGetCachedBlock(parentRoot, parentSlot)
		}
		if isNewBlock {
			logger.WithField("client", client.clientName).Infof("received block %v:%v [0x%x] backfill", utils.EpochOfSlot(parentSlot), parentSlot, parentRoot)
		} else {
			logger.WithField("client", client.clientName).Debugf("received known block %v:%v [0x%x] backfill", utils.EpochOfSlot(parentSlot), parentSlot, parentRoot)
		}
		client.ensureBlock(parentBlock, parentHead)

		finalizedEpoch := client.indexerCache.finalizedEpoch
		if client.lastFinalizedEpoch > finalizedEpoch {
			finalizedEpoch = client.lastFinalizedEpoch
		}
		if int64(utils.EpochOfSlot(parentSlot)) <= finalizedEpoch {
			logger.WithField("client", client.clientName).Debugf("backfill cache: reached finalized slot %v:%v [0x%x]", utils.EpochOfSlot(parentSlot), parentSlot, parentRoot)
			break
		}
		if parentSlot == 0 {
			logger.WithField("client", client.clientName).Debugf("backfill cache: reached gensis slot [0x%x]", parentRoot)
			break
		}
		parentRoot = parentHead.Message.ParentRoot
	}
	return nil
}

func (client *IndexerClient) setHeadBlock(root []byte, slot uint64) error {
	client.cacheMutex.Lock()
	if bytes.Equal(client.lastHeadRoot, root) {
		client.cacheMutex.Unlock()
		return nil
	}
	client.lastHeadSlot = int64(slot)
	client.lastHeadRoot = root
	client.cacheMutex.Unlock()

	return nil
}

func (client *IndexerClient) processBlockEvent(evt *rpctypes.StandardV1StreamedBlockEvent) error {
	currentBlock, isNewBlock := client.indexerCache.createOrGetCachedBlock(evt.Block, uint64(evt.Slot))
	if isNewBlock {
		logger.WithField("client", client.clientName).Infof("received block %v:%v [0x%x] stream", utils.EpochOfSlot(currentBlock.Slot), currentBlock.Slot, currentBlock.Root)
	} else {
		logger.WithField("client", client.clientName).Debugf("received known block %v:%v [0x%x] stream", utils.EpochOfSlot(currentBlock.Slot), currentBlock.Slot, currentBlock.Root)
	}
	err := client.ensureBlock(currentBlock, nil)
	if err != nil {
		return err
	}
	err = client.ensureParentBlocks(currentBlock)
	if err != nil {
		return err
	}
	client.setHeadBlock(evt.Block, uint64(evt.Slot))
	return nil
}

func (client *IndexerClient) processFinalizedEvent(evt *rpctypes.StandardV1StreamedFinalizedCheckpointEvent) error {
	time.Sleep(100 * time.Millisecond)
	client.refreshFinalityCheckpoints()
	logger.WithField("client", client.clientName).Debugf("received finalization_checkpoint event: finalized %v [0x%x], justified %v [0x%x]", client.lastFinalizedEpoch, client.lastFinalizedRoot, client.lastJustifiedEpoch, client.lastJustifiedRoot)
	client.indexerCache.setFinalizedHead(client.lastFinalizedEpoch, client.lastFinalizedRoot, client.lastJustifiedEpoch, client.lastJustifiedRoot)
	return nil
}
