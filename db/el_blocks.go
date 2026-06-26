package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

const elBlockColumns = "block_uid, status, events, transactions, transfers, data_status, data_size, fee_amount, fee_amount_raw, fee_account_id"

func InsertElBlock(ctx context.Context, dbTx *sqlx.Tx, block *dbtypes.ElBlock) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_blocks ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_blocks ",
		}),
		"(", elBlockColumns, ")",
		" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
	)

	args := []any{
		block.BlockUid,
		block.Status,
		block.Events,
		block.Transactions,
		block.Transfers,
		block.DataStatus,
		block.DataSize,
		block.FeeAmount,
		block.FeeAmountRaw,
		block.FeeAccountID,
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (block_uid) DO UPDATE SET" +
			" status = excluded.status," +
			" events = excluded.events," +
			" transactions = excluded.transactions," +
			" transfers = excluded.transfers," +
			" data_status = excluded.data_status," +
			" data_size = excluded.data_size," +
			" fee_amount = excluded.fee_amount," +
			" fee_amount_raw = excluded.fee_amount_raw," +
			" fee_account_id = excluded.fee_account_id",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElBlock(ctx context.Context, blockUid uint64) (*dbtypes.ElBlock, error) {
	block := &dbtypes.ElBlock{}
	err := ReaderDb.GetContext(ctx, block,
		"SELECT "+elBlockColumns+" FROM el_blocks WHERE block_uid = $1",
		blockUid,
	)
	if err != nil {
		return nil, err
	}
	return block, nil
}

func GetElBlocksByUids(ctx context.Context, blockUids []uint64) ([]*dbtypes.ElBlock, error) {
	if len(blockUids) == 0 {
		return []*dbtypes.ElBlock{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(blockUids))
	fmt.Fprintf(&sql, "SELECT %s FROM el_blocks WHERE block_uid IN (", elBlockColumns)
	for i, uid := range blockUids {
		args[i] = uid
	}
	appendDollarPlaceholders(&sql, 1, len(blockUids), ", ")
	fmt.Fprint(&sql, ")")

	blocks := []*dbtypes.ElBlock{}
	err := ReaderDb.SelectContext(ctx, &blocks, sql.String(), args...)
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

// ResetElBlockDataStatusBefore resets data_status and data_size for all blocks
// with block_uid below the given threshold (used for time-based pruning).
func ResetElBlockDataStatusBefore(ctx context.Context, dbTx *sqlx.Tx, blockUidThreshold uint64) (int64, error) {
	result, err := dbTx.ExecContext(ctx,
		"UPDATE el_blocks SET data_status = 0, data_size = 0 WHERE block_uid < $1 AND data_size > 0",
		blockUidThreshold,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetTotalElBlockDataSize returns the total data_size across all el_blocks.
func GetTotalElBlockDataSize(ctx context.Context) (int64, error) {
	var total int64
	err := ReaderDb.GetContext(ctx, &total, "SELECT COALESCE(SUM(data_size), 0) FROM el_blocks WHERE data_size > 0")
	if err != nil {
		return 0, err
	}
	return total, nil
}

// GetElBlockDataSizeBefore returns the summed blockdb exec-data size of blocks
// with block_uid below the threshold (used to report freed bytes on prune).
func GetElBlockDataSizeBefore(ctx context.Context, blockUidThreshold uint64) (int64, error) {
	var total int64
	err := ReaderDb.GetContext(ctx, &total,
		"SELECT COALESCE(SUM(data_size), 0) FROM el_blocks WHERE block_uid < $1 AND data_size > 0",
		blockUidThreshold)
	if err != nil {
		return 0, err
	}
	return total, nil
}

// GetOldestElBlocksWithData returns the oldest blocks that have blockdb data,
// ordered by block_uid ascending.
func GetOldestElBlocksWithData(ctx context.Context, limit uint32) ([]*dbtypes.ElBlock, error) {
	blocks := []*dbtypes.ElBlock{}
	err := ReaderDb.SelectContext(ctx, &blocks,
		"SELECT "+elBlockColumns+" FROM el_blocks WHERE data_size > 0 ORDER BY block_uid ASC LIMIT $1",
		limit,
	)
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

// HasBlockFeesByAccountID returns true if the given account has any block fee rewards.
func HasBlockFeesByAccountID(ctx context.Context, accountID uint64) (bool, error) {
	var exists bool
	err := ReaderDb.GetContext(ctx, &exists,
		"SELECT EXISTS(SELECT 1 FROM el_blocks WHERE fee_account_id = $1 AND fee_amount > 0 LIMIT 1)",
		accountID,
	)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// GetBlockFeesByAccountKeyset returns the account's fee-earning blocks keyset-
// paginated by block_uid DESC.
func GetBlockFeesByAccountKeyset(ctx context.Context, accountID uint64, beforeBlockUid uint64, limit uint32) ([]*dbtypes.ElBlock, bool, error) {
	var sql strings.Builder
	args := []any{accountID}
	fmt.Fprint(&sql, `SELECT `+elBlockColumns+` FROM el_blocks WHERE fee_account_id = $1 AND fee_amount > 0`)
	if beforeBlockUid > 0 {
		args = append(args, beforeBlockUid)
		fmt.Fprintf(&sql, " AND block_uid < $%d", len(args))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sql, " ORDER BY block_uid DESC LIMIT $%d", len(args))

	blocks := []*dbtypes.ElBlock{}
	if err := ReaderDb.SelectContext(ctx, &blocks, sql.String(), args...); err != nil {
		logger.Errorf("Error while fetching block fees by account (keyset): %v", err)
		return nil, false, err
	}
	hasMore := false
	if uint32(len(blocks)) > limit {
		hasMore = true
		blocks = blocks[:limit]
	}
	return blocks, hasMore, nil
}

// GetBlockFeesByAccountPrevAnchor probes blocks newer than afterBlockUid to
// drive the First/< controls (see GetElTransactionsByAccountPrevAnchor).
func GetBlockFeesByAccountPrevAnchor(ctx context.Context, accountID uint64, afterBlockUid uint64, limit uint32) (uint64, bool, bool, error) {
	var sql strings.Builder
	args := []any{accountID}
	fmt.Fprint(&sql, `SELECT block_uid FROM el_blocks WHERE fee_account_id = $1 AND fee_amount > 0`)
	if afterBlockUid > 0 {
		args = append(args, afterBlockUid)
		fmt.Fprintf(&sql, " AND block_uid > $%d", len(args))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sql, " ORDER BY block_uid ASC LIMIT $%d", len(args))

	uids := []uint64{}
	if err := ReaderDb.SelectContext(ctx, &uids, sql.String(), args...); err != nil {
		return 0, false, false, err
	}
	hasPrev := len(uids) > 0
	if uint32(len(uids)) > limit {
		return uids[limit], hasPrev, false, nil
	}
	return 0, hasPrev, true, nil
}

// UpdateElBlockDataStatus updates the data_status and data_size for a block
// after writing execution data to blockdb.
func UpdateElBlockDataStatus(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64, dataStatus uint16, dataSize int64) error {
	_, err := dbTx.ExecContext(ctx,
		"UPDATE el_blocks SET data_status = $1, data_size = $2 WHERE block_uid = $3",
		dataStatus, dataSize, blockUid,
	)
	return err
}
