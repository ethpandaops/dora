package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// elTransactionColumns is the column list for el_transactions queries.
const elTransactionColumns = "tx_uid, block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, eff_gas_price"

func InsertElTransactions(ctx context.Context, dbTx *sqlx.Tx, txs []*dbtypes.ElTransaction) error {
	if len(txs) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_transactions ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_transactions ",
		}),
		"(tx_uid, block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, eff_gas_price)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 18

	args := make([]any, len(txs)*fieldCount)
	for i, tx := range txs {
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

		args[argIdx+0] = tx.TxUid
		args[argIdx+1] = tx.BlockUid
		args[argIdx+2] = tx.TxHash
		args[argIdx+3] = tx.FromID
		args[argIdx+4] = tx.ToID
		args[argIdx+5] = tx.Nonce
		args[argIdx+6] = tx.Reverted
		args[argIdx+7] = tx.Amount
		args[argIdx+8] = tx.AmountRaw
		args[argIdx+9] = tx.MethodID
		args[argIdx+10] = tx.GasLimit
		args[argIdx+11] = tx.GasUsed
		args[argIdx+12] = tx.GasPrice
		args[argIdx+13] = tx.TipPrice
		args[argIdx+14] = tx.BlobCount
		args[argIdx+15] = tx.BlockNumber
		args[argIdx+16] = tx.TxType
		args[argIdx+17] = tx.EffGasPrice
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_uid, tx_hash) DO UPDATE SET tx_uid = excluded.tx_uid, from_id = excluded.from_id, to_id = excluded.to_id, nonce = excluded.nonce, reverted = excluded.reverted, amount = excluded.amount, amount_raw = excluded.amount_raw, method_id = excluded.method_id, gas_limit = excluded.gas_limit, gas_used = excluded.gas_used, gas_price = excluded.gas_price, tip_price = excluded.tip_price, blob_count = excluded.blob_count, block_number = excluded.block_number, tx_type = excluded.tx_type, eff_gas_price = excluded.eff_gas_price",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElTransaction(ctx context.Context, blockUid uint64, txHash []byte) (*dbtypes.ElTransaction, error) {
	tx := &dbtypes.ElTransaction{}
	err := ReaderDb.GetContext(ctx, tx, "SELECT "+elTransactionColumns+" FROM el_transactions WHERE block_uid = $1 AND tx_hash = $2", blockUid, txHash)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func GetElTransactionsByHash(ctx context.Context, txHash []byte) ([]*dbtypes.ElTransaction, error) {
	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.SelectContext(ctx, &txs, "SELECT "+elTransactionColumns+" FROM el_transactions WHERE tx_hash = $1 ORDER BY block_uid DESC", txHash)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

// GetElTransactionsByHashes returns all transaction records matching the given
// tx hashes. May return multiple records per hash if the tx appears in multiple
// blocks (reorgs).
func GetElTransactionsByHashes(ctx context.Context, txHashes [][]byte) ([]*dbtypes.ElTransaction, error) {
	if len(txHashes) == 0 {
		return []*dbtypes.ElTransaction{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(txHashes))

	fmt.Fprint(&sql, "SELECT "+elTransactionColumns+" FROM el_transactions WHERE tx_hash IN (")
	for i, h := range txHashes {
		args[i] = h
	}
	appendDollarPlaceholders(&sql, 1, len(txHashes), ", ")
	fmt.Fprint(&sql, ")")

	txs := []*dbtypes.ElTransaction{}
	if err := ReaderDb.SelectContext(ctx, &txs, sql.String(), args...); err != nil {
		return nil, err
	}
	return txs, nil
}

// GetElTransactionsByTxUids returns all transaction records matching the given
// tx_uids. Used by handlers to resolve tx_hash and other fields from dependent
// table results that only store tx_uid.
func GetElTransactionsByTxUids(ctx context.Context, txUids []uint64) ([]*dbtypes.ElTransaction, error) {
	if len(txUids) == 0 {
		return []*dbtypes.ElTransaction{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(txUids))

	fmt.Fprint(&sql, "SELECT "+elTransactionColumns+" FROM el_transactions WHERE tx_uid IN (")
	for i, uid := range txUids {
		args[i] = uid
	}
	appendDollarPlaceholders(&sql, 1, len(txUids), ", ")
	fmt.Fprint(&sql, ")")

	txs := []*dbtypes.ElTransaction{}
	if err := ReaderDb.SelectContext(ctx, &txs, sql.String(), args...); err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElTransactionsByBlockUid(ctx context.Context, blockUid uint64) ([]*dbtypes.ElTransaction, error) {
	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.SelectContext(ctx, &txs, "SELECT "+elTransactionColumns+" FROM el_transactions WHERE block_uid = $1", blockUid)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElTransactionsByAccountID(ctx context.Context, accountID uint64, isFrom bool, offset uint64, limit uint32) ([]*dbtypes.ElTransaction, uint64, error) {
	var sql strings.Builder
	args := []any{accountID}

	column := "to_id"
	if isFrom {
		column = "from_id"
	}

	// Use window function for count (PostgreSQL 9.5+) - avoids double scan
	// NULLS LAST matches the composite index definition to enable index scans.
	fmt.Fprintf(&sql, `
		SELECT
			%s,
			COUNT(*) OVER() AS total_count
		FROM el_transactions
		WHERE %s = $1
		ORDER BY block_uid DESC NULLS LAST, tx_hash DESC
		LIMIT $2`, elTransactionColumns, column)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	type resultRow struct {
		dbtypes.ElTransaction
		TotalCount uint64 `db:"total_count"`
	}

	rows := []resultRow{}
	err := ReaderDb.SelectContext(ctx, &rows, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el transactions by account id: %v", err)
		return nil, 0, err
	}

	if len(rows) == 0 {
		return []*dbtypes.ElTransaction{}, 0, nil
	}

	txs := make([]*dbtypes.ElTransaction, len(rows))
	var totalCount uint64
	for i, row := range rows {
		txs[i] = &row.ElTransaction
		if i == 0 {
			totalCount = row.TotalCount
		}
	}

	return txs, totalCount, nil
}

func GetElTransactionsFiltered(ctx context.Context, offset uint64, limit uint32, filter *dbtypes.ElTransactionFilter) ([]*dbtypes.ElTransaction, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprintf(&sql, `
	WITH cte AS (
		SELECT %s
		FROM el_transactions
	`, elTransactionColumns)

	filterOp := "WHERE"
	if filter.FromID > 0 {
		args = append(args, filter.FromID)
		fmt.Fprintf(&sql, " %v from_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.ToID > 0 {
		args = append(args, filter.ToID)
		fmt.Fprintf(&sql, " %v to_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.Reverted != nil {
		args = append(args, *filter.Reverted)
		fmt.Fprintf(&sql, " %v reverted = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinGasUsed != nil {
		args = append(args, *filter.MinGasUsed)
		fmt.Fprintf(&sql, " %v gas_used >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxGasUsed != nil {
		args = append(args, *filter.MaxGasUsed)
		fmt.Fprintf(&sql, " %v gas_used <= $%v", filterOp, len(args))
		filterOp = "AND"
	}

	fmt.Fprint(&sql, ")")

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS tx_uid,
		0 AS block_uid,
		null AS tx_hash,
		0 AS from_id,
		0 AS to_id,
		0 AS nonce,
		false AS reverted,
		0 AS amount,
		null AS amount_raw,
		null AS method_id,
		0 AS gas_limit,
		0 AS gas_used,
		0 AS gas_price,
		0 AS tip_price,
		0 AS blob_count,
		0 AS block_number,
		0 AS tx_type,
		0 AS eff_gas_price
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC NULLS LAST, tx_hash DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.SelectContext(ctx, &txs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el transactions: %v", err)
		return nil, 0, err
	}

	if len(txs) == 0 {
		return []*dbtypes.ElTransaction{}, 0, nil
	}

	count := txs[0].TxUid
	return txs[1:], count, nil
}

// MaxAccountTransactionCount is the maximum count returned for address transaction queries.
// If the actual count exceeds this, the query returns this limit and sets the "more" flag.
const MaxAccountTransactionCount = 100000

// GetElTransactionsByAccountIDCombined fetches transactions where the account is either
// sender (from_id) or receiver (to_id). Results are sorted by tx_uid DESC.
// Returns transactions, total count (capped at MaxAccountTransactionCount), whether count is capped, and error.
func GetElTransactionsByAccountIDCombined(ctx context.Context, accountID uint64, offset uint64, limit uint32) ([]*dbtypes.ElTransaction, uint64, bool, error) {
	// Use UNION ALL instead of OR for better index usage.
	// The second query excludes rows where from_id = accountID to avoid duplicates
	// (handles self-transfers where from_id = to_id = accountID).
	// Push LIMIT into each UNION branch so PG can use composite indexes
	// (from_id, block_uid DESC) and (to_id, block_uid DESC) efficiently.
	// NULLS LAST is required to match the index definition and enable index scans.
	var sql strings.Builder
	innerLimit := offset + uint64(limit)
	args := []any{accountID, accountID, accountID, innerLimit}

	fmt.Fprintf(&sql, `
		SELECT %s
		FROM (
			SELECT * FROM (SELECT %s
			FROM el_transactions WHERE from_id = $1
			ORDER BY block_uid DESC NULLS LAST
			LIMIT $4) AS a
			UNION ALL
			SELECT * FROM (SELECT %s
			FROM el_transactions WHERE to_id = $2 AND from_id != $3
			ORDER BY block_uid DESC NULLS LAST
			LIMIT $4) AS b
		) combined
		ORDER BY tx_uid DESC
		LIMIT $5`, elTransactionColumns, elTransactionColumns, elTransactionColumns)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.SelectContext(ctx, &txs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el transactions by account id (combined): %v", err)
		return nil, 0, false, err
	}

	// Count using separate index-only scans per direction, capped at MaxAccountTransactionCount.
	// Counts from_id and to_id separately (without from_id != exclusion) for index-only scan
	// performance. May slightly overcount self-transfers, which is acceptable since the count
	// is already an approximation (capped).
	countSQL := `
		SELECT
			(SELECT COUNT(*) FROM (SELECT 1 FROM el_transactions WHERE from_id = $1 LIMIT $2) AS a) +
			(SELECT COUNT(*) FROM (SELECT 1 FROM el_transactions WHERE to_id = $1 LIMIT $2) AS b)`
	var totalCount uint64
	err = ReaderDb.GetContext(ctx, &totalCount, countSQL, accountID, MaxAccountTransactionCount)
	if err != nil {
		logger.Errorf("Error while counting el transactions by account id (combined): %v", err)
		return nil, 0, false, err
	}

	if totalCount > MaxAccountTransactionCount {
		totalCount = MaxAccountTransactionCount
	}

	moreAvailable := totalCount >= MaxAccountTransactionCount

	return txs, totalCount, moreAvailable, nil
}

func DeleteElTransaction(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64, txHash []byte) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM el_transactions WHERE block_uid = $1 AND tx_hash = $2", blockUid, txHash)
	return err
}

func DeleteElTransactionsByBlockUid(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM el_transactions WHERE block_uid = $1", blockUid)
	return err
}
