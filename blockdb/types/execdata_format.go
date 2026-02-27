// Package types provides the per-block execution data binary format (DXTX).
//
// The format stores events, call traces, state changes, and receipt metadata
// for all transactions in a block. Each section is independently
// snappy-compressed for efficient selective decompression.
//
// Object layout:
//
//	OBJECT HEADER (40 bytes)
//	├── Magic:              [4]byte  = "DXTX"
//	├── Format Version:     uint16
//	├── Flags:              uint16   (reserved, 0)
//	├── Block Slot:         uint64
//	├── Block Number:       uint64
//	├── BlockMeta Section:  offset(8) + compLen(4) + uncompLen(4)
//	TX COUNT (4 bytes)
//	├── TX Count:           uint32
//	TX INDEX TABLE (100 bytes per tx)
//	For each TX:
//	├── TX Hash:            [32]byte
//	├── Sections Bitmap:    uint32   (0x01=ReceiptMeta, 0x02=Events, 0x04=CallTrace, 0x08=StateChanges)
//	├── ReceiptMeta Section: offset(8) + compLen(4) + uncompLen(4)
//	├── Events Section:     offset(8) + compLen(4) + uncompLen(4)
//	├── CallTrace Section:  offset(8) + compLen(4) + uncompLen(4)
//	└── StateChanges Section: offset(8) + compLen(4) + uncompLen(4)
//	DATA AREA
//	├── [BlockMeta section blob (snappy-compressed)]
//	├── [Per-TX snappy-compressed section blobs]
package types

import (
	"encoding/binary"
	"fmt"
)

// Binary format constants
var ExecDataMagic = [4]byte{'D', 'X', 'T', 'X'}

const (
	ExecDataFormatVersion = 1

	// Object header: 4 magic + 2 version + 2 flags + 8 slot + 8 blockNumber + 16 blockMeta ptr = 40
	ExecDataHeaderSize = 40

	// TX count field: 4 bytes
	ExecDataTxCountSize = 4

	// Per-TX index entry: 32 hash + 4 bitmap + 4*(8+4+4) sections = 100
	ExecDataTxEntrySize = 100

	// Section bitmap flags
	ExecDataSectionReceiptMeta = 0x01
	ExecDataSectionEvents      = 0x02
	ExecDataSectionCallTrace   = 0x04
	ExecDataSectionStateChange = 0x08
)

// ExecDataObject represents the decoded index of a per-block execution data object.
// The actual section data is not loaded until explicitly requested.
type ExecDataObject struct {
	FormatVersion uint16
	Flags         uint16
	BlockSlot     uint64
	BlockNumber   uint64

	// Block-level metadata section pointer.
	BlockMetaOffset    uint64
	BlockMetaCompLen   uint32
	BlockMetaUncompLen uint32

	Transactions []ExecDataTxEntry
}

// ExecDataTxEntry is the index entry for a single transaction.
type ExecDataTxEntry struct {
	TxHash         [32]byte
	SectionsBitmap uint32

	ReceiptMetaOffset    uint64
	ReceiptMetaCompLen   uint32
	ReceiptMetaUncompLen uint32

	EventsOffset    uint64
	EventsCompLen   uint32
	EventsUncompLen uint32

	CallTraceOffset    uint64
	CallTraceCompLen   uint32
	CallTraceUncompLen uint32

	StateChangeOffset    uint64
	StateChangeCompLen   uint32
	StateChangeUncompLen uint32
}

// ExecDataTxSectionData holds the compressed section data for a single transaction.
// Used during object construction.
type ExecDataTxSectionData struct {
	TxHash [32]byte

	// Compressed section data (nil if section not present)
	ReceiptMetaData []byte
	EventsData      []byte
	CallTraceData   []byte
	StateChangeData []byte

	// Uncompressed lengths (for the index)
	ReceiptMetaUncompLen uint32
	EventsUncompLen      uint32
	CallTraceUncompLen   uint32
	StateChangeUncompLen uint32
}

// BuildExecDataObject serializes a per-block execution data object.
// blockMeta is the pre-compressed block-level metadata section (nil if none).
// txSections contains pre-compressed section data for each transaction.
func BuildExecDataObject(
	blockSlot uint64,
	blockNumber uint64,
	blockMeta []byte,
	blockMetaUncompLen uint32,
	txSections []ExecDataTxSectionData,
) []byte {
	txCount := uint32(len(txSections))

	indexSize := ExecDataHeaderSize + ExecDataTxCountSize + int(txCount)*ExecDataTxEntrySize

	// Calculate total data area size
	dataSize := len(blockMeta)
	for i := range txSections {
		dataSize += len(txSections[i].ReceiptMetaData)
		dataSize += len(txSections[i].EventsData)
		dataSize += len(txSections[i].CallTraceData)
		dataSize += len(txSections[i].StateChangeData)
	}

	buf := make([]byte, indexSize+dataSize)

	// Write object header
	copy(buf[0:4], ExecDataMagic[:])
	binary.BigEndian.PutUint16(buf[4:6], ExecDataFormatVersion)
	binary.BigEndian.PutUint16(buf[6:8], 0) // flags reserved
	binary.BigEndian.PutUint64(buf[8:16], blockSlot)
	binary.BigEndian.PutUint64(buf[16:24], blockNumber)

	// Data area offset tracker (relative to start of DATA AREA)
	dataOffset := uint64(0)

	// Write block meta section at start of data area
	binary.BigEndian.PutUint64(buf[24:32], 0)
	binary.BigEndian.PutUint32(buf[32:36], uint32(len(blockMeta)))
	binary.BigEndian.PutUint32(buf[36:40], blockMetaUncompLen)
	if len(blockMeta) > 0 {
		copy(buf[indexSize:], blockMeta)
		dataOffset += uint64(len(blockMeta))
	}

	// Write TX count
	binary.BigEndian.PutUint32(buf[40:44], txCount)

	// Write TX index entries and section data
	for i := range txSections {
		tx := &txSections[i]
		entryOffset := ExecDataHeaderSize + ExecDataTxCountSize + i*ExecDataTxEntrySize

		// TX Hash
		copy(buf[entryOffset:entryOffset+32], tx.TxHash[:])
		entryOffset += 32

		// Build bitmap
		var bitmap uint32
		if len(tx.ReceiptMetaData) > 0 {
			bitmap |= ExecDataSectionReceiptMeta
		}
		if len(tx.EventsData) > 0 {
			bitmap |= ExecDataSectionEvents
		}
		if len(tx.CallTraceData) > 0 {
			bitmap |= ExecDataSectionCallTrace
		}
		if len(tx.StateChangeData) > 0 {
			bitmap |= ExecDataSectionStateChange
		}
		binary.BigEndian.PutUint32(buf[entryOffset:entryOffset+4], bitmap)
		entryOffset += 4

		// ReceiptMeta section
		binary.BigEndian.PutUint64(buf[entryOffset:entryOffset+8], dataOffset)
		binary.BigEndian.PutUint32(buf[entryOffset+8:entryOffset+12], uint32(len(tx.ReceiptMetaData)))
		binary.BigEndian.PutUint32(buf[entryOffset+12:entryOffset+16], tx.ReceiptMetaUncompLen)
		entryOffset += 16
		if len(tx.ReceiptMetaData) > 0 {
			copy(buf[indexSize+int(dataOffset):], tx.ReceiptMetaData)
			dataOffset += uint64(len(tx.ReceiptMetaData))
		}

		// Events section
		binary.BigEndian.PutUint64(buf[entryOffset:entryOffset+8], dataOffset)
		binary.BigEndian.PutUint32(buf[entryOffset+8:entryOffset+12], uint32(len(tx.EventsData)))
		binary.BigEndian.PutUint32(buf[entryOffset+12:entryOffset+16], tx.EventsUncompLen)
		entryOffset += 16
		if len(tx.EventsData) > 0 {
			copy(buf[indexSize+int(dataOffset):], tx.EventsData)
			dataOffset += uint64(len(tx.EventsData))
		}

		// CallTrace section
		binary.BigEndian.PutUint64(buf[entryOffset:entryOffset+8], dataOffset)
		binary.BigEndian.PutUint32(buf[entryOffset+8:entryOffset+12], uint32(len(tx.CallTraceData)))
		binary.BigEndian.PutUint32(buf[entryOffset+12:entryOffset+16], tx.CallTraceUncompLen)
		entryOffset += 16
		if len(tx.CallTraceData) > 0 {
			copy(buf[indexSize+int(dataOffset):], tx.CallTraceData)
			dataOffset += uint64(len(tx.CallTraceData))
		}

		// StateChanges section
		binary.BigEndian.PutUint64(buf[entryOffset:entryOffset+8], dataOffset)
		binary.BigEndian.PutUint32(buf[entryOffset+8:entryOffset+12], uint32(len(tx.StateChangeData)))
		binary.BigEndian.PutUint32(buf[entryOffset+12:entryOffset+16], tx.StateChangeUncompLen)
		if len(tx.StateChangeData) > 0 {
			copy(buf[indexSize+int(dataOffset):], tx.StateChangeData)
			dataOffset += uint64(len(tx.StateChangeData))
		}
	}

	return buf
}

// ParseExecDataIndex parses only the index (header + TX entries) from an
// execution data object. Does NOT read any section data.
// This is designed for use with partial reads (S3 range requests or Pebble slicing).
func ParseExecDataIndex(data []byte) (*ExecDataObject, error) {
	if len(data) < ExecDataHeaderSize+ExecDataTxCountSize {
		return nil, fmt.Errorf("exec data too short: %d bytes", len(data))
	}

	// Validate magic
	if data[0] != ExecDataMagic[0] || data[1] != ExecDataMagic[1] ||
		data[2] != ExecDataMagic[2] || data[3] != ExecDataMagic[3] {
		return nil, fmt.Errorf("invalid exec data magic: %x", data[0:4])
	}

	obj := &ExecDataObject{
		FormatVersion:      binary.BigEndian.Uint16(data[4:6]),
		Flags:              binary.BigEndian.Uint16(data[6:8]),
		BlockSlot:          binary.BigEndian.Uint64(data[8:16]),
		BlockNumber:        binary.BigEndian.Uint64(data[16:24]),
		BlockMetaOffset:    binary.BigEndian.Uint64(data[24:32]),
		BlockMetaCompLen:   binary.BigEndian.Uint32(data[32:36]),
		BlockMetaUncompLen: binary.BigEndian.Uint32(data[36:40]),
	}

	if obj.FormatVersion != ExecDataFormatVersion {
		return nil, fmt.Errorf("unsupported exec data version: %d", obj.FormatVersion)
	}

	txCount := binary.BigEndian.Uint32(data[40:44])

	expectedIndexSize := ExecDataHeaderSize + ExecDataTxCountSize + int(txCount)*ExecDataTxEntrySize
	if len(data) < expectedIndexSize {
		return nil, fmt.Errorf("exec data index truncated: need %d bytes, got %d", expectedIndexSize, len(data))
	}

	obj.Transactions = make([]ExecDataTxEntry, txCount)
	for i := range txCount {
		offset := ExecDataHeaderSize + ExecDataTxCountSize + int(i)*ExecDataTxEntrySize
		entry := &obj.Transactions[i]

		copy(entry.TxHash[:], data[offset:offset+32])
		entry.SectionsBitmap = binary.BigEndian.Uint32(data[offset+32 : offset+36])

		// ReceiptMeta
		entry.ReceiptMetaOffset = binary.BigEndian.Uint64(data[offset+36 : offset+44])
		entry.ReceiptMetaCompLen = binary.BigEndian.Uint32(data[offset+44 : offset+48])
		entry.ReceiptMetaUncompLen = binary.BigEndian.Uint32(data[offset+48 : offset+52])

		// Events
		entry.EventsOffset = binary.BigEndian.Uint64(data[offset+52 : offset+60])
		entry.EventsCompLen = binary.BigEndian.Uint32(data[offset+60 : offset+64])
		entry.EventsUncompLen = binary.BigEndian.Uint32(data[offset+64 : offset+68])

		// CallTrace
		entry.CallTraceOffset = binary.BigEndian.Uint64(data[offset+68 : offset+76])
		entry.CallTraceCompLen = binary.BigEndian.Uint32(data[offset+76 : offset+80])
		entry.CallTraceUncompLen = binary.BigEndian.Uint32(data[offset+80 : offset+84])

		// StateChanges
		entry.StateChangeOffset = binary.BigEndian.Uint64(data[offset+84 : offset+92])
		entry.StateChangeCompLen = binary.BigEndian.Uint32(data[offset+92 : offset+96])
		entry.StateChangeUncompLen = binary.BigEndian.Uint32(data[offset+96 : offset+100])
	}

	return obj, nil
}

// ExecDataIndexSize returns the size in bytes of the index (header + all TX entries)
// for a given number of transactions.
func ExecDataIndexSize(txCount uint32) int {
	return ExecDataHeaderSize + ExecDataTxCountSize + int(txCount)*ExecDataTxEntrySize
}

// ExecDataMinHeaderSize returns the minimum bytes needed to read the TX count.
func ExecDataMinHeaderSize() int {
	return ExecDataHeaderSize + ExecDataTxCountSize
}

// ParseExecDataTxCount parses the minimum header bytes needed to extract the
// transaction count from an execution data object. This is intended for partial
// reads (e.g., S3 range reads) where only the first ExecDataMinHeaderSize bytes
// are available.
func ParseExecDataTxCount(header []byte) (uint32, error) {
	if len(header) < ExecDataMinHeaderSize() {
		return 0, fmt.Errorf("exec data header too short: %d bytes", len(header))
	}

	// Validate magic
	if header[0] != ExecDataMagic[0] || header[1] != ExecDataMagic[1] ||
		header[2] != ExecDataMagic[2] || header[3] != ExecDataMagic[3] {
		return 0, fmt.Errorf("invalid exec data magic: %x", header[0:4])
	}

	ver := binary.BigEndian.Uint16(header[4:6])
	if ver != ExecDataFormatVersion {
		return 0, fmt.Errorf("unsupported exec data version: %d", ver)
	}

	return binary.BigEndian.Uint32(header[40:44]), nil
}

// ExtractSectionData extracts a specific compressed section from raw object data.
// The offset is relative to the start of the DATA AREA (after the index).
// Returns nil if the section has zero length.
func ExtractSectionData(objectData []byte, txCount uint32, sectionOffset uint64, sectionCompLen uint32) ([]byte, error) {
	if sectionCompLen == 0 {
		return nil, nil
	}

	dataAreaStart := ExecDataHeaderSize + ExecDataTxCountSize + int(txCount)*ExecDataTxEntrySize
	start := dataAreaStart + int(sectionOffset)
	end := start + int(sectionCompLen)

	if end > len(objectData) {
		return nil, fmt.Errorf(
			"section data out of bounds: offset=%d, len=%d, objectSize=%d",
			start, sectionCompLen, len(objectData),
		)
	}

	return objectData[start:end], nil
}

// ExtractBlockMeta extracts the compressed block metadata section from raw
// object data. Returns nil if no block meta section is present.
func (obj *ExecDataObject) ExtractBlockMeta(objectData []byte) ([]byte, error) {
	return ExtractSectionData(
		objectData, uint32(len(obj.Transactions)),
		obj.BlockMetaOffset, obj.BlockMetaCompLen,
	)
}

// FindTxEntry finds the index entry for a specific transaction hash.
// Returns nil if not found.
func (obj *ExecDataObject) FindTxEntry(txHash []byte) *ExecDataTxEntry {
	for i := range obj.Transactions {
		if matchesHash(obj.Transactions[i].TxHash[:], txHash) {
			return &obj.Transactions[i]
		}
	}
	return nil
}

// GetTxSectionSpan calculates the contiguous byte range in the data area
// covering the requested sections for a tx entry. mask selects which
// sections to include (ExecDataSection* constants). Returns the offset
// (relative to data area start) and total length. Returns 0,0 if no
// matching sections are present.
func (entry *ExecDataTxEntry) GetTxSectionSpan(mask uint32) (offset uint64, length uint64) {
	type sec struct {
		flag    uint32
		off     uint64
		compLen uint32
	}

	sections := []sec{
		{ExecDataSectionReceiptMeta, entry.ReceiptMetaOffset, entry.ReceiptMetaCompLen},
		{ExecDataSectionEvents, entry.EventsOffset, entry.EventsCompLen},
		{ExecDataSectionCallTrace, entry.CallTraceOffset, entry.CallTraceCompLen},
		{ExecDataSectionStateChange, entry.StateChangeOffset, entry.StateChangeCompLen},
	}

	var minOff uint64
	var maxEnd uint64
	found := false

	for _, s := range sections {
		if mask&s.flag == 0 || s.compLen == 0 {
			continue
		}

		end := s.off + uint64(s.compLen)
		if !found {
			minOff = s.off
			maxEnd = end
			found = true
		} else {
			if s.off < minOff {
				minOff = s.off
			}
			if end > maxEnd {
				maxEnd = end
			}
		}
	}

	if !found {
		return 0, 0
	}

	return minOff, maxEnd - minOff
}

// SliceTxSections extracts individual section blobs from a contiguous data
// chunk that was read starting at spanOffset in the data area. mask
// selects which sections to extract. The chunk must cover the span
// returned by GetTxSectionSpan with the same mask.
func (entry *ExecDataTxEntry) SliceTxSections(
	chunk []byte, spanOffset uint64, mask uint32,
) (events, callTrace, stateChange, receiptMeta []byte) {
	slice := func(flag uint32, off uint64, compLen uint32) []byte {
		if mask&flag == 0 || compLen == 0 {
			return nil
		}
		start := off - spanOffset
		end := start + uint64(compLen)
		if end > uint64(len(chunk)) {
			return nil
		}
		out := make([]byte, compLen)
		copy(out, chunk[start:end])
		return out
	}

	receiptMeta = slice(ExecDataSectionReceiptMeta, entry.ReceiptMetaOffset, entry.ReceiptMetaCompLen)
	events = slice(ExecDataSectionEvents, entry.EventsOffset, entry.EventsCompLen)
	callTrace = slice(ExecDataSectionCallTrace, entry.CallTraceOffset, entry.CallTraceCompLen)
	stateChange = slice(ExecDataSectionStateChange, entry.StateChangeOffset, entry.StateChangeCompLen)
	return
}

func matchesHash(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
