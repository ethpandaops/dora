package beacon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/executionproof/verifiers"
)

func TestExecutionProofCache(t *testing.T) {
	indexer := newTestMetadataIndexer(t, true)
	cache := indexer.executionProofCache

	rootA, rootB := phase0.Root{0xa}, phase0.Root{0xb}
	blockA, _ := indexer.blockCache.createOrGetBlock(rootA, 10)
	blockB, _ := indexer.blockCache.createOrGetBlock(rootB, 12)

	cache.trackBlock(10, rootA)
	cache.trackBlock(12, rootB)
	cache.trackBlock(10, rootA) // duplicate is ignored
	require.Len(t, cache.entries, 2)
	minSlot, maxSlot := cache.slotBounds()
	require.Equal(t, phase0.Slot(10), minSlot)
	require.Equal(t, phase0.Slot(12), maxSlot)

	// a request is due fetchDelay slots after the block's slot
	entryA := cache.entries[rootA]
	require.Equal(t, phase0.Slot(10+proofFetchDelaySlotsDefault), entryA.nextFetch)

	// by-root proofs of other blocks are dropped, the rest keyed by type
	addTestProofs(cache, rootA, testRpcProof(5, rootA, 100), testRpcProof(1, rootA, 50), testRpcProof(2, rootB, 10))
	require.Len(t, entryA.proofs, 2)
	require.Equal(t, int16(2), cache.getBlockProofCount(blockA))
	proofs := cache.getProofs(rootA)
	require.Len(t, proofs, 2)
	require.Equal(t, uint8(1), proofs[0].ProofType, "proofs are returned in type order")
	require.Equal(t, uint8(5), proofs[1].ProofType)
	require.Equal(t, uint32(100), proofs[1].DataSize)

	// the signature check and verification outcomes travel with the proof
	require.False(t, entryA.proofs[5].SignatureValid, "no validator set in the test indexer")
	entryA.proofs[5].SignatureValid = true
	entryA.proofs[5].ProverActive = true
	entryA.proofs[5].Verification = uint8(verifiers.StatusValid)
	entryA.proofs[5].Verifier = "v0.19.2"

	// every proof is offered as crash copy once and restores elsewhere
	entries := cache.collectDirtyEntries()
	require.Len(t, entries, 2)
	require.Empty(t, cache.collectDirtyEntries())
	other := newTestMetadataIndexer(t, true).executionProofCache
	for _, entry := range entries {
		require.NoError(t, other.restoreEntry(entry))
	}
	restored := other.getProofs(rootA)
	require.Len(t, restored, 2)
	require.Equal(t, uint8(5), restored[1].ProofType)
	require.Equal(t, uint64(5), restored[1].ValidatorIndex)
	require.Equal(t, byte(5), restored[1].Signature[0])
	require.Len(t, restored[1].ProofData, 100)
	require.True(t, restored[1].SignatureValid)
	require.True(t, restored[1].ProverActive)
	require.Equal(t, uint8(verifiers.StatusValid), restored[1].Verification)
	require.Equal(t, "v0.19.2", restored[1].Verifier)
	require.False(t, restored[0].SignatureValid)
	require.Equal(t, phase0.Slot(10), other.entries[rootA].slot)
	require.Empty(t, other.collectDirtyEntries(), "restored proofs are not written again")
	require.Equal(t, map[uint8]bool{1: true}, other.entries[rootA].unverified, "only the proof without verdict is verified again")

	// a block with a request in flight is not dropped, and its slot not reported as dropped
	entryA.fetching = true
	objects, touched, dropped := cache.dropBefore(11, false, true)
	require.Empty(t, objects)
	require.Empty(t, touched)
	require.Empty(t, dropped)
	entryA.fetching = false
	entryA.verifying = true
	_, _, dropped = cache.dropBefore(11, false, true)
	require.Empty(t, dropped, "a verification in flight keeps the block as well")
	entryA.verifying = false

	// the drop returns the proofs as per-slot object, forgets the bytes and
	// writes the count into the block
	objects, touched, dropped = cache.dropBefore(11, false, true)
	require.Len(t, objects, 1)
	require.Len(t, objects[10].ExecutionProofs, 2)
	require.Equal(t, rootA[:], objects[10].ExecutionProofs[0].BlockRoot[:])
	require.Equal(t, []*Block{blockA}, touched)
	require.Equal(t, []uint64{10}, dropped)
	require.Nil(t, cache.getProofs(rootA))
	require.Len(t, cache.entries, 1, "block B stays queued")
	require.NotNil(t, blockA.GetMetadata())
	require.Equal(t, int16(2), blockA.GetMetadata().ProofCount)
	require.Equal(t, int16(2), cache.getBlockProofCount(blockA))
	require.Equal(t, int16(0), cache.getBlockProofCount(blockB))

	// a block without proofs leaves the cache with a zero count and no object
	objects, touched, dropped = cache.dropBefore(13, false, true)
	require.Empty(t, objects)
	require.Equal(t, []*Block{blockB}, touched)
	require.Equal(t, []uint64{12}, dropped)
	require.Empty(t, cache.entries)
	require.NotNil(t, blockB.GetMetadata())
	require.Equal(t, int16(0), blockB.GetMetadata().ProofCount)
}

func TestExecutionProofVerification(t *testing.T) {
	indexer := newTestMetadataIndexer(t, false)
	cache := indexer.executionProofCache
	defer cache.close()

	// a verifier setup that fails is retried after the backoff
	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))
	cache.verifierCacheDir = filepath.Join(blocked, "cache")
	require.Nil(t, cache.getVerifiers(), "the cache dir cannot be created")
	require.False(t, cache.verifiersRetryAt.IsZero())

	// proofs stay unverified while the verifiers are not available
	root := phase0.Root{0xc}
	cache.trackBlock(10, root)
	entry := cache.entries[root]
	proofs := cache.convertProofs(entry, []*rpc.ExecutionProof{testRpcProof(1, root, 10), testRpcProof(200, root, 10)})
	cache.verifyProofs(entry, proofs)
	require.Equal(t, map[uint8]bool{1: true}, entry.unverified)
	require.Equal(t, uint8(verifiers.StatusUnknown), proofs[0].Verification)
	require.Equal(t, uint8(verifiers.StatusUnverifiable), proofs[1].Verification, "an unknown proof type has no verifier")
	require.True(t, entry.dirty[200])

	cache.verifierCacheDir = t.TempDir()
	require.Nil(t, cache.getVerifiers(), "not before the backoff")
	cache.verifiersRetryAt = time.Time{}
	require.NotNil(t, cache.getVerifiers())

	// and while the block's payload is not known; the pending verification is
	// dispatched by the slot tick in the background
	cache.verifyProofs(entry, proofs[:1])
	require.Equal(t, map[uint8]bool{1: true}, entry.unverified)
	addTestProofs(cache, root, testRpcProof(1, root, 10))
	cache.processDueFetches(10)
	require.Eventually(t, func() bool {
		cache.cacheMutex.RLock()
		defer cache.cacheMutex.RUnlock()
		return !entry.verifying
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, map[uint8]bool{1: true}, entry.unverified, "still waiting for the payload")

	// verification disabled: nothing is queued
	cache.verificationDisabled = true
	delete(entry.unverified, 1)
	cache.verifyProofs(entry, proofs[:1])
	require.Empty(t, entry.unverified)
}
