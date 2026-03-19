package beacon

import (
	"reflect"
	"unsafe"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type CacheDebugStats struct {
	BlockCache struct {
		SlotMap        CacheDebugMapStats
		RootMap        CacheDebugMapStats
		ParentMap      CacheDebugMapStats
		ExecBlockMap   CacheDebugMapStats
		BlockHeader    uint64
		BlockBodies    uint64
		BlockIndexes   uint64
		BlockSize      uint64
		EstimatedTotal int64
	}
	EpochCache struct {
		StatsMap       CacheDebugMapStats
		StateMap       CacheDebugMapStats
		StatsFull      uint64
		StatsPrecalc   uint64
		StatsPruned    uint64
		StateLoaded    uint64
		VotesCacheLen  uint64
		VotesCacheHit  uint64
		VotesCacheMiss uint64
		EstimatedTotal int64
	}
	ForkCache struct {
		ForkMap            CacheDebugMapStats
		ParentIdCacheLen   uint64
		ParentIdCacheHit   uint64
		ParentIdCacheMiss  uint64
		ParentIdsCacheLen  uint64
		ParentIdsCacheHit  uint64
		ParentIdsCacheMiss uint64
		EstimatedTotal     int64
	}
	ValidatorCache struct {
		Validators        uint64
		ValidatorDiffs    uint64
		ValidatorData     uint64
		ValidatorActivity uint64
		PubkeyMap         CacheDebugMapStats
		EstimatedTotal    int64
	}
	TotalEstimated int64
}

// CacheDebugMapStats holds the entry count and estimated memory footprint
// of a Go map's hash table structure (buckets + overhead, not values behind pointers).
type CacheDebugMapStats struct {
	Length int
	Size   int64
}

// mapOverhead estimates the memory used by a Go map's hash table structure.
func mapOverhead(m any) int64 {
	v := reflect.ValueOf(m)
	if v.Kind() != reflect.Map {
		return 0
	}

	t := v.Type()
	keySize := int64(t.Key().Size())
	valSize := int64(t.Elem().Size())
	count := int64(v.Len())

	if count == 0 {
		return 48
	}

	minBuckets := (count*2 + 12) / 13
	numBuckets := int64(1)
	for numBuckets < minBuckets {
		numBuckets *= 2
	}

	bucketSize := int64(8) + 8*keySize + 8*valSize + int64(8)
	return 48 + numBuckets*bucketSize
}

func getMapStats(m any) CacheDebugMapStats {
	v := reflect.ValueOf(m)
	return CacheDebugMapStats{
		Length: v.Len(),
		Size:   mapOverhead(m),
	}
}

func (indexer *Indexer) GetCacheDebugStats() *CacheDebugStats {
	cacheStats := &CacheDebugStats{}
	indexer.getBlockCacheDebugStats(cacheStats)
	indexer.getEpochCacheDebugStats(cacheStats)
	indexer.getForkCacheDebugStats(cacheStats)
	indexer.getValidatorCacheDebugStats(cacheStats)

	cacheStats.TotalEstimated = cacheStats.BlockCache.EstimatedTotal +
		cacheStats.EpochCache.EstimatedTotal +
		cacheStats.ForkCache.EstimatedTotal +
		cacheStats.ValidatorCache.EstimatedTotal

	return cacheStats
}

func (indexer *Indexer) getBlockCacheDebugStats(cacheStats *CacheDebugStats) {
	indexer.blockCache.cacheMutex.RLock()
	defer indexer.blockCache.cacheMutex.RUnlock()

	cacheStats.BlockCache.SlotMap = getMapStats(indexer.blockCache.slotMap)
	cacheStats.BlockCache.RootMap = getMapStats(indexer.blockCache.rootMap)
	cacheStats.BlockCache.ParentMap = getMapStats(indexer.blockCache.parentMap)
	cacheStats.BlockCache.ExecBlockMap = getMapStats(indexer.blockCache.execBlockMap)

	blockStructSize := int64(unsafe.Sizeof(Block{}))
	bodyIndexSize := int64(unsafe.Sizeof(BlockBodyIndex{}))
	cacheStats.BlockCache.BlockSize = uint64(blockStructSize)

	for _, block := range indexer.blockCache.rootMap {
		if block.header != nil {
			cacheStats.BlockCache.BlockHeader++
		}
		if block.block != nil {
			cacheStats.BlockCache.BlockBodies++
		}
		if block.blockIndex != nil {
			cacheStats.BlockCache.BlockIndexes++
		}
	}

	numBlocks := int64(len(indexer.blockCache.rootMap))
	mapsTotal := cacheStats.BlockCache.SlotMap.Size +
		cacheStats.BlockCache.RootMap.Size +
		cacheStats.BlockCache.ParentMap.Size +
		cacheStats.BlockCache.ExecBlockMap.Size
	cacheStats.BlockCache.EstimatedTotal = mapsTotal +
		numBlocks*blockStructSize +
		int64(cacheStats.BlockCache.BlockIndexes)*bodyIndexSize
}

func (indexer *Indexer) getEpochCacheDebugStats(cacheStats *CacheDebugStats) {
	indexer.epochCache.cacheMutex.RLock()
	defer indexer.epochCache.cacheMutex.RUnlock()

	cacheStats.EpochCache.StatsMap = getMapStats(indexer.epochCache.statsMap)
	cacheStats.EpochCache.StateMap = getMapStats(indexer.epochCache.stateMap)

	epochStatsSize := int64(unsafe.Sizeof(EpochStats{}))
	epochStateSize := int64(unsafe.Sizeof(epochState{}))
	epochStatsValuesSize := int64(unsafe.Sizeof(EpochStatsValues{}))

	var stateDataEstimate int64

	for _, stats := range indexer.epochCache.statsMap {
		if stats.values != nil {
			cacheStats.EpochCache.StatsFull++
			// ActiveIndices + EffectiveBalances are the large arrays
			stateDataEstimate += int64(len(stats.values.ActiveIndices)) * 8
			stateDataEstimate += int64(len(stats.values.EffectiveBalances)) * 4
			stateDataEstimate += int64(len(stats.values.ProposerDuties)) * 8
			stateDataEstimate += int64(len(stats.values.SyncCommitteeDuties)) * 8
			stateDataEstimate += epochStatsValuesSize
		}
		if stats.precalcValues != nil {
			cacheStats.EpochCache.StatsPrecalc++
		}
		if stats.prunedValues != nil {
			cacheStats.EpochCache.StatsPruned++
		}
	}

	for _, state := range indexer.epochCache.stateMap {
		if state.loadingStatus == 2 {
			cacheStats.EpochCache.StateLoaded++
			// validatorBalances and randaoMixes are the large arrays
			stateDataEstimate += int64(len(state.validatorBalances)) * 8
			stateDataEstimate += int64(len(state.randaoMixes)) * 32
			stateDataEstimate += int64(len(state.syncCommittee)) * 8
		}
	}

	cacheStats.EpochCache.VotesCacheLen = uint64(indexer.epochCache.votesCache.Len())
	cacheStats.EpochCache.VotesCacheHit = indexer.epochCache.votesCacheHit
	cacheStats.EpochCache.VotesCacheMiss = indexer.epochCache.votesCacheMiss

	numStats := int64(len(indexer.epochCache.statsMap))
	numStates := int64(len(indexer.epochCache.stateMap))
	mapsTotal := cacheStats.EpochCache.StatsMap.Size + cacheStats.EpochCache.StateMap.Size
	cacheStats.EpochCache.EstimatedTotal = mapsTotal +
		numStats*epochStatsSize +
		numStates*epochStateSize +
		stateDataEstimate
}

func (indexer *Indexer) getForkCacheDebugStats(cacheStats *CacheDebugStats) {
	indexer.forkCache.cacheMutex.RLock()
	defer indexer.forkCache.cacheMutex.RUnlock()

	cacheStats.ForkCache.ForkMap = getMapStats(indexer.forkCache.forkMap)

	cacheStats.ForkCache.ParentIdCacheLen = uint64(indexer.forkCache.parentIdCache.Len())
	cacheStats.ForkCache.ParentIdCacheHit = indexer.forkCache.parentIdCacheHit
	cacheStats.ForkCache.ParentIdCacheMiss = indexer.forkCache.parentIdCacheMiss

	cacheStats.ForkCache.ParentIdsCacheLen = uint64(indexer.forkCache.parentIdsCache.Len())
	cacheStats.ForkCache.ParentIdsCacheHit = indexer.forkCache.parentIdsCacheHit
	cacheStats.ForkCache.ParentIdsCacheMiss = indexer.forkCache.parentIdsCacheMiss

	forkSize := int64(unsafe.Sizeof(Fork{}))
	numForks := int64(len(indexer.forkCache.forkMap))
	cacheStats.ForkCache.EstimatedTotal = cacheStats.ForkCache.ForkMap.Size +
		numForks*forkSize
}

func (indexer *Indexer) getValidatorCacheDebugStats(cacheStats *CacheDebugStats) {
	indexer.validatorCache.cacheMutex.RLock()
	defer indexer.validatorCache.cacheMutex.RUnlock()

	cacheStats.ValidatorCache.Validators = uint64(len(indexer.validatorCache.valsetCache))

	entrySize := int64(unsafe.Sizeof(validatorEntry{}))
	diffSize := int64(unsafe.Sizeof(validatorDiff{}))
	validatorObjSize := int64(unsafe.Sizeof(phase0.Validator{}))

	validatorsMap := map[*phase0.Validator]bool{}
	for _, validator := range indexer.validatorCache.valsetCache {
		refs := len(validator.validatorDiffs)
		for _, diff := range validator.validatorDiffs {
			validatorsMap[diff.validator] = true
		}

		if validator.finalValidator != nil {
			validatorsMap[validator.finalValidator] = true
			refs++
		}
		cacheStats.ValidatorCache.ValidatorDiffs += uint64(refs)
	}

	cacheStats.ValidatorCache.ValidatorData = uint64(len(validatorsMap))

	var activityCount int64
	for _, recentActivity := range indexer.validatorActivity.activityMap {
		activityCount += int64(len(recentActivity))
	}
	cacheStats.ValidatorCache.ValidatorActivity = uint64(activityCount)

	if indexer.pubkeyCache.pubkeyMap != nil {
		cacheStats.ValidatorCache.PubkeyMap = getMapStats(indexer.pubkeyCache.pubkeyMap)
	}

	numValidators := int64(cacheStats.ValidatorCache.Validators)
	numDiffs := int64(cacheStats.ValidatorCache.ValidatorDiffs)
	numData := int64(cacheStats.ValidatorCache.ValidatorData)

	cacheStats.ValidatorCache.EstimatedTotal = cacheStats.ValidatorCache.PubkeyMap.Size +
		numValidators*entrySize +
		numDiffs*diffSize +
		numData*validatorObjSize +
		activityCount*8 // activity entries are uint8 slices per epoch, rough estimate
}
