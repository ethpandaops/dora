package executionproof

import (
	"encoding/hex"
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

func TestEnvelopeHashTreeRoot(t *testing.T) {
	// from the consensus-specs eip8025 pyspec, where ProofData is ByteList[MAX_PROOF_SIZE]
	envelope := &Envelope{ProofData: []byte{1, 2, 3}, ProofType: 5, BeaconBlockRoot: phase0.Root{0xaa}}
	root, err := dynssz.NewDynSsz(nil).HashTreeRoot(envelope)
	require.NoError(t, err)
	assert.Equal(t, "6c80b82462fd8639ac695bb98318d8204a061a00bf246c55876754b2b1876aad", hex.EncodeToString(root[:]))
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
