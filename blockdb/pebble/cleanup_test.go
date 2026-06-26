package pebble

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	dtypes "github.com/ethpandaops/dora/types"
)

func testEngine(t *testing.T, cfg dtypes.PebbleBlockDBConfig) *PebbleEngine {
	t.Helper()
	cfg.Path = t.TempDir()
	if cfg.CacheSize == 0 {
		cfg.CacheSize = 1
	}
	eng, err := NewPebbleEngine(cfg)
	if err != nil {
		t.Fatalf("failed to open pebble engine: %v", err)
	}
	pe, ok := eng.(*PebbleEngine)
	if !ok {
		t.Fatalf("unexpected engine type %T", eng)
	}
	t.Cleanup(func() { _ = pe.Close() })
	return pe
}

func makeRoot(prefix byte) []byte {
	root := make([]byte, 32)
	root[0] = prefix
	root[1] = 0xAB
	root[2] = 0xCD
	root[3] = prefix
	return root
}

func mustSet(t *testing.T, e *PebbleEngine, key, value []byte) {
	t.Helper()
	if err := e.db.Set(key, value, nil); err != nil {
		t.Fatalf("failed to set key: %v", err)
	}
}

func keyExists(t *testing.T, e *PebbleEngine, key []byte) bool {
	t.Helper()
	_, closer, err := e.db.Get(key)
	if err != nil {
		return false
	}
	_ = closer.Close()
	return true
}

// TestExecDataAgeEviction verifies age eviction deletes exec objects below the
// cutoff slot, keeps newer ones, and never touches co-located ns2 LRU records
// (different key length) or the tx-hash index namespaces (ns4/ns5).
func TestExecDataAgeEviction(t *testing.T) {
	ctx := context.Background()
	eng := testEngine(t, dtypes.PebbleBlockDBConfig{
		ExecDataRetention: dtypes.BlockDbRetentionConfig{
			Enabled: true, CleanupMode: "age", RetentionTime: time.Hour,
		},
	})
	cleanup := NewCacheCleanup(eng, logrus.New())
	cleanup.SetTimeToSlotFn(func(time.Time) uint64 { return 100 })

	oldRoot, newRoot := makeRoot(0x10), makeRoot(0x20)
	if _, err := eng.AddExecData(ctx, 50, oldRoot, []byte("old-exec-data")); err != nil {
		t.Fatalf("add old exec data: %v", err)
	}
	if _, err := eng.AddExecData(ctx, 150, newRoot, []byte("new-exec-data")); err != nil {
		t.Fatalf("add new exec data: %v", err)
	}

	// A co-located ns2 LRU record (34 bytes) whose leading bytes look like a low
	// slot — must survive the length filter.
	lruKey := makeLRUKey(make([]byte, 32))
	mustSet(t, eng, lruKey, make([]byte, lruValueSize))

	// A tx-hash index key (ns4) below the cutoff slot — must never be evicted.
	ns4Key := make([]byte, 14)
	binary.BigEndian.PutUint16(ns4Key[0:2], 4)
	mustSet(t, eng, ns4Key, []byte{})

	cleanup.runCleanup()

	if has, err := eng.HasExecData(ctx, 50, oldRoot); err != nil || has {
		t.Fatalf("old exec data should be age-evicted (has=%v err=%v)", has, err)
	}
	if has, err := eng.HasExecData(ctx, 150, newRoot); err != nil || !has {
		t.Fatalf("new exec data should survive (has=%v err=%v)", has, err)
	}
	if !keyExists(t, eng, lruKey) {
		t.Fatal("co-located ns2 LRU record must survive")
	}
	if !keyExists(t, eng, ns4Key) {
		t.Fatal("ns4 tx-hash index key must never be evicted")
	}
}

// TestExecDataLRUEviction verifies size-capped LRU eviction removes the
// least-recently-accessed object first.
func TestExecDataLRUEviction(t *testing.T) {
	ctx := context.Background()
	eng := testEngine(t, dtypes.PebbleBlockDBConfig{
		ExecDataRetention: dtypes.BlockDbRetentionConfig{
			Enabled: true, CleanupMode: "lru",
		},
	})
	cleanup := NewCacheCleanup(eng, logrus.New())

	payload := make([]byte, 4096) // ~4KB each
	rootOld, rootNew := makeRoot(0x10), makeRoot(0x20)
	if _, err := eng.AddExecData(ctx, 50, rootOld, payload); err != nil {
		t.Fatalf("add old: %v", err)
	}
	if _, err := eng.AddExecData(ctx, 60, rootNew, payload); err != nil {
		t.Fatalf("add new: %v", err)
	}

	// Explicit access records: rootOld accessed long ago, rootNew just now.
	writeAccess := func(slot uint64, root []byte, ts int64) {
		key := makeAccessKey(KeyNamespaceExecData, execAccessTail(slot, root))
		val := make([]byte, 8)
		binary.BigEndian.PutUint64(val, uint64(ts))
		mustSet(t, eng, key, val)
	}
	writeAccess(50, rootOld, time.Now().Add(-24*time.Hour).UnixNano())
	writeAccess(60, rootNew, time.Now().UnixNano())

	// Cap at ~5KB so exactly one ~4KB object must be evicted.
	cleanup.cleanupSlotNamespaceByLRU(KeyNamespaceExecData, execDataKeyLen, execEntityTailLen, 5*1024)

	if has, err := eng.HasExecData(ctx, 50, rootOld); err != nil || has {
		t.Fatalf("least-recently-accessed object should be evicted (has=%v err=%v)", has, err)
	}
	if has, err := eng.HasExecData(ctx, 60, rootNew); err != nil || !has {
		t.Fatalf("recently-accessed object should survive (has=%v err=%v)", has, err)
	}
}

// TestDutiesAgeEvictionWholeEpoch verifies all keys of an epoch below the cutoff
// are removed together while a newer epoch's keys survive.
func TestDutiesAgeEvictionWholeEpoch(t *testing.T) {
	eng := testEngine(t, dtypes.PebbleBlockDBConfig{
		DutiesRetention: dtypes.BlockDbRetentionConfig{
			Enabled: true, CleanupMode: "age", RetentionTime: time.Hour,
		},
	})
	cleanup := NewCacheCleanup(eng, logrus.New())
	cleanup.SetTimeToSlotFn(func(time.Time) uint64 { return 100 })

	// Old epoch (firstSlot 32): meta + two committee slots.
	oldKeys := [][]byte{
		MakeDutiesKey(32, DutiesRecordMeta, 0),
		MakeDutiesKey(32, DutiesRecordCommittees, 0),
		MakeDutiesKey(32, DutiesRecordCommittees, 1),
	}
	for _, k := range oldKeys {
		mustSet(t, eng, k, []byte("d"))
	}
	// New epoch (firstSlot 160).
	newKey := MakeDutiesKey(160, DutiesRecordMeta, 0)
	mustSet(t, eng, newKey, []byte("d"))

	cleanup.runCleanup()

	for _, k := range oldKeys {
		if keyExists(t, eng, k) {
			t.Fatal("old epoch duties key should be evicted")
		}
	}
	if !keyExists(t, eng, newKey) {
		t.Fatal("new epoch duties should survive")
	}
}
