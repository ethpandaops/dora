// Package blockdb provides the per-block execution data binary format.
//
// The format stores events, call traces, and state changes for all transactions
// in a block. Each section is independently snappy-compressed for efficient
// selective decompression.
//
// Object layout:
//
//	OBJECT HEADER (24 bytes)
//	├── Magic:           [4]byte  = "DXTX"
//	├── Format Version:  uint16
//	├── Flags:           uint16   (reserved, 0)
//	├── Block Slot:      uint64
//	├── Block Number:    uint64
//	TX COUNT (4 bytes)
//	├── TX Count:        uint32
//	TX INDEX TABLE (100 bytes per tx)
//	For each TX:
//	├── TX Hash:         [32]byte
//	├── Sections Bitmap: uint32   (0x01=Events, 0x02=CallTrace, 0x04=StateChanges)
//	├── Events Section:  offset(8) + compLen(4) + uncompLen(4)
//	├── CallTrace Section: offset(8) + compLen(4) + uncompLen(4)
//	├── StateChanges Section: offset(8) + compLen(4) + uncompLen(4)
//	└── Reserved:        [16]byte
//	DATA AREA
//	├── [Snappy-compressed section blobs]
package blockdb

import (
	"encoding/binary"
	"fmt"
)

// Binary format constants
var execDataMagic = [4]byte{'D', 'X', 'T', 'X'}

const (
	execDataFormatVersion = 1

	// Object header: 4 magic + 2 version + 2 flags + 8 slot + 8 blockNumber = 24
	execDataHeaderSize = 24

	// TX count field: 4 bytes
	execDataTxCountSize = 4

	// Per-TX index entry: 32 hash + 4 bitmap + 3*(8+4+4) sections + 16 reserved = 100
	execDataTxEntrySize = 100

	// Section bitmap flags
	ExecDataSectionEvents      = 0x01
	ExecDataSectionCallTrace   = 0x02
	ExecDataSectionStateChange = 0x04
)

// ExecDataObject represents the decoded index of a per-block execution data object.
// The actual section data is not loaded until explicitly requested.
type ExecDataObject struct {
	FormatVersion uint16
	Flags         uint16
	BlockSlot     uint64
	BlockNumber   uint64
	Transactions  []ExecDataTxEntry
}

// ExecDataTxEntry is the index entry for a single transaction.
type ExecDataTxEntry struct {
	TxHash         [32]byte
	SectionsBitmap uint32

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
	EventsData      []byte
	CallTraceData   []byte
	StateChangeData []byte

	// Uncompressed lengths (for the index)
	EventsUncompLen      uint32
	CallTraceUncompLen   uint32
	StateChangeUncompLen uint32
}

// BuildExecDataObject serializes a per-block execution data object.
// txSections contains pre-compressed section data for each transaction.
func BuildExecDataObject(
	blockSlot uint64,
	blockNumber uint64,
	txSections []ExecDataTxSectionData,
) []byte {
	txCount := uint32(len(txSections))

	indexSize := execDataHeaderSize + execDataTxCountSize + int(txCount)*execDataTxEntrySize

	// Calculate total data area size
	dataSize := 0
	for i := range txSections {
		dataSize += len(txSections[i].EventsData)
		dataSize += len(txSections[i].CallTraceData)
		dataSize += len(txSections[i].StateChangeData)
	}

	buf := make([]byte, indexSize+dataSize)

	// Write object header
	copy(buf[0:4], execDataMagic[:])
	binary.BigEndian.PutUint16(buf[4:6], execDataFormatVersion)
	binary.BigEndian.PutUint16(buf[6:8], 0) // flags reserved
	binary.BigEndian.PutUint64(buf[8:16], blockSlot)
	binary.BigEndian.PutUint64(buf[16:24], blockNumber)

	// Write TX count
	binary.BigEndian.PutUint32(buf[24:28], txCount)

	// Write TX index entries and data area
	dataOffset := uint64(0) // relative to start of DATA AREA
	for i := range txSections {
		tx := &txSections[i]
		entryOffset := execDataHeaderSize + execDataTxCountSize + i*execDataTxEntrySize

		// TX Hash
		copy(buf[entryOffset:entryOffset+32], tx.TxHash[:])
		entryOffset += 32

		// Build bitmap
		var bitmap uint32
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

		// Reserved 16 bytes (already zeroed from make)
	}

	return buf
}

// ParseExecDataIndex parses only the index (header + TX entries) from an
// execution data object. Does NOT read any section data.
// This is designed for use with partial reads (S3 range requests or Pebble slicing).
func ParseExecDataIndex(data []byte) (*ExecDataObject, error) {
	if len(data) < execDataHeaderSize+execDataTxCountSize {
		return nil, fmt.Errorf("exec data too short: %d bytes", len(data))
	}

	// Validate magic
	if data[0] != execDataMagic[0] || data[1] != execDataMagic[1] ||
		data[2] != execDataMagic[2] || data[3] != execDataMagic[3] {
		return nil, fmt.Errorf("invalid exec data magic: %x", data[0:4])
	}

	obj := &ExecDataObject{
		FormatVersion: binary.BigEndian.Uint16(data[4:6]),
		Flags:         binary.BigEndian.Uint16(data[6:8]),
		BlockSlot:     binary.BigEndian.Uint64(data[8:16]),
		BlockNumber:   binary.BigEndian.Uint64(data[16:24]),
	}

	if obj.FormatVersion != execDataFormatVersion {
		return nil, fmt.Errorf("unsupported exec data version: %d", obj.FormatVersion)
	}

	txCount := binary.BigEndian.Uint32(data[24:28])

	expectedIndexSize := execDataHeaderSize + execDataTxCountSize + int(txCount)*execDataTxEntrySize
	if len(data) < expectedIndexSize {
		return nil, fmt.Errorf("exec data index truncated: need %d bytes, got %d", expectedIndexSize, len(data))
	}

	obj.Transactions = make([]ExecDataTxEntry, txCount)
	for i := range txCount {
		offset := execDataHeaderSize + execDataTxCountSize + int(i)*execDataTxEntrySize
		entry := &obj.Transactions[i]

		copy(entry.TxHash[:], data[offset:offset+32])
		entry.SectionsBitmap = binary.BigEndian.Uint32(data[offset+32 : offset+36])

		// Events
		entry.EventsOffset = binary.BigEndian.Uint64(data[offset+36 : offset+44])
		entry.EventsCompLen = binary.BigEndian.Uint32(data[offset+44 : offset+48])
		entry.EventsUncompLen = binary.BigEndian.Uint32(data[offset+48 : offset+52])

		// CallTrace
		entry.CallTraceOffset = binary.BigEndian.Uint64(data[offset+52 : offset+60])
		entry.CallTraceCompLen = binary.BigEndian.Uint32(data[offset+60 : offset+64])
		entry.CallTraceUncompLen = binary.BigEndian.Uint32(data[offset+64 : offset+68])

		// StateChanges
		entry.StateChangeOffset = binary.BigEndian.Uint64(data[offset+68 : offset+76])
		entry.StateChangeCompLen = binary.BigEndian.Uint32(data[offset+76 : offset+80])
		entry.StateChangeUncompLen = binary.BigEndian.Uint32(data[offset+80 : offset+84])
	}

	return obj, nil
}

// ExecDataIndexSize returns the size in bytes of the index (header + all TX entries)
// for a given number of transactions. Useful for calculating how much to read
// from S3 range requests to get just the index.
func ExecDataIndexSize(txCount uint32) int {
	return execDataHeaderSize + execDataTxCountSize + int(txCount)*execDataTxEntrySize
}

// ExecDataMinHeaderSize returns the minimum bytes needed to read the TX count.
// Read this many bytes first, then calculate full index size.
func ExecDataMinHeaderSize() int {
	return execDataHeaderSize + execDataTxCountSize
}

// ExtractSectionData extracts a specific compressed section from raw object data.
// The offset is relative to the start of the DATA AREA (after the index).
// Returns nil if the section has zero length.
func ExtractSectionData(objectData []byte, txCount uint32, sectionOffset uint64, sectionCompLen uint32) ([]byte, error) {
	if sectionCompLen == 0 {
		return nil, nil
	}

	dataAreaStart := execDataHeaderSize + execDataTxCountSize + int(txCount)*execDataTxEntrySize
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
