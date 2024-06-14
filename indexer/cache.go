package indexer

import (
	"bytes"
	"sync"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/utils"
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
	prefillEpoch            int64
	processedEpoch          int64
	processingRetry         uint64
	persistEpoch            int64
	cleanupBlockEpoch       int64
	cleanupStatsEpoch       int64
	lastPersistedEpoch      int64
	slotMap                 map[uint64][]*CacheBlock
	rootMap                 map[string]*CacheBlock
	epochStatsMutex         sync.RWMutex
	epochStatsMap           map[uint64][]*EpochStats
	lastValidatorsEpoch     int64
	lastValidatorsResp      map[phase0.ValidatorIndex]*v1.Validator
	lastValidatorsPubKeyMap map[phase0.BLSPubKey]*v1.Validator
	genesisResp             *v1.Genesis
	validatorLoadingLimiter chan int
}

func newIndexerCache(indexer *Indexer) *indexerCache {
	valsetConcurrencyLimit := utils.Config.Indexer.MaxParallelValidatorSetRequests
	if valsetConcurrencyLimit < 1 {
		valsetConcurrencyLimit = 1
	}
	cache := &indexerCache{
		indexer:                 indexer,
		triggerChan:             make(chan bool, 1),
		highestSlot:             -1,
		lowestSlot:              -1,
		finalizedEpoch:          -1,
		justifiedEpoch:          -1,
		prefillEpoch:            -1,
		processedEpoch:          -2,
		persistEpoch:            -1,
		cleanupBlockEpoch:       -1,
		cleanupStatsEpoch:       -1,
		lastPersistedEpoch:      -1,
		slotMap:                 make(map[uint64][]*CacheBlock),
		rootMap:                 make(map[string]*CacheBlock),
		epochStatsMap:           make(map[uint64][]*EpochStats),
		lastValidatorsEpoch:     -1,
		validatorLoadingLimiter: make(chan int, valsetConcurrencyLimit),
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

func (cache *indexerCache) setPrefillEpoch(prefillEpoch int64) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if prefillEpoch > cache.prefillEpoch {
		cache.prefillEpoch = prefillEpoch
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
		select {
		case cache.triggerChan <- true:
		default:
		}
	}
}

func (cache *indexerCache) setGenesis(genesis *v1.Genesis) {
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

func (cache *indexerCache) setLastValidators(epoch uint64, validators map[phase0.ValidatorIndex]*v1.Validator) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	if int64(epoch) > cache.lastValidatorsEpoch {
		cache.lastValidatorsEpoch = int64(epoch)
		cache.lastValidatorsResp = validators

		cache.lastValidatorsPubKeyMap = map[phase0.BLSPubKey]*v1.Validator{}
		for idx := range validators {
			validator := validators[idx]
			cache.lastValidatorsPubKeyMap[validator.Validator.PublicKey] = validator
		}
	}
}

func (cache *indexerCache) loadStoredUnfinalizedCache() error {
	blocks := db.GetUnfinalizedBlocks()
	for _, block := range blocks {
		if block.HeaderVer != 1 {
			logger.Warnf("failed unmarshal unfinalized block header from db: unsupported header version")
			continue
		}
		header := &phase0.SignedBeaconBlockHeader{}
		err := header.UnmarshalSSZ(block.HeaderSSZ)
		if err != nil {
			logger.Warnf("failed unmarshal unfinalized block header from db: %v", err)
			continue
		}
		body, err := UnmarshalVersionedSignedBeaconBlockSSZ(block.BlockVer, block.BlockSSZ)
		if err != nil {
			logger.Warnf("Error parsing unfinalized block body from db: %v", err)
			continue
		}
		logger.Debugf("Restored unfinalized block header from db: %v", block.Slot)
		cachedBlock, _ := cache.createOrGetCachedBlock(block.Root, block.Slot)
		cachedBlock.mutex.Lock()
		cachedBlock.header = header
		cachedBlock.block = body
		cachedBlock.isInUnfinalizedDb = true
		cachedBlock.parseBlockRefs()
		cachedBlock.mutex.Unlock()
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
		parentRoot := canonicalBlock.GetParentRoot()
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
		canonicalMap[canonicalBlock.Slot] = canonicalBlock
		parentRoot := canonicalBlock.GetParentRoot()
		canonicalBlock = cache.getCachedBlock(parentRoot)
	}
	return canonicalMap
}
