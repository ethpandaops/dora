package executionproof

import dynssz "github.com/pk910/dynamic-ssz"

// MockVerifier is the verifier recorded for proofs accepted as mock proofs.
const MockVerifier = "mock"

// DecodeMock reads a zkboost mock proof (devnets): an SSZ container of the
// public values and random bytes in place of a proof. The public values are
// the serialized public input.
func DecodeMock(proof []byte) (*Committed, error) {
	var mock struct {
		PublicValues []byte `ssz-max:"4194304"`
		Proof        []byte `ssz-max:"4194304"`
	}
	if err := dynssz.NewDynSsz(nil).UnmarshalSSZ(&mock, proof); err != nil {
		return nil, err
	}

	committed := &Committed{PublicValues: mock.PublicValues}

	publicInput, err := UnmarshalPublicInput(mock.PublicValues)
	if err != nil {
		committed.DecodeError = err.Error()
	} else {
		committed.PublicInput = publicInput
	}

	return committed, nil
}
