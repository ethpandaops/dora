package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElToken(ctx context.Context, dbTx *sqlx.Tx, token *dbtypes.ElToken) (uint64, error) {
	var id uint64
	query := EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "INSERT INTO el_tokens (contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id",
		dbtypes.DBEngineSqlite: "INSERT INTO el_tokens (contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
	})

	if DbEngine == dbtypes.DBEnginePgsql {
		err := dbTx.QueryRowContext(ctx, query, token.Contract, token.TokenType, token.Name, token.Symbol, token.Decimals, token.Flags, token.MetadataURI, token.NameSynced).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		result, err := dbTx.ExecContext(ctx, query, token.Contract, token.TokenType, token.Name, token.Symbol, token.Decimals, token.Flags, token.MetadataURI, token.NameSynced)
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

func GetElTokenByID(ctx context.Context, id uint64) (*dbtypes.ElToken, error) {
	token := &dbtypes.ElToken{}
	err := ReaderDb.GetContext(ctx, token, "SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced FROM el_tokens WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func GetElTokenByContract(ctx context.Context, contract []byte) (*dbtypes.ElToken, error) {
	token := &dbtypes.ElToken{}
	err := ReaderDb.GetContext(ctx, token, "SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced FROM el_tokens WHERE contract = $1", contract)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func UpdateElToken(ctx context.Context, dbTx *sqlx.Tx, token *dbtypes.ElToken) error {
	_, err := dbTx.ExecContext(ctx, "UPDATE el_tokens SET contract = $1, token_type = $2, name = $3, symbol = $4, decimals = $5, flags = $6, metadata_uri = $7, name_synced = $8 WHERE id = $9",
		token.Contract, token.TokenType, token.Name, token.Symbol, token.Decimals, token.Flags, token.MetadataURI, token.NameSynced, token.ID)
	return err
}

// GetElTokensByContracts retrieves multiple tokens by their contract addresses in a single query.
// Returns a map from contract address (as hex string) to token for efficient lookup.
func GetElTokensByContracts(ctx context.Context, contracts [][]byte) (map[string]*dbtypes.ElToken, error) {
	if len(contracts) == 0 {
		return make(map[string]*dbtypes.ElToken), nil
	}

	var sql strings.Builder
	args := make([]any, len(contracts))

	fmt.Fprint(&sql, "SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced FROM el_tokens WHERE contract IN (")
	for i, contract := range contracts {
		args[i] = contract
	}
	appendDollarPlaceholders(&sql, 1, len(contracts), ", ")
	fmt.Fprint(&sql, ")")

	tokens := []*dbtypes.ElToken{}
	err := ReaderDb.SelectContext(ctx, &tokens, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el tokens by contracts: %v", err)
		return nil, err
	}

	// Build map from contract to token
	result := make(map[string]*dbtypes.ElToken, len(tokens))
	for _, token := range tokens {
		result[byteSliceMapKey(token.Contract)] = token
	}
	return result, nil
}

// GetElTokensByIDs retrieves multiple tokens by their IDs in a single query.
func GetElTokensByIDs(ctx context.Context, ids []uint64) ([]*dbtypes.ElToken, error) {
	if len(ids) == 0 {
		return []*dbtypes.ElToken{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(ids))

	fmt.Fprint(&sql, "SELECT id, contract, token_type, name, symbol, decimals, flags, metadata_uri, name_synced FROM el_tokens WHERE id IN (")
	for i, id := range ids {
		args[i] = id
	}
	appendDollarPlaceholders(&sql, 1, len(ids), ", ")
	fmt.Fprint(&sql, ")")

	tokens := []*dbtypes.ElToken{}
	err := ReaderDb.SelectContext(ctx, &tokens, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el tokens by IDs: %v", err)
		return nil, err
	}
	return tokens, nil
}
