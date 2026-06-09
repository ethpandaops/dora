package db

import (
	"fmt"

	"github.com/ethpandaops/dora/dbtypes"
)

// TableStats holds size information for a database table.
type TableStats struct {
	Name     string
	RowCount int64
	Size     int64 // bytes, -1 if unavailable
}

// getTableNames returns the list of user table names from the database.
func getTableNames() ([]string, error) {
	var tables []string
	var err error

	switch DbEngine {
	case dbtypes.DBEnginePgsql:
		err = ReaderDb.Select(&tables,
			"SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename")
	case dbtypes.DBEngineSqlite:
		err = ReaderDb.Select(&tables,
			"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	default:
		return nil, fmt.Errorf("unknown database engine")
	}

	if err != nil {
		return nil, err
	}

	return tables, nil
}

// GetTableStats returns estimated row counts and sizes for all known tables.
// For PostgreSQL it uses catalog statistics (pg_class.reltuples) to avoid
// expensive full table scans. For SQLite it batches the dbstat query.
func GetTableStats() ([]*TableStats, error) {
	switch DbEngine {
	case dbtypes.DBEnginePgsql:
		return getTableStatsPgsql()
	case dbtypes.DBEngineSqlite:
		return getTableStatsSqlite()
	default:
		return nil, fmt.Errorf("unknown database engine")
	}
}

// getTableStatsPgsql fetches table stats from PostgreSQL catalog in a single
// query. reltuples is an estimate maintained by ANALYZE/autovacuum.
func getTableStatsPgsql() ([]*TableStats, error) {
	type pgTableRow struct {
		Name     string `db:"name"`
		RowCount int64  `db:"row_count"`
		Size     int64  `db:"size"`
	}

	var rows []pgTableRow
	err := ReaderDb.Select(&rows, `
		SELECT
			c.relname AS name,
			GREATEST(c.reltuples, 0)::bigint AS row_count,
			pg_total_relation_size(c.oid) AS size
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r'
		ORDER BY c.relname
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query pg_class: %w", err)
	}

	results := make([]*TableStats, 0, len(rows))
	for i := range rows {
		results = append(results, &TableStats{
			Name:     rows[i].Name,
			RowCount: rows[i].RowCount,
			Size:     rows[i].Size,
		})
	}

	return results, nil
}

// getTableStatsSqlite fetches table stats for SQLite, batching the dbstat
// size lookup into a single query and using individual COUNT(*) per table.
func getTableStatsSqlite() ([]*TableStats, error) {
	tables, err := getTableNames()
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}

	// Batch-fetch sizes for all tables in a single dbstat query.
	type sizeRow struct {
		Name string `db:"name"`
		Size int64  `db:"size"`
	}

	var sizeRows []sizeRow
	sizeMap := make(map[string]int64, len(tables))

	err = ReaderDb.Select(&sizeRows,
		"SELECT name, COALESCE(SUM(pgsize), 0) AS size FROM dbstat GROUP BY name")
	if err == nil {
		for i := range sizeRows {
			sizeMap[sizeRows[i].Name] = sizeRows[i].Size
		}
	}

	results := make([]*TableStats, 0, len(tables))
	for _, table := range tables {
		stats := &TableStats{
			Name: table,
			Size: -1,
		}

		var count int64
		err := ReaderDb.Get(&count, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)) //nolint:gosec // table names come from sqlite_master
		if err != nil {
			continue
		}
		stats.RowCount = count

		if size, ok := sizeMap[table]; ok {
			stats.Size = size
		}

		results = append(results, stats)
	}

	return results, nil
}

// BlockDbStats holds aggregate stats about data stored in the block database.
type BlockDbStats struct {
	BeaconBlockCount int64
	BeaconBlockSize  int64
	ExecDataCount    int64
	ExecDataSize     int64
}

// GetBlockDbStats returns counts and sizes for beacon blocks and execution
// data tracked in the database, avoiding expensive S3/Pebble listing.
func GetBlockDbStats() (*BlockDbStats, error) {
	stats := &BlockDbStats{}

	// Beacon blocks: count all slots that have a block_size > 0
	err := ReaderDb.Get(&stats.BeaconBlockCount,
		"SELECT COUNT(*) FROM slots WHERE block_size > 0")
	if err != nil {
		stats.BeaconBlockCount = 0
	}

	err = ReaderDb.Get(&stats.BeaconBlockSize,
		"SELECT COALESCE(SUM(block_size), 0) FROM slots WHERE block_size > 0")
	if err != nil {
		stats.BeaconBlockSize = 0
	}

	// Exec data: count el_blocks with data_size > 0
	err = ReaderDb.Get(&stats.ExecDataCount,
		"SELECT COUNT(*) FROM el_blocks WHERE data_size > 0")
	if err != nil {
		stats.ExecDataCount = 0
	}

	err = ReaderDb.Get(&stats.ExecDataSize,
		"SELECT COALESCE(SUM(data_size), 0) FROM el_blocks WHERE data_size > 0")
	if err != nil {
		stats.ExecDataSize = 0
	}

	return stats, nil
}

// RecentBlockStats holds aggregate stats for a range of recent EL blocks.
type RecentBlockStats struct {
	BlockCount   int64
	Transactions int64
	Events       int64
	Transfers    int64
	ExecData     int64
	ExecDataSize int64
}

// GetRecentElBlockStats returns aggregate stats for the most recent N EL blocks.
func GetRecentElBlockStats(lastN int64) (*RecentBlockStats, error) {
	stats := &RecentBlockStats{}

	err := ReaderDb.Get(stats, `
		SELECT
			COUNT(*) as block_count,
			COALESCE(SUM(transactions), 0) as transactions,
			COALESCE(SUM(events), 0) as events,
			COALESCE(SUM(transfers), 0) as transfers,
			COALESCE(SUM(CASE WHEN data_size > 0 THEN 1 ELSE 0 END), 0) as exec_data,
			COALESCE(SUM(data_size), 0) as exec_data_size
		FROM (
			SELECT transactions, events, transfers, data_size
			FROM el_blocks
			ORDER BY block_uid DESC
			LIMIT $1
		) sub
	`, lastN)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// GetLatestElBlockSlot returns the slot of the most recently indexed EL block.
func GetLatestElBlockSlot() (uint64, error) {
	var blockUid uint64
	err := ReaderDb.Get(&blockUid, "SELECT COALESCE(MAX(block_uid), 0) FROM el_blocks")
	if err != nil {
		return 0, err
	}
	return blockUid >> 16, nil
}

// GetDatabaseSize returns the total database size in bytes.
func GetDatabaseSize() (int64, error) {
	switch DbEngine {
	case dbtypes.DBEnginePgsql:
		var size int64
		err := ReaderDb.Get(&size, "SELECT pg_database_size(current_database())")
		if err != nil {
			return 0, err
		}
		return size, nil
	case dbtypes.DBEngineSqlite:
		var pageCount int64
		var pageSize int64
		err := ReaderDb.Get(&pageCount, "PRAGMA page_count")
		if err != nil {
			return 0, err
		}
		err = ReaderDb.Get(&pageSize, "PRAGMA page_size")
		if err != nil {
			return 0, err
		}
		return pageCount * pageSize, nil
	}
	return 0, fmt.Errorf("unknown database engine")
}
