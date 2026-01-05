package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElTokenTransfers(transfers []*dbtypes.ElTokenTransfer, dbTx *sqlx.Tx) error {
	if len(transfers) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_token_transfers ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_token_transfers ",
		}),
		"(block_uid, tx_hash, tx_idx, token_id, token_type, token_index, tx_from, tx_to, amount, amount_raw)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 10

	args := make([]any, len(transfers)*fieldCount)
	for i, transfer := range transfers {
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

		args[argIdx+0] = transfer.BlockUid
		args[argIdx+1] = transfer.TxHash
		args[argIdx+2] = transfer.TxIdx
		args[argIdx+3] = transfer.TokenID
		args[argIdx+4] = transfer.TokenType
		args[argIdx+5] = transfer.TokenIndex
		args[argIdx+6] = transfer.From
		args[argIdx+7] = transfer.To
		args[argIdx+8] = transfer.Amount
		args[argIdx+9] = transfer.AmountRaw
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_uid, tx_hash, tx_idx) DO UPDATE SET token_id = excluded.token_id, token_type = excluded.token_type, token_index = excluded.token_index, tx_from = excluded.tx_from, tx_to = excluded.tx_to, amount = excluded.amount, amount_raw = excluded.amount_raw",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElTokenTransfer(blockUid uint64, txHash []byte, txIdx uint32) (*dbtypes.ElTokenTransfer, error) {
	transfer := &dbtypes.ElTokenTransfer{}
	err := ReaderDb.Get(transfer, "SELECT block_uid, tx_hash, tx_idx, token_id, token_type, token_index, tx_from, tx_to, amount, amount_raw FROM el_token_transfers WHERE block_uid = $1 AND tx_hash = $2 AND tx_idx = $3", blockUid, txHash, txIdx)
	if err != nil {
		return nil, err
	}
	return transfer, nil
}

func GetElTokenTransfersByTxHash(txHash []byte) ([]*dbtypes.ElTokenTransfer, error) {
	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.Select(&transfers, "SELECT block_uid, tx_hash, tx_idx, token_id, token_type, token_index, tx_from, tx_to, amount, amount_raw FROM el_token_transfers WHERE tx_hash = $1 ORDER BY block_uid DESC, tx_idx ASC", txHash)
	if err != nil {
		return nil, err
	}
	return transfers, nil
}

func GetElTokenTransfersByBlockUid(blockUid uint64) ([]*dbtypes.ElTokenTransfer, error) {
	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.Select(&transfers, "SELECT block_uid, tx_hash, tx_idx, token_id, token_type, token_index, tx_from, tx_to, amount, amount_raw FROM el_token_transfers WHERE block_uid = $1 ORDER BY tx_hash ASC, tx_idx ASC", blockUid)
	if err != nil {
		return nil, err
	}
	return transfers, nil
}

func GetElTokenTransfersByTokenID(tokenID uint64, offset uint64, limit uint32) ([]*dbtypes.ElTokenTransfer, uint64, error) {
	var sql strings.Builder
	args := []any{tokenID}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, tx_idx, token_id, token_type, token_index, tx_from, tx_to, amount, amount_raw
		FROM el_token_transfers
		WHERE token_id = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		null AS tx_hash,
		0 AS tx_idx,
		0 AS token_id,
		0 AS token_type,
		null AS token_index,
		null AS tx_from,
		null AS tx_to,
		0 AS amount,
		null AS amount_raw
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, tx_hash DESC, tx_idx DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.Select(&transfers, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el token transfers by token id: %v", err)
		return nil, 0, err
	}

	if len(transfers) == 0 {
		return []*dbtypes.ElTokenTransfer{}, 0, nil
	}

	count := transfers[0].BlockUid
	return transfers[1:], count, nil
}

func GetElTokenTransfersByAddress(address []byte, isFrom bool, offset uint64, limit uint32) ([]*dbtypes.ElTokenTransfer, uint64, error) {
	var sql strings.Builder
	args := []any{}

	column := "tx_to"
	if isFrom {
		column = "tx_from"
	}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, tx_idx, token_id, token_type, token_index, tx_from, tx_to, amount, amount_raw
		FROM el_token_transfers
		WHERE `, column, ` = $1
	)`)
	args = append(args, address)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		null AS tx_hash,
		0 AS tx_idx,
		0 AS token_id,
		0 AS token_type,
		null AS token_index,
		null AS tx_from,
		null AS tx_to,
		0 AS amount,
		null AS amount_raw
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, tx_hash DESC, tx_idx DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.Select(&transfers, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el token transfers by address: %v", err)
		return nil, 0, err
	}

	if len(transfers) == 0 {
		return []*dbtypes.ElTokenTransfer{}, 0, nil
	}

	count := transfers[0].BlockUid
	return transfers[1:], count, nil
}

func GetElTokenTransfersFiltered(offset uint64, limit uint32, filter *dbtypes.ElTokenTransferFilter) ([]*dbtypes.ElTokenTransfer, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, tx_idx, token_id, token_type, token_index, tx_from, tx_to, amount, amount_raw
		FROM el_token_transfers
	`)

	filterOp := "WHERE"
	if filter.TokenID != nil {
		args = append(args, *filter.TokenID)
		fmt.Fprintf(&sql, " %v token_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
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
	if filter.MinAmount != nil {
		args = append(args, *filter.MinAmount)
		fmt.Fprintf(&sql, " %v amount >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxAmount != nil {
		args = append(args, *filter.MaxAmount)
		fmt.Fprintf(&sql, " %v amount <= $%v", filterOp, len(args))
		filterOp = "AND"
	}

	fmt.Fprint(&sql, ")")

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		null AS tx_hash,
		0 AS tx_idx,
		0 AS token_id,
		0 AS token_type,
		null AS token_index,
		null AS tx_from,
		null AS tx_to,
		0 AS amount,
		null AS amount_raw
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, tx_hash DESC, tx_idx DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.Select(&transfers, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el token transfers: %v", err)
		return nil, 0, err
	}

	if len(transfers) == 0 {
		return []*dbtypes.ElTokenTransfer{}, 0, nil
	}

	count := transfers[0].BlockUid
	return transfers[1:], count, nil
}

func DeleteElTokenTransfer(blockUid uint64, txHash []byte, txIdx uint32, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_token_transfers WHERE block_uid = $1 AND tx_hash = $2 AND tx_idx = $3", blockUid, txHash, txIdx)
	return err
}

func DeleteElTokenTransfersByBlockUid(blockUid uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_token_transfers WHERE block_uid = $1", blockUid)
	return err
}

func DeleteElTokenTransfersByTxHash(txHash []byte, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_token_transfers WHERE tx_hash = $1", txHash)
	return err
}

func DeleteElTokenTransfersByTokenID(tokenID uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_token_transfers WHERE token_id = $1", tokenID)
	return err
}
