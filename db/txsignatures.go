package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types"
	"github.com/jmoiron/sqlx"
)

func GetTxFunctionSignaturesByBytes(sigBytes []types.TxSignatureBytes) []*dbtypes.TxFunctionSignature {
	fnSigs := []*dbtypes.TxFunctionSignature{}

	var sql strings.Builder
	fmt.Fprintf(&sql, `
	SELECT
		signature, bytes, name
	FROM tx_function_signatures
	WHERE bytes IN (`)
	argIdx := 0
	args := make([]any, len(sigBytes))
	for i := range sigBytes {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%v", argIdx+1)
		args[argIdx] = sigBytes[i][:]
		argIdx += 1
	}
	fmt.Fprintf(&sql, ")")

	err := ReaderDb.Select(&fnSigs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching tx function signatures: %v", err)
		return nil
	}
	return fnSigs
}

func InsertTxFunctionSignature(txFuncSig *dbtypes.TxFunctionSignature, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO tx_function_signatures (
				signature, bytes, name
			) VALUES ($1, $2, $3)
			ON CONFLICT (bytes) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE INTO tx_function_signatures (
				signature, bytes, name
			) VALUES ($1, $2, $3)`,
	}),
		txFuncSig.Signature, txFuncSig.Bytes, txFuncSig.Name)
	if err != nil {
		return err
	}
	return nil
}

func GetUnknownFunctionSignatures(sigBytes []types.TxSignatureBytes) []*dbtypes.TxUnknownFunctionSignature {
	unknownFnSigs := []*dbtypes.TxUnknownFunctionSignature{}
	if len(sigBytes) == 0 {
		return unknownFnSigs
	}
	var sql strings.Builder
	fmt.Fprintf(&sql, `
	SELECT
		bytes, lastcheck
	FROM tx_unknown_signatures
	WHERE bytes in (`)
	argIdx := 0
	args := make([]any, len(sigBytes))
	for i := range sigBytes {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%v", argIdx+1)
		args[argIdx] = sigBytes[i][:]
		argIdx += 1
	}
	fmt.Fprintf(&sql, ")")
	err := ReaderDb.Select(&unknownFnSigs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching unknown function signatures: %v", err)
		return nil
	}
	return unknownFnSigs
}

func InsertUnknownFunctionSignatures(txUnknownSigs []*dbtypes.TxUnknownFunctionSignature, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  `INSERT INTO tx_unknown_signatures (bytes, lastcheck) VALUES `,
		dbtypes.DBEngineSqlite: `INSERT OR REPLACE INTO tx_unknown_signatures (bytes, lastcheck) VALUES `,
	}))
	argIdx := 0
	args := make([]any, len(txUnknownSigs)*2)
	for i := range txUnknownSigs {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "($%v, $%v)", argIdx+1, argIdx+2)
		args[argIdx] = txUnknownSigs[i].Bytes
		args[argIdx+1] = txUnknownSigs[i].LastCheck
		argIdx += 2
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  ` ON CONFLICT (bytes) DO UPDATE SET lastcheck = excluded.lastcheck`,
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func InsertPendingFunctionSignatures(txPendingSigs []*dbtypes.TxPendingFunctionSignature, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  `INSERT INTO tx_pending_signatures (bytes, queuetime) VALUES `,
		dbtypes.DBEngineSqlite: `INSERT OR IGNORE INTO tx_pending_signatures (bytes, queuetime) VALUES `,
	}))
	argIdx := 0
	args := make([]any, len(txPendingSigs)*2)
	for i := range txPendingSigs {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "($%v, $%v)", argIdx+1, argIdx+2)
		args[argIdx] = txPendingSigs[i].Bytes
		args[argIdx+1] = txPendingSigs[i].QueueTime
		argIdx += 2
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  ` ON CONFLICT (bytes) DO NOTHING`,
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetPendingFunctionSignatures(limit uint64) []*dbtypes.TxPendingFunctionSignature {
	pendingFnSigs := []*dbtypes.TxPendingFunctionSignature{}
	err := ReaderDb.Select(&pendingFnSigs, `
	SELECT
		bytes, queuetime
	FROM tx_pending_signatures
	ORDER BY queuetime ASC
	LIMIT $1`, limit)
	if err != nil {
		logger.Errorf("Error while fetching unknown function signatures: %v", err)
		return nil
	}
	return pendingFnSigs
}

func DeletePendingFunctionSignatures(sigBytes []types.TxSignatureBytes, tx *sqlx.Tx) error {
	if len(sigBytes) == 0 {
		return nil
	}
	var sql strings.Builder
	fmt.Fprintf(&sql, `
	DELETE FROM tx_pending_signatures
	WHERE bytes in (`)
	args := make([]any, len(sigBytes))
	for i := range sigBytes {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%v", i+1)
		args[i] = sigBytes[i][:]
	}
	fmt.Fprintf(&sql, ")")
	_, err := tx.Exec(sql.String(), args...)
	return err
}
