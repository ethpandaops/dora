package pebble

import (
	"context"
	"encoding/binary"

	"github.com/cockroachdb/pebble"

	"github.com/ethpandaops/dora/blockdb/types"
)

// Compile-time guarantee that the Pebble engine provides the tx-hash index.
var _ types.TxHashIndex = (*PebbleEngine)(nil)

// makeTxHashLookupKey builds the lookup key [ns4][prefix10][tx_uid8 BE]. The
// tx_uid suffix lets multiple candidates per prefix (collisions / reorg dupes)
// coexist.
func makeTxHashLookupKey(prefix []byte, txUid uint64) []byte {
	key := make([]byte, 2+types.TxHashPrefixLen+8)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceTxHash)
	copy(key[2:2+types.TxHashPrefixLen], prefix)
	binary.BigEndian.PutUint64(key[2+types.TxHashPrefixLen:], txUid)
	return key
}

// makeTxHashLookupBound builds [ns4][prefix] for prefix scans/ranges (prefix may
// be shorter than TxHashPrefixLen for partial-hash search).
func makeTxHashLookupBound(prefix []byte) []byte {
	key := make([]byte, 2+len(prefix))
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceTxHash)
	copy(key[2:], prefix)
	return key
}

// makeTxHashPruneKey builds the prune-order key [ns5][tx_uid8 BE].
func makeTxHashPruneKey(txUid uint64) []byte {
	key := make([]byte, 2+8)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceTxHashPrune)
	binary.BigEndian.PutUint64(key[2:], txUid)
	return key
}

// keyUpperBound returns the lexicographic successor of b (exclusive upper bound
// for a prefix scan). Returns nil only if b is all 0xFF, which never happens for
// a namespaced key.
func keyUpperBound(b []byte) []byte {
	end := make([]byte, len(b))
	copy(end, b)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			return end[:i+1]
		}
	}
	return nil
}

// EstimateTxHashIndexSize returns the approximate on-disk bytes used by the
// tx-hash index, covering both the lookup (ns4) and prune-order (ns5)
// namespaces that together make up the index.
func (e *PebbleEngine) EstimateTxHashIndexSize() int64 {
	lo := makeNamespaceRangeStart(KeyNamespaceTxHash)      // ns4
	hi := makeNamespaceRangeStart(KeyNamespaceAccessTrack) // ns6 → covers ns4 + ns5
	n, err := e.db.EstimateDiskUsage(lo, hi)
	if err != nil {
		return 0
	}
	return int64(n)
}

// PutTxHashes writes the lookup and prune-order keys for each entry.
func (e *PebbleEngine) PutTxHashes(_ context.Context, entries []types.TxHashEntry) error {
	if len(entries) == 0 {
		return nil
	}

	batch := e.db.NewBatch()
	defer func() { _ = batch.Close() }()

	for _, entry := range entries {
		if len(entry.Prefix) < types.TxHashPrefixLen {
			continue
		}
		prefix := entry.Prefix[:types.TxHashPrefixLen]
		if err := batch.Set(makeTxHashLookupKey(prefix, entry.TxUid), nil, nil); err != nil {
			return err
		}
		if err := batch.Set(makeTxHashPruneKey(entry.TxUid), prefix, nil); err != nil {
			return err
		}
	}

	return batch.Commit(pebble.Sync)
}

// txHashRangeScanLimit caps the number of candidates returned by a partial-hash
// range scan (a short prefix can match a large fraction of all transactions).
const txHashRangeScanLimit = 100

// LookupTxHash returns candidate tx_uids for an exact prefix.
func (e *PebbleEngine) LookupTxHash(_ context.Context, prefix []byte) ([]uint64, error) {
	lower := makeTxHashLookupBound(prefix)
	return e.scanTxUids(lower, keyUpperBound(lower), 0)
}

// LookupTxHashRange returns candidate tx_uids for prefixes in [lo, hi), capped.
func (e *PebbleEngine) LookupTxHashRange(_ context.Context, lo, hi []byte) ([]uint64, error) {
	return e.scanTxUids(makeTxHashLookupBound(lo), makeTxHashLookupBound(hi), txHashRangeScanLimit)
}

// scanTxUids iterates lookup keys in [lower, upper) and decodes the trailing
// tx_uid from each. A limit of 0 means unbounded.
func (e *PebbleEngine) scanTxUids(lower, upper []byte, limit int) ([]uint64, error) {
	iter, err := e.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	defer func() { _ = iter.Close() }()

	var uids []uint64
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) < 8 {
			continue
		}
		uids = append(uids, binary.BigEndian.Uint64(key[len(key)-8:]))
		if limit > 0 && len(uids) >= limit {
			break
		}
	}
	return uids, iter.Error()
}

// PruneTxHashBefore deletes all index entries for slots below maxSlot using the
// slot-ordered prune namespace, returning the number of entries removed.
func (e *PebbleEngine) PruneTxHashBefore(_ context.Context, maxSlot uint64) (int64, error) {
	threshold := maxSlot << 32 // tx_uid = slot<<32 | block_index<<16 | tx_index

	lower := makeNamespaceRangeStart(KeyNamespaceTxHashPrune)
	upper := makeTxHashPruneKey(threshold)

	iter, err := e.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return 0, err
	}

	batch := e.db.NewBatch()
	defer func() { _ = batch.Close() }()

	var count int64
	for iter.First(); iter.Valid(); iter.Next() {
		pruneKey := iter.Key()
		if len(pruneKey) < 2+8 {
			continue
		}
		txUid := binary.BigEndian.Uint64(pruneKey[2:10])
		prefix := iter.Value() // 10-byte hash prefix

		if err := batch.Delete(makeTxHashLookupKey(prefix, txUid), nil); err != nil {
			_ = iter.Close()
			return count, err
		}
		pruneKeyCopy := make([]byte, len(pruneKey))
		copy(pruneKeyCopy, pruneKey)
		if err := batch.Delete(pruneKeyCopy, nil); err != nil {
			_ = iter.Close()
			return count, err
		}
		count++
	}
	if err := iter.Close(); err != nil {
		return count, err
	}

	if count == 0 {
		return 0, nil
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, err
	}
	return count, nil
}
