package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertVoluntaryExits(voluntaryExits []*dbtypes.VoluntaryExit, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO voluntary_exits ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO voluntary_exits ",
		}),
		"(slot_number, slot_index, slot_root, orphaned, validator)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 5

	args := make([]any, len(voluntaryExits)*fieldCount)
	for i, voluntaryExit := range voluntaryExits {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "(")
		for f := 0; f < fieldCount; f++ {
			if f > 0 {
				fmt.Fprintf(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", argIdx+f+1)

		}
		fmt.Fprintf(&sql, ")")

		args[argIdx+0] = voluntaryExit.SlotNumber
		args[argIdx+1] = voluntaryExit.SlotIndex
		args[argIdx+2] = voluntaryExit.SlotRoot
		args[argIdx+3] = voluntaryExit.Orphaned
		args[argIdx+4] = voluntaryExit.ValidatorIndex
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_root, slot_index) DO UPDATE SET orphaned = excluded.orphaned",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetVoluntaryExitForValidator(validator uint64) *dbtypes.VoluntaryExit {
	var sql strings.Builder
	args := []any{
		validator,
	}
	fmt.Fprint(&sql, `
	SELECT
		slot_number, slot_index, slot_root, orphaned, validator
	FROM voluntary_exits
	WHERE validator = $1
	`)

	voluntaryExit := &dbtypes.VoluntaryExit{}
	err := ReaderDb.Get(&voluntaryExit, sql.String(), args...)
	if err != nil {
		return nil
	}
	return voluntaryExit
}

func GetVoluntaryExitsFiltered(offset uint64, limit uint32, finalizedBlock uint64, filter *dbtypes.VoluntaryExitFilter) ([]*dbtypes.VoluntaryExit, uint64, error) {
	var sql strings.Builder
	args := []any{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			slot_number, slot_index, slot_root, orphaned, validator
		FROM voluntary_exits
	`)

	if filter.ValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names ON validator_names."index" = voluntary_exits.validator 
		`)
	}

	filterOp := "WHERE"
	if filter.MinSlot > 0 {
		args = append(args, filter.MinSlot)
		fmt.Fprintf(&sql, " %v slot_number >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxSlot > 0 {
		args = append(args, filter.MaxSlot)
		fmt.Fprintf(&sql, " %v slot_number <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinIndex > 0 {
		args = append(args, filter.MinIndex)
		fmt.Fprintf(&sql, " %v validator >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxIndex > 0 {
		args = append(args, filter.MaxIndex)
		fmt.Fprintf(&sql, " %v validator <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.WithOrphaned == 0 {
		args = append(args, finalizedBlock)
		fmt.Fprintf(&sql, " %v (slot_number > $%v OR orphaned = false)", filterOp, len(args))
		filterOp = "AND"
	} else if filter.WithOrphaned == 2 {
		args = append(args, finalizedBlock)
		fmt.Fprintf(&sql, " %v (slot_number > $%v OR orphaned = true)", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.ValidatorName != "" {
		args = append(args, "%"+filter.ValidatorName+"%")
		fmt.Fprintf(&sql, " %v ", filterOp)
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` validator_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` validator_names.name LIKE $%v `,
		}), len(args))

		filterOp = "AND"
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, `) 
	SELECT 
		count(*) AS slot_number, 
		0 AS slot_index,
		null AS slot_root,
		false AS orphaned, 
		0 AS validator
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY slot_number DESC, slot_index DESC
	LIMIT $%v 
	`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v ", len(args))
	}
	fmt.Fprintf(&sql, ") AS t1")

	voluntaryExits := []*dbtypes.VoluntaryExit{}
	err := ReaderDb.Select(&voluntaryExits, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered voluntary exits: %v", err)
		return nil, 0, err
	}

	return voluntaryExits[1:], voluntaryExits[0].SlotNumber, nil
}
