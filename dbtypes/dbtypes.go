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

type PayloadStatus uint8

const (
	PayloadStatusMissing PayloadStatus = iota
	PayloadStatusCanonical
	PayloadStatusOrphaned
)

type SlotHeader struct {
	Slot     uint64     `db:"slot"`
	Proposer uint64     `db:"proposer"`
	Status   SlotStatus `db:"status"`
}

type Slot struct {
	Slot                  uint64        `db:"slot"`
	Proposer              uint64        `db:"proposer"`
	Status                SlotStatus    `db:"status"`
	Root                  []byte        `db:"root"`
	ParentRoot            []byte        `db:"parent_root"`
	StateRoot             []byte        `db:"state_root"`
	Graffiti              []byte        `db:"graffiti"`
	GraffitiText          string        `db:"graffiti_text"`
	AttestationCount      uint64        `db:"attestation_count"`
	DepositCount          uint64        `db:"deposit_count"`
	ExitCount             uint64        `db:"exit_count"`
	WithdrawCount         uint64        `db:"withdraw_count"`
	WithdrawAmount        uint64        `db:"withdraw_amount"`
	AttesterSlashingCount uint64        `db:"attester_slashing_count"`
	ProposerSlashingCount uint64        `db:"proposer_slashing_count"`
	BLSChangeCount        uint64        `db:"bls_change_count"`
	EthTransactionCount   uint64        `db:"eth_transaction_count"`
	BlobCount             uint64        `db:"blob_count"`
	EthGasUsed            uint64        `db:"eth_gas_used"`
	EthGasLimit           uint64        `db:"eth_gas_limit"`
	EthBaseFee            uint64        `db:"eth_base_fee"`
	EthFeeRecipient       []byte        `db:"eth_fee_recipient"`
	EthBlockNumber        *uint64       `db:"eth_block_number"`
	EthBlockHash          []byte        `db:"eth_block_hash"`
	EthBlockExtra         []byte        `db:"eth_block_extra"`
	EthBlockExtraText     string        `db:"eth_block_extra_text"`
	SyncParticipation     float32       `db:"sync_participation"`
	ForkId                uint64        `db:"fork_id"`
	BlockSize             uint64        `db:"block_size"`
	RecvDelay             int32         `db:"recv_delay"`
	MinExecTime           uint32        `db:"min_exec_time"`
	MaxExecTime           uint32        `db:"max_exec_time"`
	ExecTimes             []byte        `db:"exec_times"`
	PayloadStatus         PayloadStatus `db:"payload_status"`
	BlockUid              uint64        `db:"block_uid"`
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
	BlobCount             uint64  `db:"blob_count"`
	EthGasUsed            uint64  `db:"eth_gas_used"`
	EthGasLimit           uint64  `db:"eth_gas_limit"`
	SyncParticipation     float32 `db:"sync_participation"`
	PayloadCount          uint64  `db:"payload_count"`
}

type OrphanedBlock struct {
	Root       []byte `db:"root"`
	HeaderVer  uint64 `db:"header_ver"`
	HeaderSSZ  []byte `db:"header_ssz"`
	BlockVer   uint64 `db:"block_ver"`
	BlockSSZ   []byte `db:"block_ssz"`
	PayloadVer uint64 `db:"payload_ver"`
	PayloadSSZ []byte `db:"payload_ssz"`
	BlockUid   uint64 `db:"block_uid"`
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

type UnfinalizedBlockStatus uint32

const (
	UnfinalizedBlockStatusUnprocessed UnfinalizedBlockStatus = iota
	UnfinalizedBlockStatusPruned
	UnfinalizedBlockStatusProcessed
)

type UnfinalizedBlock struct {
	Root        []byte                 `db:"root"`
	Slot        uint64                 `db:"slot"`
	HeaderVer   uint64                 `db:"header_ver"`
	HeaderSSZ   []byte                 `db:"header_ssz"`
	BlockVer    uint64                 `db:"block_ver"`
	BlockSSZ    []byte                 `db:"block_ssz"`
	PayloadVer  uint64                 `db:"payload_ver"`
	PayloadSSZ  []byte                 `db:"payload_ssz"`
	Status      UnfinalizedBlockStatus `db:"status"`
	ForkId      uint64                 `db:"fork_id"`
	RecvDelay   int32                  `db:"recv_delay"`
	MinExecTime uint32                 `db:"min_exec_time"`
	MaxExecTime uint32                 `db:"max_exec_time"`
	ExecTimes   []byte                 `db:"exec_times"`
	BlockUid    uint64                 `db:"block_uid"`
}

type UnfinalizedEpoch struct {
	Epoch                 uint64  `db:"epoch"`
	DependentRoot         []byte  `db:"dependent_root"`
	EpochHeadRoot         []byte  `db:"epoch_head_root"`
	EpochHeadForkId       uint64  `db:"epoch_head_fork_id"`
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
	BlobCount             uint64  `db:"blob_count"`
	EthGasUsed            uint64  `db:"eth_gas_used"`
	EthGasLimit           uint64  `db:"eth_gas_limit"`
	SyncParticipation     float32 `db:"sync_participation"`
	PayloadCount          uint64  `db:"payload_count"`
}

type OrphanedEpoch struct {
	Epoch                 uint64  `db:"epoch"`
	DependentRoot         []byte  `db:"dependent_root"`
	EpochHeadRoot         []byte  `db:"epoch_head_root"`
	EpochHeadForkId       uint64  `db:"epoch_head_fork_id"`
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
	BlobCount             uint64  `db:"blob_count"`
	EthGasUsed            uint64  `db:"eth_gas_used"`
	EthGasLimit           uint64  `db:"eth_gas_limit"`
	SyncParticipation     float32 `db:"sync_participation"`
}

type Fork struct {
	ForkId     uint64 `db:"fork_id"`
	BaseSlot   uint64 `db:"base_slot"`
	BaseRoot   []byte `db:"base_root"`
	LeafSlot   uint64 `db:"leaf_slot"`
	LeafRoot   []byte `db:"leaf_root"`
	ParentFork uint64 `db:"parent_fork"`
}

type UnfinalizedDuty struct {
	Epoch         uint64 `db:"epoch"`
	DependentRoot []byte `db:"dependent_root"`
	DutiesSSZ     []byte `db:"duties"`
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

type MevBlock struct {
	SlotNumber     uint64 `db:"slot_number"`
	BlockHash      []byte `db:"block_hash"`
	BlockNumber    uint64 `db:"block_number"`
	BuilderPubkey  []byte `db:"builder_pubkey"`
	ProposerIndex  uint64 `db:"proposer_index"`
	Proposed       uint8  `db:"proposed"`
	SeenbyRelays   uint64 `db:"seenby_relays"`
	FeeRecipient   []byte `db:"fee_recipient"`
	TxCount        uint64 `db:"tx_count"`
	GasUsed        uint64 `db:"gas_used"`
	BlockValue     []byte `db:"block_value"`
	BlockValueGwei uint64 `db:"block_value_gwei"`
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
	ValidSignature        uint8  `db:"valid_signature"`
	Orphaned              bool   `db:"orphaned"`
	TxHash                []byte `db:"tx_hash"`
	TxSender              []byte `db:"tx_sender"`
	TxTarget              []byte `db:"tx_target"`
	ForkId                uint64 `db:"fork_id"`
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
	ForkId                uint64  `db:"fork_id"`
}

type DepositWithTx struct {
	Deposit
	BlockNumber    *uint64 `db:"block_number"`
	BlockTime      *uint64 `db:"block_time"`
	BlockRoot      []byte  `db:"block_root"`
	ValidSignature *uint8  `db:"valid_signature"`
	TxHash         []byte  `db:"tx_hash"`
	TxSender       []byte  `db:"tx_sender"`
	TxTarget       []byte  `db:"tx_target"`
}

type VoluntaryExit struct {
	SlotNumber     uint64 `db:"slot_number"`
	SlotIndex      uint64 `db:"slot_index"`
	SlotRoot       []byte `db:"slot_root"`
	Orphaned       bool   `db:"orphaned"`
	ValidatorIndex uint64 `db:"validator"`
	ForkId         uint64 `db:"fork_id"`
}

type SlashingReason uint8

const (
	UnspecifiedSlashing SlashingReason = iota
	ProposerSlashing
	AttesterSlashing
)

type Slashing struct {
	SlotNumber     uint64         `db:"slot_number"`
	SlotIndex      uint64         `db:"slot_index"`
	SlotRoot       []byte         `db:"slot_root"`
	Orphaned       bool           `db:"orphaned"`
	ValidatorIndex uint64         `db:"validator"`
	SlasherIndex   uint64         `db:"slasher"`
	Reason         SlashingReason `db:"reason"`
	ForkId         uint64         `db:"fork_id"`
}

const (
	ConsolidationRequestResultUnknown uint8 = 0
	ConsolidationRequestResultSuccess uint8 = 1

	// global errors
	ConsolidationRequestResultTotalBalanceTooLow uint8 = 10
	ConsolidationRequestResultQueueFull          uint8 = 11

	// source validator errors
	ConsolidationRequestResultSrcNotFound             uint8 = 20
	ConsolidationRequestResultSrcInvalidCredentials   uint8 = 21
	ConsolidationRequestResultSrcInvalidSender        uint8 = 22
	ConsolidationRequestResultSrcNotActive            uint8 = 23
	ConsolidationRequestResultSrcNotOldEnough         uint8 = 24
	ConsolidationRequestResultSrcHasPendingWithdrawal uint8 = 25

	// target validator errors
	ConsolidationRequestResultTgtNotFound           uint8 = 30
	ConsolidationRequestResultTgtInvalidCredentials uint8 = 31
	ConsolidationRequestResultTgtNotCompounding     uint8 = 33
	ConsolidationRequestResultTgtNotActive          uint8 = 34
)

type ConsolidationRequest struct {
	SlotNumber    uint64  `db:"slot_number"`
	SlotRoot      []byte  `db:"slot_root"`
	SlotIndex     uint64  `db:"slot_index"`
	Orphaned      bool    `db:"orphaned"`
	ForkId        uint64  `db:"fork_id"`
	SourceAddress []byte  `db:"source_address"`
	SourceIndex   *uint64 `db:"source_index"`
	SourcePubkey  []byte  `db:"source_pubkey"`
	TargetIndex   *uint64 `db:"target_index"`
	TargetPubkey  []byte  `db:"target_pubkey"`
	TxHash        []byte  `db:"tx_hash"`
	BlockNumber   uint64  `db:"block_number"`
	Result        uint8   `db:"result"`
}

type ConsolidationRequestTx struct {
	BlockNumber   uint64  `db:"block_number"`
	BlockIndex    uint64  `db:"block_index"`
	BlockTime     uint64  `db:"block_time"`
	BlockRoot     []byte  `db:"block_root"`
	ForkId        uint64  `db:"fork_id"`
	SourceAddress []byte  `db:"source_address"`
	SourcePubkey  []byte  `db:"source_pubkey"`
	SourceIndex   *uint64 `db:"source_index"`
	TargetPubkey  []byte  `db:"target_pubkey"`
	TargetIndex   *uint64 `db:"target_index"`
	TxHash        []byte  `db:"tx_hash"`
	TxSender      []byte  `db:"tx_sender"`
	TxTarget      []byte  `db:"tx_target"`
	DequeueBlock  uint64  `db:"dequeue_block"`
}

const (
	WithdrawalRequestResultUnknown uint8 = 0
	WithdrawalRequestResultSuccess uint8 = 1

	// global errors
	WithdrawalRequestResultQueueFull uint8 = 10

	// validator errors
	WithdrawalRequestResultValidatorNotFound             uint8 = 20
	WithdrawalRequestResultValidatorInvalidCredentials   uint8 = 21
	WithdrawalRequestResultValidatorInvalidSender        uint8 = 22
	WithdrawalRequestResultValidatorNotActive            uint8 = 23
	WithdrawalRequestResultValidatorNotOldEnough         uint8 = 24
	WithdrawalRequestResultValidatorNotCompounding       uint8 = 25
	WithdrawalRequestResultValidatorHasPendingWithdrawal uint8 = 26
	WithdrawalRequestResultValidatorBalanceTooLow        uint8 = 27
)

type WithdrawalRequest struct {
	SlotNumber      uint64  `db:"slot_number"`
	SlotRoot        []byte  `db:"slot_root"`
	SlotIndex       uint64  `db:"slot_index"`
	Orphaned        bool    `db:"orphaned"`
	ForkId          uint64  `db:"fork_id"`
	SourceAddress   []byte  `db:"source_address"`
	ValidatorIndex  *uint64 `db:"validator_index"`
	ValidatorPubkey []byte  `db:"validator_pubkey"`
	Amount          int64   `db:"amount"`
	TxHash          []byte  `db:"tx_hash"`
	BlockNumber     uint64  `db:"block_number"`
	Result          uint8   `db:"result"`
}

type WithdrawalRequestTx struct {
	BlockNumber     uint64  `db:"block_number"`
	BlockIndex      uint64  `db:"block_index"`
	BlockTime       uint64  `db:"block_time"`
	BlockRoot       []byte  `db:"block_root"`
	ForkId          uint64  `db:"fork_id"`
	SourceAddress   []byte  `db:"source_address"`
	ValidatorPubkey []byte  `db:"validator_pubkey"`
	ValidatorIndex  *uint64 `db:"validator_index"`
	Amount          int64   `db:"amount"`
	TxHash          []byte  `db:"tx_hash"`
	TxSender        []byte  `db:"tx_sender"`
	TxTarget        []byte  `db:"tx_target"`
	DequeueBlock    uint64  `db:"dequeue_block"`
}

type Validator struct {
	ValidatorIndex             uint64 `db:"validator_index"`
	Pubkey                     []byte `db:"pubkey"`
	WithdrawalCredentials      []byte `db:"withdrawal_credentials"`
	EffectiveBalance           uint64 `db:"effective_balance"`
	Slashed                    bool   `db:"slashed"`
	ActivationEligibilityEpoch int64  `db:"activation_eligibility_epoch"`
	ActivationEpoch            int64  `db:"activation_epoch"`
	ExitEpoch                  int64  `db:"exit_epoch"`
	WithdrawableEpoch          int64  `db:"withdrawable_epoch"`
}

// EL Explorer types

type ElBlock struct {
	BlockUid     uint64 `db:"block_uid"`
	Status       uint32 `db:"status"`
	Events       uint32 `db:"events"`
	Transactions uint32 `db:"transactions"`
	Transfers    uint32 `db:"transfers"`
}

type ElTransaction struct {
	BlockUid    uint64  `db:"block_uid"`
	TxHash      []byte  `db:"tx_hash"`
	FromID      uint64  `db:"from_id"`
	ToID        uint64  `db:"to_id"`
	Nonce       uint64  `db:"nonce"`
	Reverted    bool    `db:"reverted"`
	Amount      float64 `db:"amount"`
	AmountRaw   []byte  `db:"amount_raw"`
	MethodID    []byte  `db:"method_id"`
	GasLimit    uint64  `db:"gas_limit"`
	GasUsed     uint64  `db:"gas_used"`
	GasPrice    float64 `db:"gas_price"` // Legacy: gas price; EIP-1559+: maxFeePerGas (in Gwei)
	TipPrice    float64 `db:"tip_price"` // maxPriorityFeePerGas (in Gwei)
	BlobCount   uint32  `db:"blob_count"`
	BlockNumber uint64  `db:"block_number"`
	TxType      uint8   `db:"tx_type"`
	TxIndex     uint32  `db:"tx_index"`
	EffGasPrice float64 `db:"eff_gas_price"` // Effective gas price actually paid (in Gwei)
}

type ElTxEvent struct {
	BlockUid   uint64 `db:"block_uid"`
	TxHash     []byte `db:"tx_hash"`
	EventIndex uint32 `db:"event_index"`
	SourceID   uint64 `db:"source_id"`
	Topic1     []byte `db:"topic1"`
	Topic2     []byte `db:"topic2"`
	Topic3     []byte `db:"topic3"`
	Topic4     []byte `db:"topic4"`
	Topic5     []byte `db:"topic5"`
	Data       []byte `db:"data"`
}

type ElAccount struct {
	ID           uint64 `db:"id"`
	Address      []byte `db:"address"`
	FunderID     uint64 `db:"funder_id"`
	Funded       uint64 `db:"funded"`
	IsContract   bool   `db:"is_contract"`
	LastNonce    uint64 `db:"last_nonce"`
	LastBlockUid uint64 `db:"last_block_uid"`
}

// Token type constants
const (
	TokenTypeERC20   = 1
	TokenTypeERC721  = 2
	TokenTypeERC1155 = 3
)

// Token flag constants
const (
	TokenFlagMetadataLoaded = 0x01
)

type ElToken struct {
	ID          uint64 `db:"id"`
	Contract    []byte `db:"contract"`
	TokenType   uint8  `db:"token_type"`
	Name        string `db:"name"`
	Symbol      string `db:"symbol"`
	Decimals    uint8  `db:"decimals"`
	Flags       uint8  `db:"flags"`
	MetadataURI string `db:"metadata_uri"`
	NameSynced  uint64 `db:"name_synced"`
}

type ElBalance struct {
	AccountID  uint64  `db:"account_id"`
	TokenID    uint64  `db:"token_id"`
	Balance    float64 `db:"balance"`
	BalanceRaw []byte  `db:"balance_raw"`
	Updated    uint64  `db:"updated"`
}

type ElTokenTransfer struct {
	BlockUid   uint64  `db:"block_uid"`
	TxHash     []byte  `db:"tx_hash"`
	TxPos      uint32  `db:"tx_pos"` // Transaction index in block
	TxIdx      uint32  `db:"tx_idx"` // Transfer index within transaction
	TokenID    uint64  `db:"token_id"`
	TokenType  uint8   `db:"token_type"`
	TokenIndex []byte  `db:"token_index"`
	FromID     uint64  `db:"from_id"`
	ToID       uint64  `db:"to_id"`
	Amount     float64 `db:"amount"`
	AmountRaw  []byte  `db:"amount_raw"`
}

// Withdrawal types
const (
	WithdrawalTypeBeaconWithdrawal = 0
	WithdrawalTypeFeeRecipient     = 1
)

type ElWithdrawal struct {
	BlockUid  uint64  `db:"block_uid"`
	AccountID uint64  `db:"account_id"`
	Type      uint8   `db:"type"` // 0=withdrawal, 1=fee_recipient
	Amount    float64 `db:"amount"`
	AmountRaw []byte  `db:"amount_raw"`
	Validator *uint64 `db:"validator"` // validator index for withdrawals, null for fee recipient
}
