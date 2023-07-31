package rpctypes

type BeaconBlockHeader struct {
	Slot          Uint64Str   `json:"slot"`
	ProposerIndex Uint64Str   `json:"proposer_index"`
	ParentRoot    BytesHexStr `json:"parent_root"`
	StateRoot     BytesHexStr `json:"state_root"`
	BodyRoot      BytesHexStr `json:"body_root"`
}

type SignedBeaconBlockHeader struct {
	Message   BeaconBlockHeader `json:"message"`
	Signature BytesHexStr       `json:"signature"`
}

type ProposerSlashing struct {
	SignedHeader1 SignedBeaconBlockHeader `json:"signed_header_1"`
	SignedHeader2 SignedBeaconBlockHeader `json:"signed_header_2"`
}

type Checkpoint struct {
	Epoch Uint64Str   `json:"epoch"`
	Root  BytesHexStr `json:"root"`
}

type AttestationData struct {
	Slot            Uint64Str   `json:"slot"`
	Index           Uint64Str   `json:"index"`
	BeaconBlockRoot BytesHexStr `json:"beacon_block_root"`
	Source          Checkpoint  `json:"source"`
	Target          Checkpoint  `json:"target"`
}

type IndexedAttestation struct {
	AttestingIndices []Uint64Str     `json:"attesting_indices"`
	Signature        BytesHexStr     `json:"signature"`
	Data             AttestationData `json:"data"`
}

type AttesterSlashing struct {
	Attestation1 IndexedAttestation `json:"attestation_1"`
	Attestation2 IndexedAttestation `json:"attestation_2"`
}

type Attestation struct {
	AggregationBits BytesHexStr     `json:"aggregation_bits"`
	Signature       BytesHexStr     `json:"signature"`
	Data            AttestationData `json:"data"`
}

type DepositData struct {
	Pubkey                BytesHexStr `json:"pubkey"`
	WithdrawalCredentials BytesHexStr `json:"withdrawal_credentials"`
	Amount                Uint64Str   `json:"amount"`
	Signature             BytesHexStr `json:"signature"`
}

type Deposit struct {
	Proof []BytesHexStr `json:"proof"`
	Data  DepositData   `json:"data"`
}

type VoluntaryExit struct {
	Epoch          Uint64Str `json:"epoch"`
	ValidatorIndex Uint64Str `json:"validator_index"`
}

type SignedVoluntaryExit struct {
	Message   VoluntaryExit `json:"message"`
	Signature BytesHexStr   `json:"signature"`
}

type Eth1Data struct {
	DepositRoot  BytesHexStr `json:"deposit_root"`
	DepositCount Uint64Str   `json:"deposit_count"`
	BlockHash    BytesHexStr `json:"block_hash"`
}

type SyncAggregate struct {
	SyncCommitteeBits      BytesHexStr `json:"sync_committee_bits"`
	SyncCommitteeSignature BytesHexStr `json:"sync_committee_signature"`
}

type ExecutionPayload struct {
	ParentHash    BytesHexStr   `json:"parent_hash"`
	FeeRecipient  BytesHexStr   `json:"fee_recipient"`
	StateRoot     BytesHexStr   `json:"state_root"`
	ReceiptsRoot  BytesHexStr   `json:"receipts_root"`
	LogsBloom     BytesHexStr   `json:"logs_bloom"`
	PrevRandao    BytesHexStr   `json:"prev_randao"`
	BlockNumber   Uint64Str     `json:"block_number"`
	GasLimit      Uint64Str     `json:"gas_limit"`
	GasUsed       Uint64Str     `json:"gas_used"`
	Timestamp     Uint64Str     `json:"timestamp"`
	ExtraData     BytesHexStr   `json:"extra_data"`
	BaseFeePerGas Uint64Str     `json:"base_fee_per_gas"`
	BlockHash     BytesHexStr   `json:"block_hash"`
	Transactions  []BytesHexStr `json:"transactions"`
	// present only after capella
	Withdrawals []WithdrawalPayload `json:"withdrawals"`
}

type WithdrawalPayload struct {
	Index          Uint64Str   `json:"index"`
	ValidatorIndex Uint64Str   `json:"validator_index"`
	Address        BytesHexStr `json:"address"`
	Amount         Uint64Str   `json:"amount"`
}

type BLSToExecutionChange struct {
	ValidatorIndex     Uint64Str   `json:"validator_index"`
	FromBlsPubkey      BytesHexStr `json:"from_bls_pubkey"`
	ToExecutionAddress BytesHexStr `json:"to_execution_address"`
}

type SignedBLSToExecutionChange struct {
	Message   BLSToExecutionChange `json:"message"`
	Signature BytesHexStr          `json:"signature"`
}

type BeaconBlockBody struct {
	RandaoReveal      BytesHexStr           `json:"randao_reveal"`
	Eth1Data          Eth1Data              `json:"eth1_data"`
	Graffiti          BytesHexStr           `json:"graffiti"`
	ProposerSlashings []ProposerSlashing    `json:"proposer_slashings"`
	AttesterSlashings []AttesterSlashing    `json:"attester_slashings"`
	Attestations      []Attestation         `json:"attestations"`
	Deposits          []Deposit             `json:"deposits"`
	VoluntaryExits    []SignedVoluntaryExit `json:"voluntary_exits"`

	// present only after altair
	SyncAggregate *SyncAggregate `json:"sync_aggregate,omitempty"`

	// present only after bellatrix
	ExecutionPayload *ExecutionPayload `json:"execution_payload"`

	// present only after capella
	SignedBLSToExecutionChange []*SignedBLSToExecutionChange `json:"bls_to_execution_changes"`

	// present only after deneb
	BlobKzgCommitments []BytesHexStr `json:"blob_kzg_commitments"`
}

type BeaconBlock struct {
	Slot          Uint64Str       `json:"slot"`
	ProposerIndex Uint64Str       `json:"proposer_index"`
	ParentRoot    BytesHexStr     `json:"parent_root"`
	StateRoot     BytesHexStr     `json:"state_root"`
	Body          BeaconBlockBody `json:"body"`
}

type SignedBeaconBlock struct {
	Message   BeaconBlock `json:"message"`
	Signature BytesHexStr `json:"signature"`
}

type Validator struct {
	PubKey                     BytesHexStr `json:"pubkey"`
	WithdrawalCredentials      BytesHexStr `json:"withdrawal_credentials"`
	EffectiveBalance           Uint64Str   `json:"effective_balance"`
	Slashed                    bool        `json:"slashed"`
	ActivationEligibilityEpoch Uint64Str   `json:"activation_eligibility_epoch"`
	ActivationEpoch            Uint64Str   `json:"activation_epoch"`
	ExitEpoch                  Uint64Str   `json:"exit_epoch"`
	WithdrawableEpoch          Uint64Str   `json:"withdrawable_epoch"`
}

type ValidatorEntry struct {
	Index     Uint64Str `json:"index"`
	Balance   Uint64Str `json:"balance"`
	Status    string    `json:"status"`
	Validator Validator `json:"validator"`
}

type BlobSidecar struct {
	BlockRoot       BytesHexStr `json:"block_root"`
	Index           Uint64Str   `json:"index"`
	Slot            Uint64Str   `json:"slot"`
	BlockParentRoot BytesHexStr `json:"block_parent_root"`
	ProposerIndex   Uint64Str   `json:"proposer_index"`
	Blob            BytesHexStr `json:"blob"`
	KzgCommitment   BytesHexStr `json:"kzg_commitment"`
	KzgProof        BytesHexStr `json:"kzg_proof"`
}
