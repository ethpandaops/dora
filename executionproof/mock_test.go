package executionproof

import (
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestDecodeCommittedMock(t *testing.T) {
	MockProofs = true
	defer func() { MockProofs = false }()

	expected := &PublicInput{NewPayloadRequestRoot: phase0.Root{0xa5}, SuccessfulValidation: true, ChainID: 3151908, SchemaID: 0x1501}
	// as zkboost serializes it: the offsets of public_values (8) and proof (51), then both
	proof := append(append([]byte{8, 0, 0, 0, 51, 0, 0, 0}, expected.Marshal()...), 9, 9, 9)

	committed, err := DecodeCommitted(ZkVMOpenVM, proof)
	require.NoError(t, err)
	require.Equal(t, expected, committed.PublicInput)

	_, err = DecodeCommitted(ZkVMOpenVM, []byte{1, 2, 3})
	require.Error(t, err)
}
