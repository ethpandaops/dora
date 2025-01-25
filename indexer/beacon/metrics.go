package beacon

import (
	"github.com/ethpandaops/dora/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type beaconMetrics struct {
	indexer                    *Indexer
	blockCacheSlotMapSize      prometheus.Gauge
	blockCacheRootMapSize      prometheus.Gauge
	blockCacheParentMapSize    prometheus.Gauge
	blockCacheExecBlockMapSize prometheus.Gauge
	blockCacheBlockHeaderCount prometheus.Gauge
	blockCacheBlockBodyCount   prometheus.Gauge
	blockCacheBlockIndexCount  prometheus.Gauge

	epochCacheStatsMapSize      prometheus.Gauge
	epochCacheStateMapSize      prometheus.Gauge
	epochCacheStatsFullCount    prometheus.Gauge
	epochCacheStatsPrecalcCount prometheus.Gauge
	epochCacheStatsPrunedCount  prometheus.Gauge
	epochCacheStateLoadedCount  prometheus.Gauge
	epochCacheVotesCacheLen     prometheus.Gauge
	epochCacheVotesCacheHit     prometheus.Counter
	epochCacheVotesCacheMiss    prometheus.Counter
}

func (indexer *Indexer) registerMetrics() *beaconMetrics {
	beaconMetrics := &beaconMetrics{
		indexer: indexer,
		blockCacheSlotMapSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_block_cache_slot_map_size",
			Help: "Number of entries in the block cache slot map",
		}),
		blockCacheRootMapSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_block_cache_root_map_size",
			Help: "Number of entries in the block cache root map",
		}),
		blockCacheParentMapSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_block_cache_parent_map_size",
			Help: "Number of entries in the block cache parent map",
		}),
		blockCacheExecBlockMapSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_block_cache_exec_block_map_size",
			Help: "Number of entries in the block cache exec block map",
		}),
		blockCacheBlockHeaderCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_block_cache_block_header_count",
			Help: "Number of blocks with a block header",
		}),
		blockCacheBlockBodyCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_block_cache_block_body_count",
			Help: "Number of blocks with a block body",
		}),
		blockCacheBlockIndexCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_block_cache_block_index_count",
			Help: "Number of blocks with a block index",
		}),
		epochCacheStatsMapSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_epoch_cache_stats_map_size",
			Help: "Number of entries in the epoch cache stats map",
		}),
		epochCacheStateMapSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_epoch_cache_state_map_size",
			Help: "Number of entries in the epoch cache state map",
		}),
		epochCacheStatsFullCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_epoch_cache_stats_full_count",
			Help: "Number of full epoch cache stats",
		}),
		epochCacheStatsPrecalcCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_epoch_cache_stats_precalc_count",
			Help: "Number of precalculated epoch cache stats",
		}),
		epochCacheStatsPrunedCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_epoch_cache_stats_pruned_count",
			Help: "Number of pruned epoch cache stats",
		}),
		epochCacheStateLoadedCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_epoch_cache_state_loaded_count",
			Help: "Number of loaded epoch cache state",
		}),
		epochCacheVotesCacheLen: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "dora_cl_indexer_epoch_cache_votes_cache_len",
			Help: "Number of entries in the epoch cache votes cache",
		}),
		epochCacheVotesCacheHit: promauto.NewCounter(prometheus.CounterOpts{
			Name: "dora_cl_indexer_epoch_cache_votes_cache_hit",
			Help: "Number of hits in the epoch cache votes cache",
		}),
		epochCacheVotesCacheMiss: promauto.NewCounter(prometheus.CounterOpts{
			Name: "dora_cl_indexer_epoch_cache_votes_cache_miss",
			Help: "Number of misses in the epoch cache votes cache",
		}),
	}

	metrics.AddPreCollectFn(func() {
		beaconMetrics.updateBlockCacheMetrics()
		beaconMetrics.updateEpochCacheMetrics()
	})

	return beaconMetrics
}

func (metrics *beaconMetrics) updateBlockCacheMetrics() {
	metrics.indexer.blockCache.cacheMutex.RLock()
	defer metrics.indexer.blockCache.cacheMutex.RUnlock()

	metrics.blockCacheSlotMapSize.Set(float64(len(metrics.indexer.blockCache.slotMap)))
	metrics.blockCacheRootMapSize.Set(float64(len(metrics.indexer.blockCache.rootMap)))
	metrics.blockCacheParentMapSize.Set(float64(len(metrics.indexer.blockCache.parentMap)))
	metrics.blockCacheExecBlockMapSize.Set(float64(len(metrics.indexer.blockCache.execBlockMap)))

	blockHeaderCount := 0
	blockBodyCount := 0
	blockIndexCount := 0
	for _, block := range metrics.indexer.blockCache.rootMap {
		if block.header != nil {
			blockHeaderCount++
		}
		if block.block != nil {
			blockBodyCount++
		}
		if block.blockIndex != nil {
			blockIndexCount++
		}
	}

	metrics.blockCacheBlockHeaderCount.Set(float64(blockHeaderCount))
	metrics.blockCacheBlockBodyCount.Set(float64(blockBodyCount))
	metrics.blockCacheBlockIndexCount.Set(float64(blockIndexCount))
}

func (metrics *beaconMetrics) updateEpochCacheMetrics() {
	metrics.indexer.epochCache.cacheMutex.RLock()
	defer metrics.indexer.epochCache.cacheMutex.RUnlock()

	metrics.epochCacheStatsMapSize.Set(float64(len(metrics.indexer.epochCache.statsMap)))
	metrics.epochCacheStateMapSize.Set(float64(len(metrics.indexer.epochCache.stateMap)))

	fullStatsCount := 0
	precalcStatsCount := 0
	prunedStatsCount := 0
	for _, stats := range metrics.indexer.epochCache.statsMap {
		if stats.values != nil {
			fullStatsCount++
		}
		if stats.precalcValues != nil {
			precalcStatsCount++
		}
		if stats.prunedValues != nil {
			prunedStatsCount++
		}
	}

	stateLoadedCount := 0
	for _, state := range metrics.indexer.epochCache.stateMap {
		if state.loadingStatus == 2 {
			stateLoadedCount++
		}
	}

	metrics.epochCacheStatsFullCount.Set(float64(fullStatsCount))
	metrics.epochCacheStatsPrecalcCount.Set(float64(precalcStatsCount))
	metrics.epochCacheStatsPrunedCount.Set(float64(prunedStatsCount))
	metrics.epochCacheStateLoadedCount.Set(float64(stateLoadedCount))
	metrics.epochCacheVotesCacheLen.Set(float64(metrics.indexer.epochCache.votesCache.Len()))
}
