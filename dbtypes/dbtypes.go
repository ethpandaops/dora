package dbtypes

type ExplorerState struct {
	Key   string `db:"key"`
	Value string `db:"value"`
}

type ValidatorName struct {
	Index uint64 `db:"index"`
	Name  string `db:"name"`
}

type SlotStatus uint8

const (
	Missing SlotStatus = iota
	Canonical
	Orphaned
)

type SlotHeader struct {
	Slot     uint64     `db:"slot"`
	Proposer uint64     `db:"proposer"`
	Status   SlotStatus `db:"status"`
}

type Slot struct {
	Slot                  uint64     `db:"slot"`
	Proposer              uint64     `db:"proposer"`
	Status                SlotStatus `db:"status"`
	Root                  []byte     `db:"root"`
	ParentRoot            []byte     `db:"parent_root"`
	StateRoot             []byte     `db:"state_root"`
	Graffiti              []byte     `db:"graffiti"`
	GraffitiText          string     `db:"graffiti_text"`
	AttestationCount      uint64     `db:"attestation_count"`
	DepositCount          uint64     `db:"deposit_count"`
	ExitCount             uint64     `db:"exit_count"`
	WithdrawCount         uint64     `db:"withdraw_count"`
	WithdrawAmount        uint64     `db:"withdraw_amount"`
	AttesterSlashingCount uint64     `db:"attester_slashing_count"`
	ProposerSlashingCount uint64     `db:"proposer_slashing_count"`
	BLSChangeCount        uint64     `db:"bls_change_count"`
	EthTransactionCount   uint64     `db:"eth_transaction_count"`
	EthBlockNumber        *uint64    `db:"eth_block_number"`
	EthBlockHash          []byte     `db:"eth_block_hash"`
	EthBlockExtra         []byte     `db:"eth_block_extra"`
	EthBlockExtraText     string     `db:"eth_block_extra_text"`
	SyncParticipation     float32    `db:"sync_participation"`
}

type Epoch struct {
	Epoch                 uint64  `db:"epoch"`
	ValidatorCount        uint64  `db:"validator_count"`
	ValidatorBalance      uint64  `db:"validator_balance"`
	Eligible              uint64  `db:"eligible"`
	VotedTarget           uint64  `db:"voted_target"`
	VotedHead             uint64  `db:"voted_head"`
	VotedTotal            uint64  `db:"voted_total"`
	BlockCount            uint16  `db:"block_count"`
	OrphanedCount         uint16  `db:"orphaned_count"`
	AttestationCount      uint64  `db:"attestation_count"`
	DepositCount          uint64  `db:"deposit_count"`
	ExitCount             uint64  `db:"exit_count"`
	WithdrawCount         uint64  `db:"withdraw_count"`
	WithdrawAmount        uint64  `db:"withdraw_amount"`
	AttesterSlashingCount uint64  `db:"attester_slashing_count"`
	ProposerSlashingCount uint64  `db:"proposer_slashing_count"`
	BLSChangeCount        uint64  `db:"bls_change_count"`
	EthTransactionCount   uint64  `db:"eth_transaction_count"`
	SyncParticipation     float32 `db:"sync_participation"`
}

type OrphanedBlock struct {
	Root      []byte `db:"root"`
	HeaderVer uint64 `db:"header_ver"`
	HeaderSSZ []byte `db:"header_ssz"`
	BlockVer  uint64 `db:"block_ver"`
	BlockSSZ  []byte `db:"block_ssz"`
}

type SlotAssignment struct {
	Slot     uint64 `db:"slot"`
	Proposer uint64 `db:"proposer"`
}

type SyncAssignment struct {
	Period    uint64 `db:"period"`
	Index     uint32 `db:"index"`
	Validator uint64 `db:"validator"`
}

type UnfinalizedBlock struct {
	Root      []byte `db:"root"`
	Slot      uint64 `db:"slot"`
	HeaderVer uint64 `db:"header_ver"`
	HeaderSSZ []byte `db:"header_ssz"`
	BlockVer  uint64 `db:"block_ver"`
	BlockSSZ  []byte `db:"block_ssz"`
}

type Blob struct {
	Commitment []byte  `db:"commitment"`
	Proof      []byte  `db:"proof"`
	Size       uint32  `db:"size"`
	Blob       *[]byte `db:"blob"`
}

type BlobAssignment struct {
	Root       []byte `db:"root"`
	Commitment []byte `db:"commitment"`
	Slot       uint64 `db:"slot"`
}

type TxFunctionSignature struct {
	Signature string `db:"signature"`
	Bytes     []byte `db:"bytes"`
	Name      string `db:"name"`
}

type TxUnknownFunctionSignature struct {
	Bytes     []byte `db:"bytes"`
	LastCheck uint64 `db:"lastcheck"`
}

type TxPendingFunctionSignature struct {
	Bytes     []byte `db:"bytes"`
	QueueTime uint64 `db:"queuetime"`
}

type DepositTx struct {
	Index                 uint64 `db:"deposit_index"`
	BlockNumber           uint64 `db:"block_number"`
	BlockTime             uint64 `db:"block_time"`
	BlockRoot             []byte `db:"block_root"`
	PublicKey             []byte `db:"publickey"`
	WithdrawalCredentials []byte `db:"withdrawalcredentials"`
	Amount                uint64 `db:"amount"`
	Signature             []byte `db:"signature"`
	ValidSignature        bool   `db:"valid_signature"`
	Orphaned              bool   `db:"orphaned"`
	TxHash                []byte `db:"tx_hash"`
	TxSender              []byte `db:"tx_sender"`
	TxTarget              []byte `db:"tx_target"`
}

type Deposit struct {
	Index                 *uint64 `db:"deposit_index"`
	SlotNumber            uint64  `db:"slot_number"`
	SlotIndex             uint64  `db:"slot_index"`
	SlotRoot              []byte  `db:"slot_root"`
	Orphaned              bool    `db:"orphaned"`
	PublicKey             []byte  `db:"publickey"`
	WithdrawalCredentials []byte  `db:"withdrawalcredentials"`
	Amount                uint64  `db:"amount"`
}

type Consolidation struct {
	SlotNumber  uint64 `db:"slot_number"`
	SlotIndex   uint64 `db:"slot_index"`
	SlotRoot    []byte `db:"slot_root"`
	Orphaned    bool   `db:"orphaned"`
	SourceIndex uint64 `db:"source_index"`
	TargetIndex uint64 `db:"target_index"`
	Epoch       uint64 `db:"epoch"`
}
