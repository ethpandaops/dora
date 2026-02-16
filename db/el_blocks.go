package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

const elBlockColumns = "block_uid, status, events, transactions, transfers, data_status, data_size"

func InsertElBlock(block *dbtypes.ElBlock, dbTx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_blocks ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_blocks ",
		}),
		"(", elBlockColumns, ")",
		" VALUES ($1, $2, $3, $4, $5, $6, $7)",
	)

	args := []any{
		block.BlockUid,
		block.Status,
		block.Events,
		block.Transactions,
		block.Transfers,
		block.DataStatus,
		block.DataSize,
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (block_uid) DO UPDATE SET" +
			" status = excluded.status," +
			" events = excluded.events," +
			" transactions = excluded.transactions," +
			" transfers = excluded.transfers," +
			" data_status = excluded.data_status," +
			" data_size = excluded.data_size",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElBlock(blockUid uint64) (*dbtypes.ElBlock, error) {
	block := &dbtypes.ElBlock{}
	err := ReaderDb.Get(block,
		"SELECT "+elBlockColumns+" FROM el_blocks WHERE block_uid = $1",
		blockUid,
	)
	if err != nil {
		return nil, err
	}
	return block, nil
}

func GetElBlocksByUids(blockUids []uint64) ([]*dbtypes.ElBlock, error) {
	if len(blockUids) == 0 {
		return []*dbtypes.ElBlock{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(blockUids))
	fmt.Fprintf(&sql, "SELECT %s FROM el_blocks WHERE block_uid IN (", elBlockColumns)
	for i, uid := range blockUids {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%v", i+1)
		args[i] = uid
	}
	fmt.Fprint(&sql, ")")

	blocks := []*dbtypes.ElBlock{}
	err := ReaderDb.Select(&blocks, sql.String(), args...)
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

// ResetElBlockDataStatus resets data_status and data_size for blocks
// that have been evicted from blockdb.
func ResetElBlockDataStatus(blockUids []uint64, dbTx *sqlx.Tx) error {
	if len(blockUids) == 0 {
		return nil
	}

	var sql strings.Builder
	args := make([]any, len(blockUids))
	fmt.Fprint(&sql, "UPDATE el_blocks SET data_status = 0, data_size = 0 WHERE block_uid IN (")
	for i, uid := range blockUids {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%v", i+1)
		args[i] = uid
	}
	fmt.Fprint(&sql, ")")

	_, err := dbTx.Exec(sql.String(), args...)
	return err
}

// ResetElBlockDataStatusBefore resets data_status and data_size for all blocks
// with block_uid below the given threshold (used for time-based pruning).
func ResetElBlockDataStatusBefore(blockUidThreshold uint64, dbTx *sqlx.Tx) (int64, error) {
	result, err := dbTx.Exec(
		"UPDATE el_blocks SET data_status = 0, data_size = 0 WHERE block_uid < $1 AND data_size > 0",
		blockUidThreshold,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetTotalElBlockDataSize returns the total data_size across all el_blocks.
func GetTotalElBlockDataSize() (int64, error) {
	var total int64
	err := ReaderDb.Get(&total, "SELECT COALESCE(SUM(data_size), 0) FROM el_blocks WHERE data_size > 0")
	if err != nil {
		return 0, err
	}
	return total, nil
}

// GetOldestElBlocksWithData returns the oldest blocks that have blockdb data,
// ordered by block_uid ascending.
func GetOldestElBlocksWithData(limit uint32) ([]*dbtypes.ElBlock, error) {
	blocks := []*dbtypes.ElBlock{}
	err := ReaderDb.Select(&blocks,
		"SELECT "+elBlockColumns+" FROM el_blocks WHERE data_size > 0 ORDER BY block_uid ASC LIMIT $1",
		limit,
	)
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

func DeleteElBlock(blockUid uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_blocks WHERE block_uid = $1", blockUid)
	return err
}
