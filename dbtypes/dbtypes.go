package dbtypes

type SyncState struct {
	Id       uint64 `db:"Id"`
	HeadSlot uint64 `db:"HeadSlot"`
	HeadRoot []byte `db:"HeadRoot"`
}

type Block struct {
	Slot                  uint64  `db:"Slot"`
	Root                  []byte  `db:"Root"`
	ParentRoot            []byte  `db:"ParentRoot"`
	StateRoot             []byte  `db:"StateRoot"`
	Orphaned              bool    `db:"Orphaned"`
	Proposer              uint64  `db:"Proposer"`
	Graffiti              []byte  `db:"Graffiti"`
	AttestationCount      uint64  `db:"AttestationCount"`
	DepositCount          uint64  `db:"DepositCount"`
	ExitCount             uint64  `db:"ExitCount"`
	AttesterSlashingCount uint64  `db:"AttesterSlashingCount"`
	ProposerSlashingCount uint64  `db:"ProposerSlashingCount"`
	SyncParticipation     float32 `db:"SyncParticipation"`
}

type Epoch struct {
	Epoch                 uint64  `db:"Epoch"`
	Finalized             bool    `db:"Finalized"`
	Eligible              uint64  `db:"Eligible"`
	Voted                 uint64  `db:"Voted"`
	BlockCount            uint16  `db:"BlockCount"`
	AttestationCount      uint64  `db:"AttestationCount"`
	DepositCount          uint64  `db:"DepositCount"`
	ExitCount             uint64  `db:"ExitCount"`
	AttesterSlashingCount uint64  `db:"AttesterSlashingCount"`
	ProposerSlashingCount uint64  `db:"ProposerSlashingCount"`
	SyncParticipation     float32 `db:"SyncParticipation"`
}

type OrphanedBlock struct {
	Root   []byte `db:"Root"`
	Header string `db:"Header"`
	Block  string `db:"Block"`
}
