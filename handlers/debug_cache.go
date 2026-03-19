package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb"
	bpebble "github.com/ethpandaops/dora/blockdb/pebble"
	bs3 "github.com/ethpandaops/dora/blockdb/s3"
	"github.com/ethpandaops/dora/cache"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/indexer/execution/txindexer"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/utils"
)

// DebugPage serves the debug information page.
func DebugPage(w http.ResponseWriter, r *http.Request) {
	var debugTemplateFiles = append(layoutTemplateFiles,
		"debug_cache/debug_cache.html",
	)
	var pageTemplate = templates.GetTemplate(debugTemplateFiles...)

	if !utils.Config.Frontend.Pprof {
		handlePageError(w, r, errors.New("debug pages are not enabled"))
		return
	}

	pageData := buildDebugPageData()
	data := InitPageData(w, r, "blockchain", "/debug_cache", "Debug", debugTemplateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "debug_cache.go", "Debug", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

// DebugPageData holds all data for the debug page.
type DebugPageData struct {
	BeaconCache   *beacon.CacheDebugStats
	FrontendCache *FrontendCacheDebugData
	BlockDB       *BlockDBDebugData
	Database      *DatabaseDebugData
	System        *SystemDebugData
	ExecIndexer   *ExecIndexerDebugData
}

// FrontendCacheDebugData holds frontend cache statistics.
type FrontendCacheDebugData struct {
	Service        *services.FrontendCacheStats
	Tiered         *cache.TieredCacheStats
	PageTypes      []*cache.PageTypeStats
	PageTypesTotal int64
}

// BlockDBDebugData holds block database statistics.
type BlockDBDebugData struct {
	Enabled     bool
	EngineName  string
	PebbleStats *PebbleDebugData
	S3Stats     *S3DebugData
	StoredData  *BlockDbStoredData
}

// S3DebugData holds S3-specific statistics.
type S3DebugData struct {
	Bucket     string
	PathPrefix string
	GetCount   int64
	PutCount   int64
	StatCount  int64
	GetBytes   int64
	PutBytes   int64
}

// BlockDbStoredData holds aggregate stats from the DB about data stored in blockdb.
type BlockDbStoredData struct {
	BeaconBlockCount int64
	BeaconBlockSize  int64
	ExecDataCount    int64
	ExecDataSize     int64
	TotalCount       int64
	TotalSize        int64
}

// PebbleDebugData holds pebble-specific metrics.
type PebbleDebugData struct {
	BlockCacheSize   int64
	BlockCacheHits   int64
	BlockCacheMisses int64
	KeyCount         int64
	TableCount       int64
	TotalSize        int64
	WALSize          uint64
	WALCount         int64
	MemTableSize     uint64
	MemTableCount    int64
	ZombieTableCount int64
	ZombieTableSize  uint64
	LevelCount       int
	Levels           []PebbleLevelData
	CompactionCount  int64
	CompactionDebt   uint64
}

// PebbleLevelData holds per-level pebble metrics.
type PebbleLevelData struct {
	Level    int
	NumFiles int64
	Size     int64
	Score    float64
}

// DatabaseDebugData holds database statistics.
type DatabaseDebugData struct {
	Engine    string
	TotalSize int64
	Tables    []*db.TableStats
}

// SystemDebugData holds Go runtime statistics.
type SystemDebugData struct {
	GoVersion      string
	NumGoroutine   int
	NumCPU         int
	Uptime         string
	MemAlloc       uint64
	MemTotalAlloc  uint64
	MemSys         uint64
	MemHeapAlloc   uint64
	MemHeapSys     uint64
	MemHeapIdle    uint64
	MemHeapInuse   uint64
	MemHeapObjects uint64
	MemStackInuse  uint64
	MemStackSys    uint64
	GCCycles       uint32
	GCPauseTotal   time.Duration
	GCLastPause    time.Duration
	GCNextTarget   uint64
}

// ExecIndexerDebugData holds execution indexer statistics.
type ExecIndexerDebugData struct {
	Enabled          bool
	Indexer          *txindexer.TxIndexerDebugStats
	LatestSlot       uint64
	RecentBlockStats []*RecentBlockStatsEntry
}

// RecentBlockStatsEntry holds stats for a range of recent blocks.
type RecentBlockStatsEntry struct {
	Label        string
	BlockCount   int64
	Transactions int64
	Events       int64
	Transfers    int64
	ExecData     int64
	ExecDataSize int64
}

var startTime = time.Now()

func buildDebugPageData() *DebugPageData {
	logrus.Debugf("debug page called")

	data := &DebugPageData{}

	// Beacon indexer cache stats
	data.BeaconCache = services.GlobalBeaconService.GetBeaconIndexer().GetCacheDebugStats()

	// Frontend cache stats
	data.FrontendCache = buildFrontendCacheDebugData()

	// Block database stats
	data.BlockDB = buildBlockDBDebugData()

	// Database stats
	data.Database = buildDatabaseDebugData()

	// Execution indexer stats
	data.ExecIndexer = buildExecIndexerDebugData()

	// System stats
	data.System = buildSystemDebugData()

	return data
}

func buildFrontendCacheDebugData() *FrontendCacheDebugData {
	if services.GlobalFrontendCache == nil {
		return nil
	}

	pageTypes := services.GlobalFrontendCache.GetPageTypeStats()
	var pageTypesTotal int64
	for _, pt := range pageTypes {
		pageTypesTotal += pt.TotalSize
	}

	// Sort by total size descending
	sort.Slice(pageTypes, func(i, j int) bool {
		return pageTypes[i].TotalSize > pageTypes[j].TotalSize
	})

	return &FrontendCacheDebugData{
		Service:        services.GlobalFrontendCache.GetStats(),
		Tiered:         services.GlobalFrontendCache.GetTieredCacheStats(),
		PageTypes:      pageTypes,
		PageTypesTotal: pageTypesTotal,
	}
}

func buildBlockDBDebugData() *BlockDBDebugData {
	if blockdb.GlobalBlockDb == nil {
		return &BlockDBDebugData{Enabled: false}
	}

	data := &BlockDBDebugData{
		Enabled: true,
	}

	engine := blockdb.GlobalBlockDb.GetEngine()
	switch e := engine.(type) {
	case *bpebble.PebbleEngine:
		data.EngineName = "Pebble"
		data.PebbleStats = buildPebbleDebugData(e.GetDB())
	case *bs3.S3Engine:
		data.EngineName = "S3"
		data.S3Stats = buildS3DebugData(e)
	default:
		data.EngineName = "Unknown"
	}

	data.StoredData = buildBlockDbStoredData()
	return data
}

func buildPebbleDebugData(db *pebble.DB) *PebbleDebugData {
	if db == nil {
		return nil
	}

	metrics := db.Metrics()
	data := &PebbleDebugData{
		BlockCacheSize:   metrics.BlockCache.Size,
		BlockCacheHits:   metrics.BlockCache.Hits,
		BlockCacheMisses: metrics.BlockCache.Misses,
		MemTableSize:     metrics.MemTable.Size,
		MemTableCount:    metrics.MemTable.Count,
		ZombieTableCount: metrics.Table.ZombieCount,
		ZombieTableSize:  metrics.Table.ZombieSize,
		WALSize:          metrics.WAL.Size,
		WALCount:         int64(metrics.WAL.Files),
		CompactionCount:  metrics.Compact.Count,
		CompactionDebt:   metrics.Compact.EstimatedDebt,
		LevelCount:       len(metrics.Levels),
	}

	for i, level := range metrics.Levels {
		data.KeyCount += level.NumFiles
		data.TotalSize += level.Size
		data.Levels = append(data.Levels, PebbleLevelData{
			Level:    i,
			NumFiles: level.NumFiles,
			Size:     level.Size,
			Score:    level.Score,
		})
	}

	return data
}

func buildS3DebugData(engine *bs3.S3Engine) *S3DebugData {
	stats := engine.GetStats()
	data := &S3DebugData{
		Bucket:     stats.Bucket,
		PathPrefix: stats.PathPrefix,
		GetCount:   stats.GetCount,
		PutCount:   stats.PutCount,
		StatCount:  stats.StatCount,
		GetBytes:   stats.GetBytes,
		PutBytes:   stats.PutBytes,
	}

	return data
}

func buildBlockDbStoredData() *BlockDbStoredData {
	dbStats, err := db.GetBlockDbStats()
	if err != nil {
		return nil
	}
	return &BlockDbStoredData{
		BeaconBlockCount: dbStats.BeaconBlockCount,
		BeaconBlockSize:  dbStats.BeaconBlockSize,
		ExecDataCount:    dbStats.ExecDataCount,
		ExecDataSize:     dbStats.ExecDataSize,
		TotalCount:       dbStats.BeaconBlockCount + dbStats.ExecDataCount,
		TotalSize:        dbStats.BeaconBlockSize + dbStats.ExecDataSize,
	}
}

func buildExecIndexerDebugData() *ExecIndexerDebugData {
	ti := services.GlobalBeaconService.GetTxIndexer()
	if ti == nil {
		return &ExecIndexerDebugData{Enabled: false}
	}

	indexerStats := ti.GetDebugStats()
	if indexerStats.Mode == "Disabled" {
		return &ExecIndexerDebugData{Enabled: false}
	}

	data := &ExecIndexerDebugData{
		Enabled: true,
		Indexer: indexerStats,
	}

	latestSlot, err := db.GetLatestElBlockSlot()
	if err == nil {
		data.LatestSlot = latestSlot
	}

	ranges := []struct {
		label string
		count int64
	}{
		{"Last 8 blocks", 8},
		{"Last 16 blocks", 16},
		{"Last 32 blocks", 32},
		{"Last 64 blocks", 64},
	}

	for _, r := range ranges {
		stats, err := db.GetRecentElBlockStats(r.count)
		if err != nil {
			continue
		}
		data.RecentBlockStats = append(data.RecentBlockStats, &RecentBlockStatsEntry{
			Label:        r.label,
			BlockCount:   stats.BlockCount,
			Transactions: stats.Transactions,
			Events:       stats.Events,
			Transfers:    stats.Transfers,
			ExecData:     stats.ExecData,
			ExecDataSize: stats.ExecDataSize,
		})
	}

	return data
}

func buildDatabaseDebugData() *DatabaseDebugData {
	data := &DatabaseDebugData{}

	switch db.DbEngine {
	case dbtypes.DBEngineSqlite:
		data.Engine = "SQLite"
	case dbtypes.DBEnginePgsql:
		data.Engine = "PostgreSQL"
	default:
		data.Engine = "Unknown"
	}

	totalSize, err := db.GetDatabaseSize()
	if err == nil {
		data.TotalSize = totalSize
	}

	tables, err := db.GetTableStats()
	if err == nil {
		data.Tables = tables
	}

	return data
}

func buildSystemDebugData() *SystemDebugData {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	uptime := time.Since(startTime)

	var lastPause time.Duration
	if mem.NumGC > 0 {
		lastPause = time.Duration(mem.PauseNs[(mem.NumGC+255)%256])
	}

	return &SystemDebugData{
		GoVersion:      runtime.Version(),
		NumGoroutine:   runtime.NumGoroutine(),
		NumCPU:         runtime.NumCPU(),
		Uptime:         formatDuration(uptime),
		MemAlloc:       mem.Alloc,
		MemTotalAlloc:  mem.TotalAlloc,
		MemSys:         mem.Sys,
		MemHeapAlloc:   mem.HeapAlloc,
		MemHeapSys:     mem.HeapSys,
		MemHeapIdle:    mem.HeapIdle,
		MemHeapInuse:   mem.HeapInuse,
		MemHeapObjects: mem.HeapObjects,
		MemStackInuse:  mem.StackInuse,
		MemStackSys:    mem.StackSys,
		GCCycles:       mem.NumGC,
		GCPauseTotal:   time.Duration(mem.PauseTotalNs),
		GCLastPause:    lastPause,
		GCNextTarget:   mem.NextGC,
	}
}

func formatDuration(d time.Duration) string {
	totalSeconds := int(d.Seconds())
	days := totalSeconds / 86400
	hours := (totalSeconds % 86400) / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", days, hours, minutes, seconds)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
