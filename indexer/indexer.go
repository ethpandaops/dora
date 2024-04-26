package indexer

import (
	"bytes"
	"math"
	"math/rand"
	"sort"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/rpc"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

var logger = logrus.StandardLogger().WithField("module", "indexer")

type Indexer struct {
	BlobStore             *BlobStore
	indexerCache          *indexerCache
	indexerClients        []*IndexerClient
	writeDb               bool
	disableSync           bool
	inMemoryEpochs        uint16
	cachePersistenceDelay uint16
}

func NewIndexer() (*Indexer, error) {
	inMemoryEpochs := utils.Config.Indexer.InMemoryEpochs
	if inMemoryEpochs < 2 {
		inMemoryEpochs = 2
	}
	cachePersistenceDelay := utils.Config.Indexer.CachePersistenceDelay
	if cachePersistenceDelay < 2 {
		cachePersistenceDelay = 2
	}

	indexer := &Indexer{
		BlobStore:             newBlobStore(),
		indexerClients:        make([]*IndexerClient, 0),
		writeDb:               !utils.Config.Indexer.DisableIndexWriter,
		disableSync:           utils.Config.Indexer.DisableSynchronizer,
		inMemoryEpochs:        inMemoryEpochs,
		cachePersistenceDelay: cachePersistenceDelay,
	}
	indexer.indexerCache = newIndexerCache(indexer)

	return indexer, nil
}

func (indexer *Indexer) AddClient(index uint16, endpoint *types.EndpointConfig) *IndexerClient {

	rpcClient, err := rpc.NewBeaconClient(endpoint.Url, endpoint.Name, endpoint.Headers, endpoint.Ssh)
	if err != nil {
		logger.Errorf("error while adding client %v to indexer: %v", endpoint.Name, err)
		return nil
	}
	client := newIndexerClient(index, endpoint.Name, rpcClient, indexer.indexerCache, endpoint.Archive, endpoint.Priority, endpoint.SkipValidators)
	indexer.indexerClients = append(indexer.indexerClients, client)
	return client
}

func (indexer *Indexer) GetClients() []*IndexerClient {
	return indexer.indexerClients
}

func (indexer *Indexer) GetReadyClient(archive bool, head []byte, skip []*IndexerClient) *IndexerClient {
	clientCandidates := indexer.getReadyClients(archive, head)
	candidateCount := len(clientCandidates)
	if candidateCount == 0 {
		clientCandidates = make([]*IndexerClient, 0)
		for _, client := range indexer.indexerClients {
			if client.isConnected && !client.isSynchronizing && !client.isOptimistic {
				clientCandidates = append(clientCandidates, client)
			}
		}
		candidateCount = len(clientCandidates)
	}

	// sort by prio
	sort.Slice(clientCandidates, func(a, b int) bool {
		return clientCandidates[a].priority > clientCandidates[b].priority
	})

	// filter, remove skipped & lower priority
	filteredCandidates := []*IndexerClient{}
	filteredCandidateCount := 0
clientLoop:
	for _, tClient := range clientCandidates {
		if filteredCandidateCount > 0 && tClient.priority < filteredCandidates[0].priority {
			break
		}
		for _, skipClient := range skip {
			if skipClient == tClient {
				continue clientLoop
			}
		}
		filteredCandidateCount++
		filteredCandidates = append(filteredCandidates, tClient)
	}

	if filteredCandidateCount == 0 {
		filteredCandidates = clientCandidates
		filteredCandidateCount = candidateCount
	}
	if filteredCandidateCount == 0 {
		return nil
	}
	selectedIndex := rand.Intn(filteredCandidateCount)
	return filteredCandidates[selectedIndex]
}

func (indexer *Indexer) getReadyClients(archive bool, head []byte) []*IndexerClient {
	headCandidates := indexer.GetHeadForks(true)
	if len(headCandidates) == 0 {
		allCandidates := make([]*IndexerClient, len(indexer.indexerClients))
		copy(allCandidates, indexer.indexerClients)
		return allCandidates
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
	clientCandidates := []*IndexerClient{}
	for _, client := range headFork.ReadyClients {
		if archive && !client.archive {
			continue
		}
		clientCandidates = append(clientCandidates, client)
	}
	return clientCandidates
}

func (indexer *Indexer) GetRpcClient(archive bool, head []byte) *rpc.BeaconClient {
	readyClient := indexer.GetReadyClient(archive, head, nil)
	return readyClient.rpcClient
}

func (indexer *Indexer) GetCachedGenesis() *v1.Genesis {
	return indexer.indexerCache.genesisResp
}

func (indexer *Indexer) GetFinalizationCheckpoints() (int64, []byte, int64, []byte) {
	return indexer.indexerCache.getFinalizationCheckpoints()
}

func (indexer *Indexer) GetHighestSlot() uint64 {
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()
	if indexer.indexerCache.highestSlot < 0 {
		return 0
	}
	return uint64(indexer.indexerCache.highestSlot)
}

func (indexer *Indexer) GetHeadForks(readyOnly bool) []*HeadFork {
	headForks := []*HeadFork{}
	for _, client := range indexer.indexerClients {
		if readyOnly && (!client.isConnected || client.isSynchronizing || client.isOptimistic) {
			continue
		}
		cHeadSlot, cHeadRoot, _ := client.GetLastHead()
		if cHeadSlot < 0 {
			cHeadSlot = 0
		}
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
			_, cHeadRoot, _ := client.GetLastHead()
			if !bytes.Equal(fork.Root, cHeadRoot) {
				_, headDistance = indexer.indexerCache.getCanonicalDistance(cHeadRoot, fork.Root)
			}
			if headDistance < 2 {
				fork.ReadyClients = append(fork.ReadyClients, client)
			}
		}
	}

	// sort by relevance (client count)
	sort.Slice(headForks, func(a, b int) bool {
		countA := len(headForks[a].ReadyClients)
		countB := len(headForks[b].ReadyClients)
		return countA > countB
	})

	return headForks
}

func (indexer *Indexer) GetCanonicalHead() (uint64, []byte) {
	headCandidates := indexer.GetHeadForks(true)
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
			if bytes.Equal(block.header.Message.StateRoot[:], stateroot) {
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

func (indexer *Indexer) GetCachedBlocksByExecutionBlockHash(hash []byte) []*CacheBlock {
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
			if block.IsReady() && bytes.Equal(block.Refs.ExecutionHash, hash) {
				resBlocks = append(resBlocks, block)
			}
		}
	}
	return resBlocks
}

func (indexer *Indexer) GetCachedBlocksByExecutionBlockNumber(number uint64) []*CacheBlock {
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
			if block.IsReady() && block.Refs.ExecutionNumber == number {
				resBlocks = append(resBlocks, block)
			}
		}
	}
	return resBlocks
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
		if block.IsReady() && bytes.Equal(block.header.Message.ParentRoot[:], parentRoot) {
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
	epochStatsDistance := uint64(math.MaxUint64)
	epochStatsList := indexer.indexerCache.epochStatsMap[epoch]
	dependentSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	if dependentSlot > 0 {
		dependentSlot--
	}
	for _, stats := range epochStatsList {
		dependentBlock := indexer.indexerCache.getCachedBlock(stats.DependentRoot)
		if dependentBlock != nil && indexer.indexerCache.isCanonicalBlock(stats.DependentRoot, headRoot) {
			dependentDist := dependentSlot - dependentBlock.Slot
			if epochStatsDistance == uint64(math.MaxUint64) || dependentDist < epochStatsDistance {
				epochStatsDistance = dependentDist
				epochStats = stats
			}
		}
	}
	if epochStats == nil {
		// fallback to non-canonical epoch stats (still better than showing nothing)
		maxSeen := uint64(0)
		for _, stats := range epochStatsList {
			if epochStats == nil || stats.seenCount > maxSeen {
				maxSeen = stats.seenCount
				epochStats = stats
			}
		}
	}
	return epochStats
}

func (indexer *Indexer) GetCachedValidatorSet() map[phase0.ValidatorIndex]*v1.Validator {
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
			epochTarget = firstBlock.header.Message.ParentRoot[:]
		}
	}

	// get canonical blocks
	canonicalMap := indexer.indexerCache.getCanonicalBlockMap(epoch, headRoot)
	// append next epoch blocks (needed for vote aggregation)
	for slot, block := range indexer.indexerCache.getCanonicalBlockMap(epoch+1, headRoot) {
		canonicalMap[slot] = block
	}

	// calculate votes
	return aggregateEpochVotes(canonicalMap, epoch, epochStats, epochTarget, false, false)
}

func (indexer *Indexer) BuildLiveEpoch(epoch uint64) *dbtypes.Epoch {
	dbEpoch, _ := indexer.buildLiveEpoch(epoch, nil)
	return dbEpoch
}

func (indexer *Indexer) buildLiveEpoch(epoch uint64, epochStats *EpochStats) (*dbtypes.Epoch, *EpochStats) {
	headSlot, headRoot := indexer.GetCanonicalHead()
	headEpoch := utils.EpochOfSlot(headSlot)

	if epochStats == nil {
		epochStats = indexer.getCachedEpochStats(epoch, headRoot)
	}
	if epochStats == nil || !epochStats.IsReady() {
		return nil, nil
	}

	epochStats.dbEpochMutex.Lock()
	defer epochStats.dbEpochMutex.Unlock()

	if epochStats.dbEpochCache != nil {
		return epochStats.dbEpochCache, epochStats
	}

	logger.Tracef("build live epoch data %v", epoch)
	canonicalMap := indexer.indexerCache.getCanonicalBlockMap(epoch, headRoot)
	epochVotes := indexer.getEpochVotes(epoch, epochStats)
	dbEpoch := buildDbEpoch(epoch, canonicalMap, epochStats, epochVotes, nil)
	if headEpoch > epoch && headEpoch-epoch > 2 {
		epochStats.dbEpochCache = dbEpoch
	}
	return dbEpoch, epochStats
}

func (indexer *Indexer) BuildLiveBlock(block *CacheBlock) *dbtypes.Slot {
	block.dbBlockMutex.Lock()
	defer block.dbBlockMutex.Unlock()

	dbBlock := block.dbBlockCache
	if dbBlock == nil {
		logger.Tracef("build live block data 0x%x", block.Root)
		header := block.GetHeader()
		epoch := utils.EpochOfSlot(uint64(header.Message.Slot))
		epochStats := indexer.GetCachedEpochStats(epoch)
		dbBlock = buildDbBlock(block, epochStats)
		if epochStats != nil {
			block.dbBlockCache = dbBlock
		}
	}
	if block.IsCanonical(indexer, nil) {
		dbBlock.Status = dbtypes.Canonical
	} else {
		dbBlock.Status = dbtypes.Orphaned
	}
	return dbBlock
}
