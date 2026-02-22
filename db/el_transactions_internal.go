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

// GetElTransactionsInternalByAccount returns internal transactions
// involving the given account (as sender or receiver), ordered by block_uid DESC.
func GetElTransactionsInternalByAccount(
	ctx context.Context,
	accountID uint64,
	offset uint64,
	limit uint32,
) ([]*dbtypes.ElTransactionInternal, uint64, error) {
	var sql strings.Builder
	args := []any{accountID}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, tx_callidx, call_type, from_id, to_id, value, value_raw
		FROM el_transactions_internal
		WHERE from_id = $1 OR to_id = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		null AS tx_hash,
		0 AS tx_callidx,
		0 AS call_type,
		0 AS from_id,
		0 AS to_id,
		0 AS value,
		null AS value_raw
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, tx_hash ASC, tx_callidx ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	entries := []*dbtypes.ElTransactionInternal{}
	err := ReaderDb.SelectContext(ctx, &entries, sql.String(), args...)
	if err != nil {
		return nil, 0, err
	}

	if len(entries) == 0 {
		return []*dbtypes.ElTransactionInternal{}, 0, nil
	}

	count := entries[0].BlockUid
	return entries[1:], count, nil
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
