package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// InsertElTransactionsInternal inserts internal transaction index entries in batch.
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
		"(tx_uid, tx_callidx, call_type, from_id, to_id, value, value_raw)",
		" VALUES ",
	)

	argIdx := 0
	fieldCount := 7
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
		args[argIdx+1] = entry.TxCallIdx
		args[argIdx+2] = entry.CallType
		args[argIdx+3] = entry.FromID
		args[argIdx+4] = entry.ToID
		args[argIdx+5] = entry.Value
		args[argIdx+6] = entry.ValueRaw
		argIdx += fieldCount
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (tx_uid, tx_callidx) DO UPDATE SET" +
			" call_type = excluded.call_type," +
			" from_id = excluded.from_id," +
			" to_id = excluded.to_id," +
			" value = excluded.value," +
			" value_raw = excluded.value_raw",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	return err
}

// HasElTransactionsInternalByAccount returns true if the given account has any
// internal transactions (as sender or receiver). Uses EXISTS for O(1) lookup.
func HasElTransactionsInternalByAccount(ctx context.Context, accountID uint64) (bool, error) {
	var exists bool
	err := ReaderDb.GetContext(ctx, &exists,
		"SELECT EXISTS(SELECT 1 FROM el_transactions_internal WHERE from_id = $1 LIMIT 1) OR EXISTS(SELECT 1 FROM el_transactions_internal WHERE to_id = $1 LIMIT 1)",
		accountID,
	)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// MaxAccountInternalTxCount is the maximum count returned for address internal transaction queries.
// If the actual count exceeds this, the query returns this limit and sets the "more" flag.
const MaxAccountInternalTxCount = 100000

// GetElTransactionsInternalByAccount returns internal transactions
// involving the given account (as sender or receiver), ordered by tx_uid DESC.
// Uses UNION ALL instead of OR to enable index scans on the composite indexes
// (from_id, tx_uid DESC) and (to_id, tx_uid DESC).
// NULLS LAST is required to match the index definition and enable index scans.
func GetElTransactionsInternalByAccount(
	ctx context.Context,
	accountID uint64,
	offset uint64,
	limit uint32,
) ([]*dbtypes.ElTransactionInternal, uint64, error) {
	// Data query: UNION ALL with pushed LIMIT into each branch
	var sql strings.Builder
	innerLimit := offset + uint64(limit)
	args := []any{accountID, accountID, accountID, innerLimit}

	fmt.Fprint(&sql, `
		SELECT tx_uid, tx_callidx, call_type, from_id, to_id, value, value_raw
		FROM (
			(SELECT tx_uid, tx_callidx, call_type, from_id, to_id, value, value_raw
			FROM el_transactions_internal WHERE from_id = $1
			ORDER BY tx_uid DESC NULLS LAST
			LIMIT $4)
			UNION ALL
			(SELECT tx_uid, tx_callidx, call_type, from_id, to_id, value, value_raw
			FROM el_transactions_internal WHERE to_id = $2 AND from_id != $3
			ORDER BY tx_uid DESC NULLS LAST
			LIMIT $4)
		) combined
		ORDER BY tx_uid DESC, tx_callidx ASC
		LIMIT $5`)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	entries := []*dbtypes.ElTransactionInternal{}
	err := ReaderDb.SelectContext(ctx, &entries, sql.String(), args...)
	if err != nil {
		return nil, 0, err
	}

	// Count query: separate index-only scans per direction, capped at MaxAccountInternalTxCount.
	countSQL := `
		SELECT
			(SELECT COUNT(*) FROM (SELECT 1 FROM el_transactions_internal WHERE from_id = $1 LIMIT $2) a) +
			(SELECT COUNT(*) FROM (SELECT 1 FROM el_transactions_internal WHERE to_id = $1 LIMIT $2) b)`
	var totalCount uint64
	err = ReaderDb.GetContext(ctx, &totalCount, countSQL, accountID, MaxAccountInternalTxCount)
	if err != nil {
		return nil, 0, err
	}

	if totalCount > MaxAccountInternalTxCount {
		totalCount = MaxAccountInternalTxCount
	}

	return entries, totalCount, nil
}

// GetElTransactionsInternalCountByTxUid returns the number of internal
// transactions for a given transaction UID.
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

// GetElTransactionsInternalByTxUid returns all internal transactions
// for a given transaction UID.
func GetElTransactionsInternalByTxUid(ctx context.Context, txUid uint64) ([]*dbtypes.ElTransactionInternal, error) {
	entries := []*dbtypes.ElTransactionInternal{}
	err := ReaderDb.SelectContext(ctx, &entries,
		"SELECT tx_uid, tx_callidx, call_type, from_id, to_id, value, value_raw"+
			" FROM el_transactions_internal WHERE tx_uid = $1 ORDER BY tx_callidx ASC",
		txUid,
	)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
