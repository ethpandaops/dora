package types

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTestInclusionList constructs a list for the given validator referencing
// the given transaction table indexes.
func buildTestInclusionList(validator uint64, refs []uint16, seen map[int]int32, clientCount int) *SlotInclusionList {
	list := &SlotInclusionList{
		ValidatorIndex: validator,
		TxRefs:         refs,
	}
	copy(list.DependentRoot[:], bytes.Repeat([]byte{0xd0}, 32))
	copy(list.Signature[:], bytes.Repeat([]byte{byte(validator)}, 96))
	list.SeenMask, list.SeenTimes = NewSeenObservations(seen, clientCount)
	return list
}

// encodeSlotMetaV1 packs a SlotMeta into the unsectioned version 1 layout.
func encodeSlotMetaV1(t *testing.T, s *SlotMeta) []byte {
	t.Helper()

	var buf bytes.Buffer
	buf.Write(legacyMetaMagic[:])
	_ = binary.Write(&buf, binary.BigEndian, metaFormatVersionV1)
	buf.WriteByte(0)
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.BigEndian, s.Slot)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(s.Clients)))
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(s.Bids)))
	for _, name := range s.Clients {
		buf.WriteByte(uint8(len(name)))
		buf.WriteString(name)
	}
	for _, entry := range s.Bids {
		require.NoError(t, appendBidRecord(&buf, entry, (len(s.Clients)+7)/8))
	}
	return buf.Bytes()
}

// testTxHashes derives a deterministic hash table for raw test transactions.
func testTxHashes(txs [][]byte) [][32]byte {
	hashes := make([][32]byte, len(txs))
	for i, tx := range txs {
		copy(hashes[i][:], tx)
		hashes[i][31] = 0xff
	}
	return hashes
}

// buildTestInclusionObject builds an object with bids and inclusion lists.
func buildTestInclusionObject() *SlotMeta {
	clients := []string{"client-a", "client-b", "client-c"}
	// An incompressible transaction keeps the raw transaction section larger
	// than the object prefix.
	largeTx := make([]byte, 6000)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range largeTx {
		largeTx[i] = byte(rng.Uint32())
	}
	txs := [][]byte{{0x02, 0xaa}, largeTx, {0x01}}

	src := buildTestSlotMeta(4242, clients, []map[int]int32{{0: 10}})
	src.InclusionLists = &SlotInclusionLists{
		Committees: []*SlotInclusionListCommittee{
			{DependentRoot: [32]byte{0xd0}, Members: []uint64{11, 12, 13, 14}},
		},
		TxHashes:     testTxHashes(txs),
		Transactions: txs,
		Lists: []*SlotInclusionList{
			buildTestInclusionList(11, []uint16{0, 1}, map[int]int32{0: 2100, 2: 2500}, len(clients)),
			buildTestInclusionList(12, []uint16{2, 0}, map[int]int32{}, len(clients)),
		},
		Evals: []*SlotInclusionListEval{
			{
				BlockRoot:   [32]byte{1},
				BlockHash:   [32]byte{2},
				BlockNumber: 99,
				GasLimit:    60_000_000,
				GasUsed:     59_990_000,
				BaseFee:     7,
				Timestamp:   1_700_000_000,
				ListFlags:   []uint8{ILListFlagEvaluated, ILListFlagEvaluated | ILListFlagLate},
				Txs: []SlotInclusionListTx{
					{Status: ILTxStatusIncluded},
					{Status: ILTxStatusNonceTooLow, Flags: ILTxFlagHasState, Nonce: 17, Balance: []byte{0x01, 0x00}},
					{Status: ILTxStatusInsufficientFunds, Flags: ILTxFlagHasState, Nonce: 3},
				},
			},
		},
	}

	return src
}

func TestSlotBidsDecodeV1(t *testing.T) {
	src := buildTestSlotMeta(777, []string{"client-a", "client-b", "client-c"}, []map[int]int32{
		{0: 100, 2: -50},
		{1: 4000},
	})

	decoded, err := DecodeSlotMeta(encodeSlotMetaV1(t, src))
	require.NoError(t, err)

	assert.Equal(t, src.Slot, decoded.Slot)
	assert.Equal(t, src.Clients, decoded.Clients)
	assert.Nil(t, decoded.InclusionLists)
	require.Len(t, decoded.Bids, len(src.Bids))
	for i, entry := range decoded.Bids {
		assert.Equal(t, src.Bids[i].SeenByClientIndex(), entry.SeenByClientIndex())
		assert.Equal(t, src.Bids[i].Bid.BlockHash, entry.Bid.BlockHash)
		assert.Equal(t, src.Bids[i].Bid.Value, entry.Bid.Value)
	}

	// A version 1 object is rewritten in the current format.
	data, err := EncodeSlotMeta(decoded)
	require.NoError(t, err)
	assert.Equal(t, MetaFormatVersion, binary.BigEndian.Uint16(data[4:6]))
}

func TestSlotInclusionListsRoundTrip(t *testing.T) {
	src := buildTestInclusionObject()

	data, err := EncodeSlotMeta(src)
	require.NoError(t, err)

	decoded, err := DecodeSlotMeta(data)
	require.NoError(t, err)
	require.NotNil(t, decoded.InclusionLists)

	lists := decoded.InclusionLists
	assert.Equal(t, src.InclusionLists.Transactions, lists.Transactions)
	assert.Equal(t, src.InclusionLists.TxHashes, lists.TxHashes)
	require.Len(t, lists.Committees, 1)
	assert.Equal(t, src.InclusionLists.Committees[0], lists.GetCommittee([32]byte{0xd0}))
	assert.Equal(t, src.InclusionLists.Committees[0], lists.GetCommittee([32]byte{0xee}))
	first, ok := lists.Lists[0].FirstSeen()
	assert.True(t, ok)
	assert.Equal(t, int32(2100), first)
	_, ok = lists.Lists[1].FirstSeen()
	assert.False(t, ok)
	require.Len(t, lists.Lists, 2)
	for i, list := range lists.Lists {
		want := src.InclusionLists.Lists[i]
		assert.Equal(t, want.ValidatorIndex, list.ValidatorIndex)
		assert.Equal(t, want.DependentRoot, list.DependentRoot)
		assert.Equal(t, want.Signature, list.Signature)
		assert.Equal(t, want.TxRefs, list.TxRefs)
		assert.Equal(t, want.SeenByClientIndex(), list.SeenByClientIndex())
	}

	require.Len(t, lists.Evals, 1)
	eval := lists.GetEval([32]byte{1})
	require.NotNil(t, eval)
	want := src.InclusionLists.Evals[0]
	assert.Equal(t, want.BlockHash, eval.BlockHash)
	assert.Equal(t, want.BlockNumber, eval.BlockNumber)
	assert.Equal(t, want.GasLimit, eval.GasLimit)
	assert.Equal(t, want.GasUsed, eval.GasUsed)
	assert.Equal(t, want.BaseFee, eval.BaseFee)
	assert.Equal(t, want.Timestamp, eval.Timestamp)
	assert.Equal(t, want.ListFlags, eval.ListFlags)
	require.Len(t, eval.Txs, 3)
	assert.Equal(t, ILTxStatusIncluded, eval.Txs[0].Status)
	assert.Equal(t, uint64(17), eval.Txs[1].Nonce)
	assert.Equal(t, []byte{0x01, 0x00}, eval.Txs[1].Balance)
	assert.Equal(t, ILTxFlagHasState, eval.Txs[2].Flags)
	assert.Empty(t, eval.Txs[2].Balance)

	unsatisfied, complete := eval.CountUnsatisfied()
	assert.Equal(t, 0, unsatisfied)
	assert.True(t, complete)
}

func TestSlotInclusionListsMerge(t *testing.T) {
	// Stored object: clients A/B, list of validator 11 over txs [x, y], an
	// evaluation that could not resolve y.
	stored := &SlotMeta{
		Slot:    500,
		Clients: []string{"client-a", "client-b"},
		InclusionLists: &SlotInclusionLists{
			Committees: []*SlotInclusionListCommittee{
				{DependentRoot: [32]byte{0xd0}, Members: []uint64{11, 12}},
			},
			TxHashes:     testTxHashes([][]byte{{0x01}, {0x02}}),
			Transactions: [][]byte{{0x01}, {0x02}},
			Lists: []*SlotInclusionList{
				buildTestInclusionList(11, []uint16{0, 1}, map[int]int32{0: 3000, 1: 3200}, 2),
			},
			Evals: []*SlotInclusionListEval{{
				BlockRoot: [32]byte{9},
				GasLimit:  100,
				ListFlags: []uint8{ILListFlagEvaluated},
				Txs: []SlotInclusionListTx{
					{Status: ILTxStatusIncluded},
					{Status: ILTxStatusUnknown},
				},
			}},
		},
	}

	// Live object: clients B/C, the transaction table in another order, the
	// same list seen earlier by B, a second list and a resolved evaluation.
	live := &SlotMeta{
		Slot:    500,
		Clients: []string{"client-b", "client-c"},
		InclusionLists: &SlotInclusionLists{
			Committees: []*SlotInclusionListCommittee{
				{DependentRoot: [32]byte{0xd0}, Members: []uint64{11, 12, 13}},
				{DependentRoot: [32]byte{0xd1}, Members: []uint64{21}},
			},
			TxHashes:     testTxHashes([][]byte{{0x03}, {0x02}, {0x01}}),
			Transactions: [][]byte{{0x03}, {0x02}, {0x01}},
			Lists: []*SlotInclusionList{
				buildTestInclusionList(12, []uint16{0, 1}, map[int]int32{1: 2800}, 2),
				buildTestInclusionList(11, []uint16{2, 1}, map[int]int32{0: 2900}, 2),
			},
			Evals: []*SlotInclusionListEval{{
				BlockRoot: [32]byte{9},
				GasLimit:  100,
				ListFlags: []uint8{ILListFlagEvaluated | ILListFlagLate, ILListFlagEvaluated},
				Txs: []SlotInclusionListTx{
					{Status: ILTxStatusUnsatisfied},
					{Status: ILTxStatusNonceTooLow, Flags: ILTxFlagHasState, Nonce: 5},
					{Status: ILTxStatusUnknown},
				},
			}},
		},
	}

	merged := MergeSlotMeta(stored, live)
	require.NotNil(t, merged.InclusionLists)
	lists := merged.InclusionLists

	assert.Equal(t, []string{"client-a", "client-b", "client-c"}, merged.Clients)
	assert.Equal(t, [][]byte{{0x01}, {0x02}, {0x03}}, lists.Transactions)
	assert.Equal(t, testTxHashes([][]byte{{0x01}, {0x02}, {0x03}}), lists.TxHashes)
	// The newer committee replaces the stored one of the same shuffling.
	require.Len(t, lists.Committees, 2)
	assert.Equal(t, []uint64{11, 12, 13}, lists.Committees[0].Members)
	assert.Equal(t, []uint64{21}, lists.Committees[1].Members)

	require.Len(t, lists.Lists, 2)
	assert.Equal(t, uint64(11), lists.Lists[0].ValidatorIndex)
	assert.Equal(t, []uint16{0, 1}, lists.Lists[0].TxRefs)
	// A@3000, B@min(3200,2900).
	assert.Equal(t, map[int]int32{0: 3000, 1: 2900}, lists.Lists[0].SeenByClientIndex())
	assert.Equal(t, uint64(12), lists.Lists[1].ValidatorIndex)
	assert.Equal(t, []uint16{2, 1}, lists.Lists[1].TxRefs)
	assert.Equal(t, map[int]int32{2: 2800}, lists.Lists[1].SeenByClientIndex())

	require.Len(t, lists.Evals, 1)
	eval := lists.Evals[0]
	assert.Equal(t, []uint8{ILListFlagEvaluated, ILListFlagEvaluated | ILListFlagLate}, eval.ListFlags)
	require.Len(t, eval.Txs, 3)
	// The newer unknown outcome does not replace the stored known one.
	assert.Equal(t, ILTxStatusIncluded, eval.Txs[0].Status)
	assert.Equal(t, ILTxStatusNonceTooLow, eval.Txs[1].Status)
	assert.Equal(t, uint64(5), eval.Txs[1].Nonce)
	assert.Equal(t, ILTxStatusUnsatisfied, eval.Txs[2].Status)

	unsatisfied, complete := eval.CountUnsatisfied()
	assert.Equal(t, 1, unsatisfied)
	assert.True(t, complete)

	// One-sided merges keep the inclusion lists.
	onlyBids := buildTestSlotMeta(500, []string{"client-a"}, []map[int]int32{{0: 1}})
	oneSided := MergeSlotMeta(onlyBids, live)
	require.NotNil(t, oneSided.InclusionLists)
	assert.Len(t, oneSided.InclusionLists.Lists, 2)
	assert.Len(t, oneSided.Bids, 1)

	data, err := EncodeSlotMeta(merged)
	require.NoError(t, err)
	_, err = DecodeSlotMeta(data)
	require.NoError(t, err)
}

func TestSlotBidsUnknownSection(t *testing.T) {
	src := buildTestSlotMeta(900, []string{"client-a"}, []map[int]int32{{0: 5}})
	src.Extra = []*SlotMetaSection{{Type: 0x7001, Flags: 0x8000, Data: []byte("future data")}}

	data, err := EncodeSlotMeta(src)
	require.NoError(t, err)

	decoded, err := DecodeSlotMeta(data)
	require.NoError(t, err)
	require.Len(t, decoded.Extra, 1)
	assert.Equal(t, uint16(0x7001), decoded.Extra[0].Type)
	assert.Equal(t, uint16(0x8000), decoded.Extra[0].Flags)
	assert.Equal(t, []byte("future data"), decoded.Extra[0].Data)

	// The unknown section survives a merge with an object that lacks it.
	merged := MergeSlotMeta(decoded, buildTestSlotMeta(900, []string{"client-b"}, []map[int]int32{{0: 9}}))
	data, err = EncodeSlotMeta(merged)
	require.NoError(t, err)
	decoded, err = DecodeSlotMeta(data)
	require.NoError(t, err)
	require.Len(t, decoded.Extra, 1)
	assert.Equal(t, []byte("future data"), decoded.Extra[0].Data)
}

// countingReader serves ranged reads from an encoded object and records them.
type countingReader struct {
	data  []byte
	reads [][2]int64
}

func (r *countingReader) read(offset int64, length int64) ([]byte, error) {
	r.reads = append(r.reads, [2]int64{offset, length})
	if offset >= int64(len(r.data)) {
		return []byte{}, nil
	}
	end := int64(len(r.data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return r.data[offset:end], nil
}

func TestSlotBidsSectionReads(t *testing.T) {
	src := buildTestInclusionObject()
	data, err := EncodeSlotMeta(src)
	require.NoError(t, err)
	require.Greater(t, len(data), MetaPrefixSize, "object must exceed the prefix for this test")

	tests := []struct {
		name      string
		flags     SlotMetaFlags
		wantReads int
		wantBids  bool
		wantLists bool
		wantTxs   bool
	}{
		{name: "bids only", flags: SlotMetaFlagBids, wantReads: 1, wantBids: true},
		{name: "lists without raw txs", flags: SlotMetaFlagInclusionLists, wantReads: 1, wantLists: true},
		{
			name:      "lists with raw txs",
			flags:     SlotMetaFlagInclusionLists | SlotMetaFlagInclusionTxs,
			wantReads: 2, wantLists: true, wantTxs: true,
		},
		{name: "everything", flags: SlotMetaFlagAll, wantReads: 2, wantBids: true, wantLists: true, wantTxs: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &countingReader{data: data}
			decoded, err := ReadSlotMeta(tt.flags, reader.read)
			require.NoError(t, err)
			require.NotNil(t, decoded)

			assert.Len(t, reader.reads, tt.wantReads)
			assert.Equal(t, src.Clients, decoded.Clients)
			if tt.wantBids {
				assert.Len(t, decoded.Bids, len(src.Bids))
			} else {
				assert.Empty(t, decoded.Bids)
			}

			if !tt.wantLists {
				assert.Nil(t, decoded.InclusionLists)
				return
			}
			require.NotNil(t, decoded.InclusionLists)
			assert.Equal(t, src.InclusionLists.TxHashes, decoded.InclusionLists.TxHashes)
			assert.Len(t, decoded.InclusionLists.Lists, 2)
			assert.Len(t, decoded.InclusionLists.Evals, 1)
			if tt.wantTxs {
				assert.Equal(t, src.InclusionLists.Transactions, decoded.InclusionLists.Transactions)
			} else {
				assert.Nil(t, decoded.InclusionLists.Transactions)
			}
		})
	}

	// The raw transaction section is the only one beyond the prefix.
	reader := &countingReader{data: data}
	_, err = ReadSlotMeta(SlotMetaFlagInclusionTxs, reader.read)
	require.NoError(t, err)
	require.Len(t, reader.reads, 2)
	assert.Less(t, reader.reads[1][1], int64(len(data)))

	// A missing object reads as nil.
	missing, err := ReadSlotMeta(SlotMetaFlagAll, func(int64, int64) ([]byte, error) { return nil, nil })
	require.NoError(t, err)
	assert.Nil(t, missing)

	// An object without loaded raw transactions cannot be written back.
	partial, err := DecodeSlotMetaSections(data, SlotMetaFlagInclusionLists)
	require.NoError(t, err)
	_, err = EncodeSlotMeta(partial)
	assert.Error(t, err)
}
