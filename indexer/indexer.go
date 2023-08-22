package indexer

import (
	"bytes"
	"fmt"
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
		indexerClients: make([]*IndexerClient, 0),

		writeDb:              !utils.Config.Indexer.DisableIndexWriter,
		prepopulateEpochs:    prepopulateEpochs,
		inMemoryEpochs:       inMemoryEpochs,
		epochProcessingDelay: epochProcessingDelay,
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

func (indexer *Indexer) GetCachedBlocks(slot uint64) []*BlockInfo {
	if int64(utils.EpochOfSlot(slot)) <= indexer.indexerCache.finalizedEpoch {
		return nil
	}
	_, headRoot := indexer.GetCanonicalHead()

	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()
	blocks := indexer.indexerCache.slotMap[slot]
	if blocks == nil {
		return nil
	}
	resBlocks := make([]*BlockInfo, len(blocks))
	for idx, block := range blocks {
		resBlocks[idx] = indexer.indexerCache.getBlockInfoFromCachedBlock(block, headRoot)
	}
	return resBlocks
}

func (indexer *Indexer) GetCachedBlock(root []byte) *BlockInfo {
	_, headRoot := indexer.GetCanonicalHead()
	indexer.indexerCache.cacheMutex.RLock()
	defer indexer.indexerCache.cacheMutex.RUnlock()

	block := indexer.indexerCache.rootMap[string(root)]
	if block != nil {
		return indexer.indexerCache.getBlockInfoFromCachedBlock(block, headRoot)
	}
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
	_, headRoot := indexer.GetCanonicalHead()

	indexer.indexerCache.epochStatsMutex.RLock()
	var epochStats *EpochStats
	epochStatsList := indexer.indexerCache.epochStatsMap[epoch]
	for _, stats := range epochStatsList {
		if indexer.indexerCache.isCanonicalBlock(stats.DependentRoot, headRoot) {
			epochStats = stats
			break
		}
		fmt.Printf("non-canonical epoch stats: %v (0x%x)\n", epoch, stats.DependentRoot)
	}
	indexer.indexerCache.epochStatsMutex.RUnlock()

	return epochStats
}

func (indexer *Indexer) GetCachedValidatorSet() *rpctypes.StandardV1StateValidatorsResponse {
	return indexer.indexerCache.lastValidatorsResp
}

func (indexer *Indexer) GetEpochVotes(epoch uint64) (*EpochStats, *EpochVotes) {
	_, headRoot := indexer.GetCanonicalHead()
	epochStats := indexer.GetCachedEpochStats(epoch)
	if epochStats == nil {
		return nil, nil
	}

	// get epoch target
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	firstBlock := indexer.indexerCache.getFirstCanonicalBlock(epoch, headRoot)
	var epochTarget []byte
	if firstBlock == nil {
		logger.Warnf("Counld not find epoch %v target (no block found)", epoch)
	} else {
		if firstBlock.slot == firstSlot {
			epochTarget = firstBlock.root
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
	epochVotes := aggregateEpochVotes(canonicalMap, epoch, epochStats, epochTarget, false)

	return epochStats, epochVotes
}

func (indexer *Indexer) BuildLiveEpoch(epoch uint64) *dbtypes.Epoch {
	epochStats, epochVotes := indexer.GetEpochVotes(epoch)
	if epochStats == nil {
		return nil
	}

	// get canonical blocks
	canonicalMap := indexer.indexerCache.getCanonicalBlockMap(epoch, nil)

	return buildDbEpoch(epoch, canonicalMap, epochStats, epochVotes, nil)
}

func (indexer *Indexer) BuildLiveBlock(block *BlockInfo) *dbtypes.Block {
	epoch := utils.EpochOfSlot(uint64(block.Header.Message.Slot))
	epochStats := indexer.GetCachedEpochStats(epoch)
	if epochStats == nil {
		return nil
	}
	return buildDbBlock(block.cachedBlock, epochStats)
}
