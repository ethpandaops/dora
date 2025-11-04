package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// GetElInternalTxsByTransaction retrieves all internal transactions for a transaction hash
func GetElInternalTxsByTransaction(txHash []byte) ([]*dbtypes.ElInternalTx, error) {
	internalTxs := []*dbtypes.ElInternalTx{}
	err := ReaderDb.Select(&internalTxs, `
		SELECT *
		FROM el_internal_txs
		WHERE transaction_hash = $1
		ORDER BY trace_address ASC
	`, txHash)
	return internalTxs, err
}

// GetElInternalTxsByAddress retrieves internal transactions involving an address
func GetElInternalTxsByAddress(address []byte, offset uint, limit uint) ([]*dbtypes.ElInternalTx, uint64, error) {
	var sql strings.Builder
	args := []interface{}{address, address}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_internal_txs
			WHERE (from_address = $1 OR to_address = $2)
	`)

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

	internalTxs := []*dbtypes.ElInternalTx{}
	err := ReaderDb.Select(&internalTxs, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return internalTxs, totalCount, nil
}

// InsertElInternalTxs inserts multiple internal transactions
func InsertElInternalTxs(internalTxs []*dbtypes.ElInternalTx, tx *sqlx.Tx) error {
	if len(internalTxs) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_internal_txs ",
			dbtypes.DBEngineSqlite: "INSERT INTO el_internal_txs ",
		}),
		"(transaction_hash, block_number, block_timestamp, trace_address, trace_type, call_type, ",
		"from_address, to_address, value, gas, gas_used, input_data, output_data, error, created_contract) ",
		"VALUES ",
	)

	argIdx := 0
	fieldCount := 15
	args := make([]interface{}, len(internalTxs)*fieldCount)

	for i, internalTx := range internalTxs {
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

		args[argIdx+0] = internalTx.TransactionHash
		args[argIdx+1] = internalTx.BlockNumber
		args[argIdx+2] = internalTx.BlockTimestamp
		args[argIdx+3] = internalTx.TraceAddress
		args[argIdx+4] = internalTx.TraceType
		args[argIdx+5] = internalTx.CallType
		args[argIdx+6] = internalTx.FromAddress
		args[argIdx+7] = internalTx.ToAddress
		args[argIdx+8] = internalTx.Value
		args[argIdx+9] = internalTx.Gas
		args[argIdx+10] = internalTx.GasUsed
		args[argIdx+11] = internalTx.InputData
		args[argIdx+12] = internalTx.OutputData
		args[argIdx+13] = internalTx.Error
		args[argIdx+14] = internalTx.CreatedContract
		argIdx += fieldCount
	}

	_, err := tx.Exec(sql.String(), args...)
	return err
}

// DeleteElInternalTxsOlderThan deletes internal transactions older than a given timestamp
func DeleteElInternalTxsOlderThan(timestamp uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		DELETE FROM el_internal_txs
		WHERE block_timestamp < $1
	`, timestamp)
	return err
}
