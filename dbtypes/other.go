package dbtypes

type AssignedSlot struct {
	Slot     uint64 `db:"slot"`
	Proposer uint64 `db:"proposer"`
	Block    *Slot  `db:"block"`
}

type BlockStatus struct {
	Root   []byte     `db:"root"`
	Status SlotStatus `db:"status"`
}

type AssignedBlob struct {
	Root       []byte `db:"root"`
	Commitment []byte `db:"commitment"`
	Slot       uint64 `db:"slot"`
	Blob       *Blob  `db:"blob"`
}

type BlockFilter struct {
	Graffiti      string
	ExtraData     string
	ProposerIndex *uint64
	ProposerName  string
	WithOrphaned  uint8
	WithMissing   uint8
}

type DepositTxFilter struct {
	Address       string
	PublicKey     string
	ValidatorName string
	MinAmount     uint64
	MaxAmount     uint64
	WithOrphaned  uint8
	WithValid     uint8
}
