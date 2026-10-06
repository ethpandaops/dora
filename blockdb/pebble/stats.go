package pebble

import (
	"context"
	"encoding/binary"

	"github.com/cockroachdb/pebble"

	"github.com/ethpandaops/dora/blockdb/types"
)

// GetObjectStats scans the key namespaces to count stored objects: blocks
// (ns1, header records; includes orphaned blocks), canonical vs diverging
// duties (ns3), and per-slot meta objects (ns7). The scans are key-only (no value
// reads except meta sizing) so they stay cheap enough for the debug page.
func (e *PebbleEngine) GetObjectStats(_ context.Context) (*types.BlockDbObjectStats, error) {
	stats := &types.BlockDbObjectStats{}

	// Blocks: count header component records (one per block, canonical+orphaned).
	// Block key layout: [ns:2][root:32][blockType:2].
	blockCount, err := e.countKeys(KeyNamespaceBlock, func(key []byte) bool {
		return len(key) == 2+32+2 && binary.BigEndian.Uint16(key[34:36]) == BlockTypeHeader
	})
	if err != nil {
		return nil, err
	}
	stats.BlockCount = blockCount

	// Duties: canonical meta keys are DutiesKeyLen bytes with a Meta record type;
	// diverging meta keys are DivergingDutiesKeyLen bytes with a Meta record type.
	canonical, err := e.countKeys(KeyNamespaceDuties, func(key []byte) bool {
		return len(key) == DutiesKeyLen && key[10] == DutiesRecordMeta
	})
	if err != nil {
		return nil, err
	}
	stats.CanonicalDutiesCount = canonical

	diverging, err := e.countKeys(KeyNamespaceDuties, func(key []byte) bool {
		return len(key) == DivergingDutiesKeyLen && key[42] == DutiesRecordMeta
	})
	if err != nil {
		return nil, err
	}
	stats.DivergingDutiesCount = diverging

	// Slot meta: one object per slot key.
	metaCount, metaBytes, err := e.countSlotMeta()
	if err != nil {
		return nil, err
	}
	stats.MetaCount = metaCount
	stats.MetaBytes = metaBytes

	return stats, nil
}

// countKeys counts keys within a namespace matching the predicate.
func (e *PebbleEngine) countKeys(ns uint16, match func(key []byte) bool) (uint64, error) {
	iter, err := e.db.NewIter(&pebble.IterOptions{
		LowerBound: makeNamespaceRangeStart(ns),
		UpperBound: makeNamespaceRangeStart(ns + 1),
	})
	if err != nil {
		return 0, err
	}
	defer func() { _ = iter.Close() }()

	var count uint64
	for iter.First(); iter.Valid(); iter.Next() {
		if match(iter.Key()) {
			count++
		}
	}
	return count, iter.Error()
}

// countSlotMeta counts the per-slot meta objects and sums their encoded sizes.
func (e *PebbleEngine) countSlotMeta() (count uint64, bytes uint64, err error) {
	iter, ierr := e.db.NewIter(&pebble.IterOptions{
		LowerBound: makeNamespaceRangeStart(KeyNamespaceMeta),
		UpperBound: makeNamespaceRangeStart(KeyNamespaceMeta + 1),
	})
	if ierr != nil {
		return 0, 0, ierr
	}
	defer func() { _ = iter.Close() }()

	for iter.First(); iter.Valid(); iter.Next() {
		if len(iter.Key()) != MetaKeyLen {
			continue
		}
		count++
		bytes += uint64(len(iter.Value()))
	}
	return count, bytes, iter.Error()
}
