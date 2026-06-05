package rpc

// ExecutionProof represents a signed cryptographic proof attesting that
// an execution payload is valid, as returned by the Lighthouse
// /eth/v1/beacon/execution_proofs/{block_root} endpoint.
type ExecutionProof struct {
	Message        ExecutionProofMessage `json:"message"`
	ValidatorIndex string                `json:"validator_index"`
	Signature      string                `json:"signature"`
}

// ExecutionProofMessage is the inner message of an ExecutionProof.
type ExecutionProofMessage struct {
	// Which proof type (zkVM+EL combination) this proof belongs to.
	ProofType uint8 `json:"proof_type"`

	// Hex-encoded proof bytes (e.g. "0x...").
	ProofData string `json:"proof_data"`

	// Public input the proof commits to.
	PublicInput ExecutionProofPublicInput `json:"public_input"`
}

// ExecutionProofPublicInput is the public input committed to by an execution proof.
type ExecutionProofPublicInput struct {
	NewPayloadRequestRoot string `json:"new_payload_request_root"`
}

// ExecutionProofsResponse represents the API response for execution proofs
type ExecutionProofsResponse struct {
	Data                []ExecutionProof `json:"data"`
	ExecutionOptimistic bool             `json:"execution_optimistic"`
	Finalized           bool             `json:"finalized"`
}
