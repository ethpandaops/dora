package executionproof

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
	blsu "github.com/protolambda/bls12-381-util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvelopeSignature(t *testing.T) {
	ds := dynssz.NewDynSsz(nil)

	var secret blsu.SecretKey
	var ikm [32]byte
	ikm[0] = 7
	require.NoError(t, secret.Deserialize(&ikm))
	blsPubkey, err := blsu.SkToPk(&secret)
	require.NoError(t, err)
	pubkey := phase0.BLSPubKey(blsPubkey.Serialize())

	envelope := &Envelope{ProofData: []byte{1, 2, 3}, ProofType: 5, BeaconBlockRoot: phase0.Root{0xaa}}
	domainType := phase0.DomainType{0x0f, 0, 0, 0}
	forkVersion := phase0.Version{1, 0, 0, 0}
	genesisRoot := phase0.Root{0x42}

	signingRoot, err := envelope.SigningRoot(ds, domainType, forkVersion, genesisRoot)
	require.NoError(t, err)
	signature := phase0.BLSSignature(blsu.Sign(&secret, signingRoot[:]).Serialize())

	valid, err := envelope.VerifySignature(ds, domainType, pubkey, signature, forkVersion, genesisRoot)
	require.NoError(t, err)
	assert.True(t, valid)

	// another domain, fork, block or proof changes the signing root
	valid, err = envelope.VerifySignature(ds, phase0.DomainType{0x0e, 0, 0, 0}, pubkey, signature, forkVersion, genesisRoot)
	require.NoError(t, err)
	assert.False(t, valid)
	valid, err = envelope.VerifySignature(ds, domainType, pubkey, signature, phase0.Version{2, 0, 0, 0}, genesisRoot)
	require.NoError(t, err)
	assert.False(t, valid)
	other := &Envelope{ProofData: []byte{1, 2, 4}, ProofType: 5, BeaconBlockRoot: phase0.Root{0xaa}}
	valid, err = other.VerifySignature(ds, domainType, pubkey, signature, forkVersion, genesisRoot)
	require.NoError(t, err)
	assert.False(t, valid)

	// a malformed signature is invalid, not an error
	valid, err = envelope.VerifySignature(ds, domainType, pubkey, phase0.BLSSignature{}, forkVersion, genesisRoot)
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestSignedEnvelopeEncodings(t *testing.T) {
	signed := &SignedEnvelope{
		Message: Envelope{
			ProofData:       []byte{1, 2, 3},
			ProofType:       5,
			BeaconBlockRoot: phase0.Root{0xaa},
		},
		ValidatorIndex: 42,
	}
	signed.Signature[0] = 0xbb

	// the beacon API JSON form
	encoded, err := json.Marshal(signed)
	require.NoError(t, err)
	var generic map[string]any
	require.NoError(t, json.Unmarshal(encoded, &generic))
	message := generic["message"].(map[string]any)
	assert.Equal(t, "0x010203", message["proof_data"])
	assert.Equal(t, "5", message["proof_type"])
	assert.Equal(t, "0xaa00000000000000000000000000000000000000000000000000000000000000", message["beacon_block_root"])
	assert.Equal(t, "42", generic["validator_index"])
	assert.Equal(t, "0xbb"+hexZeros(95), generic["signature"])

	// SSZ round trip
	dynSsz := dynssz.NewDynSsz(nil)
	data, err := signed.MarshalSSZ(dynSsz)
	require.NoError(t, err)
	var decoded SignedEnvelope
	require.NoError(t, dynSsz.UnmarshalSSZ(&decoded, data))
	assert.Equal(t, *signed, decoded)
}

func hexZeros(n int) string {
	zeros := make([]byte, n*2)
	for i := range zeros {
		zeros[i] = '0'
	}
	return string(zeros)
}

// TestEnvelopeRoot pins the envelope root to an independent merkleization with
// the proof bytes as List[byte, MAX_PROOF_SIZE], the root the provers sign.
func TestEnvelopeRoot(t *testing.T) {
	hash := func(a, b [32]byte) [32]byte { return sha256.Sum256(append(a[:], b[:]...)) }

	// List[byte, 4194304]: one data chunk below a tree of 131072 chunks (depth 17)
	var node [32]byte
	copy(node[:], []byte{1, 2, 3})

	var zero [32]byte
	for range 17 {
		node = hash(node, zero)
		zero = hash(zero, zero)
	}

	var length [32]byte
	length[0] = 3
	proofData := hash(node, length)

	var proofType, blockRoot [32]byte
	proofType[0] = 5
	blockRoot[0] = 0xaa
	expected := hash(hash(proofData, proofType), hash(blockRoot, [32]byte{}))

	root, err := dynssz.NewDynSsz(nil).HashTreeRoot(&Envelope{ProofData: []byte{1, 2, 3}, ProofType: 5, BeaconBlockRoot: phase0.Root{0xaa}})
	require.NoError(t, err)
	assert.Equal(t, expected, root)
}
