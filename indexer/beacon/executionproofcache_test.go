package beacon

import (
	"context"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/consensus/rpc"
	dtypes "github.com/ethpandaops/dora/types"
)

// newTestExecutionProofCache builds an execution proof cache on a minimal indexer
// with a pebble blockdb in a temp dir, so flushed proofs can be read back.
func newTestExecutionProofCache(t *testing.T) *executionProofCache {
	t.Helper()

	logger, _ := test.NewNullLogger()
	require.NoError(t, blockdb.InitWithPebble(dtypes.PebbleBlockDBConfig{Path: t.TempDir()}, logrus.NewEntry(logger)))
	t.Cleanup(func() {
		_ = blockdb.GlobalBlockDb.Close()
		blockdb.GlobalBlockDb = nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	indexer := &Indexer{
		ctx:           ctx,
		logger:        logrus.NewEntry(logger),
		consensusPool: consensus.NewPool(ctx, logrus.NewEntry(logger)),
	}
	indexer.blockCache = newBlockCache(indexer)

	cache := newExecutionProofCache(indexer)
	cache.setLiveFromSlot(0)

	return cache
}

func testRpcProof(proofType uint8, root phase0.Root, size int) *rpc.ExecutionProof {
	proof := &rpc.ExecutionProof{
		ProofType:       proofType,
		ProofData:       make([]byte, size),
		BeaconBlockRoot: root,
		Signed:          true,
		ValidatorIndex:  uint64(proofType),
	}
	proof.Signature[0] = proofType
	return proof
}

func TestExecutionProofCache(t *testing.T) {
	cache := newTestExecutionProofCache(t)
	require.True(t, cache.enabled())

	rootA, rootB := phase0.Root{0xa}, phase0.Root{0xb}
	cache.indexer.blockCache.createOrGetBlock(rootA, 10)
	cache.indexer.blockCache.createOrGetBlock(rootB, 12)

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
	converted := cache.convertProofs(entryA, proofApiByRoot, []*rpc.ExecutionProof{
		testRpcProof(5, rootA, 100),
		testRpcProof(1, rootA, 50),
		testRpcProof(2, rootB, 10),
	})
	require.Len(t, converted, 2)
	for _, proof := range converted {
		entryA.proofs[proof.ProofType] = proof
	}
	require.Equal(t, uint8(2), cache.getProofCount(10, rootA))
	proofs := cache.getProofs(rootA)
	require.Len(t, proofs, 2)
	require.Equal(t, uint8(1), proofs[0].ProofType, "proofs are returned in type order")
	require.Equal(t, uint8(5), proofs[1].ProofType)
	require.Equal(t, uint32(100), proofs[1].DataSize)

	// by-slot proofs (no block root) are only attributed to a block that is alone in its slot
	entryB := cache.entries[rootB]
	require.Len(t, cache.convertProofs(entryB, proofApiBySlot, []*rpc.ExecutionProof{testRpcProof(3, phase0.Root{}, 10)}), 1)
	cache.indexer.blockCache.createOrGetBlock(phase0.Root{0xbb}, 12)
	require.Empty(t, cache.convertProofs(entryB, proofApiBySlot, []*rpc.ExecutionProof{testRpcProof(3, phase0.Root{}, 10)}))

	// a block with a request in flight is not flushed
	entryA.fetching = true
	require.Empty(t, cache.collectFlushObjects(11, false))
	entryA.fetching = false

	// the flush returns the proofs as per-slot object, forgets the bytes and remembers the count
	objects := cache.collectFlushObjects(11, false)
	require.Len(t, objects, 1)
	require.Len(t, objects[10].ExecutionProofs, 2)
	require.Equal(t, rootA[:], objects[10].ExecutionProofs[0].BlockRoot[:])
	require.Nil(t, cache.getProofs(rootA))
	require.Len(t, cache.entries, 1, "block B stays queued")
	require.Equal(t, uint8(2), cache.getProofCount(10, rootA))
	require.Equal(t, uint8(0), cache.getProofCount(12, rootB))

	// a block without proofs is dropped silently at flush
	_, err := blockdb.GlobalBlockDb.AddSlotMeta(cache.indexer.ctx, objects[10])
	require.NoError(t, err)
	require.Empty(t, cache.collectFlushObjects(13, false))
	require.Empty(t, cache.entries)

	// the count of a block before the cache's knowledge is read from the stored object
	cache.setLiveFromSlot(20)
	delete(cache.counts, rootA)
	require.Equal(t, uint8(2), cache.getProofCount(10, rootA))
	require.Equal(t, uint8(0), cache.getProofCount(10, phase0.Root{0xaa}), "sibling block without proofs")

	stored, err := blockdb.GlobalBlockDb.GetSlotMeta(cache.indexer.ctx, 10, btypes.SlotMetaFlagExecutionProofData)
	require.NoError(t, err)
	require.Len(t, stored.ExecutionProofs, 2)
	require.Len(t, stored.ExecutionProofs[1].ProofData, 100)

	// the cleanup needs the chain specs; without them it leaves the counts alone
	cache.counts[rootB] = &proofCountEntry{slot: 0, count: 1}
	cache.cleanupCache()
	require.NotNil(t, cache.counts[rootB])
}
