package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// GetElTokenContract retrieves a token contract by address
func GetElTokenContract(address []byte) (*dbtypes.ElTokenContract, error) {
	token := &dbtypes.ElTokenContract{}
	err := ReaderDb.Get(token, `
		SELECT *
		FROM el_token_contracts
		WHERE address = $1
	`, address)
	if err != nil {
		return nil, err
	}
	return token, nil
}

// GetElTokenContracts retrieves token contracts with optional filtering
func GetElTokenContracts(offset uint, limit uint, tokenType string) ([]*dbtypes.ElTokenContract, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_token_contracts
	`)

	if tokenType != "" {
		args = append(args, tokenType)
		fmt.Fprintf(&sql, " WHERE token_type = $%v", len(args))
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY transfer_count DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	tokens := []*dbtypes.ElTokenContract{}
	err := ReaderDb.Select(&tokens, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return tokens, totalCount, nil
}

// UpsertElTokenContract inserts or updates a token contract
func UpsertElTokenContract(token *dbtypes.ElTokenContract, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_token_contracts ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_token_contracts ",
		}),
		"(address, token_type, name, symbol, decimals, total_supply, discovered_block, discovered_timestamp, ",
		"holder_count, transfer_count, last_updated_block) ",
		"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ",
	)
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			ON CONFLICT (address) DO UPDATE SET
				name = COALESCE(EXCLUDED.name, el_token_contracts.name),
				symbol = COALESCE(EXCLUDED.symbol, el_token_contracts.symbol),
				decimals = COALESCE(EXCLUDED.decimals, el_token_contracts.decimals),
				total_supply = COALESCE(EXCLUDED.total_supply, el_token_contracts.total_supply),
				holder_count = EXCLUDED.holder_count,
				transfer_count = EXCLUDED.transfer_count,
				last_updated_block = GREATEST(el_token_contracts.last_updated_block, EXCLUDED.last_updated_block)
		`,
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(),
		token.Address,
		token.TokenType,
		token.Name,
		token.Symbol,
		token.Decimals,
		token.TotalSupply,
		token.DiscoveredBlock,
		token.DiscoveredTimestamp,
		token.HolderCount,
		token.TransferCount,
		token.LastUpdatedBlock,
	)
	return err
}

// GetElTokenBalance retrieves a token balance for an address
func GetElTokenBalance(address, tokenAddress []byte) (*dbtypes.ElTokenBalance, error) {
	balance := &dbtypes.ElTokenBalance{}
	err := ReaderDb.Get(balance, `
		SELECT *
		FROM el_token_balances
		WHERE address = $1 AND token_address = $2
	`, address, tokenAddress)
	if err != nil {
		return nil, err
	}
	return balance, nil
}

// GetElTokenBalancesByAddress retrieves all token balances for an address
func GetElTokenBalancesByAddress(address []byte) ([]*dbtypes.ElTokenBalance, error) {
	balances := []*dbtypes.ElTokenBalance{}
	err := ReaderDb.Select(&balances, `
		SELECT *
		FROM el_token_balances
		WHERE address = $1
		ORDER BY balance DESC
	`, address)
	return balances, err
}

// GetElTokenHolders retrieves all holders of a token
func GetElTokenHolders(tokenAddress []byte, offset uint, limit uint) ([]*dbtypes.ElTokenBalance, uint64, error) {
	var sql strings.Builder
	args := []interface{}{tokenAddress}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_token_balances
			WHERE token_address = $1
	`)

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY balance DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	balances := []*dbtypes.ElTokenBalance{}
	err := ReaderDb.Select(&balances, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return balances, totalCount, nil
}

// UpsertElTokenBalance inserts or updates a token balance
func UpsertElTokenBalance(balance *dbtypes.ElTokenBalance, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_token_balances ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_token_balances ",
		}),
		"(address, token_address, balance, updated_block, updated_timestamp) ",
		"VALUES ($1, $2, $3, $4, $5) ",
	)
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			ON CONFLICT (address, token_address) DO UPDATE SET
				balance = EXCLUDED.balance,
				updated_block = EXCLUDED.updated_block,
				updated_timestamp = EXCLUDED.updated_timestamp
			WHERE EXCLUDED.updated_block > el_token_balances.updated_block
		`,
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(),
		balance.Address,
		balance.TokenAddress,
		balance.Balance,
		balance.UpdatedBlock,
		balance.UpdatedTimestamp,
	)
	return err
}

// GetElTokenTransfersByTransaction retrieves all token transfers in a transaction
func GetElTokenTransfersByTransaction(txHash []byte) ([]*dbtypes.ElTokenTransfer, error) {
	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.Select(&transfers, `
		SELECT *
		FROM el_token_transfers
		WHERE transaction_hash = $1
		ORDER BY log_index ASC
	`, txHash)
	return transfers, err
}

// GetElTokenTransfersByAddress retrieves token transfers involving an address
func GetElTokenTransfersByAddress(address []byte, offset uint, limit uint) ([]*dbtypes.ElTokenTransfer, uint64, error) {
	var sql strings.Builder
	args := []interface{}{address, address}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_token_transfers
			WHERE (from_address = $1 OR to_address = $2)
	`)

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY block_timestamp DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.Select(&transfers, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return transfers, totalCount, nil
}

// GetElTokenTransfersByToken retrieves token transfers for a specific token
func GetElTokenTransfersByToken(tokenAddress []byte, offset uint, limit uint) ([]*dbtypes.ElTokenTransfer, uint64, error) {
	var sql strings.Builder
	args := []interface{}{tokenAddress}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_token_transfers
			WHERE token_address = $1
	`)

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY block_timestamp DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.Select(&transfers, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return transfers, totalCount, nil
}

// InsertElTokenTransfers inserts multiple token transfers
func InsertElTokenTransfers(transfers []*dbtypes.ElTokenTransfer, tx *sqlx.Tx) error {
	if len(transfers) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_token_transfers ",
			dbtypes.DBEngineSqlite: "INSERT INTO el_token_transfers ",
		}),
		"(transaction_hash, block_number, block_timestamp, log_index, token_address, token_type, ",
		"from_address, to_address, value, token_id) ",
		"VALUES ",
	)

	argIdx := 0
	fieldCount := 10
	args := make([]interface{}, len(transfers)*fieldCount)

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

		args[argIdx+0] = transfer.TransactionHash
		args[argIdx+1] = transfer.BlockNumber
		args[argIdx+2] = transfer.BlockTimestamp
		args[argIdx+3] = transfer.LogIndex
		args[argIdx+4] = transfer.TokenAddress
		args[argIdx+5] = transfer.TokenType
		args[argIdx+6] = transfer.FromAddress
		args[argIdx+7] = transfer.ToAddress
		args[argIdx+8] = transfer.Value
		args[argIdx+9] = transfer.TokenId
		argIdx += fieldCount
	}

	_, err := tx.Exec(sql.String(), args...)
	return err
}

// DeleteElTokenTransfersOlderThan deletes token transfers older than a given timestamp
func DeleteElTokenTransfersOlderThan(timestamp uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		DELETE FROM el_token_transfers
		WHERE block_timestamp < $1
	`, timestamp)
	return err
}

// GetElIndexerState retrieves the indexer state for a fork
func GetElIndexerState(forkId uint64) (*dbtypes.ElIndexerState, error) {
	state := &dbtypes.ElIndexerState{}
	err := ReaderDb.Get(state, `
		SELECT *
		FROM el_indexer_state
		WHERE fork_id = $1
	`, forkId)
	if err != nil {
		return nil, err
	}
	return state, nil
}

// SetElIndexerState updates the indexer state for a fork
func SetElIndexerState(state *dbtypes.ElIndexerState, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_indexer_state ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_indexer_state ",
		}),
		"(fork_id, last_indexed_block, last_indexed_timestamp) ",
		"VALUES ($1, $2, $3) ",
	)
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			ON CONFLICT (fork_id) DO UPDATE SET
				last_indexed_block = EXCLUDED.last_indexed_block,
				last_indexed_timestamp = EXCLUDED.last_indexed_timestamp
		`,
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), state.ForkId, state.LastIndexedBlock, state.LastIndexedTimestamp)
	return err
}
