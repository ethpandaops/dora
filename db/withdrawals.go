package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertWithdrawals(ctx context.Context, dbTx *sqlx.Tx, withdrawals []*dbtypes.Withdrawal) error {
	if len(withdrawals) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO withdrawals ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO withdrawals ",
		}),
		"(block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 8

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
		args[argIdx+1] = w.BlockIdx
		args[argIdx+2] = w.Type
		args[argIdx+3] = w.Orphaned
		args[argIdx+4] = w.ForkId
		args[argIdx+5] = w.Validator
		args[argIdx+6] = w.AccountID
		args[argIdx+7] = w.Amount
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (block_uid, block_idx) DO UPDATE SET" +
			" type = excluded.type," +
			" orphaned = excluded.orphaned," +
			" fork_id = excluded.fork_id," +
			" validator = excluded.validator," +
			" account_id = excluded.account_id," +
			" amount = excluded.amount",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetWithdrawalsByBlockUid(ctx context.Context, blockUid uint64) ([]*dbtypes.Withdrawal, error) {
	withdrawals := []*dbtypes.Withdrawal{}
	err := ReaderDb.SelectContext(ctx, &withdrawals,
		"SELECT block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount"+
			" FROM withdrawals WHERE block_uid = $1 ORDER BY block_idx ASC",
		blockUid)
	if err != nil {
		return nil, err
	}
	return withdrawals, nil
}

func GetWithdrawalsFiltered(ctx context.Context, offset uint64, limit uint32, finalizedBlock uint64, filter *dbtypes.WithdrawalFilter) ([]*dbtypes.Withdrawal, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount
		FROM withdrawals
	`)

	if filter.ValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names ON validator_names."index" = withdrawals.validator
		`)
	}

	filterOp := "WHERE"
	if filter.Validator != nil {
		args = append(args, *filter.Validator)
		fmt.Fprintf(&sql, " %v validator = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.AccountID != nil {
		args = append(args, *filter.AccountID)
		fmt.Fprintf(&sql, " %v account_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.Types) > 0 {
		fmt.Fprintf(&sql, " %v type IN (", filterOp)
		for i, t := range filter.Types {
			if i > 0 {
				fmt.Fprint(&sql, ", ")
			}
			args = append(args, t)
			fmt.Fprintf(&sql, "$%v", len(args))
		}
		fmt.Fprint(&sql, ")")
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
	if filter.WithOrphaned == 0 {
		args = append(args, finalizedBlock)
		fmt.Fprintf(&sql, " %v (block_uid >> 16 > $%v OR orphaned = false)", filterOp, len(args))
		filterOp = "AND"
	} else if filter.WithOrphaned == 2 {
		args = append(args, finalizedBlock)
		fmt.Fprintf(&sql, " %v (block_uid >> 16 > $%v OR orphaned = true)", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.ValidatorName != "" {
		args = append(args, "%"+filter.ValidatorName+"%")
		fmt.Fprintf(&sql, " %v ", filterOp)
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` validator_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` validator_names.name LIKE $%v `,
		}), len(args))
		filterOp = "AND"
	}

	fmt.Fprint(&sql, ")")

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		0 AS block_idx,
		0 AS type,
		false AS orphaned,
		0 AS fork_id,
		0 AS validator,
		null AS account_id,
		0 AS amount
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, block_idx ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	withdrawals := []*dbtypes.Withdrawal{}
	err := ReaderDb.SelectContext(ctx, &withdrawals, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered withdrawals: %v", err)
		return nil, 0, err
	}

	if len(withdrawals) == 0 {
		return []*dbtypes.Withdrawal{}, 0, nil
	}

	count := withdrawals[0].BlockUid
	return withdrawals[1:], count, nil
}

// MaxAccountWithdrawalCount is the maximum count returned for address withdrawal queries.
const MaxAccountWithdrawalCount = 100000

func GetWithdrawalsByAccountID(ctx context.Context, accountID uint64, offset uint64, limit uint32) ([]*dbtypes.Withdrawal, uint64, error) {
	var sql strings.Builder
	args := []any{accountID}

	fmt.Fprint(&sql, `
		SELECT block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount
		FROM withdrawals
		WHERE account_id = $1
		ORDER BY block_uid DESC NULLS LAST, block_idx ASC
		LIMIT $2`)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	withdrawals := []*dbtypes.Withdrawal{}
	err := ReaderDb.SelectContext(ctx, &withdrawals, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching withdrawals by account id: %v", err)
		return nil, 0, err
	}

	var totalCount uint64
	err = ReaderDb.GetContext(ctx, &totalCount,
		"SELECT COUNT(*) FROM (SELECT 1 FROM withdrawals WHERE account_id = $1 LIMIT $2) AS a",
		accountID, MaxAccountWithdrawalCount,
	)
	if err != nil {
		logger.Errorf("Error while counting withdrawals by account id: %v", err)
		return nil, 0, err
	}

	return withdrawals, totalCount, nil
}

// HasWithdrawalsByAccountID returns true if the given account has any withdrawals.
func HasWithdrawalsByAccountID(ctx context.Context, accountID uint64) (bool, error) {
	var exists bool
	err := ReaderDb.GetContext(ctx, &exists,
		"SELECT EXISTS(SELECT 1 FROM withdrawals WHERE account_id = $1 LIMIT 1)",
		accountID,
	)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func DeleteWithdrawalsByBlockUid(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM withdrawals WHERE block_uid = $1", blockUid)
	return err
}

func GetWithdrawalsByBlockUidRange(ctx context.Context, minBlockUid uint64, maxBlockUid uint64, typeFilter *uint8) ([]*dbtypes.Withdrawal, error) {
	var sql strings.Builder
	args := []any{minBlockUid, maxBlockUid}

	fmt.Fprint(&sql,
		"SELECT block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount"+
			" FROM withdrawals WHERE block_uid >= $1 AND block_uid < $2")

	if typeFilter != nil {
		args = append(args, *typeFilter)
		fmt.Fprintf(&sql, " AND type = $%v", len(args))
	}

	fmt.Fprint(&sql, " ORDER BY block_uid ASC, block_idx ASC")

	withdrawals := []*dbtypes.Withdrawal{}
	err := ReaderDb.SelectContext(ctx, &withdrawals, sql.String(), args...)
	if err != nil {
		return nil, err
	}
	return withdrawals, nil
}

// GetWithdrawalAmountSum returns the total withdrawal amount (Gwei) for all withdrawals
// in the given block_uid range, excluding orphaned entries.
func GetWithdrawalAmountSum(ctx context.Context, minBlockUid uint64, maxBlockUid uint64) (uint64, error) {
	var total uint64
	err := ReaderDb.GetContext(ctx, &total,
		"SELECT COALESCE(SUM(amount), 0) FROM withdrawals WHERE block_uid >= $1 AND block_uid < $2 AND orphaned = false",
		minBlockUid, maxBlockUid)
	if err != nil {
		return 0, err
	}
	return total, nil
}

func UpdateWithdrawalsForkId(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64, forkId uint64, orphaned bool) error {
	_, err := dbTx.ExecContext(ctx,
		"UPDATE withdrawals SET fork_id = $1, orphaned = $2 WHERE block_uid = $3",
		forkId, orphaned, blockUid)
	return err
}
