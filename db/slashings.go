package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertSlashings(slashings []*dbtypes.Slashing, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO slashings ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO slashings ",
		}),
		"(slot_number, slot_index, slot_root, orphaned, validator, slasher, reason)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 7

	args := make([]any, len(slashings)*fieldCount)
	for i, slashing := range slashings {
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

		args[argIdx+0] = slashing.SlotNumber
		args[argIdx+1] = slashing.SlotIndex
		args[argIdx+2] = slashing.SlotRoot
		args[argIdx+3] = slashing.Orphaned
		args[argIdx+4] = slashing.ValidatorIndex
		args[argIdx+5] = slashing.SlasherIndex
		args[argIdx+6] = slashing.Reason
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_root, slot_index, validator) DO UPDATE SET orphaned = excluded.orphaned",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetSlashingForValidator(validator uint64) *dbtypes.Slashing {
	var sql strings.Builder
	args := []any{
		validator,
	}
	fmt.Fprint(&sql, `
	SELECT
		slot_number, slot_index, slot_root, orphaned, validator, slasher, reason
	FROM slashings
	WHERE validator = $1
	`)

	slashing := &dbtypes.Slashing{}
	err := ReaderDb.Get(&slashing, sql.String(), args...)
	if err != nil {
		return nil
	}
	return slashing
}

func GetSlashingsFiltered(offset uint64, limit uint32, finalizedBlock uint64, filter *dbtypes.SlashingFilter) ([]*dbtypes.Slashing, uint64, error) {
	var sql strings.Builder
	args := []any{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			slot_number, slot_index, slot_root, orphaned, validator, slasher, reason
		FROM slashings
	`)

	if filter.ValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names ON validator_names."index" = slashings.validator 
		`)
	}
	if filter.SlasherName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS slasher_names ON slasher_names."index" = slashings.slasher 
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
	if filter.WithReason > 0 {
		args = append(args, filter.WithReason)
		fmt.Fprintf(&sql, " %v reason = $%v", filterOp, len(args))
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
	if filter.SlasherName != "" {
		args = append(args, "%"+filter.SlasherName+"%")
		fmt.Fprintf(&sql, " %v ", filterOp)
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` slasher_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` slasher_names.name LIKE $%v `,
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
		0 AS validator,
		0 AS slasher,
		0 AS reason
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

	slashings := []*dbtypes.Slashing{}
	err := ReaderDb.Select(&slashings, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered slashings: %v", err)
		return nil, 0, err
	}

	return slashings[1:], slashings[0].SlotNumber, nil
}
