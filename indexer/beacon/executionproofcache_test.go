package beacon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"

	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/executionproof/verifiers"
)

// waitProofs waits until the entry of a block holds n proofs and no check is in flight.
func waitProofs(t *testing.T, cache *executionProofCache, root phase0.Root, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		cache.cacheMutex.RLock()
		defer cache.cacheMutex.RUnlock()
		entry := cache.entries[root]
		return entry != nil && len(entry.proofs) == n && !entry.checking
	}, 5*time.Second, 10*time.Millisecond)
}

func TestExecutionProofCache(t *testing.T) {
	indexer := newTestMetadataIndexer(t, true)
	cache := indexer.executionProofCache
	cache.verificationDisabled = true

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

	// delivered proofs are checked in the background and kept by type, the first
	// of each; a later proof of a known type is dropped
	cache.addProof(testRpcProof(5, rootA, 100))
	cache.addProof(testRpcProof(1, rootA, 50))
	cache.addProof(testRpcProof(1, rootA, 60))
	waitProofs(t, cache, rootA, 2)
	entryA := cache.entries[rootA]
	require.Equal(t, int16(2), cache.getBlockProofCount(blockA))
	proofs := cache.getProofs(rootA)
	require.Len(t, proofs, 2)
	require.Equal(t, uint8(1), proofs[0].ProofType, "proofs are returned in type order")
	require.Equal(t, uint32(50), proofs[0].DataSize, "the first proof of a type is kept")
	require.Equal(t, uint8(5), proofs[1].ProofType)
	require.Equal(t, uint32(100), proofs[1].DataSize)
	cache.addProof(testRpcProof(5, rootA, 7))
	require.Equal(t, uint32(100), cache.getProofs(rootA)[1].DataSize)

	// the signature check and verification outcomes travel with the proof
	proofA5 := entryA.findProof(proofKey{proofType: 5, prover: 5})
	require.False(t, proofA5.SignatureValid, "no validator set in the test indexer")
	proofA5.SignatureValid = true
	proofA5.ProverActive = true
	proofA5.Verification = uint8(verifiers.StatusValid)
	proofA5.Verifier = "v0.19.2"

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
	require.Equal(t, map[proofKey]bool{{proofType: 1, prover: 1}: true}, other.entries[rootA].unverified, "only the proof without verdict is verified again")

	// a block with a check or verification in flight is not dropped, and its slot
	// not reported as dropped
	entryA.checking = true
	objects, touched, dropped := cache.dropBefore(11, false, true)
	require.Empty(t, objects)
	require.Empty(t, touched)
	require.Empty(t, dropped)
	entryA.checking = false
	entryA.verifying = true
	_, _, dropped = cache.dropBefore(11, false, true)
	require.Empty(t, dropped)
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
	require.Len(t, cache.entries, 1, "block B stays tracked")
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

	// a proof of a block that left the metadata window is dropped
	indexer.metadataCutoff.Store(11)
	cache.addProof(testRpcProof(1, rootA, 10))
	require.Empty(t, cache.entries)
	require.Empty(t, cache.held)
	indexer.metadataCutoff.Store(0)

	// a proof of a block the indexer has not seen waits for the block
	rootC, rootD := phase0.Root{0xc}, phase0.Root{0xd}
	cache.addProof(testRpcProof(3, rootC, 10))
	cache.addProof(testRpcProof(3, rootC, 20))
	cache.addProof(testRpcProof(4, rootD, 10))
	require.Len(t, cache.held[rootC], 1, "the first proof of a type is held")
	require.Empty(t, cache.entries)
	cache.trackBlock(14, rootC)
	waitProofs(t, cache, rootC, 1)
	require.Equal(t, uint32(10), cache.getProofs(rootC)[0].DataSize)
	require.Nil(t, cache.held[rootC])

	// another prover's proof of the same type is a distinct proof, kept after
	// the earlier one
	cache.addProof(testRpcProofFrom(3, 7, rootC, 30))
	cache.addProof(testRpcProofFrom(3, 7, rootC, 40))
	waitProofs(t, cache, rootC, 2)
	proofsC := cache.getProofs(rootC)
	require.Equal(t, uint64(3), proofsC[0].ValidatorIndex)
	require.Equal(t, uint64(7), proofsC[1].ValidatorIndex)
	require.Equal(t, uint32(30), proofsC[1].DataSize)
	require.Equal(t, 2, len(cache.collectDirtyEntries()))

	// and is dropped when the block does not show up
	seen := cache.held[rootD][0].seen
	cache.processSlot(seen + proofHoldSlots)
	require.Len(t, cache.held[rootD], 1)
	cache.processSlot(seen + proofHoldSlots + 1)
	require.Empty(t, cache.held)
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
	proofs := []*btypes.SlotExecutionProof{convertProof(testRpcProof(1, root, 10)), convertProof(testRpcProof(200, root, 10))}
	cache.verifyProofs(entry, proofs)
	require.Equal(t, map[proofKey]bool{{proofType: 1, prover: 1}: true}, entry.unverified)
	require.Equal(t, uint8(verifiers.StatusUnknown), proofs[0].Verification)
	require.Equal(t, uint8(verifiers.StatusUnverifiable), proofs[1].Verification, "an unknown proof type has no verifier")
	require.True(t, entry.dirty[proofKey{proofType: 200, prover: 200}])

	cache.verifierCacheDir = t.TempDir()
	require.Nil(t, cache.getVerifiers(), "not before the backoff")
	cache.verifiersRetryAt = time.Time{}
	require.NotNil(t, cache.getVerifiers())

	// and while the block's payload is not known; the pending verification is
	// dispatched by the slot tick in the background
	cache.verifyProofs(entry, proofs[:1])
	require.Equal(t, map[proofKey]bool{{proofType: 1, prover: 1}: true}, entry.unverified)
	addTestProofs(cache, root, testRpcProof(1, root, 10))
	cache.processSlot(10)
	require.Eventually(t, func() bool {
		cache.cacheMutex.RLock()
		defer cache.cacheMutex.RUnlock()
		return !entry.verifying
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, map[proofKey]bool{{proofType: 1, prover: 1}: true}, entry.unverified, "still waiting for the payload")

	// verification disabled: nothing is queued
	cache.verificationDisabled = true
	delete(entry.unverified, proofKey{proofType: 1, prover: 1})
	cache.verifyProofs(entry, proofs[:1])
	require.Empty(t, entry.unverified)
}
