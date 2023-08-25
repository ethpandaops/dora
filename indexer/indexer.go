package indexer

import (
	"bytes"
	"fmt"
	"math/rand"
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

func (indexer *Indexer) AddClient(index uint8, name string, endpoint string, archive bool, priority int, headers map[string]string) *IndexerClient {
	rpcClient, err := rpc.NewBeaconClient(endpoint, name, headers)
	if err != nil {
		logger.Errorf("error while adding client %v to indexer: %v", name, err)
		return nil
	}
	client := newIndexerClient(index, name, rpcClient, indexer.indexerCache, archive, priority)
	indexer.indexerClients = append(indexer.indexerClients, client)
	return client
}

func (indexer *Indexer) GetClients() []*IndexerClient {
	return indexer.indexerClients
}

func (indexer *Indexer) GetReadyClient(archive bool, head []byte, skip []*IndexerClient) *IndexerClient {
	clientCandidates := indexer.GetReadyClients(archive, head)
	candidateCount := len(clientCandidates)
	if candidateCount == 0 {
		clientCandidates = make([]*IndexerClient, 0)
		for _, client := range indexer.indexerClients {
			if client.isConnected && !client.isSynchronizing {
				clientCandidates = append(clientCandidates, client)
			}
		}
		candidateCount = len(clientCandidates)
	}
	allCandidates := make([]*IndexerClient, candidateCount)
	copy(allCandidates, clientCandidates)

	// remove skipped
	for _, skipClient := range skip {
		skipIdx := -1
		for idx, tClient := range clientCandidates {
			if idx >= candidateCount {
				break
			}
			if tClient == skipClient {
				skipIdx = idx
				break
			}
		}
		if skipIdx != -1 {
			candidateCount--
			if skipIdx < candidateCount {
				clientCandidates[skipIdx] = clientCandidates[candidateCount]
			}
		}
	}

	if candidateCount == 0 {
		clientCandidates = allCandidates
		candidateCount = len(clientCandidates)
	}
	selectedIndex := rand.Intn(candidateCount)
	return clientCandidates[selectedIndex]
}

func (indexer *Indexer) GetReadyClients(archive bool, head []byte) []*IndexerClient {
	headCandidates := indexer.GetHeadForks()
	if len(headCandidates) == 0 {
		return indexer.indexerClients
	}

	var headFork *HeadFork
	if head != nil {
		cachedBlock := indexer.indexerCache.getCachedBlock(head)
		if cachedBlock != nil {
			for _, fork := range headCandidates {
				if indexer.indexerCache.isCanonicalBlock(head, fork.Root) {
					headFork = fork
					break
				}
			}
		}
	}
	if headFork == nil {
		headFork = headCandidates[0]
	}

	clientCandidates := indexer.getReadyClientCandidates(headFork, archive)
	if len(clientCandidates) == 0 && archive {
		clientCandidates = indexer.getReadyClientCandidates(headFork, false)
	}
	return clientCandidates
}

func (indexer *Indexer) getReadyClientCandidates(headFork *HeadFork, archive bool) []*IndexerClient {
	var clientCandidates []*IndexerClient = nil
	for _, client := range headFork.ReadyClients {
		if archive && !client.archive {
			continue
		}
		if clientCandidates != nil && clientCandidates[0].priority != client.priority {
			break
		}
		if clientCandidates != nil {
			clientCandidates = append(clientCandidates, client)
		} else {
			clientCandidates = []*IndexerClient{client}
		}
	}
	return clientCandidates
}

func (indexer *Indexer) GetRpcClient(archive bool, head []byte) *rpc.BeaconClient {
	readyClient := indexer.GetReadyClient(archive, head, nil)
	if head != nil {
		fmt.Printf("client for head 0x%x: %v\n", head, readyClient.clientName)
	}
	return readyClient.rpcClient
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
		cHeadSlot, cHeadRoot := client.GetLastHead()
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
				Root:       cHeadRoot,
				Slot:       uint64(cHeadSlot),
				AllClients: []*IndexerClient{client},
			}
			headForks = append(headForks, matchingFork)
		} else {
			matchingFork.AllClients = append(matchingFork.AllClients, client)
		}
	}
	for _, fork := range headForks {
		fork.ReadyClients = make([]*IndexerClient, 0)
		sort.Slice(fork.AllClients, func(a, b int) bool {
			prioA := fork.AllClients[a].priority
			prioB := fork.AllClients[b].priority
			return prioA > prioB
		})
		for _, client := range fork.AllClients {
			var headDistance uint64 = 0
			_, cHeadRoot := client.GetLastHead()
			if !bytes.Equal(fork.Root, cHeadRoot) {
				_, headDistance = indexer.indexerCache.getCanonicalDistance(cHeadRoot, fork.Root)
			}
			if headDistance < 2 {
				fork.ReadyClients = append(fork.ReadyClients, client)
			}
		}
	}

	// sort by relevance (client count & head slot)
	sort.Slice(headForks, func(a, b int) bool {
		slotA := headForks[a].Slot
		slotB := headForks[b].Slot
		if slotA > slotB && slotA-slotB >= 16 {
			return true
		} else if slotB > slotA && slotB-slotA >= 16 {
			return false
		} else {
			countA := len(headForks[a].ReadyClients)
			countB := len(headForks[b].ReadyClients)
			return countA > countB
		}
	})

	return headForks
}

func (indexer *Indexer) GetCanonicalHead() (uint64, []byte) {
	headCandidates := indexer.GetHeadForks()
	if len(headCandidates) == 0 {
		return 0, nil
	}

	return headCandidates[0].Slot, headCandidates[0].Root
}

func (indexer *Indexer) GetCachedBlocks(slot uint64) []*CacheBlock {
	if int64(utils.EpochOfSlot(slot)) <= indexer.indexerCache.finalizedEpoch {
		return nil
	}
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()
	blocks := make([]*CacheBlock, 0)
	for _, block := range indexer.indexerCache.slotMap[slot] {
		if block.IsReady() {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func (indexer *Indexer) GetCachedBlock(root []byte) *CacheBlock {
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()
	block := indexer.indexerCache.rootMap[string(root)]
	if block != nil && !block.IsReady() {
		return nil
	}
	return block
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
				if !block.IsReady() {
					return nil
				} else {
					return block
				}
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
			if block.IsReady() && uint64(block.header.Message.ProposerIndex) == proposer {
				resBlocks = append(resBlocks, block)
			}
		}
	}
	return resBlocks
}

func (indexer *Indexer) GetCachedBlocksByParentRoot(parentRoot []byte) []*CacheBlock {
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()
	resBlocks := make([]*CacheBlock, 0)
	for _, block := range indexer.indexerCache.rootMap {
		if block.IsReady() && bytes.Equal(block.header.Message.ParentRoot, parentRoot) {
			resBlocks = append(resBlocks, block)
		}
	}
	return resBlocks
}

func (indexer *Indexer) GetFirstCachedCanonicalBlock(epoch uint64, head []byte) *CacheBlock {
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()
	block := indexer.indexerCache.getFirstCanonicalBlock(epoch, head)
	return block
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
		logger.Warnf("could not find epoch %v target (no block found)", epoch)
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
