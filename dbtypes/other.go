package dbtypes

type AssignedBlock struct {
	Slot     uint64 `db:"slot"`
	Proposer uint64 `db:"proposer"`
	Block    *Block `db:"block"`
}
