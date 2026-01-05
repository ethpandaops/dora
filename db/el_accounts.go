package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElAccounts(accounts []*dbtypes.ElAccount, dbTx *sqlx.Tx) error {
	if len(accounts) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_accounts ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_accounts ",
		}),
		"(address, funder, funded, is_contract)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 4

	args := make([]any, len(accounts)*fieldCount)
	for i, account := range accounts {
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

		args[argIdx+0] = account.Address
		args[argIdx+1] = account.Funder
		args[argIdx+2] = account.Funded
		args[argIdx+3] = account.IsContract
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (address) DO UPDATE SET funder = excluded.funder, funded = excluded.funded, is_contract = excluded.is_contract",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElAccountByAddress(address []byte) (*dbtypes.ElAccount, error) {
	account := &dbtypes.ElAccount{}
	err := ReaderDb.Get(account, "SELECT address, funder, funded, is_contract FROM el_accounts WHERE address = $1", address)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func GetElAccountsByFunder(funder []byte, offset uint64, limit uint32) ([]*dbtypes.ElAccount, uint64, error) {
	var sql strings.Builder
	args := []any{funder}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT address, funder, funded, is_contract
		FROM el_accounts
		WHERE funder = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		null AS address,
		count(*) AS funder,
		0 AS funded,
		false AS is_contract
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY funded DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	accounts := []*dbtypes.ElAccount{}
	err := ReaderDb.Select(&accounts, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el accounts by funder: %v", err)
		return nil, 0, err
	}

	if len(accounts) == 0 {
		return []*dbtypes.ElAccount{}, 0, nil
	}

	count := uint64(0)
	if len(accounts[0].Funder) > 0 {
		count = uint64(accounts[0].Funder[0])
	}

	return accounts[1:], count, nil
}

func GetElAccountsFiltered(offset uint64, limit uint32, filter *dbtypes.ElAccountFilter) ([]*dbtypes.ElAccount, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT address, funder, funded, is_contract
		FROM el_accounts
	`)

	filterOp := "WHERE"
	if len(filter.Funder) > 0 {
		args = append(args, filter.Funder)
		fmt.Fprintf(&sql, " %v funder = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.IsContract != nil {
		args = append(args, *filter.IsContract)
		fmt.Fprintf(&sql, " %v is_contract = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinFunded > 0 {
		args = append(args, filter.MinFunded)
		fmt.Fprintf(&sql, " %v funded >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxFunded > 0 {
		args = append(args, filter.MaxFunded)
		fmt.Fprintf(&sql, " %v funded <= $%v", filterOp, len(args))
		filterOp = "AND"
	}

	fmt.Fprint(&sql, ")")

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		null AS address,
		count(*) AS funder,
		0 AS funded,
		false AS is_contract
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY funded DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	accounts := []*dbtypes.ElAccount{}
	err := ReaderDb.Select(&accounts, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el accounts: %v", err)
		return nil, 0, err
	}

	if len(accounts) == 0 {
		return []*dbtypes.ElAccount{}, 0, nil
	}

	count := uint64(0)
	if len(accounts[0].Funder) > 0 {
		count = uint64(accounts[0].Funder[0])
	}

	return accounts[1:], count, nil
}

func UpdateElAccount(account *dbtypes.ElAccount, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("UPDATE el_accounts SET funder = $1, funded = $2, is_contract = $3 WHERE address = $4",
		account.Funder, account.Funded, account.IsContract, account.Address)
	return err
}

func DeleteElAccount(address []byte, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_accounts WHERE address = $1", address)
	return err
}
