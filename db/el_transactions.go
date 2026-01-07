package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElTransactions(txs []*dbtypes.ElTransaction, dbTx *sqlx.Tx) error {
	if len(txs) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_transactions ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_transactions ",
		}),
		"(block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, tx_index, eff_gas_price)",
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

		args[argIdx+0] = tx.BlockUid
		args[argIdx+1] = tx.TxHash
		args[argIdx+2] = tx.FromID
		args[argIdx+3] = tx.ToID
		args[argIdx+4] = tx.Nonce
		args[argIdx+5] = tx.Reverted
		args[argIdx+6] = tx.Amount
		args[argIdx+7] = tx.AmountRaw
		args[argIdx+8] = tx.MethodID
		args[argIdx+9] = tx.GasLimit
		args[argIdx+10] = tx.GasUsed
		args[argIdx+11] = tx.GasPrice
		args[argIdx+12] = tx.TipPrice
		args[argIdx+13] = tx.BlobCount
		args[argIdx+14] = tx.BlockNumber
		args[argIdx+15] = tx.TxType
		args[argIdx+16] = tx.TxIndex
		args[argIdx+17] = tx.EffGasPrice
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_uid, tx_hash) DO UPDATE SET from_id = excluded.from_id, to_id = excluded.to_id, nonce = excluded.nonce, reverted = excluded.reverted, amount = excluded.amount, amount_raw = excluded.amount_raw, method_id = excluded.method_id, gas_limit = excluded.gas_limit, gas_used = excluded.gas_used, gas_price = excluded.gas_price, tip_price = excluded.tip_price, blob_count = excluded.blob_count, block_number = excluded.block_number, tx_type = excluded.tx_type, tx_index = excluded.tx_index, eff_gas_price = excluded.eff_gas_price",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElTransaction(blockUid uint64, txHash []byte) (*dbtypes.ElTransaction, error) {
	tx := &dbtypes.ElTransaction{}
	err := ReaderDb.Get(tx, "SELECT block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, tx_index, eff_gas_price FROM el_transactions WHERE block_uid = $1 AND tx_hash = $2", blockUid, txHash)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func GetElTransactionsByHash(txHash []byte) ([]*dbtypes.ElTransaction, error) {
	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, "SELECT block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, tx_index, eff_gas_price FROM el_transactions WHERE tx_hash = $1 ORDER BY block_uid DESC", txHash)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElTransactionsByBlockUid(blockUid uint64) ([]*dbtypes.ElTransaction, error) {
	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, "SELECT block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, tx_index, eff_gas_price FROM el_transactions WHERE block_uid = $1", blockUid)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElTransactionsByAccountID(accountID uint64, isFrom bool, offset uint64, limit uint32) ([]*dbtypes.ElTransaction, uint64, error) {
	var sql strings.Builder
	args := []any{accountID}

	column := "to_id"
	if isFrom {
		column = "from_id"
	}

	// Use window function for count (PostgreSQL 9.5+) - avoids double scan
	fmt.Fprintf(&sql, `
		SELECT
			block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw,
			method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number,
			tx_type, tx_index, eff_gas_price,
			COUNT(*) OVER() AS total_count
		FROM el_transactions
		WHERE %s = $1
		ORDER BY block_uid DESC, tx_hash DESC
		LIMIT $2`, column)
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
	err := ReaderDb.Select(&rows, sql.String(), args...)
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

func GetElTransactionsFiltered(offset uint64, limit uint32, filter *dbtypes.ElTransactionFilter) ([]*dbtypes.ElTransaction, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, tx_index, eff_gas_price
		FROM el_transactions
	`)

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
		count(*) AS block_uid,
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
		0 AS tx_index,
		0 AS eff_gas_price
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, tx_hash DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el transactions: %v", err)
		return nil, 0, err
	}

	if len(txs) == 0 {
		return []*dbtypes.ElTransaction{}, 0, nil
	}

	count := txs[0].BlockUid
	return txs[1:], count, nil
}

// MaxAccountTransactionCount is the maximum count returned for address transaction queries.
// If the actual count exceeds this, the query returns this limit and sets the "more" flag.
const MaxAccountTransactionCount = 100000

// GetElTransactionsByAccountIDCombined fetches transactions where the account is either
// sender (from_id) or receiver (to_id). Results are sorted by block_uid DESC, tx_index DESC.
// Returns transactions, total count (capped at MaxAccountTransactionCount), whether count is capped, and error.
func GetElTransactionsByAccountIDCombined(accountID uint64, offset uint64, limit uint32) ([]*dbtypes.ElTransaction, uint64, bool, error) {
	// Use UNION ALL instead of OR for better index usage.
	// The second query excludes rows where from_id = accountID to avoid duplicates
	// (handles self-transfers where from_id = to_id = accountID).
	var sql strings.Builder
	args := []any{accountID, accountID, accountID}

	fmt.Fprint(&sql, `
		SELECT block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw,
			method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number,
			tx_type, tx_index, eff_gas_price
		FROM (
			SELECT block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw,
				method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number,
				tx_type, tx_index, eff_gas_price
			FROM el_transactions WHERE from_id = $1
			UNION ALL
			SELECT block_uid, tx_hash, from_id, to_id, nonce, reverted, amount, amount_raw,
				method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number,
				tx_type, tx_index, eff_gas_price
			FROM el_transactions WHERE to_id = $2 AND from_id != $3
		) combined
		ORDER BY block_uid DESC, tx_index DESC
		LIMIT $4`)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el transactions by account id (combined): %v", err)
		return nil, 0, false, err
	}

	// Get count with a separate query, capped at MaxAccountTransactionCount
	// Using UNION ALL with LIMIT for efficient counting
	countSQL := `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM el_transactions WHERE from_id = $1
			UNION ALL
			SELECT 1 FROM el_transactions WHERE to_id = $2 AND from_id != $3
			LIMIT $4
		) limited`
	var totalCount uint64
	err = ReaderDb.Get(&totalCount, countSQL, accountID, accountID, accountID, MaxAccountTransactionCount)
	if err != nil {
		logger.Errorf("Error while counting el transactions by account id (combined): %v", err)
		return nil, 0, false, err
	}

	moreAvailable := totalCount >= MaxAccountTransactionCount

	return txs, totalCount, moreAvailable, nil
}

func DeleteElTransaction(blockUid uint64, txHash []byte, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_transactions WHERE block_uid = $1 AND tx_hash = $2", blockUid, txHash)
	return err
}

func DeleteElTransactionsByBlockUid(blockUid uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_transactions WHERE block_uid = $1", blockUid)
	return err
}
