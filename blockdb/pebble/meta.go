package pebble

import (
	"context"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/ethpandaops/dora/blockdb/types"
)

// Per-slot meta objects (KeyNamespaceMeta, see pebble.go) are stored as a
// single key per slot holding the encoded meta object.
const (
	// MetaKeyLen: [ns:2][slot:8] = 10 bytes
	MetaKeyLen = 2 + 8
	// metaEntityTailLen identifies one meta object: [slot:8].
	metaEntityTailLen = 8
)

// MakeMetaKey builds a Pebble key for a per-slot meta object. Exported so
// tooling (e.g. the blockdb-copy utility) can read/write meta objects directly.
func MakeMetaKey(slot uint64) []byte {
	return makeNamespaceSlotKey(KeyNamespaceMeta, slot)
}

// AddSlotMeta stores the encoded meta object for a slot.
func (e *PebbleEngine) AddSlotMeta(_ context.Context, meta *types.SlotMeta) (int64, error) {
	data, err := types.EncodeSlotMeta(meta)
	if err != nil {
		return 0, fmt.Errorf("failed to encode slot meta: %w", err)
	}

	key := makeNamespaceSlotKey(KeyNamespaceMeta, meta.Slot)
	if err := e.db.Set(key, data, pebble.Sync); err != nil {
		return 0, fmt.Errorf("failed to set slot meta: %w", err)
	}

	return int64(len(key) + len(data)), nil
}

// GetSlotMeta reads the meta object for a slot and decodes the parts selected
// by flags.
func (e *PebbleEngine) GetSlotMeta(_ context.Context, slot uint64, flags types.SlotMetaFlags) (*types.SlotMeta, error) {
	res, closer, err := e.db.Get(makeNamespaceSlotKey(KeyNamespaceMeta, slot))
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()

	return types.DecodeSlotMetaSections(res, flags)
}

// PruneSlotMetaBefore deletes all meta objects for slots before maxSlot.
// Returns the number of objects deleted.
func (e *PebbleEngine) PruneSlotMetaBefore(_ context.Context, maxSlot uint64) (int64, error) {
	rangeStart := makeNamespaceRangeStart(KeyNamespaceMeta)
	rangeEnd := makeNamespaceSlotKey(KeyNamespaceMeta, maxSlot)

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
