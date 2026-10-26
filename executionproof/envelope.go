package executionproof

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
	blsu "github.com/protolambda/bls12-381-util"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
)

// Envelope is the ExecutionProofEnvelope a prover signs: the proof bytes, the
// proof type and the beacon block the proof belongs to.
//
// The proof bytes are hashed as List[byte, MAX_PROOF_SIZE], as the provers sign
// and Lighthouse verifies them; the EIP-8025 spec defines ProofData as a
// ProgressiveList, which merkleizes differently.
type Envelope struct {
	ProofData       []byte      `ssz-max:"4194304"`
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

// SignedEnvelope is the SignedExecutionProofEnvelope a node serves and
// accepts: the envelope with the prover's validator index and signature.
type SignedEnvelope struct {
	Message        Envelope
	ValidatorIndex phase0.ValidatorIndex
	Signature      phase0.BLSSignature `ssz-size:"96"`
}

// MarshalSSZ returns the SSZ serialization of the signed envelope.
func (e *SignedEnvelope) MarshalSSZ(dynSsz *dynssz.DynSsz) ([]byte, error) {
	return dynSsz.MarshalSSZ(e)
}

// MarshalJSON encodes the signed envelope in the beacon API's JSON form, the
// one GET and POST /eth/v1/beacon/execution_proofs use: numbers as decimal
// strings, bytes as 0x-prefixed hex.
func (e *SignedEnvelope) MarshalJSON() ([]byte, error) {
	return json.Marshal(signedEnvelopeJSON{
		Message: envelopeJSON{
			ProofData:       fmt.Sprintf("0x%x", e.Message.ProofData),
			ProofType:       strconv.FormatUint(uint64(e.Message.ProofType), 10),
			BeaconBlockRoot: fmt.Sprintf("0x%x", e.Message.BeaconBlockRoot[:]),
		},
		ValidatorIndex: strconv.FormatUint(uint64(e.ValidatorIndex), 10),
		Signature:      fmt.Sprintf("0x%x", e.Signature[:]),
	})
}

type envelopeJSON struct {
	ProofData       string `json:"proof_data"`
	ProofType       string `json:"proof_type"`
	BeaconBlockRoot string `json:"beacon_block_root"`
}

type signedEnvelopeJSON struct {
	Message        envelopeJSON `json:"message"`
	ValidatorIndex string       `json:"validator_index"`
	Signature      string       `json:"signature"`
}
