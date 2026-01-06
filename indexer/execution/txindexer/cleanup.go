package txindexer

import (
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/utils"
)

const (
	// defaultCleanupInterval is used when no cleanup interval is configured.
	defaultCleanupInterval = 1 * time.Hour
)

// runCleanupLoop runs the cleanup routine at configured intervals.
func (t *TxIndexer) runCleanupLoop() {
	defer utils.HandleSubroutinePanic("TxIndexer.runCleanupLoop", t.runCleanupLoop)

	// Get cleanup interval from config
	cleanupInterval := utils.Config.ExecutionIndexer.CleanupInterval
	if cleanupInterval == 0 {
		cleanupInterval = defaultCleanupInterval
	}

	// Initial delay before first cleanup run
	initialDelay := time.After(1 * time.Minute)
	<-initialDelay

	// Run initial cleanup
	t.runCleanup()

	// Start periodic cleanup
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			t.runCleanup()
		}
	}
}

// runCleanup performs the actual cleanup operations.
func (t *TxIndexer) runCleanup() {
	t.logger.Debug("starting cleanup routine")

	// 1. Delete zero balances
	zeroBalancesDeleted := t.cleanupZeroBalances()

	// 2. Delete old EL data based on retention period
	retentionStats := t.cleanupRetentionData()

	// Log summary
	if zeroBalancesDeleted > 0 || retentionStats != nil {
		fields := logrus.Fields{
			"zeroBalances": zeroBalancesDeleted,
		}
		if retentionStats != nil {
			fields["transactions"] = retentionStats.TransactionsDeleted
			fields["events"] = retentionStats.EventsDeleted
			fields["transfers"] = retentionStats.TokenTransfersDeleted
			fields["blocks"] = retentionStats.BlocksDeleted
		}
		t.logger.WithFields(fields).Info("cleanup completed")
	} else {
		t.logger.Debug("cleanup completed, no items deleted")
	}
}

// cleanupZeroBalances deletes all balance entries with zero balance.
func (t *TxIndexer) cleanupZeroBalances() int64 {
	var deleted int64

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		var err error
		deleted, err = db.DeleteElZeroBalances(tx)
		return err
	})

	if err != nil {
		t.logger.WithError(err).Warn("failed to delete zero balances")
		return 0
	}

	if deleted > 0 {
		t.logger.WithField("count", deleted).Debug("deleted zero balances")
	}

	return deleted
}

// cleanupRetentionData deletes EL data older than the retention period.
func (t *TxIndexer) cleanupRetentionData() *db.CleanupStats {
	retention := utils.Config.ExecutionIndexer.Retention
	if retention == 0 {
		// No retention configured, skip cleanup
		return nil
	}

	chainState := t.indexerCtx.ChainState
	if chainState == nil {
		t.logger.Warn("chain state not available for retention cleanup")
		return nil
	}

	// Calculate cutoff time
	cutoffTime := time.Now().Add(-retention)

	// Convert cutoff time to slot
	cutoffSlot := chainState.TimeToSlot(cutoffTime)
	if cutoffSlot == 0 {
		t.logger.Debug("cutoff slot is 0, skipping retention cleanup")
		return nil
	}

	// Calculate block_uid threshold: (slot << 16)
	// All block_uids with slot < cutoffSlot should be deleted
	blockUidThreshold := uint64(cutoffSlot) << 16

	t.logger.WithFields(logrus.Fields{
		"retention":         retention,
		"cutoffTime":        cutoffTime,
		"cutoffSlot":        cutoffSlot,
		"blockUidThreshold": blockUidThreshold,
	}).Debug("calculating retention cleanup threshold")

	// Check if we have any data to delete
	oldestBlockUid, err := db.GetOldestElBlockUid()
	if err != nil {
		t.logger.WithError(err).Warn("failed to get oldest block uid")
		return nil
	}

	if oldestBlockUid == 0 || oldestBlockUid >= blockUidThreshold {
		// No data older than threshold
		return nil
	}

	oldestSlot := phase0.Slot(oldestBlockUid >> 16)
	t.logger.WithFields(logrus.Fields{
		"oldestBlockUid": oldestBlockUid,
		"oldestSlot":     oldestSlot,
		"cutoffSlot":     cutoffSlot,
	}).Info("deleting EL data older than retention period")

	var stats *db.CleanupStats

	err = db.RunDBTransaction(func(tx *sqlx.Tx) error {
		var err error
		stats, err = db.DeleteElDataBeforeBlockUid(blockUidThreshold, tx)
		return err
	})

	if err != nil {
		t.logger.WithError(err).Error("failed to delete old EL data")
		return nil
	}

	return stats
}
