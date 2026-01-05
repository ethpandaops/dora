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
		dbtypes.DBEnginePgsql:  "INSERT INTO el_tokens (contract, name, symbol, decimals, name_synced) VALUES ($1, $2, $3, $4, $5) RETURNING id",
		dbtypes.DBEngineSqlite: "INSERT INTO el_tokens (contract, name, symbol, decimals, name_synced) VALUES ($1, $2, $3, $4, $5)",
	})

	if DbEngine == dbtypes.DBEnginePgsql {
		err := dbTx.QueryRow(query, token.Contract, token.Name, token.Symbol, token.Decimals, token.NameSynced).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		result, err := dbTx.Exec(query, token.Contract, token.Name, token.Symbol, token.Decimals, token.NameSynced)
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
	err := ReaderDb.Get(token, "SELECT id, contract, name, symbol, decimals, name_synced FROM el_tokens WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func GetElTokenByContract(contract []byte) (*dbtypes.ElToken, error) {
	token := &dbtypes.ElToken{}
	err := ReaderDb.Get(token, "SELECT id, contract, name, symbol, decimals, name_synced FROM el_tokens WHERE contract = $1", contract)
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
		SELECT id, contract, name, symbol, decimals, name_synced
		FROM el_tokens
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS id,
		null AS contract,
		'' AS name,
		'' AS symbol,
		0 AS decimals,
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
		SELECT id, contract, name, symbol, decimals, name_synced
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
		'' AS name,
		'' AS symbol,
		0 AS decimals,
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
	_, err := dbTx.Exec("UPDATE el_tokens SET contract = $1, name = $2, symbol = $3, decimals = $4, name_synced = $5 WHERE id = $6",
		token.Contract, token.Name, token.Symbol, token.Decimals, token.NameSynced, token.ID)
	return err
}

func DeleteElToken(id uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_tokens WHERE id = $1", id)
	return err
}
