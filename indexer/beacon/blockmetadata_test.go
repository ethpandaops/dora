package beacon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	dtypes "github.com/ethpandaops/dora/types"
)

// newTestMetadataIndexer builds a minimal indexer with the metadata caches, a
// migrated sqlite database and, if withBlockDb is set, a pebble blockdb in a
// temp dir. Both stores are shared by the indexers of one test.
func newTestMetadataIndexer(t *testing.T, withBlockDb bool) *Indexer {
	t.Helper()

	logger, _ := test.NewNullLogger()
	if db.ReaderDb == nil {
		// a PostgreSQL test database if DORA_TEST_PGSQL_HOST is set (reset to an
		// empty schema first), otherwise a sqlite file in a temp dir
		if host := os.Getenv("DORA_TEST_PGSQL_HOST"); host != "" {
			pgsql := &dtypes.PgsqlDatabaseConfig{
				Host:     host,
				Port:     os.Getenv("DORA_TEST_PGSQL_PORT"),
				Username: os.Getenv("DORA_TEST_PGSQL_USER"),
				Password: os.Getenv("DORA_TEST_PGSQL_PASSWORD"),
				Name:     os.Getenv("DORA_TEST_PGSQL_NAME"),
			}
			db.MustInitDB(&dtypes.DatabaseConfig{Engine: "pgsql", Pgsql: pgsql, PgsqlWriter: (*dtypes.PgsqlWriterDatabaseConfig)(pgsql)})
			require.NoError(t, db.RunDBTransaction(func(tx *sqlx.Tx) error {
				_, err := tx.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public")
				return err
			}))
		} else {
			db.MustInitDB(&dtypes.DatabaseConfig{
				Engine: "sqlite",
				Sqlite: &dtypes.SqliteDatabaseConfig{File: filepath.Join(t.TempDir(), "test.sqlite")},
			})
		}
		require.NoError(t, db.ApplyEmbeddedDbSchema(-2))
		t.Cleanup(func() {
			// MustCloseDB keeps the closed handle; drop it so the next test opens its own
			db.MustCloseDB()
			db.ReaderDb = nil
		})
	}
	if withBlockDb && blockdb.GlobalBlockDb == nil {
		require.NoError(t, blockdb.InitWithPebble(dtypes.PebbleBlockDBConfig{Path: t.TempDir()}, logrus.NewEntry(logger)))
		t.Cleanup(func() {
			_ = blockdb.GlobalBlockDb.Close()
			blockdb.GlobalBlockDb = nil
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	indexer := &Indexer{
		ctx:                   ctx,
		logger:                logrus.NewEntry(logger),
		consensusPool:         consensus.NewPool(ctx, logrus.NewEntry(logger)),
		dynSsz:                dynssz.NewDynSsz(nil),
		metadataHistoryLength: 6,
	}
	indexer.blockCache = newBlockCache(indexer)
	indexer.inclusionListCache = newInclusionListCache(indexer)
	indexer.executionProofCache = newExecutionProofCache(indexer)
	indexer.blockBidCache = newBlockBidCache(indexer)

	return indexer
}

// testRpcProof builds a signed proof of the given type and size as served by a node.
func testRpcProof(proofType uint8, root phase0.Root, size int) *rpc.ExecutionProof {
	proof := &rpc.ExecutionProof{
		ProofType:       proofType,
		ProofData:       make([]byte, size),
		BeaconBlockRoot: root,
		Signed:          true,
		ValidatorIndex:  uint64(proofType),
	}
	proof.Signature[0] = proofType
	for i := range proof.ProofData {
		proof.ProofData[i] = byte(i)
	}
	return proof
}

// addTestProofs merges proofs into the entry of a block as a fetch would.
func addTestProofs(cache *executionProofCache, root phase0.Root, proofs ...*rpc.ExecutionProof) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	entry := cache.entries[root]
	cache.addProofsLocked(entry, cache.convertProofs(entry, proofApiByRoot, proofs))
}

// insertTestUnfinalizedBlock stores a minimal unfinalized block row for a cached block.
func insertTestUnfinalizedBlock(t *testing.T, block *Block) {
	t.Helper()

	require.NoError(t, db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertUnfinalizedBlock(context.Background(), tx, &dbtypes.UnfinalizedBlock{
			Root: block.Root[:], Slot: uint64(block.Slot), HeaderSSZ: []byte{}, BlockSSZ: []byte{}, BlockUid: block.BlockUID,
		})
	}))
	block.isInUnfinalizedDb = true
}

func countUnfinalizedMetadataRows(t *testing.T) int {
	t.Helper()

	rows := 0
	require.NoError(t, db.StreamUnfinalizedMetadata(context.Background(), 0, func(*dbtypes.UnfinalizedMetadata) { rows++ }))
	return rows
}

// The collected items are written as crash copy on the tick and restored into
// the caches of the next start; the flush writes the index values into the
// blocks and their rows, the objects into the blockdb, and drops the copies.
func TestUnfinalizedMetadataCrashCopy(t *testing.T) {
	indexer := newTestMetadataIndexer(t, true)

	const slot = phase0.Slot(40)
	rootA, rootB := phase0.Root{0xa}, phase0.Root{0xb}
	txA := []byte{0xf0, 0x01}
	blockA, _ := indexer.blockCache.createOrGetBlock(rootA, slot)
	blockB, _ := indexer.blockCache.createOrGetBlock(rootB, slot+1)
	insertTestUnfinalizedBlock(t, blockA)
	insertTestUnfinalizedBlock(t, blockB)

	indexer.inclusionListCache.addInclusionList(testInclusionList(slot, 11, 1, txA), "client-a", 2500)
	indexer.inclusionListCache.addInclusionList(testInclusionList(slot, 11, 1, txA), "client-b", 2100)
	indexer.inclusionListCache.setEvaluation(slot, &btypes.SlotInclusionLists{
		TxHashes:     [][32]byte{InclusionListTxHash(txA)},
		Transactions: [][]byte{txA},
		Lists:        []*btypes.SlotInclusionList{{ValidatorIndex: 11, Signature: [96]byte{1}, TxRefs: []uint16{0}}},
		Evals: []*btypes.SlotInclusionListEval{{
			BlockRoot: rootB,
			ListFlags: []uint8{btypes.ILListFlagEvaluated},
			Txs:       []btypes.SlotInclusionListTx{{Status: btypes.ILTxStatusUnsatisfied}},
		}},
	})
	indexer.executionProofCache.trackBlock(slot, rootA)
	addTestProofs(indexer.executionProofCache, rootA, testRpcProof(1, rootA, 64))

	// nothing left the caches yet: the index values come from the live data
	assert.Nil(t, blockA.GetMetadata())
	assert.Equal(t, int16(1), indexer.inclusionListCache.getBlockListCount(blockA))
	assert.Equal(t, int16(1), indexer.inclusionListCache.getBlockUnsatisfied(blockB))
	assert.Equal(t, int16(1), indexer.executionProofCache.getBlockProofCount(blockA))

	indexer.persistUnfinalizedMetadata()
	assert.Equal(t, 3, countUnfinalizedMetadataRows(t), "one list, one evaluation, one proof")
	// a tick without changes writes nothing
	assert.Empty(t, indexer.inclusionListCache.collectDirtyEntries())
	assert.Empty(t, indexer.executionProofCache.collectDirtyEntries())

	// a new start restores the window from the copies
	restarted := newTestMetadataIndexer(t, true)
	blockA, _ = restarted.blockCache.createOrGetBlock(rootA, slot)
	blockB, _ = restarted.blockCache.createOrGetBlock(rootB, slot+1)
	blockA.isInUnfinalizedDb, blockB.isInUnfinalizedDb = true, true
	restarted.restoreUnfinalizedMetadata(slot-2, slot+2)
	lists := restarted.inclusionListCache.getInclusionLists(slot)
	require.Len(t, lists, 1)
	assert.Equal(t, int32(2100), lists[0].FirstSeen)
	require.NotNil(t, restarted.inclusionListCache.getEvaluation(slot, rootB))
	require.Len(t, restarted.executionProofCache.getProofs(rootA), 1)
	assert.Equal(t, uint32(64), restarted.executionProofCache.getProofs(rootA)[0].DataSize)
	assert.Equal(t, byte(63), restarted.executionProofCache.getProofs(rootA)[0].ProofData[63])
	// the blocks of the window are queued for further proofs
	assert.NotNil(t, restarted.executionProofCache.entries[rootB])
	// restored items are not written again
	assert.Empty(t, restarted.inclusionListCache.collectDirtyEntries())
	assert.Empty(t, restarted.executionProofCache.collectDirtyEntries())

	// a block far enough ahead opens the flush window
	rootC := phase0.Root{0xc}
	restarted.blockCache.createOrGetBlock(rootC, slot+20)
	restarted.executionProofCache.trackBlock(slot+20, rootC)
	require.NoError(t, restarted.blockBidCache.checkAndFlush())

	// the index values are in the blocks and their rows
	require.NotNil(t, blockA.GetMetadata())
	assert.Equal(t, &BlockMetadata{IlCount: 1, IlUnsatisfied: 0, ProofCount: 1}, blockA.GetMetadata())
	require.NotNil(t, blockB.GetMetadata())
	assert.Equal(t, &BlockMetadata{IlCount: 0, IlUnsatisfied: 1, ProofCount: 0}, blockB.GetMetadata())
	assert.Equal(t, int16(1), restarted.inclusionListCache.getBlockListCount(blockA))
	assert.Equal(t, int16(1), restarted.inclusionListCache.getBlockUnsatisfied(blockB))
	assert.Equal(t, int16(1), restarted.executionProofCache.getBlockProofCount(blockA))
	assert.Empty(t, restarted.executionProofCache.getProofs(rootA), "the proof bytes left memory")
	assert.Empty(t, restarted.inclusionListCache.getInclusionLists(slot))
	rowA := db.GetUnfinalizedBlock(context.Background(), rootA[:], false, false, false)
	require.NotNil(t, rowA)
	assert.Equal(t, blockA.encodedMetadata(), rowA.Metadata)

	// the objects are in the blockdb, the copies are gone
	assert.Equal(t, 0, countUnfinalizedMetadataRows(t))
	stored, err := blockdb.GlobalBlockDb.GetSlotMeta(context.Background(), uint64(slot), btypes.SlotMetaFlagAll)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Len(t, stored.InclusionLists.Lists, 1)
	require.NotNil(t, stored.InclusionLists.GetEval(rootB))
	assert.Len(t, stored.ExecutionProofs, 1)

	// another start restores the values with the blocks, without any lookup
	third := newTestMetadataIndexer(t, true)
	blockA, _ = third.blockCache.createOrGetBlock(rootA, slot)
	require.NoError(t, blockA.restoreMetadata(rowA.Metadata))
	assert.Equal(t, &BlockMetadata{IlCount: 1, ProofCount: 1}, blockA.GetMetadata())
	third.restoreUnfinalizedMetadata(slot-2, slot+40)
	assert.Equal(t, int16(1), third.executionProofCache.getBlockProofCount(blockA))

	// a block the caches never held is unknown before the cutoff, nothing after it
	syncBlock := newBlock(nil, phase0.Root{0xd}, slot, 0)
	assert.Equal(t, dbtypes.MetadataUnknown, third.executionProofCache.getBlockProofCount(syncBlock))
	assert.Equal(t, dbtypes.MetadataUnknown, third.inclusionListCache.getBlockListCount(syncBlock))
	assert.Equal(t, dbtypes.MetadataUnknown, third.inclusionListCache.getSlotListCount(slot+3))
	liveBlock, _ := third.blockCache.createOrGetBlock(phase0.Root{0xe}, slot+30)
	assert.Equal(t, int16(0), third.executionProofCache.getBlockProofCount(liveBlock))
	assert.Equal(t, int16(0), third.inclusionListCache.getSlotListCount(slot+30))

	// the retention of copies needs the chain specs; without them nothing is dropped
	require.NoError(t, db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.UpsertUnfinalizedMetadata(context.Background(), tx, &dbtypes.UnfinalizedMetadata{Slot: 1, Kind: dbtypes.UnfinalizedMetadataSlotListCount, Key: []byte{}, Data: []byte{0, 1}})
	}))
	third.cleanupUnfinalizedMetadata(slot + 40)
	assert.Equal(t, 1, countUnfinalizedMetadataRows(t))
}

// Without a blockdb the copies are the store: the flush drops the data from
// memory and keeps the copies, which serve the slot views.
func TestUnfinalizedMetadataWithoutBlockDb(t *testing.T) {
	indexer := newTestMetadataIndexer(t, false)

	const slot = phase0.Slot(50)
	rootA := phase0.Root{0xa}
	blockA, _ := indexer.blockCache.createOrGetBlock(rootA, slot)

	indexer.inclusionListCache.addInclusionList(testInclusionList(slot, 11, 1, []byte{0xf0, 0x01}), "client-a", 2500)
	indexer.inclusionListCache.addInclusionList(testInclusionList(slot+2, 12, 1, []byte{0xf0, 0x02}), "client-a", 2500)
	indexer.executionProofCache.trackBlock(slot, rootA)
	addTestProofs(indexer.executionProofCache, rootA, testRpcProof(2, rootA, 32))
	indexer.persistUnfinalizedMetadata()
	assert.Equal(t, 3, countUnfinalizedMetadataRows(t))

	objects, touched, dropped := indexer.inclusionListCache.dropBefore(slot+3, false)
	assert.Empty(t, objects, "no object without blockdb")
	assert.Len(t, touched, 1)
	assert.Len(t, dropped, 2)
	objects, touched, dropped = indexer.executionProofCache.dropBefore(slot+3, false, false)
	assert.Empty(t, objects)
	assert.Len(t, touched, 1)
	assert.Equal(t, []uint64{uint64(slot)}, dropped)
	indexer.metadataCutoff.Store(uint64(slot + 3))

	assert.Equal(t, &BlockMetadata{IlCount: 1, ProofCount: 1}, blockA.GetMetadata())
	assert.Empty(t, indexer.executionProofCache.getProofs(rootA))
	assert.Equal(t, int16(1), indexer.executionProofCache.getBlockProofCount(blockA))
	// the lists of a slot without a block keep their count, persisted as copy
	assert.Equal(t, int16(1), indexer.inclusionListCache.getSlotListCount(slot+2))
	indexer.persistUnfinalizedMetadata()
	assert.Equal(t, 4, countUnfinalizedMetadataRows(t), "the copies remain, the slot count is added")

	restarted := newTestMetadataIndexer(t, false)
	restarted.restoreUnfinalizedMetadata(slot-2, slot+4)
	assert.Equal(t, int16(1), restarted.inclusionListCache.getSlotListCount(slot+2))

	proofs := indexer.executionProofCache.loadStoredProofs(context.Background(), slot, rootA, true)
	require.Len(t, proofs, 1)
	assert.Len(t, proofs[0].ProofData, 32)
	assert.Equal(t, uint32(32), proofs[0].DataSize)
	proofs = indexer.executionProofCache.loadStoredProofs(context.Background(), slot, rootA, false)
	require.Len(t, proofs, 1)
	assert.Nil(t, proofs[0].ProofData)

	obj := indexer.inclusionListCache.loadStoredSlotObject(context.Background(), slot)
	require.NotNil(t, obj)
	assert.Len(t, obj.InclusionLists.Lists, 1)
	assert.Nil(t, indexer.inclusionListCache.loadStoredSlotObject(context.Background(), slot+1))
}
