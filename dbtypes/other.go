package dbtypes

type AssignedBlock struct {
	Slot     uint64 `db:"slot"`
	Proposer uint64 `db:"proposer"`
	Block    *Block `db:"block"`
}

type AssignedBlob struct {
	Root       []byte `db:"root"`
	Commitment []byte `db:"commitment"`
	Slot       uint64 `db:"slot"`
	Blob       *Blob  `db:"blob"`
}

type BlockFilter struct {
	Graffiti      string
	ProposerIndex *uint64
	ProposerName  string
	WithOrphaned  uint8
	WithMissing   uint8
}
