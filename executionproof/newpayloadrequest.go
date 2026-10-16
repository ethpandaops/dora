package executionproof

import (
	"crypto/sha256"
	"fmt"

	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/deneb"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
)

// NewPayloadRequest is the request the engine is asked to validate and the
// guest programs prove; its hash tree root is the NewPayloadRequestRoot of a
// proof's public input. The payload and requests are fork-agnostic and hash
// through their fork's view. The guests merkleize the request as a plain
// container with a progressive list of versioned hashes (execution-specs
// stateless schema), which the test vectors pin down.
type NewPayloadRequest struct {
	ExecutionPayload      *all.ExecutionPayload
	VersionedHashes       []deneb.VersionedHash `ssz-type:"progressive-list"`
	ParentBeaconBlockRoot phase0.Root
	ExecutionRequests     *all.ExecutionRequests
}

// VersionedHash returns the versioned hash of a blob KZG commitment.
func VersionedHash(commitment deneb.KZGCommitment) deneb.VersionedHash {
	hash := sha256.Sum256(commitment[:])
	hash[0] = 0x01 // VERSIONED_HASH_VERSION_KZG
	return deneb.VersionedHash(hash)
}

// BlockNewPayloadRequest builds the NewPayloadRequest of a block from its
// signed block (for the bid's blob commitments) and its payload envelope.
func BlockNewPayloadRequest(block *all.SignedBeaconBlock, envelope *all.SignedExecutionPayloadEnvelope) (*NewPayloadRequest, error) {
	if block == nil || block.Message == nil || block.Message.Body == nil || block.Message.Body.SignedExecutionPayloadBid == nil || block.Message.Body.SignedExecutionPayloadBid.Message == nil {
		return nil, fmt.Errorf("block without execution payload bid")
	}
	if envelope == nil || envelope.Message == nil || envelope.Message.Payload == nil || envelope.Message.ExecutionRequests == nil {
		return nil, fmt.Errorf("block without execution payload envelope")
	}

	commitments := block.Message.Body.SignedExecutionPayloadBid.Message.BlobKZGCommitments
	hashes := make([]deneb.VersionedHash, 0, len(commitments))
	for _, commitment := range commitments {
		hashes = append(hashes, VersionedHash(commitment))
	}

	return &NewPayloadRequest{
		ExecutionPayload:      envelope.Message.Payload,
		VersionedHashes:       hashes,
		ParentBeaconBlockRoot: envelope.Message.ParentBeaconBlockRoot,
		ExecutionRequests:     envelope.Message.ExecutionRequests,
	}, nil
}

// Root returns the hash tree root of the request.
func (r *NewPayloadRequest) Root(dynSsz *dynssz.DynSsz) (phase0.Root, error) {
	return dynSsz.HashTreeRoot(r)
}
