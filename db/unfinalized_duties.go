package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertUnfinalizedDuty(ctx context.Context, tx *sqlx.Tx, duty *dbtypes.UnfinalizedDuty) error {
	_, err := tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO unfinalized_duties (
				epoch, dependent_root, duties
			) VALUES ($1, $2, $3)
			ON CONFLICT (epoch, dependent_root) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE INTO unfinalized_duties (
				epoch, dependent_root, duties
			) VALUES ($1, $2, $3)`,
	}), duty.Epoch, duty.DependentRoot, duty.DutiesSSZ)
	if err != nil {
		return err
	}
	return nil
}

func StreamUnfinalizedDuties(ctx context.Context, epoch uint64, cb func(duty *dbtypes.UnfinalizedDuty)) error {
	var sql strings.Builder
	args := []any{epoch}

	fmt.Fprint(&sql, `SELECT epoch, dependent_root, duties FROM unfinalized_duties WHERE epoch >= $1`)

	rows, err := ReaderDb.QueryContext(ctx, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized duties: %v", err)
		return nil
	}

	for rows.Next() {
		duty := dbtypes.UnfinalizedDuty{}
		err := rows.Scan(&duty.Epoch, &duty.DependentRoot, &duty.DutiesSSZ)
		if err != nil {
			logger.Errorf("Error while scanning unfinalized duty: %v", err)
			return err
		}
		cb(&duty)
	}

	return nil
}

func GetUnfinalizedDuty(ctx context.Context, epoch uint64, dependentRoot []byte) *dbtypes.UnfinalizedDuty {
	duty := dbtypes.UnfinalizedDuty{}
	err := ReaderDb.GetContext(ctx, &duty, `
	SELECT epoch, dependent_root, duties
	FROM unfinalized_duties
	WHERE epoch = $1 AND dependent_root = $2
	`, epoch, dependentRoot)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized duty %v/0x%x: %v", epoch, dependentRoot, err)
		return nil
	}
	return &duty
}

func DeleteUnfinalizedDutiesBefore(ctx context.Context, tx *sqlx.Tx, epoch uint64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM unfinalized_duties WHERE epoch < $1`, epoch)
	if err != nil {
		return err
	}
	return nil
}
