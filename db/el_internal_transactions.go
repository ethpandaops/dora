package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElInternalTransactions(txs []*dbtypes.ElInternalTransaction, dbTx *sqlx.Tx) error {
	if len(txs) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_internal_transactions ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_internal_transactions ",
		}),
		"(block_uid, tx_hash, tx_idx, from_id, to_id, amount, amount_raw)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 7

	args := make([]any, len(txs)*fieldCount)
	for i, tx := range txs {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprint(&sql, "(")
		for f := range fieldCount {
			if f > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", argIdx+f+1)
		}
		fmt.Fprint(&sql, ")")

		args[argIdx+0] = tx.BlockUid
		args[argIdx+1] = tx.TxHash
		args[argIdx+2] = tx.TxIdx
		args[argIdx+3] = tx.FromID
		args[argIdx+4] = tx.ToID
		args[argIdx+5] = tx.Amount
		args[argIdx+6] = tx.AmountRaw
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_uid, tx_hash, tx_idx) DO UPDATE SET from_id = excluded.from_id, to_id = excluded.to_id, amount = excluded.amount, amount_raw = excluded.amount_raw",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElInternalTransaction(blockUid uint64, txHash []byte, txIdx uint32) (*dbtypes.ElInternalTransaction, error) {
	tx := &dbtypes.ElInternalTransaction{}
	err := ReaderDb.Get(tx, "SELECT block_uid, tx_hash, tx_idx, from_id, to_id, amount, amount_raw FROM el_internal_transactions WHERE block_uid = $1 AND tx_hash = $2 AND tx_idx = $3", blockUid, txHash, txIdx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func GetElInternalTransactionsByTxHash(txHash []byte) ([]*dbtypes.ElInternalTransaction, error) {
	txs := []*dbtypes.ElInternalTransaction{}
	err := ReaderDb.Select(&txs, "SELECT block_uid, tx_hash, tx_idx, from_id, to_id, amount, amount_raw FROM el_internal_transactions WHERE tx_hash = $1 ORDER BY block_uid DESC, tx_idx ASC", txHash)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElInternalTransactionsByBlockUid(blockUid uint64) ([]*dbtypes.ElInternalTransaction, error) {
	txs := []*dbtypes.ElInternalTransaction{}
	err := ReaderDb.Select(&txs, "SELECT block_uid, tx_hash, tx_idx, from_id, to_id, amount, amount_raw FROM el_internal_transactions WHERE block_uid = $1 ORDER BY tx_idx ASC", blockUid)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElInternalTransactionsByBlockUidAndTxHash(blockUid uint64, txHash []byte) ([]*dbtypes.ElInternalTransaction, error) {
	txs := []*dbtypes.ElInternalTransaction{}
	err := ReaderDb.Select(&txs, "SELECT block_uid, tx_hash, tx_idx, from_id, to_id, amount, amount_raw FROM el_internal_transactions WHERE block_uid = $1 AND tx_hash = $2 ORDER BY tx_idx ASC", blockUid, txHash)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

// MaxAccountInternalTransactionCount is the maximum count returned for address internal transaction queries.
// If the actual count exceeds this, the query returns this limit and sets the "more" flag.
const MaxAccountInternalTransactionCount = 100000

// GetElInternalTransactionsByAccountIDCombined fetches internal transactions where the account is either
// sender (from_id) or receiver (to_id). Results are sorted by block_uid DESC, tx_idx DESC.
// Returns transactions, total count (capped at MaxAccountInternalTransactionCount), whether count is capped, and error.
func GetElInternalTransactionsByAccountIDCombined(accountID uint64, offset uint64, limit uint32) ([]*dbtypes.ElInternalTransaction, uint64, bool, error) {
	// Use UNION ALL instead of OR for better index usage.
	// The second query excludes rows where from_id = accountID to avoid duplicates
	// (handles self-transfers where from_id = to_id = accountID).
	var sql strings.Builder
	args := []any{accountID, accountID, accountID}

	fmt.Fprint(&sql, `
		SELECT block_uid, tx_hash, tx_idx, from_id, to_id, amount, amount_raw
		FROM (
			SELECT block_uid, tx_hash, tx_idx, from_id, to_id, amount, amount_raw
			FROM el_internal_transactions WHERE from_id = $1
			UNION ALL
			SELECT block_uid, tx_hash, tx_idx, from_id, to_id, amount, amount_raw
			FROM el_internal_transactions WHERE to_id = $2 AND from_id != $3
		) combined
		ORDER BY block_uid DESC, tx_idx DESC
		LIMIT $4`)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	txs := []*dbtypes.ElInternalTransaction{}
	err := ReaderDb.Select(&txs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el internal transactions by account id (combined): %v", err)
		return nil, 0, false, err
	}

	// Get count with a separate query, capped at MaxAccountInternalTransactionCount
	// Using UNION ALL with LIMIT for efficient counting
	countSQL := `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM el_internal_transactions WHERE from_id = $1
			UNION ALL
			SELECT 1 FROM el_internal_transactions WHERE to_id = $2 AND from_id != $3
			LIMIT $4
		) limited`
	var totalCount uint64
	err = ReaderDb.Get(&totalCount, countSQL, accountID, accountID, accountID, MaxAccountInternalTransactionCount)
	if err != nil {
		logger.Errorf("Error while counting el internal transactions by account id (combined): %v", err)
		return nil, 0, false, err
	}

	moreAvailable := totalCount >= MaxAccountInternalTransactionCount

	return txs, totalCount, moreAvailable, nil
}

func DeleteElInternalTransaction(blockUid uint64, txHash []byte, txIdx uint32, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_internal_transactions WHERE block_uid = $1 AND tx_hash = $2 AND tx_idx = $3", blockUid, txHash, txIdx)
	return err
}

func DeleteElInternalTransactionsByBlockUid(blockUid uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_internal_transactions WHERE block_uid = $1", blockUid)
	return err
}

func DeleteElInternalTransactionsByTxHash(txHash []byte, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_internal_transactions WHERE tx_hash = $1", txHash)
	return err
}
