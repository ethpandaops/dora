package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

type Consolidation struct {
	SlotNumber  uint64 `db:"slot_number"`
	SlotIndex   uint64 `db:"slot_index"`
	SlotRoot    []byte `db:"slot_root"`
	Orphaned    bool   `db:"orphaned"`
	SourceIndex uint64 `db:"source_index"`
	TargetIndex uint64 `db:"target_index"`
	Epoch       uint64 `db:"epoch"`
}

func InsertConsolidations(consolidations []*dbtypes.Consolidation, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO consolidations ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO consolidations ",
		}),
		"(slot_number, slot_index, slot_root, orphaned, source_index, target_index, epoch)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 7

	args := make([]any, len(consolidations)*fieldCount)
	for i, consolidation := range consolidations {
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

		args[argIdx+0] = consolidation.SlotNumber
		args[argIdx+1] = consolidation.SlotIndex
		args[argIdx+2] = consolidation.SlotRoot[:]
		args[argIdx+3] = consolidation.Orphaned
		args[argIdx+4] = consolidation.SourceIndex
		args[argIdx+5] = consolidation.TargetIndex
		args[argIdx+6] = consolidation.Epoch
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_index, slot_root) DO UPDATE SET orphaned = excluded.orphaned",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}
