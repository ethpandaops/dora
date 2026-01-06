package db

import (
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
func DeleteElDataBeforeBlockUid(blockUidThreshold uint64, dbTx *sqlx.Tx) (*CleanupStats, error) {
	stats := &CleanupStats{}

	// Delete transactions
	result, err := dbTx.Exec("DELETE FROM el_transactions WHERE block_uid < $1", blockUidThreshold)
	if err != nil {
		return stats, err
	}
	stats.TransactionsDeleted, _ = result.RowsAffected()

	// Delete events
	result, err = dbTx.Exec("DELETE FROM el_tx_events WHERE block_uid < $1", blockUidThreshold)
	if err != nil {
		return stats, err
	}
	stats.EventsDeleted, _ = result.RowsAffected()

	// Delete token transfers
	result, err = dbTx.Exec("DELETE FROM el_token_transfers WHERE block_uid < $1", blockUidThreshold)
	if err != nil {
		return stats, err
	}
	stats.TokenTransfersDeleted, _ = result.RowsAffected()

	// Delete withdrawals
	result, err = dbTx.Exec("DELETE FROM el_withdrawals WHERE block_uid < $1", blockUidThreshold)
	if err != nil {
		return stats, err
	}
	stats.WithdrawalsDeleted, _ = result.RowsAffected()

	// Delete blocks
	result, err = dbTx.Exec("DELETE FROM el_blocks WHERE block_uid < $1", blockUidThreshold)
	if err != nil {
		return stats, err
	}
	stats.BlocksDeleted, _ = result.RowsAffected()

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
