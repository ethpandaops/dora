package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElAccount(account *dbtypes.ElAccount, dbTx *sqlx.Tx) (uint64, error) {
	var id uint64
	query := EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "INSERT INTO el_accounts (address, funder_id, funded, is_contract, last_nonce, last_block_uid) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id",
		dbtypes.DBEngineSqlite: "INSERT INTO el_accounts (address, funder_id, funded, is_contract, last_nonce, last_block_uid) VALUES ($1, $2, $3, $4, $5, $6)",
	})

	if DbEngine == dbtypes.DBEnginePgsql {
		err := dbTx.QueryRow(query, account.Address, account.FunderID, account.Funded, account.IsContract, account.LastNonce, account.LastBlockUid).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		result, err := dbTx.Exec(query, account.Address, account.FunderID, account.Funded, account.IsContract, account.LastNonce, account.LastBlockUid)
		if err != nil {
			return 0, err
		}
		lastID, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		id = uint64(lastID)
	}
	return id, nil
}

func GetElAccountByID(id uint64) (*dbtypes.ElAccount, error) {
	account := &dbtypes.ElAccount{}
	err := ReaderDb.Get(account, "SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid FROM el_accounts WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func GetElAccountByAddress(address []byte) (*dbtypes.ElAccount, error) {
	account := &dbtypes.ElAccount{}
	err := ReaderDb.Get(account, "SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid FROM el_accounts WHERE address = $1", address)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func GetElAccountsByFunder(funderID uint64, offset uint64, limit uint32) ([]*dbtypes.ElAccount, uint64, error) {
	var sql strings.Builder
	args := []any{funderID}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid
		FROM el_accounts
		WHERE funder_id = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS id,
		null AS address,
		0 AS funder_id,
		0 AS funded,
		false AS is_contract,
		0 AS last_nonce,
		0 AS last_block_uid
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

	count := accounts[0].ID
	return accounts[1:], count, nil
}

func GetElAccountsFiltered(offset uint64, limit uint32, filter *dbtypes.ElAccountFilter) ([]*dbtypes.ElAccount, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid
		FROM el_accounts
	`)

	filterOp := "WHERE"
	if filter.FunderID > 0 {
		args = append(args, filter.FunderID)
		fmt.Fprintf(&sql, " %v funder_id = $%v", filterOp, len(args))
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
		count(*) AS id,
		null AS address,
		0 AS funder_id,
		0 AS funded,
		false AS is_contract,
		0 AS last_nonce,
		0 AS last_block_uid
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

	count := accounts[0].ID
	return accounts[1:], count, nil
}

func UpdateElAccount(account *dbtypes.ElAccount, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("UPDATE el_accounts SET funder_id = $1, funded = $2, is_contract = $3, last_nonce = $4, last_block_uid = $5 WHERE id = $6",
		account.FunderID, account.Funded, account.IsContract, account.LastNonce, account.LastBlockUid, account.ID)
	return err
}

// UpdateElAccountsLastNonce batch updates last_nonce and last_block_uid for multiple accounts by ID.
// Uses VALUES clause for efficient batch update - 10-50x faster than individual updates.
func UpdateElAccountsLastNonce(accounts []*dbtypes.ElAccount, dbTx *sqlx.Tx) error {
	if len(accounts) == 0 {
		return nil
	}

	// Filter out accounts with zero ID
	validAccounts := make([]*dbtypes.ElAccount, 0, len(accounts))
	for _, account := range accounts {
		if account.ID > 0 {
			validAccounts = append(validAccounts, account)
		}
	}

	if len(validAccounts) == 0 {
		return nil
	}

	var sql strings.Builder
	args := make([]any, 0, len(validAccounts)*3)

	if DbEngine == dbtypes.DBEnginePgsql {
		// PostgreSQL: use UPDATE ... FROM VALUES with explicit type casts
		fmt.Fprint(&sql, `
			UPDATE el_accounts AS a SET
				last_nonce = v.last_nonce,
				last_block_uid = v.last_block_uid
			FROM (VALUES `)

		for i, account := range validAccounts {
			if i > 0 {
				fmt.Fprint(&sql, ", ")
			}
			argIdx := len(args) + 1
			fmt.Fprintf(&sql, "($%d::bigint, $%d::bigint, $%d::bigint)", argIdx, argIdx+1, argIdx+2)
			args = append(args, account.ID, account.LastNonce, account.LastBlockUid)
		}

		fmt.Fprint(&sql, `) AS v(id, last_nonce, last_block_uid)
			WHERE a.id = v.id`)
	} else {
		// SQLite: use UPDATE with CASE statements (works in all SQLite versions)
		// For SQLite 3.33.0+, could use UPDATE ... FROM VALUES, but CASE is more compatible
		if len(validAccounts) == 1 {
			// Single update - simple case
			args = append(args, validAccounts[0].LastNonce, validAccounts[0].LastBlockUid, validAccounts[0].ID)
			fmt.Fprint(&sql, `UPDATE el_accounts SET last_nonce = $1, last_block_uid = $2 WHERE id = $3`)
		} else {
			// Multiple updates - use CASE statements
			fmt.Fprint(&sql, `UPDATE el_accounts SET
				last_nonce = CASE id `)

			for _, account := range validAccounts {
				argIdx := len(args) + 1
				fmt.Fprintf(&sql, "WHEN $%d THEN $%d ", argIdx, argIdx+1)
				args = append(args, account.ID, account.LastNonce)
			}
			fmt.Fprint(&sql, "ELSE last_nonce END, last_block_uid = CASE id ")

			for _, account := range validAccounts {
				argIdx := len(args) + 1
				fmt.Fprintf(&sql, "WHEN $%d THEN $%d ", argIdx, argIdx+1)
				args = append(args, account.ID, account.LastBlockUid)
			}

			fmt.Fprint(&sql, "ELSE last_block_uid END WHERE id IN (")
			for i, account := range validAccounts {
				if i > 0 {
					fmt.Fprint(&sql, ", ")
				}
				argIdx := len(args) + 1
				fmt.Fprintf(&sql, "$%d", argIdx)
				args = append(args, account.ID)
			}
			fmt.Fprint(&sql, ")")
		}
	}

	_, err := dbTx.Exec(sql.String(), args...)
	return err
}

func DeleteElAccount(id uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_accounts WHERE id = $1", id)
	return err
}

// GetElAccountsByAddresses retrieves multiple accounts by their addresses in a single query.
// Returns a map from address (as hex string) to account for efficient lookup.
func GetElAccountsByAddresses(addresses [][]byte) (map[string]*dbtypes.ElAccount, error) {
	if len(addresses) == 0 {
		return make(map[string]*dbtypes.ElAccount), nil
	}

	var sql strings.Builder
	args := make([]any, len(addresses))

	fmt.Fprint(&sql, "SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid FROM el_accounts WHERE address IN (")
	for i, addr := range addresses {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%d", i+1)
		args[i] = addr
	}
	fmt.Fprint(&sql, ")")

	accounts := []*dbtypes.ElAccount{}
	err := ReaderDb.Select(&accounts, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el accounts by addresses: %v", err)
		return nil, err
	}

	// Build map from address to account
	result := make(map[string]*dbtypes.ElAccount, len(accounts))
	for _, account := range accounts {
		// Use hex string of address as key for efficient lookup
		key := fmt.Sprintf("%x", account.Address)
		result[key] = account
	}
	return result, nil
}

// GetElAccountsByIDs retrieves multiple accounts by their IDs in a single query.
func GetElAccountsByIDs(ids []uint64) ([]*dbtypes.ElAccount, error) {
	if len(ids) == 0 {
		return []*dbtypes.ElAccount{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(ids))

	fmt.Fprint(&sql, "SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid FROM el_accounts WHERE id IN (")
	for i, id := range ids {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%d", i+1)
		args[i] = id
	}
	fmt.Fprint(&sql, ")")

	accounts := []*dbtypes.ElAccount{}
	err := ReaderDb.Select(&accounts, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el accounts by IDs: %v", err)
		return nil, err
	}
	return accounts, nil
}
