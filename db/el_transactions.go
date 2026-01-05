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
		"(block_uid, tx_hash, tx_from, tx_to, reverted, amount, data, gas_used, block_number)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 9

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
		args[argIdx+2] = tx.From
		args[argIdx+3] = tx.To
		args[argIdx+4] = tx.Reverted
		args[argIdx+5] = tx.Amount
		args[argIdx+6] = tx.Data
		args[argIdx+7] = tx.GasUsed
		args[argIdx+8] = tx.BlockNumber
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_uid, tx_hash) DO UPDATE SET tx_from = excluded.tx_from, tx_to = excluded.tx_to, reverted = excluded.reverted, amount = excluded.amount, data = excluded.data, gas_used = excluded.gas_used, block_number = excluded.block_number",
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
	err := ReaderDb.Get(tx, "SELECT block_uid, tx_hash, tx_from, tx_to, reverted, amount, data, gas_used, block_number FROM el_transactions WHERE block_uid = $1 AND tx_hash = $2", blockUid, txHash)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func GetElTransactionsByHash(txHash []byte) ([]*dbtypes.ElTransaction, error) {
	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, "SELECT block_uid, tx_hash, tx_from, tx_to, reverted, amount, data, gas_used, block_number FROM el_transactions WHERE tx_hash = $1 ORDER BY block_uid DESC", txHash)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElTransactionsByBlockUid(blockUid uint64) ([]*dbtypes.ElTransaction, error) {
	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, "SELECT block_uid, tx_hash, tx_from, tx_to, reverted, amount, data, gas_used, block_number FROM el_transactions WHERE block_uid = $1", blockUid)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElTransactionsByAddress(address []byte, isFrom bool, offset uint64, limit uint32) ([]*dbtypes.ElTransaction, uint64, error) {
	var sql strings.Builder
	args := []any{}

	column := "tx_to"
	if isFrom {
		column = "tx_from"
	}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, tx_from, tx_to, reverted, amount, data, gas_used, block_number
		FROM el_transactions
		WHERE `, column, ` = $1
	)`)
	args = append(args, address)

	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		null AS tx_hash,
		null AS tx_from,
		null AS tx_to,
		false AS reverted,
		null AS amount,
		null AS data,
		0 AS gas_used,
		0 AS block_number
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, tx_hash DESC
	LIMIT $%v`, len(args)+1)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el transactions by address: %v", err)
		return nil, 0, err
	}

	if len(txs) == 0 {
		return []*dbtypes.ElTransaction{}, 0, nil
	}

	count := txs[0].BlockUid
	return txs[1:], count, nil
}

func GetElTransactionsFiltered(offset uint64, limit uint32, filter *dbtypes.ElTransactionFilter) ([]*dbtypes.ElTransaction, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, tx_from, tx_to, reverted, amount, data, gas_used, block_number
		FROM el_transactions
	`)

	filterOp := "WHERE"
	if len(filter.From) > 0 {
		args = append(args, filter.From)
		fmt.Fprintf(&sql, " %v tx_from = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.To) > 0 {
		args = append(args, filter.To)
		fmt.Fprintf(&sql, " %v tx_to = $%v", filterOp, len(args))
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
		null AS tx_from,
		null AS tx_to,
		false AS reverted,
		null AS amount,
		null AS data,
		0 AS gas_used,
		0 AS block_number
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

func DeleteElTransaction(blockUid uint64, txHash []byte, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_transactions WHERE block_uid = $1 AND tx_hash = $2", blockUid, txHash)
	return err
}

func DeleteElTransactionsByBlockUid(blockUid uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_transactions WHERE block_uid = $1", blockUid)
	return err
}
