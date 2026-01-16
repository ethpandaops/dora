package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElToken(token *dbtypes.ElToken, dbTx *sqlx.Tx) (uint64, error) {
	var id uint64
	query := EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "INSERT INTO el_tokens (contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id",
		dbtypes.DBEngineSqlite: "INSERT INTO el_tokens (contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
	})

	if DbEngine == dbtypes.DBEnginePgsql {
		err := dbTx.QueryRow(query, token.Contract, token.TokenType, token.Name, token.Symbol, token.Decimals, token.Flags, token.MetadataURI, token.NameSynced).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		result, err := dbTx.Exec(query, token.Contract, token.TokenType, token.Name, token.Symbol, token.Decimals, token.Flags, token.MetadataURI, token.NameSynced)
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

func GetElTokenByID(id uint64) (*dbtypes.ElToken, error) {
	token := &dbtypes.ElToken{}
	err := ReaderDb.Get(token, "SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced FROM el_tokens WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func GetElTokenByContract(contract []byte) (*dbtypes.ElToken, error) {
	token := &dbtypes.ElToken{}
	err := ReaderDb.Get(token, "SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced FROM el_tokens WHERE contract = $1", contract)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func GetElTokens(offset uint64, limit uint32) ([]*dbtypes.ElToken, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced
		FROM el_tokens
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS id,
		null AS contract,
		0 AS token_type,
		'' AS name,
		'' AS symbol,
		0 AS decimals,
		0 AS flags,
		'' AS metadata_uri,
		0 AS name_synced
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY id ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	tokens := []*dbtypes.ElToken{}
	err := ReaderDb.Select(&tokens, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el tokens: %v", err)
		return nil, 0, err
	}

	if len(tokens) == 0 {
		return []*dbtypes.ElToken{}, 0, nil
	}

	count := tokens[0].ID
	return tokens[1:], count, nil
}

func GetElTokensFiltered(offset uint64, limit uint32, filter *dbtypes.ElTokenFilter) ([]*dbtypes.ElToken, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced
		FROM el_tokens
	`)

	filterOp := "WHERE"
	if len(filter.Contract) > 0 {
		args = append(args, filter.Contract)
		fmt.Fprintf(&sql, " %v contract = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.Name != "" {
		args = append(args, "%"+filter.Name+"%")
		fmt.Fprintf(&sql, " %v name LIKE $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.Symbol != "" {
		args = append(args, "%"+filter.Symbol+"%")
		fmt.Fprintf(&sql, " %v symbol LIKE $%v", filterOp, len(args))
		filterOp = "AND"
	}

	fmt.Fprint(&sql, ")")

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS id,
		null AS contract,
		0 AS token_type,
		'' AS name,
		'' AS symbol,
		0 AS decimals,
		0 AS flags,
		'' AS metadata_uri,
		0 AS name_synced
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY id ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	tokens := []*dbtypes.ElToken{}
	err := ReaderDb.Select(&tokens, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el tokens: %v", err)
		return nil, 0, err
	}

	if len(tokens) == 0 {
		return []*dbtypes.ElToken{}, 0, nil
	}

	count := tokens[0].ID
	return tokens[1:], count, nil
}

func UpdateElToken(token *dbtypes.ElToken, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("UPDATE el_tokens SET contract = $1, token_type = $2, name = $3, symbol = $4, decimals = $5, flags = $6, metadata_uri = $7, name_synced = $8 WHERE id = $9",
		token.Contract, token.TokenType, token.Name, token.Symbol, token.Decimals, token.Flags, token.MetadataURI, token.NameSynced, token.ID)
	return err
}

func DeleteElToken(id uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_tokens WHERE id = $1", id)
	return err
}

// GetElTokensByContracts retrieves multiple tokens by their contract addresses in a single query.
// Returns a map from contract address (as hex string) to token for efficient lookup.
func GetElTokensByContracts(contracts [][]byte) (map[string]*dbtypes.ElToken, error) {
	if len(contracts) == 0 {
		return make(map[string]*dbtypes.ElToken), nil
	}

	var sql strings.Builder
	args := make([]any, len(contracts))

	fmt.Fprint(&sql, "SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced FROM el_tokens WHERE contract IN (")
	for i, contract := range contracts {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%d", i+1)
		args[i] = contract
	}
	fmt.Fprint(&sql, ")")

	tokens := []*dbtypes.ElToken{}
	err := ReaderDb.Select(&tokens, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el tokens by contracts: %v", err)
		return nil, err
	}

	// Build map from contract to token
	result := make(map[string]*dbtypes.ElToken, len(tokens))
	for _, token := range tokens {
		// Use hex string of contract address as key for efficient lookup
		key := fmt.Sprintf("%x", token.Contract)
		result[key] = token
	}
	return result, nil
}

// GetElTokensByIDs retrieves multiple tokens by their IDs in a single query.
func GetElTokensByIDs(ids []uint64) ([]*dbtypes.ElToken, error) {
	if len(ids) == 0 {
		return []*dbtypes.ElToken{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(ids))

	fmt.Fprint(&sql, "SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced FROM el_tokens WHERE id IN (")
	for i, id := range ids {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%d", i+1)
		args[i] = id
	}
	fmt.Fprint(&sql, ")")

	tokens := []*dbtypes.ElToken{}
	err := ReaderDb.Select(&tokens, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el tokens by IDs: %v", err)
		return nil, err
	}
	return tokens, nil
}
