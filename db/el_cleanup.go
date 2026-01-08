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
	EventsDeleted         int64
	TokenTransfersDeleted int64
	WithdrawalsDeleted    int64
	BlocksDeleted         int64
}

// DeleteElDataBeforeBlockUid deletes all EL data (transactions, events, transfers, blocks)
// with block_uid less than the specified threshold.
// Returns statistics about deleted rows.
// Uses batched deletes to avoid long locks - deletes in chunks and commits between batches.
// Note: dbTx parameter is ignored as batching requires managing its own transactions.
// Uses default batch size of 50000 rows per batch.
func DeleteElDataBeforeBlockUid(blockUidThreshold uint64, dbTx *sqlx.Tx) (*CleanupStats, error) {
	batchSize := int64(50000) // Default batch size
	stats := &CleanupStats{}

	// Delete transactions in batches
	for {
		var deleted int64
		err := RunDBTransaction(func(tx *sqlx.Tx) error {
			var query string
			if DbEngine == dbtypes.DBEnginePgsql {
				// PostgreSQL: use ctid for efficient row selection
				query = `
					DELETE FROM el_transactions 
					WHERE block_uid < $1 
					AND ctid IN (
						SELECT ctid FROM el_transactions 
						WHERE block_uid < $1 
						LIMIT $2
					)`
			} else {
				// SQLite: use ROWID for efficient row selection
				query = `
					DELETE FROM el_transactions 
					WHERE block_uid < $1 
					AND rowid IN (
						SELECT rowid FROM el_transactions 
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
			return stats, err
		}
		stats.TransactionsDeleted += deleted

		if deleted < batchSize {
			break // No more rows to delete
		}

		// Small delay to allow other operations
		time.Sleep(100 * time.Millisecond)
	}

	// Delete events in batches
	for {
		var deleted int64
		err := RunDBTransaction(func(tx *sqlx.Tx) error {
			var query string
			if DbEngine == dbtypes.DBEnginePgsql {
				query = `
					DELETE FROM el_tx_events 
					WHERE block_uid < $1 
					AND ctid IN (
						SELECT ctid FROM el_tx_events 
						WHERE block_uid < $1 
						LIMIT $2
					)`
			} else {
				query = `
					DELETE FROM el_tx_events 
					WHERE block_uid < $1 
					AND rowid IN (
						SELECT rowid FROM el_tx_events 
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
			return stats, err
		}
		stats.EventsDeleted += deleted

		if deleted < batchSize {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Delete token transfers in batches
	for {
		var deleted int64
		err := RunDBTransaction(func(tx *sqlx.Tx) error {
			var query string
			if DbEngine == dbtypes.DBEnginePgsql {
				query = `
					DELETE FROM el_token_transfers 
					WHERE block_uid < $1 
					AND ctid IN (
						SELECT ctid FROM el_token_transfers 
						WHERE block_uid < $1 
						LIMIT $2
					)`
			} else {
				query = `
					DELETE FROM el_token_transfers 
					WHERE block_uid < $1 
					AND rowid IN (
						SELECT rowid FROM el_token_transfers 
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
			return stats, err
		}
		stats.TokenTransfersDeleted += deleted

		if deleted < batchSize {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Delete withdrawals in batches
	for {
		var deleted int64
		err := RunDBTransaction(func(tx *sqlx.Tx) error {
			var query string
			if DbEngine == dbtypes.DBEnginePgsql {
				query = `
					DELETE FROM el_withdrawals 
					WHERE block_uid < $1 
					AND ctid IN (
						SELECT ctid FROM el_withdrawals 
						WHERE block_uid < $1 
						LIMIT $2
					)`
			} else {
				query = `
					DELETE FROM el_withdrawals 
					WHERE block_uid < $1 
					AND rowid IN (
						SELECT rowid FROM el_withdrawals 
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
			return stats, err
		}
		stats.WithdrawalsDeleted += deleted

		if deleted < batchSize {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Delete blocks in batches
	for {
		var deleted int64
		err := RunDBTransaction(func(tx *sqlx.Tx) error {
			var query string
			if DbEngine == dbtypes.DBEnginePgsql {
				query = `
					DELETE FROM el_blocks 
					WHERE block_uid < $1 
					AND ctid IN (
						SELECT ctid FROM el_blocks 
						WHERE block_uid < $1 
						LIMIT $2
					)`
			} else {
				query = `
					DELETE FROM el_blocks 
					WHERE block_uid < $1 
					AND rowid IN (
						SELECT rowid FROM el_blocks 
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
			return stats, err
		}
		stats.BlocksDeleted += deleted

		if deleted < batchSize {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	return stats, nil
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
