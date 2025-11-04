package db

import (
	"github.com/jmoiron/sqlx"
)

// CleanupElData removes execution layer data older than cutoff timestamp
// This is used when data retention is configured to limit DB size
func CleanupElData(cutoffTimestamp uint64) error {
	return RunDBTransaction(func(tx *sqlx.Tx) error {
		// Delete old transactions
		if err := DeleteElTransactionsOlderThan(cutoffTimestamp, tx); err != nil {
			return err
		}

		// Delete old internal txs
		if err := DeleteElInternalTxsOlderThan(cutoffTimestamp, tx); err != nil {
			return err
		}

		// Delete old events
		if err := DeleteElEventsOlderThan(cutoffTimestamp, tx); err != nil {
			return err
		}

		// Delete old token transfers
		if err := DeleteElTokenTransfersOlderThan(cutoffTimestamp, tx); err != nil {
			return err
		}

		// Note: We keep address metadata, token contracts, and current balances
		// We also keep block metadata (lightweight)

		return nil
	})
}
