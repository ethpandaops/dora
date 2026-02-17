package db

import (
	"time"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// CleanupStats holds statistics from cleanup operations.
type CleanupStats struct {
	ZeroBalancesDeleted   int64
	TransactionsDeleted   int64
	InternalTxsDeleted    int64
	EventIndicesDeleted   int64
	TokenTransfersDeleted int64
	WithdrawalsDeleted    int64
	BlocksDeleted         int64
}

// DeleteElDataBeforeBlockUid deletes all EL data (transactions, internal txs,
// event index, transfers, withdrawals, blocks) with block_uid less than the
// specified threshold.
// Returns statistics about deleted rows.
// Uses batched deletes to avoid long locks - deletes in chunks and commits
// between batches. Uses default batch size of 50000 rows per batch.
func DeleteElDataBeforeBlockUid(blockUidThreshold uint64, _ *sqlx.Tx) (*CleanupStats, error) {
	batchSize := int64(50000)
	stats := &CleanupStats{}

	// Delete transactions in batches
	deleted, err := batchDeleteBeforeBlockUid("el_transactions", blockUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.TransactionsDeleted = deleted

	// Delete internal transactions in batches
	deleted, err = batchDeleteBeforeBlockUid("el_transactions_internal", blockUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.InternalTxsDeleted = deleted

	// Delete event index entries in batches
	deleted, err = batchDeleteBeforeBlockUid("el_event_index", blockUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.EventIndicesDeleted = deleted

	// Delete token transfers in batches
	deleted, err = batchDeleteBeforeBlockUid("el_token_transfers", blockUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.TokenTransfersDeleted = deleted

	// Delete withdrawals in batches
	deleted, err = batchDeleteBeforeBlockUid("el_withdrawals", blockUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.WithdrawalsDeleted = deleted

	// Delete blocks in batches
	deleted, err = batchDeleteBeforeBlockUid("el_blocks", blockUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.BlocksDeleted = deleted

	return stats, nil
}

// batchDeleteBeforeBlockUid deletes rows from a table where block_uid < threshold,
// in batches of batchSize to avoid long locks.
func batchDeleteBeforeBlockUid(table string, blockUidThreshold uint64, batchSize int64) (int64, error) {
	var totalDeleted int64

	for {
		var deleted int64
		err := RunDBTransaction(func(tx *sqlx.Tx) error {
			var query string
			if DbEngine == dbtypes.DBEnginePgsql {
				query = `DELETE FROM ` + table + `
					WHERE block_uid < $1
					AND ctid IN (
						SELECT ctid FROM ` + table + `
						WHERE block_uid < $1
						LIMIT $2
					)`
			} else {
				query = `DELETE FROM ` + table + `
					WHERE block_uid < $1
					AND rowid IN (
						SELECT rowid FROM ` + table + `
						WHERE block_uid < $1
						LIMIT $2
					)`
			}
			result, err := tx.Exec(query, blockUidThreshold, batchSize)
			if err != nil {
				return err
			}
			deleted, err = result.RowsAffected()
			return err
		})
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += deleted

		if deleted < batchSize {
			break
		}

		// Small delay to allow other operations
		time.Sleep(100 * time.Millisecond)
	}

	return totalDeleted, nil
}

// GetOldestElBlockUid returns the oldest (minimum) block_uid in the el_blocks table.
// Returns 0 if no blocks exist.
func GetOldestElBlockUid() (uint64, error) {
	var blockUid uint64
	err := ReaderDb.Get(&blockUid, "SELECT COALESCE(MIN(block_uid), 0) FROM el_blocks")
	if err != nil {
		return 0, err
	}
	return blockUid, nil
}

// GetNewestElBlockUid returns the newest (maximum) block_uid in the el_blocks table.
// Returns 0 if no blocks exist.
func GetNewestElBlockUid() (uint64, error) {
	var blockUid uint64
	err := ReaderDb.Get(&blockUid, "SELECT COALESCE(MAX(block_uid), 0) FROM el_blocks")
	if err != nil {
		return 0, err
	}
	return blockUid, nil
}
