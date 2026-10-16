package rpc

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// ExecutionProof is one EIP-8025 execution proof of a block's payload as a node
// delivers it on its execution_proof event stream: the signed proof envelope.
type ExecutionProof struct {
	// ProofType identifies the zkVM+EL combination that produced the proof.
	ProofType uint8
	ProofData []byte
	// BeaconBlockRoot is the block whose payload the proof attests.
	BeaconBlockRoot phase0.Root
	// ValidatorIndex and Signature identify the prover and its envelope signature.
	ValidatorIndex uint64
	Signature      phase0.BLSSignature
}

// executionProofEnvelope is a signed proof envelope as the execution_proof event
// carries it. validator_index is a quoted decimal; proof_type is a quoted decimal
// (Prysm) or a bare number (Lighthouse).
type executionProofEnvelope struct {
	Message struct {
		ProofType       jsonUint8 `json:"proof_type"`
		ProofData       string    `json:"proof_data"`
		BeaconBlockRoot string    `json:"beacon_block_root"`
	} `json:"message"`
	ValidatorIndex string `json:"validator_index"`
	Signature      string `json:"signature"`
}

// jsonUint8 is a uint8 that decodes from a JSON number or a quoted decimal.
type jsonUint8 uint8

func (v *jsonUint8) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	value, err := strconv.ParseUint(text, 10, 8)
	if err != nil {
		return fmt.Errorf("invalid uint8 %s: %w", string(data), err)
	}
	*v = jsonUint8(value)
	return nil
}
