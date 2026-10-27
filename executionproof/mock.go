package executionproof

import dynssz "github.com/pk910/dynamic-ssz"

// MockProofs makes DecodeCommitted read the proofs as zkboost mock proofs
// (devnets only).
var MockProofs bool

// MockVerifier is the verifier recorded for proofs checked as mock proofs.
const MockVerifier = "mock"

// decodeMock reads a zkboost mock proof: an SSZ container of the public values
// and random bytes in place of a proof.
func decodeMock(proof []byte) (*Committed, error) {
	var mock struct {
		PublicValues []byte `ssz-max:"4194304"`
		Proof        []byte `ssz-max:"4194304"`
	}
	if err := dynssz.NewDynSsz(nil).UnmarshalSSZ(&mock, proof); err != nil {
		return nil, err
	}
	return &Committed{PublicValues: mock.PublicValues, ProofMode: MockVerifier}, nil
}
