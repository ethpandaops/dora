package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertSlotAssignments(slotAssignments []*dbtypes.SlotAssignment, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "INSERT INTO slot_assignments (slot, proposer) VALUES ",
		dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO slot_assignments (slot, proposer) VALUES ",
	}))
	argIdx := 0
	args := make([]any, len(slotAssignments)*2)
	for i, slotAssignment := range slotAssignments {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "($%v, $%v)", argIdx+1, argIdx+2)
		args[argIdx] = slotAssignment.Slot
		args[argIdx+1] = slotAssignment.Proposer
		argIdx += 2
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot) DO UPDATE SET proposer = excluded.proposer",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetSlotAssignmentsForSlots(firstSlot uint64, lastSlot uint64) []*dbtypes.SlotAssignment {
	assignments := []*dbtypes.SlotAssignment{}
	err := ReaderDb.Select(&assignments, `
	SELECT
		slot, proposer
	FROM slot_assignments
	WHERE slot <= $1 AND slot >= $2 
	`, firstSlot, lastSlot)
	if err != nil {
		logger.Errorf("Error while fetching slot assignments: %v", err)
		return nil
	}
	return assignments
}

func GetSlotAssignment(slot uint64) *dbtypes.SlotAssignment {
	assignment := dbtypes.SlotAssignment{}
	err := ReaderDb.Get(&assignment, `
	SELECT
		slot, proposer
	FROM slot_assignments
	WHERE slot = $1 
	`, slot)
	if err != nil {
		return nil
	}
	return &assignment
}
