package db

import (
	"context"
	"time"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// CleanupStats holds statistics from cleanup operations.
type CleanupStats struct {
	ZeroBalancesDeleted   int64
	TransactionsDeleted   int64
	InternalTxsDeleted    int64
	TokenTransfersDeleted int64
	BlocksDeleted         int64
	RevertReasonsDeleted  int64
}

// DeleteElDataBeforeBlockUid deletes the relational EL data (transactions,
// internal txs, transfers, blocks) with block_uid less than the specified
// threshold. This is the short (relational) retention boundary.
//
// It intentionally does NOT touch the el_txhash index: that index lives with the
// longer blockdb (details) retention and is pruned separately by the indexer's
// cleanupBlockdbRetention, so /tx/{hash} can resolve pruned transactions from
// blockdb after their relational rows are gone.
//
// Returns statistics about deleted rows.
// Uses batched deletes to avoid long locks - deletes in chunks and commits
// between batches. Uses default batch size of 50000 rows per batch.
func DeleteElDataBeforeBlockUid(ctx context.Context, blockUidThreshold uint64, _ *sqlx.Tx) (*CleanupStats, error) {
	batchSize := int64(50000)
	stats := &CleanupStats{}

	// tx_uid encodes block_uid in upper bits: tx_uid = block_uid << 16 | tx_index.
	// All tx_uids for blocks below the threshold satisfy tx_uid < txUidThreshold.
	txUidThreshold := blockUidThreshold << 16

	// Delete transactions in batches (uses block_uid column)
	deleted, err := batchDeleteBefore(ctx, "el_transactions", "block_uid", blockUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.TransactionsDeleted = deleted

	// Delete internal transactions in batches (uses tx_uid column)
	deleted, err = batchDeleteBefore(ctx, "el_transactions_internal", "tx_uid", txUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.InternalTxsDeleted = deleted

	// Delete token transfers in batches (uses tx_uid column)
	deleted, err = batchDeleteBefore(ctx, "el_token_transfers", "tx_uid", txUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.TokenTransfersDeleted = deleted

	// Delete blocks in batches (uses block_uid column)
	deleted, err = batchDeleteBefore(ctx, "el_blocks", "block_uid", blockUidThreshold, batchSize)
	if err != nil {
		return stats, err
	}
	stats.BlocksDeleted = deleted

	// Reclaim revert reasons no longer referenced by any surviving transaction
	// (their most recent reference fell below the pruned tx_uid window). A single
	// indexed range delete — no join back to el_transactions.
	deleted, err = PruneElRevertReasonsBefore(ctx, txUidThreshold)
	if err != nil {
		return stats, err
	}
	stats.RevertReasonsDeleted = deleted

	return stats, nil
}

// batchDeleteBefore deletes rows from a table where the given column < threshold,
// in batches of batchSize to avoid long locks.
func batchDeleteBefore(ctx context.Context, table string, column string, threshold uint64, batchSize int64) (int64, error) {
	var totalDeleted int64

	for {
		var deleted int64
		err := RunDBTransaction(func(tx *sqlx.Tx) error {
			var query string
			if DbEngine == dbtypes.DBEnginePgsql {
				query = `DELETE FROM ` + table + `
					WHERE ` + column + ` < $1
					AND ctid IN (
						SELECT ctid FROM ` + table + `
						WHERE ` + column + ` < $1
						LIMIT $2
					)`
			} else {
				query = `DELETE FROM ` + table + `
					WHERE ` + column + ` < $1
					AND rowid IN (
						SELECT rowid FROM ` + table + `
						WHERE ` + column + ` < $1
						LIMIT $2
					)`
			}
			result, err := tx.ExecContext(ctx, query, threshold, batchSize)
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
func GetOldestElBlockUid(ctx context.Context) (uint64, error) {
	var blockUid uint64
	err := ReaderDb.GetContext(ctx, &blockUid, "SELECT COALESCE(MIN(block_uid), 0) FROM el_blocks")
	if err != nil {
		return 0, err
	}
	return blockUid, nil
}

// GetNewestElBlockUid returns the newest (maximum) block_uid in the el_blocks table.
// Returns 0 if no blocks exist.
func GetNewestElBlockUid(ctx context.Context) (uint64, error) {
	var blockUid uint64
	err := ReaderDb.GetContext(ctx, &blockUid, "SELECT COALESCE(MAX(block_uid), 0) FROM el_blocks")
	if err != nil {
		return 0, err
	}
	return blockUid, nil
}
