package executionproof

import (
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
