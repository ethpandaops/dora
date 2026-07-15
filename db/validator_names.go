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

func GetValidatorNameHistory(ctx context.Context) []*dbtypes.ValidatorNameHistory {
	rows := []*dbtypes.ValidatorNameHistory{}
	err := ReaderDb.SelectContext(ctx, &rows, `
		SELECT "range_start", "range_end", "start_slot", "end_slot", "name"
		FROM validator_name_history
		ORDER BY "start_slot" ASC, "range_start" ASC`)
	if err != nil {
		logger.Errorf("Error while fetching validator name history: %v", err)
		return nil
	}
	return rows
}

// ReplaceValidatorNameHistory replaces the full validator_name_history table.
// The table is range-compressed and devnet-sized (ranges x reassignments), so a full replace is cheap.
func ReplaceValidatorNameHistory(ctx context.Context, tx *sqlx.Tx, rows []*dbtypes.ValidatorNameHistory) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM validator_name_history`)
	if err != nil {
		return err
	}

	batchSize := 1000
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}

		var sql strings.Builder
		fmt.Fprint(&sql, `INSERT INTO validator_name_history ("range_start", "range_end", "start_slot", "end_slot", "name") VALUES `)
		args := make([]any, 0, (end-start)*5)
		for i, row := range rows[start:end] {
			if i > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "($%v, $%v, $%v, $%v, $%v)", len(args)+1, len(args)+2, len(args)+3, len(args)+4, len(args)+5)
			args = append(args, row.RangeStart, row.RangeEnd, row.StartSlot, row.EndSlot, row.Name)
		}
		_, err = tx.ExecContext(ctx, sql.String(), args...)
		if err != nil {
			return err
		}
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
