package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElWithdrawals(ctx context.Context, dbTx *sqlx.Tx, withdrawals []*dbtypes.ElWithdrawal) error {
	if len(withdrawals) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_withdrawals ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_withdrawals ",
		}),
		"(block_uid, block_index, account_id, type, amount, amount_raw, validator)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 7

	args := make([]any, len(withdrawals)*fieldCount)
	for i, w := range withdrawals {
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

		args[argIdx+0] = w.BlockUid
		args[argIdx+1] = w.BlockIndex
		args[argIdx+2] = w.AccountID
		args[argIdx+3] = w.Type
		args[argIdx+4] = w.Amount
		args[argIdx+5] = w.AmountRaw
		args[argIdx+6] = w.Validator
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (block_uid, block_index) DO UPDATE SET" +
			" account_id = excluded.account_id," +
			" type = excluded.type," +
			" amount = excluded.amount," +
			" amount_raw = excluded.amount_raw," +
			" validator = excluded.validator",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElWithdrawal(ctx context.Context, blockUid uint64, blockIndex uint16) (*dbtypes.ElWithdrawal, error) {
	w := &dbtypes.ElWithdrawal{}
	err := ReaderDb.GetContext(ctx, w,
		"SELECT block_uid, block_index, account_id, type, amount, amount_raw, validator"+
			" FROM el_withdrawals WHERE block_uid = $1 AND block_index = $2",
		blockUid, blockIndex)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func GetElWithdrawalsByBlockUid(ctx context.Context, blockUid uint64) ([]*dbtypes.ElWithdrawal, error) {
	withdrawals := []*dbtypes.ElWithdrawal{}
	err := ReaderDb.SelectContext(ctx, &withdrawals,
		"SELECT block_uid, block_index, account_id, type, amount, amount_raw, validator"+
			" FROM el_withdrawals WHERE block_uid = $1 ORDER BY block_index ASC",
		blockUid)
	if err != nil {
		return nil, err
	}
	return withdrawals, nil
}

func GetElWithdrawalsByAccountID(ctx context.Context, accountID uint64, offset uint64, limit uint32) ([]*dbtypes.ElWithdrawal, uint64, error) {
	var sql strings.Builder
	args := []any{accountID}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, block_index, account_id, type, amount, amount_raw, validator
		FROM el_withdrawals
		WHERE account_id = $1
	)`)

	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		0 AS block_index,
		0 AS account_id,
		0 AS type,
		0 AS amount,
		null AS amount_raw,
		null AS validator
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC NULLS LAST, block_index ASC
	LIMIT $%v`, len(args)+1)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	withdrawals := []*dbtypes.ElWithdrawal{}
	err := ReaderDb.SelectContext(ctx, &withdrawals, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el withdrawals by account id: %v", err)
		return nil, 0, err
	}

	if len(withdrawals) == 0 {
		return []*dbtypes.ElWithdrawal{}, 0, nil
	}

	count := withdrawals[0].BlockUid
	return withdrawals[1:], count, nil
}

func GetElWithdrawalsFiltered(ctx context.Context, offset uint64, limit uint32, filter *dbtypes.ElWithdrawalFilter) ([]*dbtypes.ElWithdrawal, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, block_index, account_id, type, amount, amount_raw, validator
		FROM el_withdrawals
	`)

	filterOp := "WHERE"
	if filter.AccountID > 0 {
		args = append(args, filter.AccountID)
		fmt.Fprintf(&sql, " %v account_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.Type != nil {
		args = append(args, *filter.Type)
		fmt.Fprintf(&sql, " %v type = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.Validator != nil {
		args = append(args, *filter.Validator)
		fmt.Fprintf(&sql, " %v validator = $%v", filterOp, len(args))
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
		0 AS block_index,
		0 AS account_id,
		0 AS type,
		0 AS amount,
		null AS amount_raw,
		null AS validator
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC NULLS LAST, block_index ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	withdrawals := []*dbtypes.ElWithdrawal{}
	err := ReaderDb.SelectContext(ctx, &withdrawals, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el withdrawals: %v", err)
		return nil, 0, err
	}

	if len(withdrawals) == 0 {
		return []*dbtypes.ElWithdrawal{}, 0, nil
	}

	count := withdrawals[0].BlockUid
	return withdrawals[1:], count, nil
}

func DeleteElWithdrawal(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64, blockIndex uint16) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM el_withdrawals WHERE block_uid = $1 AND block_index = $2", blockUid, blockIndex)
	return err
}

func DeleteElWithdrawalsByBlockUid(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM el_withdrawals WHERE block_uid = $1", blockUid)
	return err
}
