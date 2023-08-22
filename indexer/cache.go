package indexer

import (
	"encoding/json"
	"sync"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type indexerCache struct {
	indexer                 *Indexer
	triggerChan             chan bool
	synchronizer            *synchronizerState
	cacheMutex              sync.RWMutex
	highestSlot             int64
	lowestSlot              int64
	finalizedEpoch          int64
	finalizedRoot           []byte
	processedEpoch          int64
	persistEpoch            int64
	cleanupEpoch            int64
	slotMap                 map[uint64][]*indexerCacheBlock
	rootMap                 map[string]*indexerCacheBlock
	epochStatsMutex         sync.RWMutex
	epochStatsMap           map[uint64][]*EpochStats
	lastValidatorsEpoch     int64
	lastValidatorsResp      *rpctypes.StandardV1StateValidatorsResponse
	validatorLoadingLimiter chan int
}

func newIndexerCache(indexer *Indexer) *indexerCache {
	cache := &indexerCache{
		indexer:                 indexer,
		triggerChan:             make(chan bool, 10),
		highestSlot:             -1,
		lowestSlot:              -1,
		finalizedEpoch:          -1,
		processedEpoch:          -2,
		persistEpoch:            -1,
		cleanupEpoch:            -1,
		slotMap:                 make(map[uint64][]*indexerCacheBlock),
		rootMap:                 make(map[string]*indexerCacheBlock),
		epochStatsMap:           make(map[uint64][]*EpochStats),
		lastValidatorsEpoch:     -1,
		validatorLoadingLimiter: make(chan int, 2),
	}
	cache.loadStoredUnfinalizedCache()
	go cache.runCacheLoop()
	return cache
}

func (cache *indexerCache) startSynchronizer(startEpoch uint64) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if cache.synchronizer == nil {
		cache.synchronizer = newSynchronizer(cache.indexer)
	}
	if !cache.synchronizer.isEpochAhead(startEpoch) {
		cache.synchronizer.startSync(startEpoch)
	}
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

func (cache *indexerCache) getFinalizedHead() (int64, []byte) {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()
	return cache.finalizedEpoch, cache.finalizedRoot
}

func (cache *indexerCache) setLastValidators(epoch uint64, validators *rpctypes.StandardV1StateValidatorsResponse) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	if int64(epoch) > cache.lastValidatorsEpoch {
		cache.lastValidatorsEpoch = int64(epoch)
		cache.lastValidatorsResp = validators
	}
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
		epochStats, _ := cache.createOrGetEpochStats(epochDuty.Epoch, epochDuty.DependentRoot)
		epochStats.dutiesInDb = true
	}
	return nil
}

/*
func (cache *indexerCache) resetLowestSlot() {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	var lowestSlot int64 = -1
	for slot := range cache.slotMap {
		if lowestSlot == -1 || int64(slot) < lowestSlot {
			lowestSlot = int64(slot)
		}
	}
	if lowestSlot != cache.lowestSlot {
		logger.Debugf("Reset lowest cached slot: %v", lowestSlot)
		cache.lowestSlot = lowestSlot
	}
}
*/

func (cache *indexerCache) getLastCanonicalBlock(epoch uint64, head []byte) *indexerCacheBlock {
	if head == nil {
		head = cache.finalizedRoot
	}
	canonicalBlock := cache.getCachedBlock(head)
	for canonicalBlock != nil && utils.EpochOfSlot(canonicalBlock.slot) > epoch {
		parentRoot := canonicalBlock.getParentRoot()
		if parentRoot == nil {
			return nil
		}
		canonicalBlock = cache.getCachedBlock(parentRoot)
		if canonicalBlock == nil {
			return nil
		}
	}
	if utils.EpochOfSlot(canonicalBlock.slot) == epoch {
		return canonicalBlock
	} else {
		return nil
	}
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

func (cache *indexerCache) getCanonicalBlockMap(epoch uint64, head []byte) map[uint64]*indexerCacheBlock {
	canonicalMap := make(map[uint64]*indexerCacheBlock)
	canonicalBlock := cache.getLastCanonicalBlock(epoch, head)
	for canonicalBlock != nil && utils.EpochOfSlot(canonicalBlock.slot) == epoch {
		canonicalBlock.mutex.RLock()
		parentRoot := []byte(canonicalBlock.header.Message.ParentRoot)
		canonicalMap[canonicalBlock.slot] = canonicalBlock
		canonicalBlock.mutex.RUnlock()
		canonicalBlock = cache.getCachedBlock(parentRoot)
	}
	return canonicalMap
}
