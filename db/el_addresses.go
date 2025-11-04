package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// GetElAddress retrieves an address by its address bytes
func GetElAddress(address []byte) (*dbtypes.ElAddress, error) {
	addr := &dbtypes.ElAddress{}
	err := ReaderDb.Get(addr, `
		SELECT *
		FROM el_addresses
		WHERE address = $1
	`, address)
	if err != nil {
		return nil, err
	}
	return addr, nil
}

// GetElAddresses retrieves addresses with optional filtering and pagination
func GetElAddresses(offset uint, limit uint, filter *dbtypes.ElAddressFilter) ([]*dbtypes.ElAddress, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_addresses
	`)

	filterOp := "WHERE"
	if filter != nil {
		if filter.IsContract != nil {
			args = append(args, *filter.IsContract)
			fmt.Fprintf(&sql, " %v is_contract = $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.MinTxCount != nil {
			args = append(args, *filter.MinTxCount)
			fmt.Fprintf(&sql, " %v tx_count >= $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.WithENSName {
			fmt.Fprintf(&sql, " %v ens_name IS NOT NULL", filterOp)
			filterOp = "AND"
		}
		if filter.SearchName != "" {
			args = append(args, "%"+filter.SearchName+"%")
			fmt.Fprintf(&sql, " %v (ens_name ILIKE $%v OR custom_name ILIKE $%v)", filterOp, len(args), len(args))
			filterOp = "AND"
		}
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY tx_count DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	// Get total count
	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	// Get results
	addresses := []*dbtypes.ElAddress{}
	err := ReaderDb.Select(&addresses, `SELECT * FROM cte`, args[:len(args)-1]...) // Exclude OFFSET from results query
	if err != nil {
		return nil, 0, err
	}

	return addresses, totalCount, nil
}

// UpsertElAddress inserts or updates an address
func UpsertElAddress(address *dbtypes.ElAddress, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_addresses ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_addresses ",
		}),
		"(address, is_contract, contract_creator, creation_tx_hash, creation_block_number, creation_timestamp, ",
		"first_seen_block, first_seen_timestamp, last_seen_block, last_seen_timestamp, ",
		"tx_count, in_tx_count, out_tx_count, balance, balance_updated_block, ",
		"ens_name, ens_updated_at, custom_name, contract_bytecode_hash, contract_verified) ",
		"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20) ",
	)
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			ON CONFLICT (address) DO UPDATE SET
				is_contract = EXCLUDED.is_contract,
				contract_creator = COALESCE(el_addresses.contract_creator, EXCLUDED.contract_creator),
				creation_tx_hash = COALESCE(el_addresses.creation_tx_hash, EXCLUDED.creation_tx_hash),
				creation_block_number = COALESCE(el_addresses.creation_block_number, EXCLUDED.creation_block_number),
				creation_timestamp = COALESCE(el_addresses.creation_timestamp, EXCLUDED.creation_timestamp),
				last_seen_block = GREATEST(el_addresses.last_seen_block, EXCLUDED.last_seen_block),
				last_seen_timestamp = GREATEST(el_addresses.last_seen_timestamp, EXCLUDED.last_seen_timestamp),
				tx_count = el_addresses.tx_count + EXCLUDED.tx_count,
				in_tx_count = el_addresses.in_tx_count + EXCLUDED.in_tx_count,
				out_tx_count = el_addresses.out_tx_count + EXCLUDED.out_tx_count,
				balance = CASE WHEN EXCLUDED.balance_updated_block > el_addresses.balance_updated_block THEN EXCLUDED.balance ELSE el_addresses.balance END,
				balance_updated_block = GREATEST(el_addresses.balance_updated_block, EXCLUDED.balance_updated_block),
				ens_name = COALESCE(EXCLUDED.ens_name, el_addresses.ens_name),
				ens_updated_at = COALESCE(EXCLUDED.ens_updated_at, el_addresses.ens_updated_at),
				custom_name = COALESCE(EXCLUDED.custom_name, el_addresses.custom_name),
				contract_bytecode_hash = COALESCE(EXCLUDED.contract_bytecode_hash, el_addresses.contract_bytecode_hash),
				contract_verified = EXCLUDED.contract_verified OR el_addresses.contract_verified
		`,
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(),
		address.Address,
		address.IsContract,
		address.ContractCreator,
		address.CreationTxHash,
		address.CreationBlockNumber,
		address.CreationTimestamp,
		address.FirstSeenBlock,
		address.FirstSeenTimestamp,
		address.LastSeenBlock,
		address.LastSeenTimestamp,
		address.TxCount,
		address.InTxCount,
		address.OutTxCount,
		address.Balance,
		address.BalanceUpdatedBlock,
		address.EnsName,
		address.EnsUpdatedAt,
		address.CustomName,
		address.ContractBytecodeHash,
		address.ContractVerified,
	)
	return err
}

// UpdateAddressBalance updates the balance for an address
func UpdateAddressBalance(address []byte, balance []byte, blockNumber uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		UPDATE el_addresses
		SET balance = $1, balance_updated_block = $2
		WHERE address = $3 AND balance_updated_block < $2
	`, balance, blockNumber, address)
	return err
}

// UpdateENSName updates the ENS name for an address
func UpdateENSName(address []byte, ensName string, timestamp uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		UPDATE el_addresses
		SET ens_name = $1, ens_updated_at = $2
		WHERE address = $3
	`, ensName, timestamp, address)
	return err
}

// GetTopAddressesByBalance retrieves the richest addresses
func GetTopAddressesByBalance(limit uint) ([]*dbtypes.ElAddress, error) {
	addresses := []*dbtypes.ElAddress{}
	err := ReaderDb.Select(&addresses, `
		SELECT *
		FROM el_addresses
		WHERE balance != $1
		ORDER BY balance DESC
		LIMIT $2
	`, []byte{0}, limit)
	return addresses, err
}
