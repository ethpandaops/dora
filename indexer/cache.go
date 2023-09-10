package indexer

import (
	"bytes"
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
	justifiedEpoch          int64
	justifiedRoot           []byte
	processedEpoch          int64
	persistEpoch            int64
	cleanupEpoch            int64
	slotMap                 map[uint64][]*CacheBlock
	rootMap                 map[string]*CacheBlock
	epochStatsMutex         sync.RWMutex
	epochStatsMap           map[uint64][]*EpochStats
	lastValidatorsEpoch     int64
	lastValidatorsResp      *rpctypes.StandardV1StateValidatorsResponse
	genesisResp             *rpctypes.StandardV1GenesisResponse
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
		slotMap:                 make(map[uint64][]*CacheBlock),
		rootMap:                 make(map[string]*CacheBlock),
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

	if cache.indexer.disableSync {
		return
	}
	if cache.synchronizer == nil {
		cache.synchronizer = newSynchronizer(cache.indexer)
	}
	if !cache.synchronizer.isEpochAhead(startEpoch) {
		cache.synchronizer.startSync(startEpoch)
	}
}

func (cache *indexerCache) setFinalizedHead(finalizedEpoch int64, finalizedRoot []byte, justifiedEpoch int64, justifiedRoot []byte) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if justifiedEpoch > cache.justifiedEpoch {
		cache.justifiedEpoch = justifiedEpoch
		cache.justifiedRoot = justifiedRoot
	}
	if finalizedEpoch > cache.finalizedEpoch {
		cache.finalizedEpoch = finalizedEpoch
		cache.finalizedRoot = finalizedRoot

		// trigger processing
		cache.triggerChan <- true
	}
}

func (cache *indexerCache) setGenesis(genesis *rpctypes.StandardV1GenesisResponse) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	if cache.genesisResp == nil {
		cache.genesisResp = genesis
	}
}

func (cache *indexerCache) getFinalizationCheckpoints() (int64, []byte, int64, []byte) {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()
	return cache.finalizedEpoch, cache.finalizedRoot, cache.justifiedEpoch, cache.justifiedRoot
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
	blocks := db.GetUnfinalizedBlocks()
	for _, block := range blocks {
		var header rpctypes.SignedBeaconBlockHeader
		err := json.Unmarshal([]byte(block.Header), &header)
		if err != nil {
			logger.Warnf("Error parsing unfinalized block header from db: %v", err)
			continue
		}
		var body rpctypes.SignedBeaconBlock
		err = json.Unmarshal([]byte(block.Block), &body)
		if err != nil {
			logger.Warnf("Error parsing unfinalized block body from db: %v", err)
			continue
		}
		logger.Debugf("Restored unfinalized block header from db: %v", block.Slot)
		cachedBlock, _ := cache.createOrGetCachedBlock(block.Root, block.Slot)
		cachedBlock.mutex.Lock()
		cachedBlock.header = &header
		cachedBlock.block = &body
		cachedBlock.isInDb = true
		cachedBlock.parseBlockRefs()
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

func (cache *indexerCache) isCanonicalBlock(blockRoot []byte, head []byte) bool {
	res, _ := cache.getCanonicalDistance(blockRoot, head)
	return res
}

func (cache *indexerCache) getCanonicalDistance(blockRoot []byte, head []byte) (bool, uint64) {
	if head == nil {
		head = cache.justifiedRoot
	}
	block := cache.getCachedBlock(blockRoot)
	var blockSlot uint64
	if block == nil {
		blockSlot = uint64(cache.finalizedEpoch+1) * utils.Config.Chain.Config.SlotsPerEpoch
	} else {
		blockSlot = block.Slot
	}
	canonicalBlock := cache.getCachedBlock(head)
	var distance uint64 = 0
	if canonicalBlock == nil {
		return false, 0
	}
	if bytes.Equal(canonicalBlock.Root, blockRoot) {
		return true, distance
	}
	for canonicalBlock != nil {
		if canonicalBlock.Slot < blockSlot {
			return false, 0
		}
		parentRoot := canonicalBlock.GetParentRoot()
		if parentRoot == nil {
			return false, 0
		}
		distance++
		if bytes.Equal(parentRoot, blockRoot) {
			return true, distance
		}
		canonicalBlock = cache.getCachedBlock(parentRoot)
		if canonicalBlock == nil {
			return false, 0
		}
	}
	return false, 0
}

func (cache *indexerCache) getLastCanonicalBlock(epoch uint64, head []byte) *CacheBlock {
	if head == nil {
		head = cache.justifiedRoot
	}
	canonicalBlock := cache.getCachedBlock(head)
	for canonicalBlock != nil && utils.EpochOfSlot(canonicalBlock.Slot) > epoch {
		parentRoot := canonicalBlock.GetParentRoot()
		if parentRoot == nil {
			return nil
		}
		canonicalBlock = cache.getCachedBlock(parentRoot)
		if canonicalBlock == nil {
			return nil
		}
	}
	if canonicalBlock != nil && utils.EpochOfSlot(canonicalBlock.Slot) == epoch {
		return canonicalBlock
	} else {
		return nil
	}
}

func (cache *indexerCache) getFirstCanonicalBlock(epoch uint64, head []byte) *CacheBlock {
	canonicalBlock := cache.getLastCanonicalBlock(epoch, head)
	for canonicalBlock != nil {
		canonicalBlock.mutex.RLock()
		var parentRoot []byte = nil
		if canonicalBlock.header != nil {
			parentRoot = []byte(canonicalBlock.header.Message.ParentRoot)
		}
		canonicalBlock.mutex.RUnlock()
		if parentRoot == nil {
			return canonicalBlock
		}
		parentCanonicalBlock := cache.getCachedBlock(parentRoot)
		if parentCanonicalBlock == nil || utils.EpochOfSlot(parentCanonicalBlock.Slot) != epoch {
			return canonicalBlock
		}
		canonicalBlock = parentCanonicalBlock
	}
	return nil
}

func (cache *indexerCache) getCanonicalBlockMap(epoch uint64, head []byte) map[uint64]*CacheBlock {
	canonicalMap := make(map[uint64]*CacheBlock)
	canonicalBlock := cache.getLastCanonicalBlock(epoch, head)
	for canonicalBlock != nil && utils.EpochOfSlot(canonicalBlock.Slot) == epoch {
		canonicalBlock.mutex.RLock()
		parentRoot := []byte(canonicalBlock.header.Message.ParentRoot)
		canonicalMap[canonicalBlock.Slot] = canonicalBlock
		canonicalBlock.mutex.RUnlock()
		canonicalBlock = cache.getCachedBlock(parentRoot)
	}
	return canonicalMap
}
