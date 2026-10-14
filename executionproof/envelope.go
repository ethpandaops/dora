package executionproof

import (
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
	blsu "github.com/protolambda/bls12-381-util"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
)

// Envelope is the ExecutionProofEnvelope a prover signs: the proof bytes, the
// proof type and the beacon block the proof belongs to.
type Envelope struct {
	ProofData       []byte      `ssz-type:"progressive-list"`
	ProofType       uint8       `ssz-type:"uint8"`
	BeaconBlockRoot phase0.Root `ssz-size:"32"`
}

// SigningRoot returns the signing root of the envelope under the execution
// proof domain (DOMAIN_EXECUTION_PROOF of the chain spec) of the given fork.
func (e *Envelope) SigningRoot(dynSsz *dynssz.DynSsz, domainType phase0.DomainType, forkVersion phase0.Version, genesisValidatorsRoot phase0.Root) (phase0.Root, error) {
	root, err := dynSsz.HashTreeRoot(e)
	if err != nil {
		return phase0.Root{}, err
	}

	domain := zrnt_common.ComputeDomain(
		zrnt_common.BLSDomainType(domainType),
		zrnt_common.Version(forkVersion),
		zrnt_common.Root(genesisValidatorsRoot),
	)
	return phase0.Root(zrnt_common.ComputeSigningRoot(zrnt_common.Root(root), domain)), nil
}

// VerifySignature checks the prover signature of the envelope against the
// prover's public key.
func (e *Envelope) VerifySignature(dynSsz *dynssz.DynSsz, domainType phase0.DomainType, pubkey phase0.BLSPubKey, signature phase0.BLSSignature, forkVersion phase0.Version, genesisValidatorsRoot phase0.Root) (bool, error) {
	signingRoot, err := e.SigningRoot(dynSsz, domainType, forkVersion, genesisValidatorsRoot)
	if err != nil {
		return false, err
	}

	pubkeyData := zrnt_common.BLSPubkey(pubkey)
	blsPubkey, err := pubkeyData.Pubkey()
	if err != nil {
		return false, nil
	}
	signatureData := zrnt_common.BLSSignature(signature)
	blsSignature, err := signatureData.Signature()
	if err != nil {
		return false, nil
	}

	return blsu.Verify(blsPubkey, signingRoot[:], blsSignature), nil
}
