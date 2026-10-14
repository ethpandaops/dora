package executionproof

import (
	"encoding/hex"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureProof rebuilds a proof from its recorded head and tail, with zero bytes
// in between: the extractors only read the ends.
func fixtureProof(t *testing.T, zkvm ZkVM) []byte {
	t.Helper()

	vector, ok := proofVectors[zkvm]
	require.True(t, ok, "vector for %s", zkvm)
	head, err := hex.DecodeString(vector.head)
	require.NoError(t, err)
	tail, err := hex.DecodeString(vector.tail)
	require.NoError(t, err)

	proof := make([]byte, vector.size)
	copy(proof, head)
	copy(proof[len(proof)-len(tail):], tail)
	return proof
}

func TestDecodeCommitted(t *testing.T) {
	expected, err := UnmarshalPublicInput(mustHex(fixturePublicValues))
	require.NoError(t, err)
	assert.Equal(t, "68e2250786a622ab175a8149c63d220704e7ed9610a12b7864047510235e7bb7", hex.EncodeToString(expected.NewPayloadRequestRoot[:]))
	assert.True(t, expected.SuccessfulValidation)
	assert.Equal(t, uint64(1), expected.ChainID)
	schemaID, ok := SchemaIDForFork(version.DataVersionGloas)
	require.True(t, ok)
	assert.Equal(t, schemaID, expected.SchemaID)
	assert.Equal(t, mustHex(fixturePublicValues), expected.Marshal())

	tests := []struct {
		zkvm        ZkVM
		valuesLen   int
		proofMode   string
		sdkVersion  string
		programVKOf uint8
	}{
		{zkvm: ZkVMSP1, valuesLen: PublicInputSize, proofMode: "compressed", sdkVersion: "v6.1.0"},
		{zkvm: ZkVMZisk, valuesLen: 256, programVKOf: 3},
		{zkvm: ZkVMOpenVM, valuesLen: 256},
	}
	for _, test := range tests {
		t.Run(string(test.zkvm), func(t *testing.T) {
			committed, err := DecodeCommitted(test.zkvm, fixtureProof(t, test.zkvm))
			require.NoError(t, err)
			assert.Len(t, committed.PublicValues, test.valuesLen)
			assert.Empty(t, committed.DecodeError)
			require.NotNil(t, committed.PublicInput)
			assert.Equal(t, expected, committed.PublicInput)
			assert.Equal(t, test.proofMode, committed.ProofMode)
			assert.Equal(t, test.sdkVersion, committed.SdkVersion)
			if test.programVKOf != 0 {
				assert.Equal(t, Lookup(test.programVKOf).ProgramVK, committed.ProgramVK, "the zisk proof carries its program vk")
			} else {
				assert.Nil(t, committed.ProgramVK)
			}
		})
	}

	// a proof whose public values do not decode is reported, not rejected
	proof := fixtureProof(t, ZkVMZisk)
	proof[len(proof)-18-8] = 0xff // the last public value word, zero padding in a valid proof
	committed, err := DecodeCommitted(ZkVMZisk, proof)
	require.NoError(t, err)
	assert.Nil(t, committed.PublicInput)
	assert.Contains(t, committed.DecodeError, "padding")

	// a proof of another encoding is rejected
	_, err = DecodeCommitted(ZkVMSP1, fixtureProof(t, ZkVMZisk))
	assert.Error(t, err)
	_, err = DecodeCommitted(ZkVMZisk, fixtureProof(t, ZkVMSP1))
	assert.Error(t, err)
	_, err = DecodeCommitted(ZkVMOpenVM, []byte{1, 2, 3})
	assert.Error(t, err)
}

func TestProofTypes(t *testing.T) {
	for id := uint8(1); id <= 7; id++ {
		proofType := Lookup(id)
		require.NotNil(t, proofType, "type %d", id)
		assert.Equal(t, id, proofType.ID)
		assert.NotEmpty(t, proofType.ProgramVK)
		if proofType.ZkVM != ZkVMOpenVM {
			assert.Len(t, proofType.ProgramVK, 32)
		}
	}
	assert.Nil(t, Lookup(0))
	assert.Nil(t, Lookup(8))
	assert.Equal(t, "SP1 + reth", Lookup(5).Name())
}

func TestSchemaIDForFork(t *testing.T) {
	schemaID, ok := SchemaIDForFork(version.DataVersionGloas)
	require.True(t, ok)
	assert.Equal(t, uint16(0x1501), schemaID)

	_, ok = SchemaIDForFork(version.DataVersionHeze)
	assert.False(t, ok, "no schema assigned yet")
	_, ok = SchemaIDForFork(version.DataVersionFulu)
	assert.False(t, ok, "no execution proofs before gloas")
}
