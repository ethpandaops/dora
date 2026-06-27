package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// InsertElTransactionsInternal inserts per-account internal-tx aggregates in
// batch. Each entry is one (tx_uid, account_id) row.
func InsertElTransactionsInternal(ctx context.Context, dbTx *sqlx.Tx, entries []*dbtypes.ElTransactionInternal) error {
	if len(entries) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_transactions_internal ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_transactions_internal ",
		}),
		"(tx_uid, account_id, in_count, out_count, call_type_mask, value_in, value_out, gas_used)",
		" VALUES ",
	)

	argIdx := 0
	fieldCount := 8
	args := make([]any, len(entries)*fieldCount)

	for i, entry := range entries {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprint(&sql, "(")
		for f := 0; f < fieldCount; f++ {
			if f > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", argIdx+f+1)
		}
		fmt.Fprint(&sql, ")")

		args[argIdx+0] = entry.TxUid
		args[argIdx+1] = entry.AccountID
		args[argIdx+2] = entry.InCount
		args[argIdx+3] = entry.OutCount
		args[argIdx+4] = entry.CallTypeMask
		args[argIdx+5] = entry.ValueIn
		args[argIdx+6] = entry.ValueOut
		args[argIdx+7] = entry.GasUsed
		argIdx += fieldCount
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (tx_uid, account_id) DO UPDATE SET" +
			" in_count = excluded.in_count," +
			" out_count = excluded.out_count," +
			" call_type_mask = excluded.call_type_mask," +
			" value_in = excluded.value_in," +
			" value_out = excluded.value_out," +
			" gas_used = excluded.gas_used",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	return err
}

// HasElTransactionsInternalByAccount returns true if the given account has any
// internal-tx aggregate rows. EXISTS scan on the (account_id, tx_uid DESC) idx.
func HasElTransactionsInternalByAccount(ctx context.Context, accountID uint64) (bool, error) {
	var exists bool
	err := ReaderDb.GetContext(ctx, &exists,
		"SELECT EXISTS(SELECT 1 FROM el_transactions_internal WHERE account_id = $1 LIMIT 1)",
		accountID,
	)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// GetElTransactionsInternalByAccountKeyset returns the account's internal-call
// aggregates keyset-paginated by tx_uid DESC.
func GetElTransactionsInternalByAccountKeyset(ctx context.Context, accountID uint64, beforeTxUid uint64, limit uint32) ([]*dbtypes.ElTransactionInternal, bool, error) {
	var sql strings.Builder
	args := []any{accountID}
	fmt.Fprint(&sql, `SELECT tx_uid, account_id, in_count, out_count, call_type_mask, value_in, value_out, gas_used
		FROM el_transactions_internal WHERE account_id = $1`)
	if beforeTxUid > 0 {
		args = append(args, beforeTxUid)
		fmt.Fprintf(&sql, " AND tx_uid < $%d", len(args))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sql, " ORDER BY tx_uid DESC LIMIT $%d", len(args))

	entries := []*dbtypes.ElTransactionInternal{}
	if err := ReaderDb.SelectContext(ctx, &entries, sql.String(), args...); err != nil {
		return nil, false, err
	}
	hasMore := false
	if uint32(len(entries)) > limit {
		hasMore = true
		entries = entries[:limit]
	}
	return entries, hasMore, nil
}

// GetElTransactionsInternalByAccountPrevAnchor probes rows newer than afterTxUid
// to drive the First/< controls (see GetElTransactionsByAccountPrevAnchor).
func GetElTransactionsInternalByAccountPrevAnchor(ctx context.Context, accountID uint64, afterTxUid uint64, limit uint32) (uint64, bool, bool, error) {
	var sql strings.Builder
	args := []any{accountID}
	fmt.Fprint(&sql, `SELECT tx_uid FROM el_transactions_internal WHERE account_id = $1`)
	if afterTxUid > 0 {
		args = append(args, afterTxUid)
		fmt.Fprintf(&sql, " AND tx_uid > $%d", len(args))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sql, " ORDER BY tx_uid ASC LIMIT $%d", len(args))

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

// GetElTransactionsInternalCountByTxUid returns the number of distinct
// accounts touched by the transaction's internal calls.
func GetElTransactionsInternalCountByTxUid(ctx context.Context, txUid uint64) (uint64, error) {
	var count uint64
	err := ReaderDb.GetContext(ctx, &count,
		"SELECT COUNT(*) FROM el_transactions_internal WHERE tx_uid = $1",
		txUid,
	)
	if err != nil {
		return 0, err
	}
	return count, nil
}
