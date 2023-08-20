package indexer

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type indexerCacheBlock struct {
	root   []byte
	slot   uint64
	mutex  sync.RWMutex
	seenBy uint64
	isInDb bool
	header *rpctypes.SignedBeaconBlockHeader
	block  *rpctypes.SignedBeaconBlock
}

type indexerCache struct {
	cacheMutex      sync.RWMutex
	highestSlot     int64
	lowestSlot      int64
	finalizedEpoch  int64
	finalizedRoot   []byte
	processedEpoch  int64
	cleanupEpoch    int64
	slotMap         map[uint64][]*indexerCacheBlock
	rootMap         map[string]*indexerCacheBlock
	epochStatsMutex sync.RWMutex
	epochStatsMap   map[uint64][]*EpochStats
	triggerChan     chan bool
	writeDb         bool
}

func newIndexerCache(writeDb bool) *indexerCache {
	cache := &indexerCache{
		highestSlot:    -1,
		lowestSlot:     -1,
		finalizedEpoch: -1,
		processedEpoch: -1,
		cleanupEpoch:   -1,
		slotMap:        make(map[uint64][]*indexerCacheBlock),
		rootMap:        make(map[string]*indexerCacheBlock),
		epochStatsMap:  make(map[uint64][]*EpochStats),
		triggerChan:    make(chan bool, 10),
		writeDb:        writeDb,
	}
	cache.loadStoredUnfinalizedCache()
	if writeDb {
		syncState := dbtypes.IndexerSyncState{}
		_, err := db.GetExplorerState("indexer.syncstate", &syncState)
		if err == nil {
			cache.processedEpoch = int64(syncState.Epoch)
		}
	}
	go cache.runCacheLoop()
	return cache
}

func (cache *indexerCache) setFinalizedHead(epoch int64, root []byte) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	if epoch > cache.finalizedEpoch {
		cache.finalizedEpoch = epoch
		cache.finalizedRoot = root

		// trigger processing
		cache.triggerChan <- true
	}
}

func (cache *indexerCache) getCachedBlock(root []byte) *indexerCacheBlock {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()
	rootKey := string(root)
	return cache.rootMap[rootKey]
}

func (cache *indexerCache) createOrGetCachedBlock(root []byte, slot uint64) (*indexerCacheBlock, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	rootKey := string(root)
	if cache.rootMap[rootKey] != nil {
		return cache.rootMap[rootKey], false
	}
	cacheBlock := &indexerCacheBlock{
		root: root,
		slot: slot,
	}
	cache.rootMap[rootKey] = cacheBlock
	if cache.slotMap[slot] == nil {
		cache.slotMap[slot] = []*indexerCacheBlock{cacheBlock}
	} else {
		cache.slotMap[slot] = append(cache.slotMap[slot], cacheBlock)
	}
	if int64(slot) > cache.highestSlot {
		cache.highestSlot = int64(slot)
	}
	if int64(slot) > cache.lowestSlot {
		cache.lowestSlot = int64(slot)
	}
	return cacheBlock, true
}

func (cache *indexerCache) loadStoredUnfinalizedCache() error {
	blockHeaders := db.GetUnfinalizedBlockHeader()
	for _, blockHeader := range blockHeaders {
		var header rpctypes.SignedBeaconBlockHeader
		err := json.Unmarshal([]byte(blockHeader.Header), &header)
		if err != nil {
			logger.Warnf("Error parsing unfinalized block header from db: %v", err)
			continue
		}
		logger.Debugf("Restored unfinalized block header from db: %v", blockHeader.Slot)
		cachedBlock, _ := cache.createOrGetCachedBlock(blockHeader.Root, blockHeader.Slot)
		cachedBlock.mutex.Lock()
		cachedBlock.header = &header
		cachedBlock.isInDb = true
		cachedBlock.mutex.Unlock()
	}
	epochDuties := db.GetUnfinalizedEpochDutyRefs()
	for _, epochDuty := range epochDuties {
		logger.Debugf("Restored unfinalized block duty ref from db: %v/0x%x", epochDuty.Epoch, epochDuty.DependentRoot)
		epochStats := cache.createOrGetEpochStats(epochDuty.Epoch, epochDuty.DependentRoot, nil)
		epochStats.dutiesInDb = true
	}
	return nil
}

func (cache *indexerCache) runCacheLoop() {
	for {
		select {
		case <-cache.triggerChan:
		case <-time.After(30 * time.Second):
			break
		}
		logger.Debugf("Run indexer cache logic")
		err := cache.runCacheLogic()
		if err != nil {
			logger.Errorf("Indexer cache error: %v, retrying in 10 sec...", err)
			time.Sleep(10 * time.Second)
		}
	}
}

func (cache *indexerCache) runCacheLogic() error {
	if cache.highestSlot <= 0 {
		return nil
	}
	currentSlot := utils.TimeToSlot(uint64(time.Now().Unix()))
	currentEpoch := utils.EpochOfSlot(currentSlot)

	if cache.processedEpoch < cache.finalizedEpoch {
		// process finalized epochs
		cache.processFinalizedEpochs()
	}

	if cache.lowestSlot >= 0 && int64(utils.EpochOfSlot(uint64(cache.lowestSlot))) < cache.processedEpoch {
		// process new blocks already processed epochs (duplicates or new orphaned blocks)

	}

	if cache.cleanupEpoch < int64(currentEpoch) {
		// process cache cleanup
		// TODO: move old but unfinalized block bodies to db
	}

	return nil
}

func (cache *indexerCache) processFinalizedEpochs() error {
	for cache.processedEpoch < cache.finalizedEpoch {
		processEpoch := uint64(cache.processedEpoch + 1)
		err := cache.processFinalizedEpoch(processEpoch)
		if err != nil {
			return err
		}
		cache.processedEpoch = int64(processEpoch)
	}
	return nil
}

func (cache *indexerCache) getLastCanonicalBlock(epoch uint64, head []byte) *indexerCacheBlock {
	if head == nil {
		if int64(epoch) >= cache.finalizedEpoch {
			return nil
		}
		head = cache.finalizedRoot
	}
	canonicalBlock := cache.getCachedBlock(head)
	for canonicalBlock != nil && utils.EpochOfSlot(canonicalBlock.slot) > epoch {
		canonicalBlock.mutex.RLock()
		parentRoot := []byte(canonicalBlock.header.Message.ParentRoot)
		canonicalBlock.mutex.RUnlock()
		canonicalBlock = cache.getCachedBlock(parentRoot)
	}
	return canonicalBlock
}

func (cache *indexerCache) getFirstCanonicalBlock(epoch uint64, head []byte) *indexerCacheBlock {
	canonicalBlock := cache.getLastCanonicalBlock(epoch, head)

	for canonicalBlock != nil {
		canonicalBlock.mutex.RLock()
		parentRoot := []byte(canonicalBlock.header.Message.ParentRoot)
		canonicalBlock.mutex.RUnlock()
		parentCanonicalBlock := cache.getCachedBlock(parentRoot)
		if parentCanonicalBlock == nil || utils.EpochOfSlot(parentCanonicalBlock.slot) != epoch {
			return canonicalBlock
		}
		canonicalBlock = parentCanonicalBlock
	}
	return nil
}

func (cache *indexerCache) getCanonicalBlockMap(epoch uint64, head []byte) map[string]*indexerCacheBlock {
	canonicalMap := make(map[string]*indexerCacheBlock)
	canonicalBlock := cache.getLastCanonicalBlock(epoch, head)
	for canonicalBlock != nil && utils.EpochOfSlot(canonicalBlock.slot) == epoch {
		canonicalBlock.mutex.RLock()
		parentRoot := []byte(canonicalBlock.header.Message.ParentRoot)
		canonicalKey := string(canonicalBlock.root)
		canonicalMap[canonicalKey] = canonicalBlock
		canonicalBlock.mutex.RUnlock()
		canonicalBlock = cache.getCachedBlock(parentRoot)
	}
	return canonicalMap
}

func (cache *indexerCache) processFinalizedEpoch(epoch uint64) error {
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	firstBlock := cache.getFirstCanonicalBlock(epoch, nil)
	var epochTarget []byte
	var epochDependentRoot []byte
	if firstBlock == nil {
		logger.Warnf("Counld not find epoch %v target (no block found)", epoch)
	} else {
		if firstBlock.slot == firstSlot {
			epochTarget = firstBlock.root
		} else {
			epochTarget = firstBlock.header.Message.ParentRoot
		}
		epochDependentRoot = firstBlock.header.Message.ParentRoot
	}

	logger.Infof("Processing finalized epoch %v:  target: 0x%x, dependent: 0x%x", epoch, epochTarget, epochDependentRoot)
	return nil
}
