package beacon

import (
	"context"
	"testing"

	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus"
)

// newTestInclusionListCache builds an inclusion list cache on a minimal indexer.
func newTestInclusionListCache(t *testing.T) *inclusionListCache {
	t.Helper()

	logger, _ := test.NewNullLogger()
	indexer := &Indexer{
		ctx:           context.Background(),
		logger:        logrus.NewEntry(logger),
		consensusPool: consensus.NewPool(context.Background(), logrus.NewEntry(logger)),
	}
	indexer.blockCache = newBlockCache(indexer)

	cache := newInclusionListCache(indexer)
	cache.setLiveFromSlot(0)

	return cache
}

// testInclusionList builds a signed inclusion list for the given slot.
func testInclusionList(slot phase0.Slot, validator phase0.ValidatorIndex, signature byte, txs ...[]byte) *v1.SignedInclusionList {
	list := &v1.SignedInclusionList{
		Message: &v1.InclusionList{
			Slot:           slot,
			ValidatorIndex: validator,
			DependentRoot:  phase0.Root{0xd0},
			Transactions:   make([]bellatrix.Transaction, 0, len(txs)),
		},
	}
	list.Signature[0] = signature
	for _, tx := range txs {
		list.Message.Transactions = append(list.Message.Transactions, tx)
	}

	return list
}

func TestInclusionListCache(t *testing.T) {
	cache := newTestInclusionListCache(t)

	const slot = phase0.Slot(100)
	txA, txB, txC := []byte{0xf0, 0x01}, []byte{0xf0, 0x02}, []byte{0xf0, 0x03}
	targetRoot := phase0.Root{0x77}

	// The same list reported by two clients is one list with two
	// observations; a different list of the same member is kept as well.
	cache.addInclusionList(testInclusionList(slot, 11, 1, txA, txB), "client-a", 2500)
	cache.addInclusionList(testInclusionList(slot, 11, 1, txA, txB), "client-b", 2100)
	cache.addInclusionList(testInclusionList(slot, 11, 1, txA, txB), "client-b", 2900)
	cache.addInclusionList(testInclusionList(slot, 12, 1, txB, txC), "client-a", 9000)
	cache.addInclusionList(testInclusionList(slot, 12, 2, txC), "client-a", 9100)
	cache.addInclusionList(testInclusionList(slot+1, 13, 1, txA), "client-a", 100)

	assert.ElementsMatch(t, []phase0.Slot{slot, slot + 1}, cache.getSlots())

	observations := cache.getInclusionLists(slot)
	require.Len(t, observations, 3)
	assert.True(t, observations[0].HasSeen)
	assert.Equal(t, int32(2100), observations[0].FirstSeen)

	assert.Equal(t, int16(3), cache.getListCount(slot))
	assert.Equal(t, int16(0), cache.getListCount(slot+5))
	// Lists exist but have not been evaluated against the block.
	assert.Equal(t, int16(-1), cache.getUnsatisfied(slot+1, targetRoot))
	// No lists were published in the slot before.
	assert.Equal(t, int16(0), cache.getUnsatisfied(slot, targetRoot))

	// The evaluation carries its own tables in another order.
	assert.Nil(t, cache.getEvaluation(slot, targetRoot))
	cache.setEvaluation(slot, &btypes.SlotInclusionLists{
		TxHashes:     [][32]byte{InclusionListTxHash(txC), InclusionListTxHash(txA), InclusionListTxHash(txB)},
		Transactions: [][]byte{txC, txA, txB},
		Lists: []*btypes.SlotInclusionList{
			{ValidatorIndex: 12, Signature: [96]byte{2}, TxRefs: []uint16{0}},
			{ValidatorIndex: 11, Signature: [96]byte{1}, TxRefs: []uint16{1, 2}},
		},
		Evals: []*btypes.SlotInclusionListEval{{
			BlockRoot: targetRoot,
			ListFlags: []uint8{btypes.ILListFlagEvaluated | btypes.ILListFlagEquivocation, btypes.ILListFlagEvaluated},
			Txs: []btypes.SlotInclusionListTx{
				{Status: btypes.ILTxStatusListEquivocation},
				{Status: btypes.ILTxStatusIncluded},
				{Status: btypes.ILTxStatusUnsatisfied},
			},
		}},
	})
	require.NotNil(t, cache.getEvaluation(slot, targetRoot))
	assert.Equal(t, int16(1), cache.getUnsatisfied(slot+1, targetRoot))

	obj := cache.getSlotObject(slot)
	require.NotNil(t, obj)
	lists := obj.InclusionLists
	assert.Equal(t, uint64(slot), obj.Slot)
	assert.Equal(t, [][]byte{txA, txB, txC}, lists.Transactions)
	assert.Equal(t, InclusionListTxHash(txB), lists.TxHashes[1])
	require.Len(t, lists.Lists, 3)
	assert.Equal(t, []uint16{0, 1}, lists.Lists[0].TxRefs)
	first, ok := lists.Lists[0].FirstSeen()
	assert.True(t, ok)
	assert.Equal(t, int32(2100), first)
	assert.Len(t, lists.Lists[0].SeenTimes, 2)

	eval := lists.GetEval(targetRoot)
	require.NotNil(t, eval)
	// Outcomes and flags follow the slot's tables, not the evaluation's.
	assert.Equal(t, btypes.ILTxStatusIncluded, eval.Txs[0].Status)
	assert.Equal(t, btypes.ILTxStatusUnsatisfied, eval.Txs[1].Status)
	assert.Equal(t, btypes.ILTxStatusListEquivocation, eval.Txs[2].Status)
	assert.Equal(t, btypes.ILListFlagEvaluated, eval.ListFlags[0])
	assert.Equal(t, uint8(0), eval.ListFlags[1])
	assert.Equal(t, btypes.ILListFlagEvaluated|btypes.ILListFlagEquivocation, eval.ListFlags[2])

	// The object survives the encoding it is persisted with.
	data, err := btypes.EncodeSlotMeta(obj)
	require.NoError(t, err)
	stored, err := btypes.DecodeSlotMeta(data)
	require.NoError(t, err)

	// Flushing removes the slot but keeps its index values.
	flushed := cache.collectFlushObjects(slot + 1)
	require.Len(t, flushed, 1)
	require.NotNil(t, flushed[uint64(slot)])
	assert.Empty(t, cache.getInclusionLists(slot))
	assert.Nil(t, cache.getSlotObject(slot))
	assert.Equal(t, int16(3), cache.getListCount(slot))
	assert.Equal(t, int16(1), cache.getUnsatisfied(slot+1, targetRoot))
	// A block that was not known when the slot was flushed is not evaluated.
	assert.Equal(t, int16(-1), cache.getUnsatisfied(slot+1, phase0.Root{0x99}))
	assert.Equal(t, []phase0.Slot{slot + 1}, cache.getSlots())

	// Restoring the stored object brings lists, observations and the
	// evaluation back.
	cache.restoreSlotObject(stored)
	restored := cache.getSlotObject(slot)
	require.NotNil(t, restored)
	require.Len(t, restored.InclusionLists.Lists, 3)
	first, ok = restored.InclusionLists.Lists[0].FirstSeen()
	assert.True(t, ok)
	assert.Equal(t, int32(2100), first)
	assert.Equal(t, [][]byte{txA, txB, txC}, restored.InclusionLists.Transactions)
	assert.Equal(t, int16(1), cache.getUnsatisfied(slot+1, targetRoot))
	require.NotNil(t, restored.InclusionLists.GetEval(targetRoot))
	assert.Equal(t, btypes.ILTxStatusUnsatisfied, restored.InclusionLists.GetEval(targetRoot).Txs[1].Status)
}
