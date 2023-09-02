package dbtypes

type AssignedBlock struct {
	Slot     uint64 `db:"slot"`
	Proposer uint64 `db:"proposer"`
	Block    *Block `db:"block"`
}

type BlockFilter struct {
	Graffiti      string
	ProposerIndex *uint64
	ProposerName  string
	WithOrphaned  uint8
	WithMissing   uint8
}
