package txindexer

import (
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/db"
)

// Object-type identifiers for per-object pruning stats.
const (
	PruneObjZeroBalances   = "zero_balances"
	PruneObjTransactions   = "transactions"
	PruneObjInternalTxs    = "internal_txs"
	PruneObjTokenTransfers = "token_transfers"
	PruneObjBlocks         = "blocks"
	PruneObjExecData       = "exec_data"
	PruneObjTxHashIndex    = "tx_hash_index"
)

// maxPruningRuns is how many recent cleanup cycles are retained for display.
const maxPruningRuns = 3

// ElPruningStatus is the persisted EL pruning status, also exposed for UI
// display. It is stored in explorer_state under cleanupStateKey.
type ElPruningStatus struct {
	LastCleanup int64 `json:"last_cleanup"` // unix seconds of the last cleanup cycle

	// RelationalPrunedEpoch is the epoch below which relational rows
	// (transactions/transfers/internal/blocks) have been pruned. Data is
	// available from this epoch up to IndexedEpoch.
	RelationalEnabled     bool   `json:"rel_enabled"`
	RelationalPrunedEpoch uint64 `json:"rel_pruned_epoch"`

	// DetailsPrunedEpoch is the epoch below which blockdb exec data and the
	// tx-hash index have been pruned (drives by-hash availability).
	DetailsEnabled     bool   `json:"det_enabled"`
	DetailsPrunedEpoch uint64 `json:"det_pruned_epoch"`

	// Runs holds the most recent cleanup cycles (newest first, capped).
	Runs []ElPruningRun `json:"runs"`
}

// ElPruningRun captures the result of one cleanup cycle.
type ElPruningRun struct {
	Time       int64                 `json:"time"`        // unix seconds
	DurationMs int64                 `json:"duration_ms"` // total cycle duration
	Objects    []ElPruningObjectStat `json:"objects"`     // per-object-type results
}

// ElPruningObjectStat is the per-object-type result of a cleanup cycle.
type ElPruningObjectStat struct {
	Type      string `json:"type"`
	Deleted   int64  `json:"deleted"`
	SizeBytes int64  `json:"size_bytes"` // freed bytes where known, else 0
}

// GetPruningStatus returns a copy of the current pruning status for display.
func (t *TxIndexer) GetPruningStatus() ElPruningStatus {
	t.pruningMutex.RLock()
	defer t.pruningMutex.RUnlock()

	status := t.pruningStatus
	status.Runs = append([]ElPruningRun(nil), t.pruningStatus.Runs...)
	return status
}

// loadPruningStatus restores the pruning status (and lastCleanup) from the DB.
func (t *TxIndexer) loadPruningStatus() {
	status := ElPruningStatus{}
	if _, err := db.GetExplorerState(t.ctx, cleanupStateKey, &status); err != nil {
		t.logger.WithError(err).Debug("no existing cleanup state found")
		t.lastCleanup = time.Time{}
		return
	}

	if status.LastCleanup > 0 {
		t.lastCleanup = time.Unix(status.LastCleanup, 0)
	}

	t.pruningMutex.Lock()
	t.pruningStatus = status
	t.pruningMutex.Unlock()

	t.logger.WithField("lastCleanup", t.lastCleanup).Info("restored cleanup state from database")
}

// savePruningStatus persists the current pruning status.
func (t *TxIndexer) savePruningStatus() {
	t.pruningMutex.RLock()
	status := t.pruningStatus
	t.pruningMutex.RUnlock()

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.SetExplorerState(t.ctx, tx, cleanupStateKey, &status)
	})
	if err != nil {
		t.logger.WithError(err).Error("failed to save cleanup state")
	}
}

// retentionCutoffEpoch returns the epoch corresponding to the retention cutoff
// (0 if retention is disabled or chain state is unavailable).
func (t *TxIndexer) retentionCutoffEpoch(retention time.Duration) uint64 {
	if retention == 0 {
		return 0
	}
	chainState := t.indexerCtx.ChainState
	if chainState == nil {
		return 0
	}
	cutoffSlot := chainState.TimeToSlot(time.Now().Add(-retention))
	if cutoffSlot == 0 {
		return 0
	}
	return uint64(chainState.EpochOfSlot(cutoffSlot))
}

// recordPruningRun updates the in-memory pruning status with the result of a
// cleanup cycle. A run with no deletions only refreshes the bounds. The
// head/indexed epoch is intentionally not stored here — it moves independently
// of the tail cleanup and is read live where needed.
func (t *TxIndexer) recordPruningRun(run ElPruningRun, relEnabled bool, relEpoch uint64, detEnabled bool, detEpoch uint64) {
	t.pruningMutex.Lock()
	defer t.pruningMutex.Unlock()

	t.pruningStatus.LastCleanup = t.lastCleanup.Unix()
	t.pruningStatus.RelationalEnabled = relEnabled
	if relEpoch > 0 {
		t.pruningStatus.RelationalPrunedEpoch = relEpoch
	}
	t.pruningStatus.DetailsEnabled = detEnabled
	if detEpoch > 0 {
		t.pruningStatus.DetailsPrunedEpoch = detEpoch
	}

	if len(run.Objects) > 0 {
		runs := append([]ElPruningRun{run}, t.pruningStatus.Runs...)
		if len(runs) > maxPruningRuns {
			runs = runs[:maxPruningRuns]
		}
		t.pruningStatus.Runs = runs
	}
}
