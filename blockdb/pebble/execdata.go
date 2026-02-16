package pebble

import (
	"context"
	"encoding/binary"

	"github.com/cockroachdb/pebble"
)

const (
	// KeyNamespaceExecData is the namespace prefix for execution data keys.
	// Slot-first ordering enables efficient range deletion for pruning.
	KeyNamespaceExecData uint16 = 2
)

const (
	// execDataKeyLen is the total key length:
	// 2 bytes namespace + 8 bytes slot + 4 bytes blockHash prefix = 14 bytes
	execDataKeyLen = 2 + 8 + 4
)

// makeExecDataKey builds a Pebble key for execution data.
// Format: [namespace:2][slot:8 big-endian][blockHash prefix:4]
func makeExecDataKey(slot uint64, blockHash []byte) []byte {
	key := make([]byte, execDataKeyLen)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceExecData)
	binary.BigEndian.PutUint64(key[2:10], slot)

	hashLen := len(blockHash)
	if hashLen > 4 {
		hashLen = 4
	}
	copy(key[10:10+hashLen], blockHash[:hashLen])

	return key
}

// makeExecDataPrefixKey builds a prefix key for range operations up to (excluding) maxSlot.
// Format: [namespace:2][maxSlot:8 big-endian]
func makeExecDataPrefixKey(maxSlot uint64) []byte {
	key := make([]byte, 10)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceExecData)
	binary.BigEndian.PutUint64(key[2:10], maxSlot)
	return key
}

// execDataRangeStart returns the key range start for the exec data namespace.
func execDataRangeStart() []byte {
	key := make([]byte, 2)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceExecData)
	return key
}

// AddExecData stores execution data for a block. Returns stored size.
func (e *PebbleEngine) AddExecData(_ context.Context, slot uint64, blockHash []byte, data []byte) (int64, error) {
	key := makeExecDataKey(slot, blockHash)

	if err := e.db.Set(key, data, pebble.Sync); err != nil {
		return 0, err
	}

	return int64(len(data)), nil
}

// GetExecData retrieves full execution data for a block.
func (e *PebbleEngine) GetExecData(_ context.Context, slot uint64, blockHash []byte) ([]byte, error) {
	key := makeExecDataKey(slot, blockHash)

	res, closer, err := e.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()

	// Copy the data since the slice is only valid until closer.Close()
	data := make([]byte, len(res))
	copy(data, res)

	return data, nil
}

// GetExecDataRange retrieves a byte range of execution data.
// For Pebble, reads the full value and returns the requested slice.
func (e *PebbleEngine) GetExecDataRange(_ context.Context, slot uint64, blockHash []byte, offset int64, length int64) ([]byte, error) {
	key := makeExecDataKey(slot, blockHash)

	res, closer, err := e.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()

	if offset >= int64(len(res)) {
		return nil, nil
	}

	end := offset + length
	if end > int64(len(res)) {
		end = int64(len(res))
	}

	data := make([]byte, end-offset)
	copy(data, res[offset:end])

	return data, nil
}

// HasExecData checks if execution data exists for a block.
func (e *PebbleEngine) HasExecData(_ context.Context, slot uint64, blockHash []byte) (bool, error) {
	key := makeExecDataKey(slot, blockHash)

	_, closer, err := e.db.Get(key)
	if err == pebble.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_ = closer.Close()

	return true, nil
}

// DeleteExecData deletes execution data for a specific block.
func (e *PebbleEngine) DeleteExecData(_ context.Context, slot uint64, blockHash []byte) error {
	key := makeExecDataKey(slot, blockHash)
	return e.db.Delete(key, pebble.Sync)
}

// PruneExecDataBefore deletes execution data for all slots before maxSlot.
// Uses Pebble's DeleteRange for efficient bulk deletion since keys are
// slot-ordered within the namespace.
func (e *PebbleEngine) PruneExecDataBefore(_ context.Context, maxSlot uint64) (int64, error) {
	startKey := execDataRangeStart()
	endKey := makeExecDataPrefixKey(maxSlot)

	// Count entries to be deleted by iterating before deleting
	var count int64

	iter, err := e.db.NewIter(&pebble.IterOptions{
		LowerBound: startKey,
		UpperBound: endKey,
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

	// DeleteRange is very efficient for Pebble - it creates a single range tombstone
	if err := e.db.DeleteRange(startKey, endKey, pebble.Sync); err != nil {
		return 0, err
	}

	return count, nil
}
