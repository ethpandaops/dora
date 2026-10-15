package rpc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// ErrExecutionProofsUnsupported is returned when the node has no proof engine (HTTP 501).
var ErrExecutionProofsUnsupported = errors.New("execution proofs not supported by node")

// ErrExecutionProofsNotFound is returned when the node answers 404, which means the block
// is unknown to it or, on nodes without the route, that the route does not exist.
var ErrExecutionProofsNotFound = errors.New("execution proofs not found")

// ExecutionProof is one EIP-8025 execution proof of a block's payload as served by a
// node's execution proofs API: the signed proof envelope.
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

// executionProofEnvelope is a signed proof envelope as served by
// GET /eth/v1/beacon/execution_proofs/{block_id}. validator_index is a quoted
// decimal; proof_type is a bare number (Lighthouse) or a quoted one.
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
