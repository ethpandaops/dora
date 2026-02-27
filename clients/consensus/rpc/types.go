package rpc

// ExecutionProof represents a cryptographic proof of execution that
// an execution payload is valid.
type ExecutionProof struct {
	// Which proof type (zkVM+EL combination) this proof belongs to
	ProofId uint8 `json:"proof_id"`

	// The slot of the beacon block this proof validates
	Slot string `json:"slot"`

	// The block hash of the execution payload this proof validates
	BlockHash string `json:"block_hash"`

	// The beacon block root corresponding to the beacon block
	BlockRoot string `json:"block_root"`

	// The actual proof data (byte array)
	ProofData []byte `json:"proof_data"`
}

// ExecutionProofsResponse represents the API response for execution proofs
type ExecutionProofsResponse struct {
	Data                []ExecutionProof `json:"data"`
	ExecutionOptimistic bool             `json:"execution_optimistic"`
	Finalized           bool             `json:"finalized"`
}
