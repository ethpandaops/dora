package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertConsolidationRequests(consolidations []*dbtypes.ConsolidationRequest, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO consolidation_requests ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO consolidation_requests ",
		}),
		"(slot_number, slot_root, slot_index, orphaned, fork_id, source_address, source_index, source_pubkey, target_index, target_pubkey, tx_hash)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 11

	args := make([]interface{}, len(consolidations)*fieldCount)
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
		args[argIdx+1] = consolidation.SlotRoot[:]
		args[argIdx+2] = consolidation.SlotIndex
		args[argIdx+3] = consolidation.Orphaned
		args[argIdx+4] = consolidation.ForkId
		args[argIdx+5] = consolidation.SourceAddress[:]
		args[argIdx+6] = consolidation.SourceIndex
		args[argIdx+7] = consolidation.SourcePubkey[:]
		args[argIdx+8] = consolidation.TargetIndex
		args[argIdx+9] = consolidation.TargetPubkey[:]
		args[argIdx+10] = consolidation.TxHash[:]
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_index, slot_root) DO UPDATE SET orphaned = excluded.orphaned, fork_id = excluded.fork_id",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}
