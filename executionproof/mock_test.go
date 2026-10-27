package executionproof

import (
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeMock(t *testing.T) {
	expected := &PublicInput{NewPayloadRequestRoot: phase0.Root{0xa5}, SuccessfulValidation: true, ChainID: 3151908, SchemaID: 0x1501}
	// as zkboost serializes it: the offsets of public_values (8) and proof (51), then both
	proof := append(append([]byte{8, 0, 0, 0, 51, 0, 0, 0}, expected.Marshal()...), 9, 9, 9)

	committed, err := DecodeMock(proof)
	require.NoError(t, err)
	assert.Equal(t, expected, committed.PublicInput)

	// public values that are no public input
	odd := append(append([]byte{8, 0, 0, 0, 10, 0, 0, 0}, 1, 2), 9)
	committed, err = DecodeMock(odd)
	require.NoError(t, err)
	assert.Nil(t, committed.PublicInput)
	assert.NotEmpty(t, committed.DecodeError)

	_, err = DecodeMock([]byte{1, 2, 3})
	assert.Error(t, err)
}
