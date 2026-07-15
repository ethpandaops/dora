package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func GetValidatorNames(ctx context.Context, minIdx uint64, maxIdx uint64) []*dbtypes.ValidatorName {
	names := []*dbtypes.ValidatorName{}
	err := ReaderDb.SelectContext(ctx, &names, `SELECT "index", "name" FROM validator_names WHERE "index" >= $1 AND "index" <= $2`, minIdx, maxIdx)
	if err != nil {
		logger.Errorf("Error while fetching validator names: %v", err)
		return nil
	}
	return names
}

func InsertValidatorNames(ctx context.Context, tx *sqlx.Tx, validatorNames []*dbtypes.ValidatorName) error {
	var sql strings.Builder
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  `INSERT INTO validator_names ("index", "name") VALUES `,
		dbtypes.DBEngineSqlite: `INSERT OR REPLACE INTO validator_names ("index", "name") VALUES `,
	}))
	argIdx := 0
	args := make([]any, len(validatorNames)*2)
	for i, validatorName := range validatorNames {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "($%v, $%v)", argIdx+1, argIdx+2)
		args[argIdx] = validatorName.Index
		args[argIdx+1] = validatorName.Name
		argIdx += 2
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  ` ON CONFLICT ("index") DO UPDATE SET name = excluded.name`,
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func DeleteValidatorNames(ctx context.Context, tx *sqlx.Tx, validatorNames []uint64) error {
	var sql strings.Builder
	fmt.Fprint(&sql, `DELETE FROM validator_names WHERE "index" IN (`)
	argIdx := 0
	args := make([]any, len(validatorNames))
	for i, validatorName := range validatorNames {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%v", argIdx+1)
		args[argIdx] = validatorName
		argIdx += 1
	}
	fmt.Fprint(&sql, ")")
	_, err := tx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}
