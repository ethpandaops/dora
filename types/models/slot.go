package models

import (
	"time"

	"github.com/ethpandaops/dora/types"
)

// SlotPageData is a struct to hold info for the slot details page
type SlotPageData struct {
	Slot                   uint64                `json:"slot"`
	Epoch                  uint64                `json:"epoch"`
	EpochFinalized         bool                  `json:"epoch_finalized"`
	EpochParticipationRate float64               `json:"epoch_participation_rate"`
	Ts                     time.Time             `json:"time"`
	NextSlot               uint64                `json:"next_slot"`
	PreviousSlot           uint64                `json:"prev_slot"`
	Status                 uint16                `json:"status"`
	Future                 bool                  `json:"future"`
	Proposer               uint64                `json:"proposer"`
	ProposerName           string                `json:"proposer_name"`
	Block                  *SlotPageBlockData    `json:"block"`
	Badges                 []*SlotPageBlockBadge `json:"badges"`
	SlotBlocks             []*SlotPageSlotBlock  `json:"slot_blocks"`
	TracoorUrl             string                `json:"tracoor_url"`
}

// SlotPageSlotBlock represents a block entry for the slot (for multi-block display)
type SlotPageSlotBlock struct {
	BlockRoot []byte `json:"block_root"`
	Status    uint16 `json:"status"` // 0: missed, 1: canonical, 2: orphaned
	IsCurrent bool   `json:"is_current"`
}

type SlotPageBlockBadge struct {
	Title       string `json:"title"`
	Icon        string `json:"icon"`
	Description string `json:"descr"`
	ClassName   string `json:"class"`
}

type SlotStatus uint16

const (
	SlotStatusMissed   SlotStatus = 0
	SlotStatusFound    SlotStatus = 1
	SlotStatusOrphaned SlotStatus = 2
)

type SlotPageBlockData struct {
	BlockRoot                  []byte                 `json:"blockroot"`
	ParentRoot                 []byte                 `json:"parentroot"`
	StateRoot                  []byte                 `json:"stateroot"`
	BodyRoot                   []byte                 `json:"bodyroot"`
	Signature                  []byte                 `json:"signature"`
	RandaoReveal               []byte                 `json:"randaoreveal"`
	Graffiti                   []byte                 `json:"graffiti"`
	Eth1dataDepositroot        []byte                 `json:"eth1data_depositroot"`
	Eth1dataDepositcount       uint64                 `json:"eth1data_depositcount"`
	Eth1dataBlockhash          []byte                 `json:"eth1data_blockhash"`
	SyncAggregateBits          []byte                 `json:"syncaggregate_bits"`
	SyncAggregateSignature     []byte                 `json:"syncaggregate_signature"`
	SyncAggParticipation       float64                `json:"syncaggregate_participation"`
	SyncAggCommittee           []types.NamedValidator `json:"syncaggregate_committee"`
	ValidatorNames             map[uint64]string      `json:"validator_names"`
	SpecValues                 map[string]interface{} `json:"spec_values"`
	ProposerSlashingsCount     uint64                 `json:"proposer_slashings_count"`
	AttesterSlashingsCount     uint64                 `json:"attester_slashings_count"`
	AttestationsCount          uint64                 `json:"attestations_count"`
	DepositsCount              uint64                 `json:"deposits_count"`
	WithdrawalsCount           uint64                 `json:"withdrawals_count"`
	BLSChangesCount            uint64                 `json:"bls_changes_count"`
	VoluntaryExitsCount        uint64                 `json:"voluntaryexits_count"`
	SlashingsCount             uint64                 `json:"slashings_count"`
	BlobsCount                 uint64                 `json:"blobs_count"`
	TransactionsCount          uint64                 `json:"transactions_count"`
	DepositRequestsCount       uint64                 `json:"deposit_receipts_count"`
	WithdrawalRequestsCount    uint64                 `json:"withdrawal_requests_count"`
	ConsolidationRequestsCount uint64                 `json:"consolidation_requests_count"`
	BidsCount                  uint64                 `json:"bids_count"`
	PtcVotesCount              uint64                 `json:"ptc_votes_count"`

	PayloadHeader *SlotPagePayloadHeader `json:"payload_header"`
	ExecutionData *SlotPageExecutionData `json:"execution_data"`

	Attestations          []*SlotPageAttestation          `json:"attestations"`           // Attestations included in this block
	Deposits              []*SlotPageDeposit              `json:"deposits"`               // Deposits included in this block
	VoluntaryExits        []*SlotPageVoluntaryExit        `json:"voluntary_exits"`        // Voluntary Exits included in this block
	AttesterSlashings     []*SlotPageAttesterSlashing     `json:"attester_slashings"`     // Attester Slashings included in this block
	ProposerSlashings     []*SlotPageProposerSlashing     `json:"proposer_slashings"`     // Proposer Slashings included in this block
	BLSChanges            []*SlotPageBLSChange            `json:"bls_changes"`            // BLSChanges included in this block
	Withdrawals           []*SlotPageWithdrawal           `json:"withdrawals"`            // Withdrawals included in this block
	Blobs                 []*SlotPageBlob                 `json:"blobs"`                  // Blob sidecars included in this block
	Transactions          []*SlotPageTransaction          `json:"transactions"`           // Transactions included in this block
	DepositRequests       []*SlotPageDepositRequest       `json:"deposit_receipts"`       // DepositRequests included in this block
	WithdrawalRequests    []*SlotPageWithdrawalRequest    `json:"withdrawal_requests"`    // WithdrawalRequests included in this block
	ConsolidationRequests []*SlotPageConsolidationRequest `json:"consolidation_requests"` // ConsolidationRequests included in this block
	Bids                  []*SlotPageBid                  `json:"bids"`                   // Execution payload bids for this block (ePBS)
	PtcVotes              *SlotPagePtcVotes               `json:"ptc_votes"`              // PTC votes included in this block (for previous slot)
}

type SlotPageExecutionData struct {
	ParentHash         []byte    `json:"parent_hash"`
	FeeRecipient       []byte    `json:"fee_recipient"`
	StateRoot          []byte    `json:"state_root"`
	ReceiptsRoot       []byte    `json:"receipts_root"`
	LogsBloom          []byte    `json:"logs_bloom"`
	Random             []byte    `json:"random"`
	GasLimit           uint64    `json:"gas_limit"`
	GasUsed            uint64    `json:"gas_used"`
	Timestamp          uint64    `json:"timestamp"`
	Time               time.Time `json:"time"`
	ExtraData          []byte    `json:"extra_data"`
	BaseFeePerGas      uint64    `json:"base_fee_per_gas"`
	BlockHash          []byte    `json:"block_hash"`
	BlockNumber        uint64    `json:"block_number"`
	BlobGasUsed        *uint64   `json:"blob_gas_used,omitempty"`
	BlobLimit          *uint64   `json:"blob_limit,omitempty"`
	BlobGasLimit       *uint64   `json:"blob_gas_limit,omitempty"`
	ExcessBlobGas      *uint64   `json:"excess_blob_gas,omitempty"`
	BlobBaseFee        *uint64   `json:"blob_base_fee,omitempty"`
	BlobBaseFeeEIP7918 *uint64   `json:"blob_base_fee_eip7918,omitempty"`
	IsEIP7918Active    bool      `json:"is_eip7918_active"`
	HasExecData        bool      `json:"has_exec_data"`
}

type SlotPagePayloadHeader struct {
	PayloadStatus      uint16   `json:"payload_status"`
	ParentBlockHash    []byte   `json:"parent_block_hash"`
	ParentBlockRoot    []byte   `json:"parent_block_root"`
	BlockHash          []byte   `json:"block_hash"`
	GasLimit           uint64   `json:"gas_limit"`
	BuilderIndex       uint64   `json:"builder_index"`
	BuilderName        string   `json:"builder_name"`
	Slot               uint64   `json:"slot"`
	Value              uint64   `json:"value"`
	BlobKZGCommitments [][]byte `json:"blob_kzg_commitments"`
	Signature          []byte   `json:"signature"`
}

type SlotPageAttestation struct {
	Slot           uint64   `json:"slot"`
	CommitteeIndex []uint64 `json:"committeeindex"`
	TotalActive    uint64   `json:"total_active"`

	AggregationBits    []byte   `json:"aggregationbits"`
	Validators         []uint64 `json:"validators"`
	IncludedValidators []uint64 `json:"included_validators"`

	Signature []byte `json:"signature"`

	BeaconBlockRoot []byte `json:"beaconblockroot"`
	BeaconBlockSlot uint64 `json:"beaconblockslot"`
	SourceEpoch     uint64 `json:"source_epoch"`
	SourceRoot      []byte `json:"source_root"`
	TargetEpoch     uint64 `json:"target_epoch"`
	TargetRoot      []byte `json:"target_root"`
}

type SlotPageDeposit struct {
	PublicKey             []byte `json:"publickey"`
	Withdrawalcredentials []byte `json:"withdrawalcredentials"`
	Amount                uint64 `json:"amount"`
	Signature             []byte `json:"signature"`
}

type SlotPageVoluntaryExit struct {
	ValidatorIndex uint64 `json:"validatorindex"`
	ValidatorName  string `json:"validatorname"`
	IsBuilder      bool   `json:"is_builder"`
	Epoch          uint64 `json:"epoch"`
	Signature      []byte `json:"signature"`
}

// BlockPageAttesterSlashing is a struct to hold data for attester slashings on the block page
type SlotPageAttesterSlashing struct {
	Attestation1Indices         []uint64               `json:"attestation1_indices"`
	Attestation1Signature       []byte                 `json:"attestation1_signature"`
	Attestation1Slot            uint64                 `json:"attestation1_slot"`
	Attestation1Index           uint64                 `json:"attestation1_index"`
	Attestation1BeaconBlockRoot []byte                 `json:"attestation1_beaconblockroot"`
	Attestation1SourceEpoch     uint64                 `json:"attestation1_source_epoch"`
	Attestation1SourceRoot      []byte                 `json:"attestation1_source_root"`
	Attestation1TargetEpoch     uint64                 `json:"attestation1_target_epoch"`
	Attestation1TargetRoot      []byte                 `json:"attestation1_target_root"`
	Attestation2Indices         []uint64               `json:"attestation2_indices"`
	Attestation2Signature       []byte                 `json:"attestation2_signature"`
	Attestation2Slot            uint64                 `json:"attestation2_slot"`
	Attestation2Index           uint64                 `json:"attestation2_index"`
	Attestation2BeaconBlockRoot []byte                 `json:"attestation2_beaconblockroot"`
	Attestation2SourceEpoch     uint64                 `json:"attestation2_source_epoch"`
	Attestation2SourceRoot      []byte                 `json:"attestation2_source_root"`
	Attestation2TargetEpoch     uint64                 `json:"attestation2_target_epoch"`
	Attestation2TargetRoot      []byte                 `json:"attestation2_target_root"`
	SlashedValidators           []types.NamedValidator `json:"validators"`
}

// BlockPageProposerSlashing is a struct to hold data for proposer slashings on the block page
type SlotPageProposerSlashing struct {
	ProposerIndex     uint64 `json:"proposerindex"`
	ProposerName      string `json:"proposername"`
	Header1Slot       uint64 `json:"header1_slot"`
	Header1ParentRoot []byte `json:"header1_parentroot"`
	Header1StateRoot  []byte `json:"header1_stateroot"`
	Header1BodyRoot   []byte `json:"header1_bodyroot"`
	Header1Signature  []byte `json:"header1_signature"`
	Header2Slot       uint64 `json:"header2_slot"`
	Header2ParentRoot []byte `json:"header2_parentroot"`
	Header2StateRoot  []byte `json:"header2_stateroot"`
	Header2BodyRoot   []byte `json:"header2_bodyroot"`
	Header2Signature  []byte `json:"header2_signature"`
}

type SlotPageBLSChange struct {
	ValidatorIndex uint64 `json:"validatorindex"`
	ValidatorName  string `json:"validatorname"`
	BlsPubkey      []byte `json:"pubkey"`
	Address        []byte `json:"address"`
	Signature      []byte `json:"signature"`
}

type SlotPageWithdrawal struct {
	Index          uint64 `json:"index"`
	ValidatorIndex uint64 `json:"validatorindex"`
	ValidatorName  string `json:"validatorname"`
	Address        []byte `json:"address"`
	Amount         uint64 `json:"amount"`
}

type SlotPageBlob struct {
	Index         uint64 `json:"index"`
	KzgCommitment []byte `json:"kzg_commitment"`
	HaveData      bool   `json:"have_data"`
	IsShort       bool   `json:"is_short"`
	BlobShort     []byte `json:"blob_short"`
	Blob          []byte `json:"blob"`
	KzgProof      []byte `json:"kzg_proof"`
}

type SlotPageBlobDetails struct {
	Index         uint64 `json:"index"`
	Blob          string `json:"blob"`
	KzgCommitment string `json:"kzg_commitment"`
	KzgProof      string `json:"kzg_proof"`
}

type SlotPageTransaction struct {
	Index         uint64  `json:"index"`
	Hash          []byte  `json:"hash"`
	From          []byte  `json:"from"`
	To            []byte  `json:"to"`
	Value         float64 `json:"value"`
	Data          []byte  `json:"data"`
	DataLen       uint64  `json:"datalen"`
	FuncSigStatus uint64  `json:"func_sig_status"`
	FuncBytes     string  `json:"func_bytes"`
	FuncName      string  `json:"func_name"`
	FuncSig       string  `json:"func_sig"`
	Type          uint64  `json:"type"`
	TypeName      string  `json:"type_name"`

	// EL-enriched data (only available when execution indexer is enabled)
	HasElData   bool    `json:"has_el_data"`
	Reverted    bool    `json:"reverted"`
	GasUsed     uint64  `json:"gas_used"`
	GasLimit    uint64  `json:"gas_limit"`
	TxFee       float64 `json:"tx_fee"`        // Transaction fee in ETH
	EffGasPrice float64 `json:"eff_gas_price"` // Effective gas price in Gwei
}

type SlotPageDepositRequest struct {
	PublicKey       []byte `db:"pubkey"`
	Exists          bool   `db:"exists"`
	ValidatorIndex  uint64 `db:"valindex"`
	ValidatorName   string `db:"valname"`
	WithdrawalCreds []byte `db:"withdrawal_creds"`
	Amount          uint64 `db:"amount"`
	Signature       []byte `db:"signature"`
	Index           uint64 `db:"index"`
}

type SlotPageWithdrawalRequest struct {
	Address        []byte `db:"address"`
	PublicKey      []byte `db:"pubkey"`
	Exists         bool   `db:"exists"`
	ValidatorIndex uint64 `db:"valindex"`
	ValidatorName  string `db:"valname"`
	Amount         uint64 `db:"amount"`
}

type SlotPageConsolidationRequest struct {
	Address      []byte `db:"address"`
	SourcePubkey []byte `db:"source_pubkey"`
	SourceFound  bool   `db:"source_bool"`
	SourceIndex  uint64 `db:"source_index"`
	SourceName   string `db:"source_name"`
	TargetPubkey []byte `db:"target_pubkey"`
	TargetFound  bool   `db:"target_bool"`
	TargetIndex  uint64 `db:"target_index"`
	TargetName   string `db:"target_name"`
	Epoch        uint64 `db:"epoch"`
}

type SlotPageBid struct {
	ParentRoot   []byte `json:"parent_root"`
	ParentHash   []byte `json:"parent_hash"`
	BlockHash    []byte `json:"block_hash"`
	FeeRecipient []byte `json:"fee_recipient"`
	GasLimit     uint64 `json:"gas_limit"`
	BuilderIndex uint64 `json:"builder_index"`
	BuilderName  string `json:"builder_name"`
	IsSelfBuilt  bool   `json:"is_self_built"`
	Slot         uint64 `json:"slot"`
	Value        uint64 `json:"value"`
	ElPayment    uint64 `json:"el_payment"`
	TotalValue   uint64 `json:"total_value"`
	IsWinning    bool   `json:"is_winning"`
}

// SlotPagePtcVotes holds PTC (Payload Timeliness Committee) vote information for a slot.
// These are payload attestations included in this block for the PREVIOUS slot.
type SlotPagePtcVotes struct {
	VotedSlot      uint64                  `json:"voted_slot"`       // The slot the votes are for (previous slot)
	VotedBlockRoot []byte                  `json:"voted_block_root"` // The block root being voted on
	TotalPtcSize   uint64                  `json:"total_ptc_size"`   // Total PTC committee size
	Aggregates     []*SlotPagePtcAggregate `json:"aggregates"`       // Up to 4 aggregates for different vote flag combinations
	PtcCommittee   []types.NamedValidator  `json:"ptc_committee"`    // Full PTC committee with participation status
	Participation  float64                 `json:"participation"`    // Overall participation rate
}

// SlotPagePtcAggregate represents a single PTC vote aggregate for a specific vote flag combination.
type SlotPagePtcAggregate struct {
	PayloadPresent    bool     `json:"payload_present"`     // Whether the payload was present
	BlobDataAvailable bool     `json:"blob_data_available"` // Whether blob data was available
	AggregationBits   []byte   `json:"aggregation_bits"`    // Bitfield of participating validators
	Validators        []uint64 `json:"validators"`          // Validator indices that voted
	Signature         []byte   `json:"signature"`           // Aggregate signature
	VoteCount         uint64   `json:"vote_count"`          // Number of votes in this aggregate
}
