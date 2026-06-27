package pebble

import (
	"context"
	"sort"
	"testing"

	dtypes "github.com/ethpandaops/dora/types"

	"github.com/ethpandaops/dora/blockdb/types"
)

func prefix10(first byte) []byte {
	p := make([]byte, types.TxHashPrefixLen)
	p[0] = first
	p[1] = 0xAA
	p[9] = 0xBB
	return p
}

func txUID(slot uint64, blockIdx, txIdx uint16) uint64 {
	return slot<<32 | uint64(blockIdx)<<16 | uint64(txIdx)
}

func sortedUids(uids []uint64) []uint64 {
	out := append([]uint64(nil), uids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestTxHashLookupAndCollision(t *testing.T) {
	ctx := context.Background()
	eng := testEngine(t, dtypes.PebbleBlockDBConfig{})

	// Same prefix, two different tx_uids (collision / reorg dupes).
	p := prefix10(0x42)
	u1 := txUID(10, 0, 3)
	u2 := txUID(11, 0, 7)
	other := prefix10(0x99)
	u3 := txUID(12, 0, 0)

	err := eng.PutTxHashes(ctx, []types.TxHashEntry{
		{Prefix: p, TxUid: u1},
		{Prefix: p, TxUid: u2},
		{Prefix: other, TxUid: u3},
	})
	if err != nil {
		t.Fatalf("PutTxHashes: %v", err)
	}

	got, err := eng.LookupTxHash(ctx, p)
	if err != nil {
		t.Fatalf("LookupTxHash: %v", err)
	}
	got = sortedUids(got)
	if len(got) != 2 || got[0] != u1 || got[1] != u2 {
		t.Fatalf("expected [%d %d], got %v", u1, u2, got)
	}

	got, err = eng.LookupTxHash(ctx, other)
	if err != nil {
		t.Fatalf("LookupTxHash other: %v", err)
	}
	if len(got) != 1 || got[0] != u3 {
		t.Fatalf("expected [%d], got %v", u3, got)
	}
}

func TestTxHashLookupRange(t *testing.T) {
	ctx := context.Background()
	eng := testEngine(t, dtypes.PebbleBlockDBConfig{})

	entries := []types.TxHashEntry{
		{Prefix: prefix10(0x10), TxUid: txUID(1, 0, 0)},
		{Prefix: prefix10(0x20), TxUid: txUID(2, 0, 0)},
		{Prefix: prefix10(0x30), TxUid: txUID(3, 0, 0)},
	}
	if err := eng.PutTxHashes(ctx, entries); err != nil {
		t.Fatalf("PutTxHashes: %v", err)
	}

	// [0x10, 0x30) covers the first two prefixes, excludes 0x30.
	got, err := eng.LookupTxHashRange(ctx, []byte{0x10}, []byte{0x30})
	if err != nil {
		t.Fatalf("LookupTxHashRange: %v", err)
	}
	got = sortedUids(got)
	if len(got) != 2 || got[0] != txUID(1, 0, 0) || got[1] != txUID(2, 0, 0) {
		t.Fatalf("unexpected range result: %v", got)
	}
}

func TestTxHashPruneBefore(t *testing.T) {
	ctx := context.Background()
	eng := testEngine(t, dtypes.PebbleBlockDBConfig{})

	pOld, pNew := prefix10(0x01), prefix10(0x02)
	uOld := txUID(10, 0, 0)
	uNew := txUID(20, 0, 0)
	if err := eng.PutTxHashes(ctx, []types.TxHashEntry{
		{Prefix: pOld, TxUid: uOld},
		{Prefix: pNew, TxUid: uNew},
	}); err != nil {
		t.Fatalf("PutTxHashes: %v", err)
	}

	// Prune slots < 15: removes the slot-10 entry, keeps slot-20.
	n, err := eng.PruneTxHashBefore(ctx, 15)
	if err != nil {
		t.Fatalf("PruneTxHashBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}

	if got, _ := eng.LookupTxHash(ctx, pOld); len(got) != 0 {
		t.Fatalf("old entry should be pruned, got %v", got)
	}
	if got, _ := eng.LookupTxHash(ctx, pNew); len(got) != 1 || got[0] != uNew {
		t.Fatalf("new entry should remain, got %v", got)
	}

	// The prune-order key must also be gone (no resurrection on re-prune).
	if n, _ := eng.PruneTxHashBefore(ctx, 15); n != 0 {
		t.Fatalf("re-prune should remove nothing, got %d", n)
	}
}
