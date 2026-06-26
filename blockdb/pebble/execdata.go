package pebble

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/ethpandaops/dora/blockdb/types"
)

// execDataKeyLen: [ns:2][slot:8][blockHash:4] = 14 bytes
const execDataKeyLen = 2 + 8 + 4

// makeExecDataKey builds a Pebble key for execution data.
// Format: [namespace:2][slot:8 big-endian][blockHash prefix:4]
func makeExecDataKey(slot uint64, blockHash []byte) []byte {
	key := make([]byte, execDataKeyLen)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceExecData)
	binary.BigEndian.PutUint64(key[2:10], slot)
	copyHashPrefix(key[10:14], blockHash, 4)
	return key
}

// AddExecData stores execution data for a block as a full DXTX blob.
// Returns the stored size in bytes.
func (e *PebbleEngine) AddExecData(_ context.Context, slot uint64, blockHash []byte, data []byte) (int64, error) {
	key := makeExecDataKey(slot, blockHash)

	if err := e.db.Set(key, data, pebble.Sync); err != nil {
		return 0, fmt.Errorf("failed to set exec data: %w", err)
	}

	return int64(len(key)) + int64(len(data)), nil
}

// GetExecData retrieves the full DXTX blob for a block.
// Returns nil, nil if not found.
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

	data := make([]byte, len(res))
	copy(data, res)

	return data, nil
}

// GetExecDataRange retrieves a byte range of the DXTX blob.
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

	end := min(offset+length, int64(len(res)))

	data := make([]byte, end-offset)
	copy(data, res[offset:end])

	return data, nil
}

// GetExecDataTxSections retrieves compressed section data for a single
// transaction by loading the full DXTX blob and extracting the requested
// sections from it.
func (e *PebbleEngine) GetExecDataTxSections(_ context.Context, slot uint64, blockHash []byte, txHash []byte, sections uint32) (*types.ExecDataTxSections, error) {
	key := makeExecDataKey(slot, blockHash)

	res, closer, err := e.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Copy data before closing — Pebble slices are only valid until Close.
	data := make([]byte, len(res))
	copy(data, res)
	_ = closer.Close()

	obj, err := types.ParseExecDataIndex(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse exec data: %w", err)
	}

	entry := obj.FindTxEntry(txHash)
	if entry == nil {
		return nil, nil
	}

	txCount := uint32(len(obj.Transactions))
	result := &types.ExecDataTxSections{}

	if sections&types.ExecDataSectionReceiptMeta != 0 && entry.ReceiptMetaCompLen > 0 {
		result.ReceiptMetaData, err = types.ExtractSectionData(data, txCount, entry.ReceiptMetaOffset, entry.ReceiptMetaCompLen)
		if err != nil {
			return nil, err
		}
	}

	if sections&types.ExecDataSectionEvents != 0 && entry.EventsCompLen > 0 {
		result.EventsData, err = types.ExtractSectionData(data, txCount, entry.EventsOffset, entry.EventsCompLen)
		if err != nil {
			return nil, err
		}
	}

	if sections&types.ExecDataSectionCallTrace != 0 && entry.CallTraceCompLen > 0 {
		result.CallTraceData, err = types.ExtractSectionData(data, txCount, entry.CallTraceOffset, entry.CallTraceCompLen)
		if err != nil {
			return nil, err
		}
	}

	if sections&types.ExecDataSectionStateChange != 0 && entry.StateChangeCompLen > 0 {
		result.StateChangeData, err = types.ExtractSectionData(data, txCount, entry.StateChangeOffset, entry.StateChangeCompLen)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
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
// Returns the number of objects deleted.
func (e *PebbleEngine) PruneExecDataBefore(_ context.Context, maxSlot uint64) (int64, error) {
	rangeStart := makeNamespaceRangeStart(KeyNamespaceExecData)
	rangeEnd := makeNamespaceSlotKey(KeyNamespaceExecData, maxSlot)

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
