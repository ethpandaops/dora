package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElAccount(ctx context.Context, dbTx *sqlx.Tx, account *dbtypes.ElAccount) (uint64, error) {
	var id uint64

	if DbEngine == dbtypes.DBEnginePgsql {
		// Use ON CONFLICT with a no-op update to return the existing id on conflict.
		query := "INSERT INTO el_accounts (address, funder_id, funded, is_contract, last_nonce, last_block_uid) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (address) DO UPDATE SET address = excluded.address RETURNING id"
		err := dbTx.QueryRowContext(ctx, query, account.Address, account.FunderID, account.Funded, account.IsContract, account.LastNonce, account.LastBlockUid).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		// SQLite: use INSERT OR IGNORE to skip on duplicate address, then look up the id.
		query := "INSERT OR IGNORE INTO el_accounts (address, funder_id, funded, is_contract, last_nonce, last_block_uid) VALUES ($1, $2, $3, $4, $5, $6)"
		result, err := dbTx.ExecContext(ctx, query, account.Address, account.FunderID, account.Funded, account.IsContract, account.LastNonce, account.LastBlockUid)
		if err != nil {
			return 0, err
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}

		if rowsAffected > 0 {
			lastID, err := result.LastInsertId()
			if err != nil {
				return 0, err
			}
			id = uint64(lastID)
		} else {
			// Row already exists, look up the existing id.
			err := dbTx.QueryRowContext(ctx, "SELECT id FROM el_accounts WHERE address = $1", account.Address).Scan(&id)
			if err != nil {
				return 0, fmt.Errorf("failed to get existing account id: %w", err)
			}
		}
	}
	return id, nil
}

func GetElAccountByID(ctx context.Context, id uint64) (*dbtypes.ElAccount, error) {
	account := &dbtypes.ElAccount{}
	err := ReaderDb.GetContext(ctx, account, "SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid FROM el_accounts WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func GetElAccountByAddress(ctx context.Context, address []byte) (*dbtypes.ElAccount, error) {
	account := &dbtypes.ElAccount{}
	err := ReaderDb.GetContext(ctx, account, "SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid FROM el_accounts WHERE address = $1", address)
	if err != nil {
		return nil, err
	}
	return account, nil
}

// UpdateElAccountsLastNonce batch updates last_nonce and last_block_uid for multiple accounts by ID.
// Uses VALUES clause for efficient batch update - 10-50x faster than individual updates.
func UpdateElAccountsLastNonce(ctx context.Context, dbTx *sqlx.Tx, accounts []*dbtypes.ElAccount) error {
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

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	return err
}

// GetElAccountsByAddresses retrieves multiple accounts by their addresses in a single query.
// Returns a map from address (as hex string) to account for efficient lookup.
func GetElAccountsByAddresses(ctx context.Context, addresses [][]byte) (map[string]*dbtypes.ElAccount, error) {
	if len(addresses) == 0 {
		return make(map[string]*dbtypes.ElAccount), nil
	}

	var sql strings.Builder
	args := make([]any, len(addresses))

	fmt.Fprint(&sql, "SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid FROM el_accounts WHERE address IN (")
	for i, addr := range addresses {
		args[i] = addr
	}
	appendDollarPlaceholders(&sql, 1, len(addresses), ", ")
	fmt.Fprint(&sql, ")")

	accounts := []*dbtypes.ElAccount{}
	err := ReaderDb.SelectContext(ctx, &accounts, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el accounts by addresses: %v", err)
		return nil, err
	}

	// Build map from address to account
	result := make(map[string]*dbtypes.ElAccount, len(accounts))
	for _, account := range accounts {
		result[byteSliceMapKey(account.Address)] = account
	}
	return result, nil
}

// GetElAccountsByIDs retrieves multiple accounts by their IDs in a single query.
func GetElAccountsByIDs(ctx context.Context, ids []uint64) ([]*dbtypes.ElAccount, error) {
	if len(ids) == 0 {
		return []*dbtypes.ElAccount{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(ids))

	fmt.Fprint(&sql, "SELECT id, address, funder_id, funded, is_contract, last_nonce, last_block_uid FROM el_accounts WHERE id IN (")
	for i, id := range ids {
		args[i] = id
	}
	appendDollarPlaceholders(&sql, 1, len(ids), ", ")
	fmt.Fprint(&sql, ")")

	accounts := []*dbtypes.ElAccount{}
	err := ReaderDb.SelectContext(ctx, &accounts, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el accounts by IDs: %v", err)
		return nil, err
	}
	return accounts, nil
}
