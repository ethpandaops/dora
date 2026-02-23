package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func IsSyncCommitteeSynchronized(ctx context.Context, period uint64) bool {
	var count uint64
	err := ReaderDb.GetContext(ctx, &count, `SELECT COUNT(*) FROM sync_assignments WHERE period = $1`, period)
	if err != nil {
		return false
	}
	return count > 0
}

func InsertSyncAssignments(ctx context.Context, tx *sqlx.Tx, syncAssignments []*dbtypes.SyncAssignment) error {
	var sql strings.Builder
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  `INSERT INTO sync_assignments (period, "index", validator) VALUES `,
		dbtypes.DBEngineSqlite: `INSERT OR REPLACE INTO sync_assignments (period, "index", validator) VALUES `,
	}))
	argIdx := 0
	args := make([]any, len(syncAssignments)*3)
	for i, slotAssignment := range syncAssignments {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "($%v, $%v, $%v)", argIdx+1, argIdx+2, argIdx+3)
		args[argIdx] = slotAssignment.Period
		args[argIdx+1] = slotAssignment.Index
		args[argIdx+2] = slotAssignment.Validator
		argIdx += 3
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  ` ON CONFLICT (period, "index") DO UPDATE SET validator = excluded.validator`,
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetSyncAssignmentsForPeriod(ctx context.Context, period uint64) []uint64 {
	assignments := []uint64{}
	err := ReaderDb.SelectContext(ctx, &assignments, `
	SELECT
		validator
	FROM sync_assignments
	WHERE period = $1
	ORDER BY "index" ASC
	`, period)
	if err != nil {
		logger.Errorf("Error while fetching sync assignments: %v", err)
		return nil
	}
	return assignments
}
