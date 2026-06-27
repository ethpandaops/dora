package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/dbtypes"
)

// ResolveElRevertReason bumps an existing reason's last_tx_uid to the highest
// referencing tx_uid, or inserts it if new (id assigned >= 10), then returns its
// id. Runs in the caller's tx and is self-healing across reclaim.
//
// The bump is an UPDATE keyed by reason_hash, which does NOT allocate an id; only
// a genuinely new reason consumes one (via the insert below). So the id sequence
// advances per distinct reason, not per reference — keeping it far below the int
// limit even on busy chains. (An ON CONFLICT upsert, by contrast, burns a
// sequence value on every reference because the id default is evaluated before
// the conflict is detected, on both Postgres and SQLite.)
func ResolveElRevertReason(ctx context.Context, dbTx *sqlx.Tx, reason string, reasonHash []byte, lastTxUid uint64) (uint32, error) {
	bump := EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "UPDATE el_revert_reason SET last_tx_uid = GREATEST(last_tx_uid, $1) WHERE reason_hash = $2",
		dbtypes.DBEngineSqlite: "UPDATE el_revert_reason SET last_tx_uid = MAX(last_tx_uid, $1) WHERE reason_hash = $2",
	})
	res, err := dbTx.ExecContext(ctx, bump, lastTxUid, reasonHash)
	if err != nil {
		return 0, err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		// New reason: insert it. ON CONFLICT DO UPDATE covers a concurrent insert
		// of the same new reason by another block worker (it still bumps
		// last_tx_uid correctly); this is the only path that consumes an id.
		insert := EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_revert_reason (reason, reason_hash, last_tx_uid) VALUES ($1, $2, $3) ON CONFLICT (reason_hash) DO UPDATE SET last_tx_uid = GREATEST(el_revert_reason.last_tx_uid, excluded.last_tx_uid)",
			dbtypes.DBEngineSqlite: "INSERT INTO el_revert_reason (reason, reason_hash, last_tx_uid) VALUES ($1, $2, $3) ON CONFLICT (reason_hash) DO UPDATE SET last_tx_uid = MAX(el_revert_reason.last_tx_uid, excluded.last_tx_uid)",
		})
		if _, err := dbTx.ExecContext(ctx, insert, reason, reasonHash, lastTxUid); err != nil {
			return 0, err
		}
	}

	var id uint32
	if err := dbTx.QueryRowxContext(ctx, "SELECT id FROM el_revert_reason WHERE reason_hash = $1", reasonHash).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// GetElRevertReasonsByIDs batch-loads reason strings for the given ids (for
// display tooltips). Reserved ids (1-9) resolve through the same table.
func GetElRevertReasonsByIDs(ctx context.Context, ids []uint32) (map[uint32]string, error) {
	result := make(map[uint32]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}

	var sql strings.Builder
	args := make([]any, len(ids))
	fmt.Fprint(&sql, "SELECT id, reason FROM el_revert_reason WHERE id IN (")
	for i, id := range ids {
		args[i] = id
	}
	appendDollarPlaceholders(&sql, 1, len(ids), ", ")
	fmt.Fprint(&sql, ")")

	rows := []dbtypes.ElRevertReason{}
	if err := ReaderDb.SelectContext(ctx, &rows, sql.String(), args...); err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.ID] = r.Reason
	}
	return result, nil
}

// PruneElRevertReasonsBefore deletes dynamic reasons (id >= 10) whose most
// recent reference is below the threshold — i.e. no surviving transaction
// references them. Reserved sentinels (id < 10) are never removed. The table is
// tiny, so this is a single delete (no batching).
func PruneElRevertReasonsBefore(ctx context.Context, txUidThreshold uint64) (int64, error) {
	var deleted int64
	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		res, derr := tx.ExecContext(ctx,
			"DELETE FROM el_revert_reason WHERE last_tx_uid < $1 AND id >= $2",
			txUidThreshold, dbtypes.RevertIDDynamicMin)
		if derr != nil {
			return derr
		}
		deleted, derr = res.RowsAffected()
		return derr
	})
	return deleted, err
}
