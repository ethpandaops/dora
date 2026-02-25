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
		"(block_uid, tx_hash, tx_callidx, call_type, from_id, to_id, value, value_raw)",
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

		args[argIdx+0] = entry.BlockUid
		args[argIdx+1] = entry.TxHash
		args[argIdx+2] = entry.TxCallIdx
		args[argIdx+3] = entry.CallType
		args[argIdx+4] = entry.FromID
		args[argIdx+5] = entry.ToID
		args[argIdx+6] = entry.Value
		args[argIdx+7] = entry.ValueRaw
		argIdx += fieldCount
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (block_uid, tx_hash, tx_callidx) DO UPDATE SET" +
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

// MaxAccountInternalTxCount is the maximum count returned for address internal transaction queries.
// If the actual count exceeds this, the query returns this limit and sets the "more" flag.
const MaxAccountInternalTxCount = 100000

// GetElTransactionsInternalByAccount returns internal transactions
// involving the given account (as sender or receiver), ordered by block_uid DESC.
// Uses UNION ALL instead of OR to enable index scans on the composite indexes
// (from_id, block_uid DESC) and (to_id, block_uid DESC).
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
		SELECT block_uid, tx_hash, tx_callidx, call_type, from_id, to_id, value, value_raw
		FROM (
			(SELECT block_uid, tx_hash, tx_callidx, call_type, from_id, to_id, value, value_raw
			FROM el_transactions_internal WHERE from_id = $1
			ORDER BY block_uid DESC NULLS LAST
			LIMIT $4)
			UNION ALL
			(SELECT block_uid, tx_hash, tx_callidx, call_type, from_id, to_id, value, value_raw
			FROM el_transactions_internal WHERE to_id = $2 AND from_id != $3
			ORDER BY block_uid DESC NULLS LAST
			LIMIT $4)
		) combined
		ORDER BY block_uid DESC, tx_hash ASC, tx_callidx ASC
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
	// Counts from_id and to_id separately (without from_id != exclusion) for index-only scan
	// performance. May slightly overcount self-referencing internal calls, which is acceptable
	// since the count is already an approximation (capped).
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

// GetElTransactionsInternalCountByTxHash returns the number of internal
// transactions for a given transaction hash. Uses an index-only scan.
func GetElTransactionsInternalCountByTxHash(ctx context.Context, txHash []byte) (uint64, error) {
	var count uint64
	err := ReaderDb.GetContext(ctx, &count,
		"SELECT COUNT(*) FROM el_transactions_internal WHERE tx_hash = $1",
		txHash,
	)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GetElTransactionsInternalByTxHash returns all internal transactions
// for a given transaction hash.
func GetElTransactionsInternalByTxHash(ctx context.Context, txHash []byte) ([]*dbtypes.ElTransactionInternal, error) {
	entries := []*dbtypes.ElTransactionInternal{}
	err := ReaderDb.SelectContext(ctx, &entries,
		"SELECT block_uid, tx_hash, tx_callidx, call_type, from_id, to_id, value, value_raw"+
			" FROM el_transactions_internal WHERE tx_hash = $1 ORDER BY tx_callidx ASC",
		txHash,
	)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
