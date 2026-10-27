package beacon

import (
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/assert"

	"github.com/ethpandaops/dora/executionproof"
	"github.com/ethpandaops/dora/executionproof/verifiers"
)

func TestMockVerdict(t *testing.T) {
	expected := &executionproof.PublicInput{NewPayloadRequestRoot: phase0.Root{0xa5}, SuccessfulValidation: true, ChainID: 3151908, SchemaID: 0x1501}
	mock := func(input *executionproof.PublicInput) []byte {
		return append(append([]byte{8, 0, 0, 0, 51, 0, 0, 0}, input.Marshal()...), 9, 9, 9)
	}
	other := *expected
	other.NewPayloadRequestRoot[31] = 1

	tests := []struct {
		name   string
		proof  []byte
		status verifiers.Status
		isMock bool
	}{
		{name: "mock proof of this block", proof: mock(expected), status: verifiers.StatusValid, isMock: true},
		{name: "mock proof of another payload", proof: mock(&other), status: verifiers.StatusInvalid, isMock: true},
		{name: "no mock proof keeps the zkVM verdict", proof: []byte{1, 2, 3}, status: verifiers.StatusUnknown, isMock: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, isMock := mockVerdict(tt.proof, expected)
			assert.Equal(t, tt.status, status)
			assert.Equal(t, tt.isMock, isMock)
		})
	}
}
