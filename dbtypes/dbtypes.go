package dbtypes

type ExplorerState struct {
	Key   string `db:"key"`
	Value string `db:"value"`
}

type Block struct {
	Root                  []byte  `db:"root"`
	Slot                  uint64  `db:"slot"`
	ParentRoot            []byte  `db:"parent_root"`
	StateRoot             []byte  `db:"state_root"`
	Orphaned              bool    `db:"orphaned"`
	Proposer              uint64  `db:"proposer"`
	Graffiti              []byte  `db:"graffiti"`
	AttestationCount      uint64  `db:"attestation_count"`
	DepositCount          uint64  `db:"deposit_count"`
	ExitCount             uint64  `db:"exit_count"`
	AttesterSlashingCount uint64  `db:"attester_slashing_count"`
	ProposerSlashingCount uint64  `db:"proposer_slashing_count"`
	BLSChangeCount        uint64  `db:"bls_change_count"`
	EthTransactionCount   uint64  `db:"eth_transaction_count"`
	EthBlockNumber        uint64  `db:"eth_block_number"`
	EthBlockHash          []byte  `db:"eth_block_hash"`
	SyncParticipation     float32 `db:"sync_participation"`
}

type Epoch struct {
	Epoch                 uint64  `db:"epoch"`
	ValidatorCount        uint64  `db:"validator_count"`
	Eligible              uint64  `db:"eligible"`
	VotedTarget           uint64  `db:"voted_target"`
	VotedHead             uint64  `db:"voted_head"`
	VotedTotal            uint64  `db:"voted_total"`
	BlockCount            uint16  `db:"block_count"`
	OrphanedCount         uint16  `db:"orphaned_count"`
	AttestationCount      uint64  `db:"attestation_count"`
	DepositCount          uint64  `db:"deposit_count"`
	ExitCount             uint64  `db:"exit_count"`
	AttesterSlashingCount uint64  `db:"attester_slashing_count"`
	ProposerSlashingCount uint64  `db:"proposer_slashing_count"`
	BLSChangeCount        uint64  `db:"bls_change_count"`
	EthTransactionCount   uint64  `db:"eth_transaction_count"`
	SyncParticipation     float32 `db:"sync_participation"`
}

type OrphanedBlock struct {
	Root   []byte `db:"root"`
	Header string `db:"header"`
	Block  string `db:"block"`
}
