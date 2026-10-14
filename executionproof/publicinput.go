package executionproof

import (
	"encoding/binary"
	"fmt"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

const (
	// SchemaID is the stateless input schema the proofs of the current devnets
	// commit to: the Amsterdam fork (0x15) at schema revision 1.
	SchemaID uint16 = 0x1501
	// PublicInputSize is the size of the serialized public input.
	PublicInputSize = 43
)

// PublicInput is what an execution proof commits to: the root of the
// NewPayloadRequest the guest validated, the outcome, and the chain and schema
// it was produced for. The zkVM guests reveal its SSZ serialization as their
// public values.
type PublicInput struct {
	NewPayloadRequestRoot phase0.Root
	SuccessfulValidation  bool
	ChainID               uint64
	SchemaID              uint16
}

// Marshal returns the SSZ serialization: root, boolean, uint64 and uint16 in
// little endian.
func (p *PublicInput) Marshal() []byte {
	buf := make([]byte, PublicInputSize)
	copy(buf[0:32], p.NewPayloadRequestRoot[:])
	if p.SuccessfulValidation {
		buf[32] = 1
	}
	binary.LittleEndian.PutUint64(buf[33:41], p.ChainID)
	binary.LittleEndian.PutUint16(buf[41:43], p.SchemaID)
	return buf
}

// UnmarshalPublicInput decodes exactly one serialized public input.
func UnmarshalPublicInput(data []byte) (*PublicInput, error) {
	if len(data) != PublicInputSize {
		return nil, fmt.Errorf("invalid public input size: %d", len(data))
	}
	if data[32] > 1 {
		return nil, fmt.Errorf("invalid public input boolean: %d", data[32])
	}
	return &PublicInput{
		NewPayloadRequestRoot: phase0.Root(data[0:32]),
		SuccessfulValidation:  data[32] == 1,
		ChainID:               binary.LittleEndian.Uint64(data[33:41]),
		SchemaID:              binary.LittleEndian.Uint16(data[41:43]),
	}, nil
}

// PublicInputFromPublicValues decodes the public input from the public values a
// zkVM reveals, following the verifiers' acceptance rule: SP1 reveals exactly
// the serialization, OpenVM and Zisk pad it with zero bytes to their fixed
// public value size.
func PublicInputFromPublicValues(zkvm ZkVM, publicValues []byte) (*PublicInput, error) {
	if len(publicValues) < PublicInputSize {
		return nil, fmt.Errorf("public values too short: %d", len(publicValues))
	}
	if zkvm == ZkVMSP1 && len(publicValues) != PublicInputSize {
		return nil, fmt.Errorf("unexpected public values size for sp1: %d", len(publicValues))
	}
	for _, b := range publicValues[PublicInputSize:] {
		if b != 0 {
			return nil, fmt.Errorf("non-zero public value padding")
		}
	}
	return UnmarshalPublicInput(publicValues[:PublicInputSize])
}
