package indexer

import (
	"bytes"
	"sort"

	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

var logger = logrus.StandardLogger().WithField("module", "indexer")

type Indexer struct {
	indexerCache   *indexerCache
	indexerClients []*IndexerClient

	writeDb        bool
	inMemoryEpochs uint16
}

func NewIndexer() (*Indexer, error) {
	inMemoryEpochs := utils.Config.Indexer.InMemoryEpochs
	if inMemoryEpochs < 2 {
		inMemoryEpochs = 2
	}

	indexer := &Indexer{
		indexerClients: make([]*IndexerClient, 0),

		writeDb:        !utils.Config.Indexer.DisableIndexWriter,
		inMemoryEpochs: inMemoryEpochs,
	}
	indexer.indexerCache = newIndexerCache(indexer)

	return indexer, nil
}

func (indexer *Indexer) AddClient(index uint8, name string, endpoint string) *IndexerClient {
	rpcClient, err := rpc.NewBeaconClient(endpoint)
	if err != nil {
		logger.Errorf("error while adding client %v to indexer: %v", name, err)
		return nil
	}
	client := newIndexerClient(index, name, rpcClient, indexer.indexerCache)
	indexer.indexerClients = append(indexer.indexerClients, client)
	return client
}

func (indexer *Indexer) getReadyClient() *IndexerClient {
	return indexer.indexerClients[0]
}

func (indexer *Indexer) GetRpcClient() *rpc.BeaconClient {
	return indexer.indexerClients[0].rpcClient
}

func (indexer *Indexer) GetFinalizedEpoch() (int64, []byte) {
	return indexer.indexerCache.getFinalizedHead()
}

func (indexer *Indexer) GetHighestSlot() uint64 {
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()
	if indexer.indexerCache.highestSlot < 0 {
		return 0
	}
	return uint64(indexer.indexerCache.highestSlot)
}

func (indexer *Indexer) GetHeadForks() []*HeadFork {
	headForks := []*HeadFork{}
	for _, client := range indexer.indexerClients {
		if !client.isConnected || client.isSynchronizing {
			continue
		}
		cHeadSlot, cHeadRoot := client.getLastHead()
		var matchingFork *HeadFork
		for _, fork := range headForks {
			if bytes.Equal(fork.Root, cHeadRoot) || indexer.indexerCache.isCanonicalBlock(cHeadRoot, fork.Root) {
				matchingFork = fork
				break
			}
			if indexer.indexerCache.isCanonicalBlock(fork.Root, cHeadRoot) {
				fork.Root = cHeadRoot
				fork.Slot = uint64(cHeadSlot)
				matchingFork = fork
				break
			}
		}
		if matchingFork == nil {
			matchingFork = &HeadFork{
				Root:    cHeadRoot,
				Slot:    uint64(cHeadSlot),
				Clients: []*IndexerClient{client},
			}
			headForks = append(headForks, matchingFork)
		} else {
			matchingFork.Clients = append(matchingFork.Clients, client)
		}
	}
	return headForks
}

func (indexer *Indexer) GetCanonicalHead() (uint64, []byte) {
	headCandidates := indexer.GetHeadForks()
	if len(headCandidates) == 0 {
		return 0, nil
	}

	// sort by client count
	sort.Slice(headCandidates, func(a, b int) bool {
		countA := len(headCandidates[a].Clients)
		countB := len(headCandidates[b].Clients)
		return countA < countB
	})

	return headCandidates[0].Slot, headCandidates[0].Root
}

func (indexer *Indexer) GetCachedBlocks(slot uint64) []*CacheBlock {
	if int64(utils.EpochOfSlot(slot)) <= indexer.indexerCache.finalizedEpoch {
		return nil
	}
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()
	blocks := indexer.indexerCache.slotMap[slot]
	if blocks == nil {
		return nil
	}
	return blocks
}

func (indexer *Indexer) GetCachedBlock(root []byte) *CacheBlock {
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()

	return indexer.indexerCache.rootMap[string(root)]
}

func (indexer *Indexer) GetCachedBlockByStateroot(stateroot []byte) *CacheBlock {
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()

	var lowestSlotIdx int64
	if indexer.indexerCache.finalizedEpoch >= 0 {
		lowestSlotIdx = (indexer.indexerCache.finalizedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	} else {
		lowestSlotIdx = 0
	}
	for slotIdx := int64(indexer.indexerCache.highestSlot); slotIdx >= lowestSlotIdx; slotIdx-- {
		slot := uint64(slotIdx)
		blocks := indexer.indexerCache.slotMap[slot]
		for _, block := range blocks {
			if bytes.Equal(block.header.Message.StateRoot, stateroot) {
				return block
			}
		}
	}
	return nil
}

func (indexer *Indexer) GetCachedBlocksByProposer(proposer uint64) []*CacheBlock {
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()

	resBlocks := make([]*CacheBlock, 0)
	var lowestSlotIdx int64
	if indexer.indexerCache.finalizedEpoch >= 0 {
		lowestSlotIdx = (indexer.indexerCache.finalizedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	} else {
		lowestSlotIdx = 0
	}
	for slotIdx := int64(indexer.indexerCache.highestSlot); slotIdx >= lowestSlotIdx; slotIdx-- {
		slot := uint64(slotIdx)
		blocks := indexer.indexerCache.slotMap[slot]
		for _, block := range blocks {
			if uint64(block.header.Message.ProposerIndex) == proposer {
				resBlocks = append(resBlocks, block)
			}
		}
	}
	return resBlocks
}

func (indexer *Indexer) GetCachedEpochStats(epoch uint64) *EpochStats {
	_, headRoot := indexer.GetCanonicalHead()
	return indexer.getCachedEpochStats(epoch, headRoot)
}

func (indexer *Indexer) getCachedEpochStats(epoch uint64, headRoot []byte) *EpochStats {
	indexer.indexerCache.epochStatsMutex.RLock()
	defer indexer.indexerCache.epochStatsMutex.RUnlock()
	var epochStats *EpochStats
	epochStatsList := indexer.indexerCache.epochStatsMap[epoch]
	for _, stats := range epochStatsList {
		if indexer.indexerCache.isCanonicalBlock(stats.DependentRoot, headRoot) {
			epochStats = stats
			break
		}
	}
	return epochStats
}

func (indexer *Indexer) GetCachedValidatorSet() *rpctypes.StandardV1StateValidatorsResponse {
	return indexer.indexerCache.lastValidatorsResp
}

func (indexer *Indexer) GetEpochVotes(epoch uint64) (*EpochStats, *EpochVotes) {
	epochStats := indexer.GetCachedEpochStats(epoch)
	if epochStats == nil {
		return nil, nil
	}
	return epochStats, indexer.getEpochVotes(epoch, epochStats)
}

func (indexer *Indexer) getEpochVotes(epoch uint64, epochStats *EpochStats) *EpochVotes {
	_, headRoot := indexer.GetCanonicalHead()

	// get epoch target
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	firstBlock := indexer.indexerCache.getFirstCanonicalBlock(epoch, headRoot)
	var epochTarget []byte
	if firstBlock == nil {
		logger.Warnf("Counld not find epoch %v target (no block found)", epoch)
	} else {
		if firstBlock.Slot == firstSlot {
			epochTarget = firstBlock.Root
		} else {
			epochTarget = firstBlock.header.Message.ParentRoot
		}
	}

	// get canonical blocks
	canonicalMap := indexer.indexerCache.getCanonicalBlockMap(epoch, headRoot)
	// append next epoch blocks (needed for vote aggregation)
	for slot, block := range indexer.indexerCache.getCanonicalBlockMap(epoch+1, headRoot) {
		canonicalMap[slot] = block
	}

	// calculate votes
	return aggregateEpochVotes(canonicalMap, epoch, epochStats, epochTarget, false)
}

func (indexer *Indexer) BuildLiveEpoch(epoch uint64) *dbtypes.Epoch {
	headSlot, headRoot := indexer.GetCanonicalHead()
	headEpoch := utils.EpochOfSlot(headSlot)

	epochStats := indexer.getCachedEpochStats(epoch, headRoot)
	if epochStats == nil {
		return nil
	}

	epochStats.dbEpochMutex.Lock()
	defer epochStats.dbEpochMutex.Unlock()

	if epochStats.dbEpochCache != nil {
		return epochStats.dbEpochCache
	}

	logger.Debugf("Build live epoch data %v", epoch)
	canonicalMap := indexer.indexerCache.getCanonicalBlockMap(epoch, headRoot)
	epochVotes := indexer.getEpochVotes(epoch, epochStats)
	dbEpoch := buildDbEpoch(epoch, canonicalMap, epochStats, epochVotes, nil)
	if headEpoch > epoch && headEpoch-epoch > 2 {
		epochStats.dbEpochCache = dbEpoch
	}
	return dbEpoch
}

func (indexer *Indexer) BuildLiveBlock(block *CacheBlock) *dbtypes.Block {
	block.dbBlockMutex.Lock()
	defer block.dbBlockMutex.Unlock()

	if block.dbBlockCache == nil {
		logger.Debugf("Build live block data 0x%x", block.Root)
		header := block.GetHeader()
		epoch := utils.EpochOfSlot(uint64(header.Message.Slot))
		epochStats := indexer.GetCachedEpochStats(epoch)
		if epochStats == nil {
			return nil
		}
		block.dbBlockCache = buildDbBlock(block, epochStats)
	}
	block.dbBlockCache.Orphaned = !block.IsCanonical(indexer, nil)
	return block.dbBlockCache
}
