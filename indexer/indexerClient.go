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

type indexerClient struct {
	clientIdx          uint8
	clientName         string
	rpcClient          *rpc.BeaconClient
	indexerCache       *indexerCache
	cacheMutex         sync.RWMutex
	isSynchronizing    bool
	isConnected        bool
	lastHeadSlot       int64
	lastHeadRoot       []byte
	lastEpochStats     int64
	lastFinalizedEpoch int64
	lastFinalizedRoot  []byte
}

func newIndexerClient(clientIdx uint8, clientName string, rpcClient *rpc.BeaconClient, indexerCache *indexerCache) *indexerClient {
	client := indexerClient{
		clientIdx:          clientIdx,
		clientName:         clientName,
		rpcClient:          rpcClient,
		indexerCache:       indexerCache,
		lastHeadSlot:       -1,
		lastEpochStats:     -1,
		lastFinalizedEpoch: -1,
	}
	go client.runIndexerClientLoop()
	return &client
}

func (client *indexerClient) runIndexerClientLoop() {
	for {
		err := client.runIndexerClient()
		if err != nil {
			logger.WithField("client", client.clientName).Errorf("Indexer client error: %v, retrying in 10 sec...", err)
			time.Sleep(10 * time.Second)
		} else {
			return
		}
	}
}

func (client *indexerClient) runIndexerClient() error {
	// check genesis
	genesis, err := client.rpcClient.GetGenesis()
	if err != nil {
		return fmt.Errorf("Error while fetching genesis: %v", err)
	}
	if genesis == nil {
		return fmt.Errorf("No genesis block found")
	}
	genesisTime := uint64(genesis.Data.GenesisTime)
	if genesisTime != utils.Config.Chain.GenesisTimestamp {
		return fmt.Errorf("Genesis time from RPC does not match the genesis time from explorer configuration")
	}
	if genesis.Data.GenesisForkVersion.String() != utils.Config.Chain.Config.GenesisForkVersion {
		return fmt.Errorf("Genesis fork version from RPC does not match the genesis fork version explorer configuration")
	}

	// check syncronization state
	syncStatus, err := client.rpcClient.GetNodeSyncing()
	if err != nil {
		return fmt.Errorf("Error while fetching synchronization status: %v", err)
	}
	if syncStatus == nil {
		return fmt.Errorf("Could not get synchronization status")
	}
	client.isSynchronizing = syncStatus.Data.IsSyncing
	if syncStatus.Data.IsSyncing {
		return fmt.Errorf("Beacon node is synchronizing.")
	}

	// start event stream
	blockStream := client.rpcClient.NewBlockStream(rpc.StreamBlockEvent | rpc.StreamHeadEvent | rpc.StreamFinalizedEvent)
	defer blockStream.Close()

	// get finalized header
	finalizedHeader, err := client.rpcClient.GetFinalizedBlockHead()
	if err != nil {
		logger.WithField("client", client.clientName).Warnf("Could not get finalized header: %v", err)
	}
	var finalizedSlot uint64
	if finalizedHeader != nil {
		client.cacheMutex.Lock()
		finalizedSlot = uint64(finalizedHeader.Data.Header.Message.Slot)
		client.lastFinalizedEpoch = int64(utils.EpochOfSlot(uint64(finalizedHeader.Data.Header.Message.Slot)) - 1)
		client.lastFinalizedRoot = finalizedHeader.Data.Root
		client.cacheMutex.Unlock()
	}

	// prefill cache
	err = client.prefillCache(finalizedSlot)

	// set finalized head and trigger epoch processing / synchronization
	if finalizedHeader != nil {
		client.indexerCache.setFinalizedHead(client.lastFinalizedEpoch, client.lastFinalizedRoot)
	}

	// process events
	for {
		select {
		case evt := <-blockStream.EventChan:
			now := time.Now()
			switch evt.Event {
			case rpc.StreamBlockEvent:
				client.processBlockEvent(evt.Data.(*rpctypes.StandardV1StreamedBlockEvent))
				break
			case rpc.StreamHeadEvent:
				client.processHeadEvent(evt.Data.(*rpctypes.StandardV1StreamedHeadEvent))
				break
			case rpc.StreamFinalizedEvent:
				client.processFinalizedEvent(evt.Data.(*rpctypes.StandardV1StreamedFinalizedCheckpointEvent))
				break
			}
			logger.WithField("client", client.clientName).Debugf("event (%v) processing time: %v ms", evt.Event, time.Now().Sub(now).Milliseconds())
		case ready := <-blockStream.ReadyChan:
			client.isConnected = ready
			if ready {
				logger.WithField("client", client.clientName).Debug("Event stream connected")
			} else {
				logger.WithField("client", client.clientName).Debug("Event stream disconnected")
			}
			break
		case <-blockStream.CloseChan:
			return fmt.Errorf("Lost connection to beacon event stream.")
		case <-time.After(30 * time.Second):
			logger.WithField("client", client.clientName).Info("No head event since 30 secs, polling chain head")
			err := client.pollLatestBlocks()
			if err != nil {
				return err
			}
		}

		currentEpoch := utils.TimeToEpoch(time.Now())
		if currentEpoch > client.lastEpochStats {
			// ensure latest epoch stats are loaded for chain of this client
			client.ensureEpochStats(uint64(currentEpoch), client.lastHeadRoot)
		}
	}
}

func (client *indexerClient) prefillCache(finalizedSlot uint64) error {
	// get latest header
	latestHeader, err := client.rpcClient.GetLatestBlockHead()
	if err != nil {
		return fmt.Errorf("Could not get latest header: %v", err)
	}
	if latestHeader == nil {
		return fmt.Errorf("Could not find latest header")
	}
	client.setHeadBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))

	currentBlock, isNewBlock := client.indexerCache.createOrGetCachedBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))
	if isNewBlock {
		logger.WithField("client", client.clientName).Infof("Prefill new block: head slot %v (0x%x)", client.lastHeadSlot, client.lastHeadRoot)
	} else {
		logger.WithField("client", client.clientName).Debugf("Prefill known block cache: head slot %v (0x%x)", client.lastHeadSlot, client.lastHeadRoot)
	}
	client.ensureBlock(currentBlock, &latestHeader.Data.Header)

	// walk backwards and load all blocks until we reach a finalized epoch
	parentRoot := []byte(currentBlock.header.Message.ParentRoot)
	for {
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
				return fmt.Errorf("Could not load parent header: %v", err)
			}
			if headerRsp == nil {
				return fmt.Errorf("Could not find parent header 0x%x", parentRoot)
			}
			parentHead = &headerRsp.Data.Header
		}
		parentSlot := uint64(parentHead.Message.Slot)
		var isNewBlock bool
		if parentBlock == nil {
			parentBlock, isNewBlock = client.indexerCache.createOrGetCachedBlock(parentRoot, parentSlot)
		}
		if isNewBlock {
			logger.WithField("client", client.clientName).Infof("Prefill new block: slot %v (0x%x)", parentSlot, parentRoot)
		} else {
			logger.WithField("client", client.clientName).Debugf("Prefill known block: slot %v (0x%x)", parentSlot, parentRoot)
		}
		client.ensureBlock(parentBlock, parentHead)

		if parentSlot <= finalizedSlot {
			logger.WithField("client", client.clientName).Debugf("Prefill block cache: reached finalized slot %v (0x%x)", parentSlot, parentRoot)
			break
		}
		if parentSlot == 0 {
			logger.WithField("client", client.clientName).Debugf("Prefill block cache: reached gensis slot (0x%x)", parentRoot)
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
	currentEpoch := utils.TimeToEpoch(time.Now())
	if currentEpoch < 0 {
		currentEpoch = -1
	}
	for epoch := firstEpoch; int64(epoch) <= currentEpoch; epoch++ {
		fmt.Printf("enshure epoch stats %v\n", epoch)
		client.ensureEpochStats(epoch, currentBlock.root)
	}

	return nil
}

func (client *indexerClient) ensureEpochStats(epoch uint64, head []byte) error {
	firstBlock := client.indexerCache.getFirstCanonicalBlock(epoch, head)
	var dependendRoot []byte
	logger.WithField("client", client.clientName).Debugf("canonical first block for epoch %v: %v/0x%x", epoch, firstBlock.slot, firstBlock.root)
	if firstBlock != nil {
		dependendRoot = firstBlock.header.Message.ParentRoot
	} else {
		fmt.Printf("get proposer duties %v\n", epoch)
		proposerRsp, err := client.rpcClient.GetProposerDuties(epoch)
		if err != nil {
			logger.WithField("client", client.clientName).Warnf("Could not load proposer duties for epoch %v: %v", epoch, err)
		}
		if proposerRsp == nil {
			return fmt.Errorf("Could not fine proposer duties for epoch %v", epoch)
		}
		dependendRoot = proposerRsp.DependentRoot
	}
	logger.WithField("client", client.clientName).Debugf("Ensure epoch stats for epoch %v (dependend: 0x%x)", epoch, dependendRoot)
	client.indexerCache.createOrGetEpochStats(epoch, dependendRoot, client)
	if int64(epoch) > client.lastEpochStats {
		client.lastEpochStats = int64(epoch)
	}
	return nil
}

func (client *indexerClient) ensureBlock(block *indexerCacheBlock, header *rpctypes.SignedBeaconBlockHeader) error {
	// ensure the cached block is loaded (header & block body), load missing parts
	block.mutex.Lock()
	defer block.mutex.Unlock()
	if block.header == nil {
		if header == nil {
			headerRsp, err := client.rpcClient.GetBlockHeaderByBlockroot(block.root)
			if err != nil {
				return err
			}
			header = &headerRsp.Data.Header
		}
		block.header = header
	}
	if block.block == nil && !block.isInDb {
		blockRsp, err := client.rpcClient.GetBlockBodyByBlockroot(block.root)
		if err != nil {
			return err
		}
		block.block = &blockRsp.Data
	}
	// set seen flag
	clientFlag := uint64(1) << client.clientIdx
	block.seenBy |= clientFlag
	return nil
}

func (client *indexerClient) pollLatestBlocks() error {
	// get latest header
	latestHeader, err := client.rpcClient.GetLatestBlockHead()
	if err != nil {
		return fmt.Errorf("Could not get latest header: %v", err)
	}
	if latestHeader == nil {
		return fmt.Errorf("Could not find latest header")
	}
	client.setHeadBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))

	currentBlock, isNewBlock := client.indexerCache.createOrGetCachedBlock(latestHeader.Data.Root, uint64(latestHeader.Data.Header.Message.Slot))
	if isNewBlock {
		logger.WithField("client", client.clientName).Infof("Polled new block: head slot %v (0x%x)", client.lastHeadSlot, client.lastHeadRoot)
	} else {
		logger.WithField("client", client.clientName).Debugf("Polled known block: head slot %v (0x%x)", client.lastHeadSlot, client.lastHeadRoot)
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

func (client *indexerClient) ensureParentBlocks(currentBlock *indexerCacheBlock) error {
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
				return fmt.Errorf("Could not load parent header: %v", err)
			}
			if headerRsp == nil {
				return fmt.Errorf("Could not find parent header 0x%x", parentRoot)
			}
			parentHead = &headerRsp.Data.Header
		}
		parentSlot := uint64(parentHead.Message.Slot)
		if int64(utils.EpochOfSlot(parentSlot)) <= client.lastFinalizedEpoch {
			logger.WithField("client", client.clientName).Debugf("Check for parent blocks reached finalized slot %v (0x%x)", parentSlot, parentRoot)
			break
		}
		if parentSlot == 0 {
			logger.WithField("client", client.clientName).Debugf("Check for parent blocks reached gensis slot (0x%x)", parentRoot)
			break
		}
		isNewBlock := false
		if parentBlock == nil {
			parentBlock, isNewBlock = client.indexerCache.createOrGetCachedBlock(parentRoot, parentSlot)
		}
		if isNewBlock {
			logger.WithField("client", client.clientName).Infof("Backfilled new block: slot %v (0x%x)", parentSlot, parentRoot)
		} else {
			logger.WithField("client", client.clientName).Infof("Backfilled known block: slot %v (0x%x)", parentSlot, parentRoot)
		}
		client.ensureBlock(parentBlock, parentHead)
		parentRoot = parentHead.Message.ParentRoot
	}
	return nil
}

func (client *indexerClient) setHeadBlock(root []byte, slot uint64) error {
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

func (client *indexerClient) processBlockEvent(evt *rpctypes.StandardV1StreamedBlockEvent) error {
	currentBlock, isNewBlock := client.indexerCache.createOrGetCachedBlock(evt.Block, uint64(evt.Slot))
	if isNewBlock {
		logger.WithField("client", client.clientName).Infof("Streamed new block: slot %v (0x%x)", currentBlock.slot, currentBlock.root)
	} else {
		logger.WithField("client", client.clientName).Debugf("Streamed known block: slot %v (0x%x)", currentBlock.slot, currentBlock.root)
	}
	err := client.ensureBlock(currentBlock, nil)
	if err != nil {
		return err
	}
	err = client.ensureParentBlocks(currentBlock)
	if err != nil {
		return err
	}
	return nil
}

func (client *indexerClient) processHeadEvent(evt *rpctypes.StandardV1StreamedHeadEvent) error {
	currentBlock := client.indexerCache.getCachedBlock(evt.Block)
	if currentBlock == nil {
		return fmt.Errorf("Received head event for non existing block: %v", evt.Block.String())
	}
	return client.setHeadBlock(evt.Block, uint64(evt.Slot))
}

func (client *indexerClient) processFinalizedEvent(evt *rpctypes.StandardV1StreamedFinalizedCheckpointEvent) error {
	logger.WithField("client", client.clientName).Debugf("Received finalization_checkpoint event: epoch %v (%s)", evt.Epoch, evt.Block.String())
	client.indexerCache.setFinalizedHead(int64(evt.Epoch), evt.Block)
	return nil
}
