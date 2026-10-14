package beacon

import (
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/clients/consensus/rpc"
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

	// by-slot proofs (no block root) are only attributed to a block that is alone in its slot
	entryB := cache.entries[rootB]
	require.Len(t, cache.convertProofs(entryB, proofApiBySlot, []*rpc.ExecutionProof{testRpcProof(3, phase0.Root{}, 10)}), 1)
	indexer.blockCache.createOrGetBlock(phase0.Root{0xbb}, 12)
	require.Empty(t, cache.convertProofs(entryB, proofApiBySlot, []*rpc.ExecutionProof{testRpcProof(3, phase0.Root{}, 10)}))

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
	require.True(t, restored[1].Signed)
	require.Equal(t, byte(5), restored[1].Signature[0])
	require.Len(t, restored[1].ProofData, 100)
	require.Equal(t, phase0.Slot(10), other.entries[rootA].slot)
	require.Empty(t, other.collectDirtyEntries(), "restored proofs are not written again")

	// a block with a request in flight is not dropped, and its slot not reported as dropped
	entryA.fetching = true
	objects, touched, dropped := cache.dropBefore(11, false, true)
	require.Empty(t, objects)
	require.Empty(t, touched)
	require.Empty(t, dropped)
	entryA.fetching = false

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
