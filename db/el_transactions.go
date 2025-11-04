package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// GetElTransaction retrieves a transaction by its hash
func GetElTransaction(hash []byte, forkIds []uint64) (*dbtypes.ElTransaction, error) {
	tx := &dbtypes.ElTransaction{}

	var sql strings.Builder
	args := []interface{}{hash}

	fmt.Fprint(&sql, `
		SELECT *
		FROM el_transactions
		WHERE hash = $1 AND orphaned = false
	`)

	if len(forkIds) > 0 {
		args = append(args, forkIds)
		fmt.Fprintf(&sql, " AND fork_id = ANY($%v)", len(args))
	}

	fmt.Fprint(&sql, " ORDER BY fork_id DESC LIMIT 1")

	err := ReaderDb.Get(tx, sql.String(), args...)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// GetElTransactionsByBlock retrieves all transactions in a block
func GetElTransactionsByBlock(blockHash []byte, forkIds []uint64) ([]*dbtypes.ElTransaction, error) {
	txs := []*dbtypes.ElTransaction{}

	var sql strings.Builder
	args := []interface{}{blockHash}

	fmt.Fprint(&sql, `
		SELECT *
		FROM el_transactions
		WHERE block_hash = $1 AND orphaned = false
	`)

	if len(forkIds) > 0 {
		args = append(args, forkIds)
		fmt.Fprintf(&sql, " AND fork_id = ANY($%v)", len(args))
	}

	fmt.Fprint(&sql, " ORDER BY transaction_index ASC")

	err := ReaderDb.Select(&txs, sql.String(), args...)
	return txs, err
}

// GetElTransactionsByAddress retrieves transactions involving an address
func GetElTransactionsByAddress(address []byte, offset uint, limit uint, forkIds []uint64) ([]*dbtypes.ElTransaction, uint64, error) {
	var sql strings.Builder
	args := []interface{}{address, address}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_transactions
			WHERE (from_address = $1 OR to_address = $2) AND orphaned = false
	`)

	if len(forkIds) > 0 {
		args = append(args, forkIds)
		fmt.Fprintf(&sql, " AND fork_id = ANY($%v)", len(args))
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY block_timestamp DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	// Count
	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	// Results
	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return txs, totalCount, nil
}

// GetElTransactionsFiltered retrieves transactions with optional filtering
func GetElTransactionsFiltered(offset uint, limit uint, forkIds []uint64, filter *dbtypes.ElTransactionFilter) ([]*dbtypes.ElTransaction, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_transactions
	`)

	filterOp := "WHERE"

	if len(forkIds) > 0 {
		args = append(args, forkIds)
		fmt.Fprintf(&sql, " %v fork_id = ANY($%v)", filterOp, len(args))
		filterOp = "AND"
	}

	if filter != nil {
		if filter.FromAddress != nil {
			args = append(args, filter.FromAddress)
			fmt.Fprintf(&sql, " %v from_address = $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.ToAddress != nil {
			args = append(args, filter.ToAddress)
			fmt.Fprintf(&sql, " %v to_address = $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.MethodId != nil {
			args = append(args, filter.MethodId)
			fmt.Fprintf(&sql, " %v method_id = $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.Status != nil {
			args = append(args, *filter.Status)
			fmt.Fprintf(&sql, " %v status = $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.WithOrphaned == 1 {
			fmt.Fprintf(&sql, " %v orphaned = true", filterOp)
			filterOp = "AND"
		} else if filter.WithOrphaned == 2 {
			fmt.Fprintf(&sql, " %v orphaned = false", filterOp)
			filterOp = "AND"
		}
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY block_timestamp DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.Select(&txs, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return txs, totalCount, nil
}

// InsertElTransactions inserts multiple transactions
func InsertElTransactions(txs []*dbtypes.ElTransaction, tx *sqlx.Tx) error {
	if len(txs) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_transactions ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_transactions ",
		}),
		"(hash, block_number, block_hash, block_timestamp, transaction_index, ",
		"from_address, to_address, value, nonce, ",
		"gas_limit, gas_price, max_fee_per_gas, max_priority_fee_per_gas, effective_gas_price, gas_used, ",
		"input_data, method_id, transaction_type, status, error_message, ",
		"contract_address, logs_count, internal_tx_count, orphaned, fork_id) ",
		"VALUES ",
	)

	argIdx := 0
	fieldCount := 25
	args := make([]interface{}, len(txs)*fieldCount)

	for i, transaction := range txs {
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

		args[argIdx+0] = transaction.Hash
		args[argIdx+1] = transaction.BlockNumber
		args[argIdx+2] = transaction.BlockHash
		args[argIdx+3] = transaction.BlockTimestamp
		args[argIdx+4] = transaction.TransactionIndex
		args[argIdx+5] = transaction.FromAddress
		args[argIdx+6] = transaction.ToAddress
		args[argIdx+7] = transaction.Value
		args[argIdx+8] = transaction.Nonce
		args[argIdx+9] = transaction.GasLimit
		args[argIdx+10] = transaction.GasPrice
		args[argIdx+11] = transaction.MaxFeePerGas
		args[argIdx+12] = transaction.MaxPriorityFeePerGas
		args[argIdx+13] = transaction.EffectiveGasPrice
		args[argIdx+14] = transaction.GasUsed
		args[argIdx+15] = transaction.InputData
		args[argIdx+16] = transaction.MethodId
		args[argIdx+17] = transaction.TransactionType
		args[argIdx+18] = transaction.Status
		args[argIdx+19] = transaction.ErrorMessage
		args[argIdx+20] = transaction.ContractAddress
		args[argIdx+21] = transaction.LogsCount
		args[argIdx+22] = transaction.InternalTxCount
		args[argIdx+23] = transaction.Orphaned
		args[argIdx+24] = transaction.ForkId
		argIdx += fieldCount
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			ON CONFLICT (hash, fork_id) DO UPDATE SET
				orphaned = EXCLUDED.orphaned,
				logs_count = EXCLUDED.logs_count,
				internal_tx_count = EXCLUDED.internal_tx_count
		`,
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	return err
}

// DeleteElTransactionsOlderThan deletes transactions older than a given timestamp
func DeleteElTransactionsOlderThan(timestamp uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		DELETE FROM el_transactions
		WHERE block_timestamp < $1
	`, timestamp)
	return err
}
