package pebble

import (
	"context"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/ethpandaops/dora/blockdb/types"
)

// Per-slot bids objects (KeyNamespaceBids, see pebble.go) are stored as a
// single key per slot holding the encoded BIDS object.
const (
	// BidsKeyLen: [ns:2][slot:8] = 10 bytes
	BidsKeyLen = 2 + 8
	// bidsEntityTailLen identifies one bids object: [slot:8].
	bidsEntityTailLen = 8
)

// MakeBidsKey builds a Pebble key for a per-slot bids object. Exported so
// tooling (e.g. the blockdb-copy utility) can read/write bids objects directly.
func MakeBidsKey(slot uint64) []byte {
	return makeNamespaceSlotKey(KeyNamespaceBids, slot)
}

// AddSlotBids stores the encoded bids object for a slot.
func (e *PebbleEngine) AddSlotBids(_ context.Context, bids *types.SlotBids) (int64, error) {
	data, err := types.EncodeSlotBids(bids)
	if err != nil {
		return 0, fmt.Errorf("failed to encode bids: %w", err)
	}

	key := makeNamespaceSlotKey(KeyNamespaceBids, bids.Slot)
	if err := e.db.Set(key, data, pebble.Sync); err != nil {
		return 0, fmt.Errorf("failed to set bids: %w", err)
	}

	return int64(len(key) + len(data)), nil
}

// GetSlotBids reads and decodes the bids object for a slot.
func (e *PebbleEngine) GetSlotBids(_ context.Context, slot uint64) (*types.SlotBids, error) {
	res, closer, err := e.db.Get(makeNamespaceSlotKey(KeyNamespaceBids, slot))
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()

	return types.DecodeSlotBids(res)
}

// PruneSlotBidsBefore deletes all bids objects for slots before maxSlot.
// Returns the number of objects deleted.
func (e *PebbleEngine) PruneSlotBidsBefore(_ context.Context, maxSlot uint64) (int64, error) {
	rangeStart := makeNamespaceRangeStart(KeyNamespaceBids)
	rangeEnd := makeNamespaceSlotKey(KeyNamespaceBids, maxSlot)

	var count int64

	iter, err := e.db.NewIter(&pebble.IterOptions{
		LowerBound: rangeStart,
		UpperBound: rangeEnd,
	})
	if err != nil {
		return 0, err
	}
	for iter.First(); iter.Valid(); iter.Next() {
		count++
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}

	if count == 0 {
		return 0, nil
	}

	if err := e.db.DeleteRange(rangeStart, rangeEnd, pebble.Sync); err != nil {
		return 0, err
	}

	return count, nil
}
