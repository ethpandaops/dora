package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElBalances(balances []*dbtypes.ElBalance, dbTx *sqlx.Tx) error {
	if len(balances) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_balances ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_balances ",
		}),
		"(account_id, token_id, balance, balance_raw, updated)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 5

	args := make([]any, len(balances)*fieldCount)
	for i, balance := range balances {
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

		args[argIdx+0] = balance.AccountID
		args[argIdx+1] = balance.TokenID
		args[argIdx+2] = balance.Balance
		args[argIdx+3] = balance.BalanceRaw
		args[argIdx+4] = balance.Updated
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (account_id, token_id) DO UPDATE SET balance = excluded.balance, balance_raw = excluded.balance_raw, updated = excluded.updated",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElBalance(accountID uint64, tokenID uint64) (*dbtypes.ElBalance, error) {
	balance := &dbtypes.ElBalance{}
	err := ReaderDb.Get(balance, "SELECT account_id, token_id, balance, balance_raw, updated FROM el_balances WHERE account_id = $1 AND token_id = $2", accountID, tokenID)
	if err != nil {
		return nil, err
	}
	return balance, nil
}

func GetElBalancesByAccountID(accountID uint64, offset uint64, limit uint32) ([]*dbtypes.ElBalance, uint64, error) {
	var sql strings.Builder
	args := []any{accountID}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT account_id, token_id, balance, balance_raw, updated
		FROM el_balances
		WHERE account_id = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		0 AS account_id,
		count(*) AS token_id,
		0 AS balance,
		null AS balance_raw,
		0 AS updated
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY balance DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	balances := []*dbtypes.ElBalance{}
	err := ReaderDb.Select(&balances, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el balances by account: %v", err)
		return nil, 0, err
	}

	if len(balances) == 0 {
		return []*dbtypes.ElBalance{}, 0, nil
	}

	count := balances[0].TokenID
	return balances[1:], count, nil
}

func GetElBalancesByTokenID(tokenID uint64, offset uint64, limit uint32) ([]*dbtypes.ElBalance, uint64, error) {
	var sql strings.Builder
	args := []any{tokenID}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT account_id, token_id, balance, balance_raw, updated
		FROM el_balances
		WHERE token_id = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		0 AS account_id,
		count(*) AS token_id,
		0 AS balance,
		null AS balance_raw,
		0 AS updated
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY balance DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	balances := []*dbtypes.ElBalance{}
	err := ReaderDb.Select(&balances, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el balances by token id: %v", err)
		return nil, 0, err
	}

	if len(balances) == 0 {
		return []*dbtypes.ElBalance{}, 0, nil
	}

	count := balances[0].TokenID
	return balances[1:], count, nil
}

func GetElBalancesFiltered(offset uint64, limit uint32, accountID uint64, filter *dbtypes.ElBalanceFilter) ([]*dbtypes.ElBalance, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT account_id, token_id, balance, balance_raw, updated
		FROM el_balances
	`)

	filterOp := "WHERE"
	if accountID > 0 {
		args = append(args, accountID)
		fmt.Fprintf(&sql, " %v account_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.TokenID != nil {
		args = append(args, *filter.TokenID)
		fmt.Fprintf(&sql, " %v token_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinBalance != nil {
		args = append(args, *filter.MinBalance)
		fmt.Fprintf(&sql, " %v balance >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxBalance != nil {
		args = append(args, *filter.MaxBalance)
		fmt.Fprintf(&sql, " %v balance <= $%v", filterOp, len(args))
		filterOp = "AND"
	}

	fmt.Fprint(&sql, ")")

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		0 AS account_id,
		count(*) AS token_id,
		0 AS balance,
		null AS balance_raw,
		0 AS updated
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY balance DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	balances := []*dbtypes.ElBalance{}
	err := ReaderDb.Select(&balances, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el balances: %v", err)
		return nil, 0, err
	}

	if len(balances) == 0 {
		return []*dbtypes.ElBalance{}, 0, nil
	}

	count := balances[0].TokenID
	return balances[1:], count, nil
}

func UpdateElBalance(balance *dbtypes.ElBalance, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("UPDATE el_balances SET balance = $1, balance_raw = $2, updated = $3 WHERE account_id = $4 AND token_id = $5",
		balance.Balance, balance.BalanceRaw, balance.Updated, balance.AccountID, balance.TokenID)
	return err
}

func DeleteElBalance(accountID uint64, tokenID uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_balances WHERE account_id = $1 AND token_id = $2", accountID, tokenID)
	return err
}

func DeleteElBalancesByAccountID(accountID uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_balances WHERE account_id = $1", accountID)
	return err
}

func DeleteElBalancesByTokenID(tokenID uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_balances WHERE token_id = $1", tokenID)
	return err
}

// DeleteElZeroBalances deletes all balance entries with zero balance.
// Returns the number of deleted rows.
func DeleteElZeroBalances(dbTx *sqlx.Tx) (int64, error) {
	result, err := dbTx.Exec("DELETE FROM el_balances WHERE balance = 0")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
