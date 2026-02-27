package dbtypes

import (
	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type AssignedSlot struct {
	Slot     uint64 `db:"slot"`
	Proposer uint64 `db:"proposer"`
	Block    *Slot  `db:"block"`
}

type BlockStatus struct {
	Root   []byte     `db:"root"`
	Status SlotStatus `db:"status"`
}

type BlockBlobCount struct {
	Root         []byte `db:"root"`
	EthBlockHash []byte `db:"eth_block_hash"`
	BlobCount    uint64 `db:"blob_count"`
}

type BlockHead struct {
	Slot       uint64 `db:"slot"`
	Root       []byte `db:"root"`
	ParentRoot []byte `db:"parent_root"`
	ForkId     uint64 `db:"fork_id"`
	BlockUid   uint64 `db:"block_uid"`
}

type AssignedBlob struct {
	Root       []byte `db:"root"`
	Commitment []byte `db:"commitment"`
	Slot       uint64 `db:"slot"`
	Blob       *Blob  `db:"blob"`
}

type UnfinalizedBlockFilter struct {
	MinSlot  uint64
	MaxSlot  uint64
	WithBody bool
}

type BlockFilter struct {
	Graffiti             string
	InvertGraffiti       bool
	ExtraData            string
	InvertExtraData      bool
	ProposerIndex        *uint64
	ProposerName         string
	InvertProposer       bool
	WithOrphaned         uint8
	WithMissing          uint8
	MinSyncParticipation *float32
	MaxSyncParticipation *float32
	MinExecTime          *uint32
	MaxExecTime          *uint32
	MinTxCount           *uint64
	MaxTxCount           *uint64
	MinBlobCount         *uint64
	MaxBlobCount         *uint64
	Slot                 *uint64  // Filter by specific slot number
	BlockRoot            []byte   // Filter by specific block root
	BlockUids            []uint64 // Filter by specific block UIDs (slot << 16 | unique_index)
	ForkIds              []uint64 // Filter by fork IDs
	EthBlockNumber       *uint64  // Filter by EL block number
	EthBlockHash         []byte   // Filter by EL block hash
	MinGasUsed           *uint64  // Filter by minimum gas used
	MaxGasUsed           *uint64  // Filter by maximum gas used
	MinGasLimit          *uint64  // Filter by minimum gas limit
	MaxGasLimit          *uint64  // Filter by maximum gas limit
	MinBlockSize         *uint64  // Filter by minimum block size (bytes)
	MaxBlockSize         *uint64  // Filter by maximum block size (bytes)
	WithMevBlock         uint8    // 0=hide mev, 1=show all, 2=mev only
	MinEpoch             *uint64  // Filter by minimum epoch
	MaxEpoch             *uint64  // Filter by maximum epoch
	MinSlot              *uint64  // Filter by minimum slot (derived from MinEpoch)
	MaxSlot              *uint64  // Filter by maximum slot (derived from MaxEpoch)
}

type MevBlockFilter struct {
	MinSlot       uint64
	MaxSlot       uint64
	MinIndex      uint64
	MaxIndex      uint64
	ProposerName  string
	BuilderPubkey []byte
	Proposed      []uint8
	MevRelay      []uint8
}

type DepositTxFilter struct {
	MinIndex          uint64
	MaxIndex          uint64
	Address           []byte
	TargetAddress     []byte
	PublicKey         []byte
	PublicKeys        [][]byte
	WithdrawalAddress []byte
	ValidatorName     string
	MinAmount         uint64
	MaxAmount         uint64
	WithOrphaned      uint8
	WithValid         uint8
}

type DepositFilter struct {
	MinIndex      uint64
	MaxIndex      uint64
	PublicKey     []byte
	ValidatorName string
	MinAmount     uint64
	MaxAmount     uint64
	WithOrphaned  uint8
}

type VoluntaryExitFilter struct {
	MinSlot       uint64
	MaxSlot       uint64
	MinIndex      uint64
	MaxIndex      uint64
	ValidatorName string
	WithOrphaned  uint8
}

type SlashingFilter struct {
	MinSlot       uint64
	MaxSlot       uint64
	MinIndex      uint64
	MaxIndex      uint64
	ValidatorName string
	SlasherName   string
	WithOrphaned  uint8
	WithReason    SlashingReason
}

type WithdrawalRequestFilter struct {
	MinSlot       uint64
	MaxSlot       uint64
	PublicKey     []byte
	SourceAddress []byte
	MinIndex      uint64
	MaxIndex      uint64
	ValidatorName string
	MinAmount     *uint64
	MaxAmount     *uint64
	WithOrphaned  uint8
}

type WithdrawalRequestTxFilter struct {
	MinDequeue    uint64
	MaxDequeue    uint64
	PublicKey     []byte
	SourceAddress []byte
	MinIndex      uint64
	MaxIndex      uint64
	ValidatorName string
	MinAmount     *uint64
	MaxAmount     *uint64
	WithOrphaned  uint8
}

type ConsolidationRequestFilter struct {
	MinSlot          uint64
	MaxSlot          uint64
	PublicKey        []byte
	SourceAddress    []byte
	MinSrcIndex      uint64
	MaxSrcIndex      uint64
	SrcValidatorName string
	MinTgtIndex      uint64
	MaxTgtIndex      uint64
	TgtValidatorName string
	WithOrphaned     uint8
}

type ConsolidationRequestTxFilter struct {
	MinDequeue       uint64
	MaxDequeue       uint64
	PublicKey        []byte
	SourceAddress    []byte
	MinSrcIndex      uint64
	MaxSrcIndex      uint64
	SrcValidatorName string
	MinTgtIndex      uint64
	MaxTgtIndex      uint64
	TgtValidatorName string
	WithOrphaned     uint8
}

type ValidatorOrder uint8

const (
	ValidatorOrderIndexAsc ValidatorOrder = iota
	ValidatorOrderIndexDesc
	ValidatorOrderPubKeyAsc
	ValidatorOrderPubKeyDesc
	ValidatorOrderBalanceAsc
	ValidatorOrderBalanceDesc
	ValidatorOrderActivationEpochAsc
	ValidatorOrderActivationEpochDesc
	ValidatorOrderExitEpochAsc
	ValidatorOrderExitEpochDesc
	ValidatorOrderWithdrawableEpochAsc
	ValidatorOrderWithdrawableEpochDesc
)

type ValidatorFilter struct {
	MinIndex          *uint64
	MaxIndex          *uint64
	Indices           []phase0.ValidatorIndex
	PubKey            []byte
	WithdrawalAddress []byte
	WithdrawalCreds   []byte
	ValidatorName     string
	Status            []v1.ValidatorState

	OrderBy ValidatorOrder
	Limit   uint64
	Offset  uint64
}

// EL Explorer filters

type ElTransactionFilter struct {
	FromID     uint64
	ToID       uint64
	Reverted   *bool
	MinGasUsed *uint64
	MaxGasUsed *uint64
}

type ElEventIndexFilter struct {
	SourceID uint64
	Topic1   []byte
}

type ElTransactionInternalFilter struct {
	FromID uint64
	ToID   uint64
}

type ElAccountFilter struct {
	FunderID   uint64
	IsContract *bool
	MinFunded  uint64
	MaxFunded  uint64
}

type ElTokenFilter struct {
	Contract []byte
	Name     string
	Symbol   string
}

type ElBalanceFilter struct {
	TokenID    *uint64
	MinBalance *float64
	MaxBalance *float64
}

type ElTokenTransferFilter struct {
	TokenID   *uint64
	FromID    uint64
	ToID      uint64
	MinAmount *float64
	MaxAmount *float64
}

type ElWithdrawalFilter struct {
	AccountID uint64
	Type      *uint8 // 0=withdrawal, 1=fee_recipient
	Validator *uint64
	MinAmount *float64
	MaxAmount *float64
}
