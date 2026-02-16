package pebble

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/ethpandaops/dora/blockdb/types"
)

const (
	// KeyNamespaceExecData is the namespace prefix for execution data keys (legacy full blob).
	// Slot-first ordering enables efficient range deletion for pruning.
	KeyNamespaceExecData uint16 = 2

	// KeyNamespaceExecDataTxSec is the namespace for per-tx section data.
	// Each key stores a single compressed section for one transaction.
	KeyNamespaceExecDataTxSec uint16 = 3
)

const (
	// execDataKeyLen: [ns:2][slot:8][blockHash:4] = 14 bytes
	execDataKeyLen = 2 + 8 + 4

	// execDataTxSecKeyLen: [ns:2][slot:8][blockHash:4][txHash:8][section:1] = 23 bytes
	execDataTxSecKeyLen = 2 + 8 + 4 + 8 + 1
)

// makeExecDataKey builds a Pebble key for execution data (namespace 2).
// Format: [namespace:2][slot:8 big-endian][blockHash prefix:4]
func makeExecDataKey(slot uint64, blockHash []byte) []byte {
	key := make([]byte, execDataKeyLen)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceExecData)
	binary.BigEndian.PutUint64(key[2:10], slot)
	copyHashPrefix(key[10:14], blockHash, 4)
	return key
}

// makeExecDataTxSecKey builds a Pebble key for a per-tx section (namespace 3).
// Format: [namespace:2][slot:8][blockHash:4][txHash prefix:8][section:1]
func makeExecDataTxSecKey(slot uint64, blockHash []byte, txHash []byte, section uint8) []byte {
	key := make([]byte, execDataTxSecKeyLen)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceExecDataTxSec)
	binary.BigEndian.PutUint64(key[2:10], slot)
	copyHashPrefix(key[10:14], blockHash, 4)
	copyHashPrefix(key[14:22], txHash, 8)
	key[22] = section
	return key
}

// makeExecDataTxSecBlockPrefix builds a prefix for all tx sections of a block.
// Format: [namespace:2][slot:8][blockHash:4]
func makeExecDataTxSecBlockPrefix(slot uint64, blockHash []byte) []byte {
	key := make([]byte, 14)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceExecDataTxSec)
	binary.BigEndian.PutUint64(key[2:10], slot)
	copyHashPrefix(key[10:14], blockHash, 4)
	return key
}

// makeNamespaceRangeStart returns the range start key for a namespace.
func makeNamespaceRangeStart(ns uint16) []byte {
	key := make([]byte, 2)
	binary.BigEndian.PutUint16(key[0:2], ns)
	return key
}

// makeNamespaceSlotKey returns [namespace:2][slot:8] for range operations.
func makeNamespaceSlotKey(ns uint16, slot uint64) []byte {
	key := make([]byte, 10)
	binary.BigEndian.PutUint16(key[0:2], ns)
	binary.BigEndian.PutUint64(key[2:10], slot)
	return key
}

// copyHashPrefix copies up to n bytes from src to dst.
func copyHashPrefix(dst []byte, src []byte, n int) {
	l := len(src)
	if l > n {
		l = n
	}
	copy(dst[:l], src[:l])
}

// sectionFlagToByte maps ExecDataSection* bitmask flags to the single-byte
// section identifier used in Pebble keys.
func sectionFlagToByte(flag uint32) uint8 {
	switch flag {
	case types.ExecDataSectionEvents:
		return 1
	case types.ExecDataSectionCallTrace:
		return 2
	case types.ExecDataSectionStateChange:
		return 4
	default:
		return 0
	}
}

// AddExecData stores execution data for a block. Parses the DXTX blob and
// decomposes it into per-tx per-section entries (namespace 3) plus a
// metadata marker (namespace 2). Uses a Pebble Batch for atomicity.
func (e *PebbleEngine) AddExecData(_ context.Context, slot uint64, blockHash []byte, data []byte) (int64, error) {
	obj, err := types.ParseExecDataIndex(data)
	if err != nil {
		return 0, fmt.Errorf("failed to parse exec data: %w", err)
	}

	txCount := uint32(len(obj.Transactions))
	indexSize := types.ExecDataIndexSize(txCount)

	batch := e.db.NewBatch()
	defer func() { _ = batch.Close() }()

	var totalSize int64

	// Store per-tx per-section entries
	for i := range obj.Transactions {
		entry := &obj.Transactions[i]

		type secInfo struct {
			flag    uint32
			offset  uint64
			compLen uint32
		}

		sections := []secInfo{
			{types.ExecDataSectionEvents, entry.EventsOffset, entry.EventsCompLen},
			{types.ExecDataSectionCallTrace, entry.CallTraceOffset, entry.CallTraceCompLen},
			{types.ExecDataSectionStateChange, entry.StateChangeOffset, entry.StateChangeCompLen},
		}

		for _, sec := range sections {
			if sec.compLen == 0 {
				continue
			}

			secByte := sectionFlagToByte(sec.flag)
			key := makeExecDataTxSecKey(slot, blockHash, entry.TxHash[:], secByte)

			start := indexSize + int(sec.offset)
			end := start + int(sec.compLen)
			if end > len(data) {
				return 0, fmt.Errorf(
					"section data out of bounds: tx=%d sec=%d",
					i, sec.flag,
				)
			}

			value := make([]byte, sec.compLen)
			copy(value, data[start:end])

			if err := batch.Set(key, value, nil); err != nil {
				return 0, fmt.Errorf("failed to set tx section: %w", err)
			}

			totalSize += int64(len(key)) + int64(sec.compLen)
		}
	}

	// Store metadata marker under namespace 2 (empty value signals
	// decomposed format; old format had DXTX magic in the value)
	metaKey := makeExecDataKey(slot, blockHash)
	if err := batch.Set(metaKey, []byte{}, nil); err != nil {
		return 0, fmt.Errorf("failed to set exec data metadata: %w", err)
	}

	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, fmt.Errorf("failed to commit exec data batch: %w", err)
	}

	return totalSize, nil
}

// GetExecData retrieves full execution data for a block.
// For backward compat: if the ns=2 value contains DXTX magic, return it.
// For new decomposed format (empty ns=2 marker), returns nil (callers
// should use GetExecDataTxSections instead).
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

	// New decomposed format: empty value or no DXTX magic
	if len(res) < 4 || res[0] != 'D' || res[1] != 'X' || res[2] != 'T' || res[3] != 'X' {
		return nil, nil
	}

	// Legacy full blob format
	data := make([]byte, len(res))
	copy(data, res)
	return data, nil
}

// GetExecDataRange retrieves a byte range of execution data.
// Only works with legacy full-blob format.
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

// GetExecDataTxSections retrieves compressed section data for a single
// transaction. Each requested section is a direct Pebble key lookup.
func (e *PebbleEngine) GetExecDataTxSections(_ context.Context, slot uint64, blockHash []byte, txHash []byte, sections uint32) (*types.ExecDataTxSections, error) {
	result := &types.ExecDataTxSections{}
	found := false

	readSection := func(flag uint32) ([]byte, error) {
		if sections&flag == 0 {
			return nil, nil
		}

		secByte := sectionFlagToByte(flag)
		key := makeExecDataTxSecKey(slot, blockHash, txHash, secByte)

		res, closer, err := e.db.Get(key)
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer func() { _ = closer.Close() }()

		found = true
		data := make([]byte, len(res))
		copy(data, res)
		return data, nil
	}

	var err error

	result.EventsData, err = readSection(types.ExecDataSectionEvents)
	if err != nil {
		return nil, err
	}

	result.CallTraceData, err = readSection(types.ExecDataSectionCallTrace)
	if err != nil {
		return nil, err
	}

	result.StateChangeData, err = readSection(types.ExecDataSectionStateChange)
	if err != nil {
		return nil, err
	}

	if !found {
		// Fall back to legacy ns=2 full DXTX blob format
		return e.getExecDataTxSectionsLegacy(slot, blockHash, txHash, sections)
	}

	return result, nil
}

// getExecDataTxSectionsLegacy extracts section data from a legacy full DXTX
// blob stored under namespace 2. Used for backward compatibility with data
// written before the decomposed namespace 3 format was introduced.
func (e *PebbleEngine) getExecDataTxSectionsLegacy(slot uint64, blockHash []byte, txHash []byte, sections uint32) (*types.ExecDataTxSections, error) {
	key := makeExecDataKey(slot, blockHash)

	res, closer, err := e.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()

	// Must have DXTX magic to be a legacy blob
	if len(res) < 4 || res[0] != 'D' || res[1] != 'X' || res[2] != 'T' || res[3] != 'X' {
		return nil, nil
	}

	obj, err := types.ParseExecDataIndex(res)
	if err != nil {
		return nil, fmt.Errorf("failed to parse legacy exec data: %w", err)
	}

	entry := obj.FindTxEntry(txHash)
	if entry == nil {
		return nil, nil
	}

	txCount := uint32(len(obj.Transactions))
	result := &types.ExecDataTxSections{}
	hasData := false

	if sections&types.ExecDataSectionEvents != 0 && entry.EventsCompLen > 0 {
		data, err := types.ExtractSectionData(res, txCount, entry.EventsOffset, entry.EventsCompLen)
		if err != nil {
			return nil, err
		}
		result.EventsData = data
		hasData = true
	}

	if sections&types.ExecDataSectionCallTrace != 0 && entry.CallTraceCompLen > 0 {
		data, err := types.ExtractSectionData(res, txCount, entry.CallTraceOffset, entry.CallTraceCompLen)
		if err != nil {
			return nil, err
		}
		result.CallTraceData = data
		hasData = true
	}

	if sections&types.ExecDataSectionStateChange != 0 && entry.StateChangeCompLen > 0 {
		data, err := types.ExtractSectionData(res, txCount, entry.StateChangeOffset, entry.StateChangeCompLen)
		if err != nil {
			return nil, err
		}
		result.StateChangeData = data
		hasData = true
	}

	if !hasData {
		return nil, nil
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
// Removes both the namespace 2 metadata and all namespace 3 tx sections.
func (e *PebbleEngine) DeleteExecData(_ context.Context, slot uint64, blockHash []byte) error {
	batch := e.db.NewBatch()
	defer func() { _ = batch.Close() }()

	// Delete ns=2 metadata key
	metaKey := makeExecDataKey(slot, blockHash)
	if err := batch.Delete(metaKey, nil); err != nil {
		return err
	}

	// Delete all ns=3 tx section keys for this block using range delete
	prefix := makeExecDataTxSecBlockPrefix(slot, blockHash)
	rangeEnd := make([]byte, len(prefix))
	copy(rangeEnd, prefix)
	// Increment last byte to create exclusive upper bound
	for i := len(rangeEnd) - 1; i >= 0; i-- {
		rangeEnd[i]++
		if rangeEnd[i] != 0 {
			break
		}
	}

	if err := batch.DeleteRange(prefix, rangeEnd, nil); err != nil {
		return err
	}

	return batch.Commit(pebble.Sync)
}

// PruneExecDataBefore deletes execution data for all slots before maxSlot.
// Uses Pebble's DeleteRange for both namespace 2 and namespace 3.
func (e *PebbleEngine) PruneExecDataBefore(_ context.Context, maxSlot uint64) (int64, error) {
	// Count entries in namespace 2 before deleting
	ns2Start := makeNamespaceRangeStart(KeyNamespaceExecData)
	ns2End := makeNamespaceSlotKey(KeyNamespaceExecData, maxSlot)

	var count int64

	iter, err := e.db.NewIter(&pebble.IterOptions{
		LowerBound: ns2Start,
		UpperBound: ns2End,
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

	batch := e.db.NewBatch()
	defer func() { _ = batch.Close() }()

	// Delete namespace 2 range
	if err := batch.DeleteRange(ns2Start, ns2End, nil); err != nil {
		return 0, err
	}

	// Delete namespace 3 range (same slot boundary)
	ns3Start := makeNamespaceRangeStart(KeyNamespaceExecDataTxSec)
	ns3End := makeNamespaceSlotKey(KeyNamespaceExecDataTxSec, maxSlot)
	if err := batch.DeleteRange(ns3Start, ns3End, nil); err != nil {
		return 0, err
	}

	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, err
	}

	return count, nil
}
