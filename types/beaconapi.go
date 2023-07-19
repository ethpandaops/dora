package types

type SyncAggregate struct {
	SyncCommitteeValidators    []uint64
	SyncCommitteeBits          []byte
	SyncCommitteeSignature     []byte
	SyncAggregateParticipation float64
}

// Block is a struct to hold block data
type Block struct {
	Status                     uint64
	Proposer                   uint64
	BlockRoot                  []byte
	Slot                       uint64
	ParentRoot                 []byte
	StateRoot                  []byte
	Signature                  []byte
	RandaoReveal               []byte
	Graffiti                   []byte
	Eth1Data                   *Eth1Data
	BodyRoot                   []byte
	ProposerSlashings          []*ProposerSlashing
	AttesterSlashings          []*AttesterSlashing
	Attestations               []*Attestation
	Deposits                   []*Deposit
	VoluntaryExits             []*VoluntaryExit
	SyncAggregate              *SyncAggregate    // warning: sync aggregate may be nil, for phase0 blocks
	ExecutionPayload           *ExecutionPayload // warning: payload may be nil, for phase0/altair blocks
	Canonical                  bool
	SignedBLSToExecutionChange []*SignedBLSToExecutionChange
}

type SignedBLSToExecutionChange struct {
	Message   BLSToExecutionChange
	Signature []byte
}

type BLSToExecutionChange struct {
	Validatorindex uint64
	BlsPubkey      []byte
	Address        []byte
}

type Transaction struct {
	Raw []byte
	// Note: below values may be nil/0 if Raw fails to decode into a valid transaction
	TxHash       []byte
	AccountNonce uint64
	// big endian
	Price     []byte
	GasLimit  uint64
	Sender    []byte
	Recipient []byte
	// big endian
	Amount  []byte
	Payload []byte

	MaxPriorityFeePerGas uint64
	MaxFeePerGas         uint64
}

type ExecutionPayload struct {
	ParentHash    []byte
	FeeRecipient  []byte
	StateRoot     []byte
	ReceiptsRoot  []byte
	LogsBloom     []byte
	Random        []byte
	BlockNumber   uint64
	GasLimit      uint64
	GasUsed       uint64
	Timestamp     uint64
	ExtraData     []byte
	BaseFeePerGas uint64
	BlockHash     []byte
	Transactions  []*Transaction
	Withdrawals   []*Withdrawals
}

type Withdrawals struct {
	Slot           uint64
	BlockRoot      []byte
	Index          uint64
	ValidatorIndex uint64
	Address        []byte
	Amount         uint64
}

type WithdrawalsByEpoch struct {
	Epoch          uint64
	ValidatorIndex uint64
	Amount         uint64
}

type WithdrawalsNotification struct {
	Slot           uint64
	Index          uint64
	ValidatorIndex uint64
	Address        []byte
	Amount         uint64
	Pubkey         []byte
}

// Eth1Data is a struct to hold the ETH1 data
type Eth1Data struct {
	DepositRoot  []byte
	DepositCount uint64
	BlockHash    []byte
}

// ProposerSlashing is a struct to hold proposer slashing data
type ProposerSlashing struct {
	ProposerIndex uint64
	Header1       *Block
	Header2       *Block
}

// AttesterSlashing is a struct to hold attester slashing
type AttesterSlashing struct {
	Attestation1 *IndexedAttestation
	Attestation2 *IndexedAttestation
}

// IndexedAttestation is a struct to hold indexed attestation data
type IndexedAttestation struct {
	Data             *AttestationData
	AttestingIndices []uint64
	Signature        []byte
}

// Attestation is a struct to hold attestation header data
type Attestation struct {
	AggregationBits []byte
	Attesters       []uint64
	Data            *AttestationData
	Signature       []byte
}

// AttestationData to hold attestation detail data
type AttestationData struct {
	Slot            uint64
	CommitteeIndex  uint64
	BeaconBlockRoot []byte
	Source          *Checkpoint
	Target          *Checkpoint
}

// Checkpoint is a struct to hold checkpoint data
type Checkpoint struct {
	Epoch uint64
	Root  []byte
}

// Deposit is a struct to hold deposit data
type Deposit struct {
	Proof                 [][]byte
	PublicKey             []byte
	WithdrawalCredentials []byte
	Amount                uint64
	Signature             []byte
}

// VoluntaryExit is a struct to hold voluntary exit data
type VoluntaryExit struct {
	Epoch          uint64
	ValidatorIndex uint64
	Signature      []byte
}
