// Package types: per-epoch duties object format (DUTY).
//
// One object stores the resolved attester committees and PTC (payload timeliness
// committee) duty mappings for a single finalized epoch, keyed by the epoch's
// first slot. The layout is deterministic: committee byte offsets are a pure
// function of the epoch's active-validator count, so a reader can address any
// (slot, committee) range without an index table.
//
// Object layout:
//
//	HEADER (40 bytes)
//	├── Magic:             [4]byte = "DUTY"
//	├── Version:           uint16
//	├── Flags:             uint8   (reserved, 0)
//	├── IndexWidth:        uint8   (bytes per validator index, = 6)
//	├── Epoch:             uint64
//	├── ValidatorCount:    uint64  (active validator count; drives attester offsets)
//	├── SlotsPerEpoch:     uint32
//	├── CommitteesPerSlot: uint32  (stored so the object reads without spec constants)
//	└── PtcSize:           uint32  (0 if pre-Gloas)
//	ATTESTER SECTION: ValidatorCount * IndexWidth bytes
//	│   flat list of global validator indices in (slotIndex, committeeIndex, position) order
//	PTC SECTION: SlotsPerEpoch * PtcSize * IndexWidth bytes (omitted if PtcSize == 0)
package types

import (
	"encoding/binary"
	"fmt"
)

// DutiesMagic identifies a duties object.
var DutiesMagic = [4]byte{'D', 'U', 'T', 'Y'}

const (
	// DutiesFormatVersion is the current duties object format version.
	DutiesFormatVersion uint16 = 1

	// DutiesHeaderSize is the fixed header size for version 1.
	DutiesHeaderSize = 40

	// DutiesIndexWidth is the number of bytes used to encode a validator index.
	DutiesIndexWidth uint8 = 6
)

// DutiesHeader is the decoded header of a duties object. Section offsets are
// derived, not stored.
type DutiesHeader struct {
	Version           uint16
	Flags             uint8
	IndexWidth        uint8
	Epoch             uint64
	ValidatorCount    uint64
	SlotsPerEpoch     uint64
	CommitteesPerSlot uint64
	PtcSize           uint64
}

// EpochDuties holds the resolved duty mappings for one epoch. It is the input
// to EncodeEpochDuties and the result of decoding a full object.
type EpochDuties struct {
	FirstSlot         uint64
	Epoch             uint64
	ValidatorCount    uint64
	SlotsPerEpoch     uint64
	CommitteesPerSlot uint64
	PtcSize           uint64

	// Committees[slotIndex][committeeIndex] holds the global validator indices
	// of that committee in attestation-bit order.
	Committees [][][]uint64
	// Ptc[slotIndex] holds the PTC members (global validator indices), each
	// slice exactly PtcSize long. Nil if PtcSize == 0.
	Ptc [][]uint64
}

// maxIndexValue is the largest validator index encodable in DutiesIndexWidth bytes.
const maxIndexValue = uint64(1)<<(8*uint64(DutiesIndexWidth)) - 1

// EncodeEpochDuties serializes the duties for an epoch into the DUTY object format.
func EncodeEpochDuties(d *EpochDuties) ([]byte, error) {
	if d.ValidatorCount > maxIndexValue {
		return nil, fmt.Errorf("validator count %d exceeds %d-byte index range", d.ValidatorCount, DutiesIndexWidth)
	}
	if uint64(len(d.Committees)) != d.SlotsPerEpoch {
		return nil, fmt.Errorf("committees cover %d slots, expected %d", len(d.Committees), d.SlotsPerEpoch)
	}
	if d.PtcSize > 0 && uint64(len(d.Ptc)) != d.SlotsPerEpoch {
		return nil, fmt.Errorf("ptc covers %d slots, expected %d", len(d.Ptc), d.SlotsPerEpoch)
	}

	w := int(DutiesIndexWidth)
	attesterLen := int(d.ValidatorCount) * w
	ptcLen := int(d.SlotsPerEpoch) * int(d.PtcSize) * w

	buf := make([]byte, DutiesHeaderSize+attesterLen+ptcLen)
	writeDutiesHeader(buf, d)

	// Attester section: flat shuffled list in (slot, committee, position) order.
	pos := DutiesHeaderSize
	written := uint64(0)
	for _, committees := range d.Committees {
		for _, committee := range committees {
			for _, idx := range committee {
				if idx > maxIndexValue {
					return nil, fmt.Errorf("validator index %d exceeds %d-byte range", idx, DutiesIndexWidth)
				}
				putUint48(buf[pos:], idx)
				pos += w
				written++
			}
		}
	}
	if written != d.ValidatorCount {
		return nil, fmt.Errorf("attester duties hold %d indices, expected %d", written, d.ValidatorCount)
	}

	// PTC section: SlotsPerEpoch * PtcSize entries.
	if d.PtcSize > 0 {
		for slotIndex, ptc := range d.Ptc {
			if uint64(len(ptc)) != d.PtcSize {
				return nil, fmt.Errorf("ptc slot %d holds %d members, expected %d", slotIndex, len(ptc), d.PtcSize)
			}
			for _, idx := range ptc {
				if idx > maxIndexValue {
					return nil, fmt.Errorf("ptc index %d exceeds %d-byte range", idx, DutiesIndexWidth)
				}
				putUint48(buf[pos:], idx)
				pos += w
			}
		}
	}

	return buf, nil
}

// DecodeEpochDuties decodes a full DUTY object into the resolved EpochDuties.
func DecodeEpochDuties(firstSlot uint64, data []byte) (*EpochDuties, error) {
	h, err := DecodeDutiesHeader(data)
	if err != nil {
		return nil, err
	}

	d := &EpochDuties{
		FirstSlot:         firstSlot,
		Epoch:             h.Epoch,
		ValidatorCount:    h.ValidatorCount,
		SlotsPerEpoch:     h.SlotsPerEpoch,
		CommitteesPerSlot: h.CommitteesPerSlot,
		PtcSize:           h.PtcSize,
	}

	d.Committees = make([][][]uint64, h.SlotsPerEpoch)
	for slotIndex := range h.SlotsPerEpoch {
		off, length := h.AttesterSlotRange(slotIndex)
		if int64(len(data)) < off+length {
			return nil, fmt.Errorf("data too short for attester slot %d", slotIndex)
		}
		committees, err := h.SplitSlotCommittees(slotIndex, data[off:off+length])
		if err != nil {
			return nil, err
		}
		d.Committees[slotIndex] = committees
	}

	if h.PtcSize > 0 {
		d.Ptc = make([][]uint64, h.SlotsPerEpoch)
		for slotIndex := range h.SlotsPerEpoch {
			off, length := h.PtcSlotRange(slotIndex)
			if int64(len(data)) < off+length {
				return nil, fmt.Errorf("data too short for ptc slot %d", slotIndex)
			}
			d.Ptc[slotIndex] = DecodeIndexList(data[off:off+length], h.IndexWidth)
		}
	}

	return d, nil
}

// EncodeSlotCommittees serializes a single slot's attester committees into a
// self-delimited blob (used by the Pebble backend's per-slot keys):
//
//	[committeeCount:2] then per committee [memberCount:4][packed indices]
func EncodeSlotCommittees(committees [][]uint64) ([]byte, error) {
	w := int(DutiesIndexWidth)
	size := 2
	for _, committee := range committees {
		size += 4 + len(committee)*w
	}

	buf := make([]byte, size)
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(committees)))
	pos := 2
	for _, committee := range committees {
		binary.BigEndian.PutUint32(buf[pos:], uint32(len(committee)))
		pos += 4
		for _, idx := range committee {
			if idx > maxIndexValue {
				return nil, fmt.Errorf("validator index %d exceeds %d-byte range", idx, DutiesIndexWidth)
			}
			putUint48(buf[pos:], idx)
			pos += w
		}
	}
	return buf, nil
}

// DecodeSlotCommittees decodes a blob produced by EncodeSlotCommittees.
func DecodeSlotCommittees(data []byte) ([][]uint64, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("slot committees blob too short")
	}
	w := int(DutiesIndexWidth)
	count := int(binary.BigEndian.Uint16(data[0:2]))
	pos := 2
	committees := make([][]uint64, count)
	for c := range count {
		if pos+4 > len(data) {
			return nil, fmt.Errorf("slot committees blob truncated at committee %d", c)
		}
		memberCount := int(binary.BigEndian.Uint32(data[pos:]))
		pos += 4
		if pos+memberCount*w > len(data) {
			return nil, fmt.Errorf("slot committees blob truncated in committee %d members", c)
		}
		committees[c] = decodeIndexList(data[pos:pos+memberCount*w], DutiesIndexWidth)
		pos += memberCount * w
	}
	return committees, nil
}

// EncodeIndexList serializes a list of validator indices as packed width-byte values.
func EncodeIndexList(indices []uint64) ([]byte, error) {
	w := int(DutiesIndexWidth)
	buf := make([]byte, len(indices)*w)
	for i, idx := range indices {
		if idx > maxIndexValue {
			return nil, fmt.Errorf("validator index %d exceeds %d-byte range", idx, DutiesIndexWidth)
		}
		putUint48(buf[i*w:], idx)
	}
	return buf, nil
}

// EncodeDutiesHeader returns the fixed-size DUTY header bytes for the epoch.
func EncodeDutiesHeader(d *EpochDuties) []byte {
	buf := make([]byte, DutiesHeaderSize)
	writeDutiesHeader(buf, d)
	return buf
}

// writeDutiesHeader writes the fixed 40-byte header into the start of buf.
func writeDutiesHeader(buf []byte, d *EpochDuties) {
	copy(buf[0:4], DutiesMagic[:])
	binary.BigEndian.PutUint16(buf[4:6], DutiesFormatVersion)
	buf[6] = 0 // flags
	buf[7] = DutiesIndexWidth
	binary.BigEndian.PutUint64(buf[8:16], d.Epoch)
	binary.BigEndian.PutUint64(buf[16:24], d.ValidatorCount)
	binary.BigEndian.PutUint32(buf[24:28], uint32(d.SlotsPerEpoch))
	binary.BigEndian.PutUint32(buf[28:32], uint32(d.CommitteesPerSlot))
	binary.BigEndian.PutUint32(buf[32:36], uint32(d.PtcSize))
	// buf[36:40] reserved
}

// DecodeDutiesHeader parses and validates a duties object header.
func DecodeDutiesHeader(b []byte) (*DutiesHeader, error) {
	if len(b) < DutiesHeaderSize {
		return nil, fmt.Errorf("data too short for duties header: %d < %d", len(b), DutiesHeaderSize)
	}
	if b[0] != DutiesMagic[0] || b[1] != DutiesMagic[1] || b[2] != DutiesMagic[2] || b[3] != DutiesMagic[3] {
		return nil, fmt.Errorf("invalid duties magic")
	}

	h := &DutiesHeader{
		Version:           binary.BigEndian.Uint16(b[4:6]),
		Flags:             b[6],
		IndexWidth:        b[7],
		Epoch:             binary.BigEndian.Uint64(b[8:16]),
		ValidatorCount:    binary.BigEndian.Uint64(b[16:24]),
		SlotsPerEpoch:     uint64(binary.BigEndian.Uint32(b[24:28])),
		CommitteesPerSlot: uint64(binary.BigEndian.Uint32(b[28:32])),
		PtcSize:           uint64(binary.BigEndian.Uint32(b[32:36])),
	}
	if h.IndexWidth == 0 || h.IndexWidth > 8 {
		return nil, fmt.Errorf("invalid duties index width: %d", h.IndexWidth)
	}
	return h, nil
}

// attesterOffset returns the byte offset of the attester section.
func (h *DutiesHeader) attesterOffset() int64 {
	return DutiesHeaderSize
}

// ptcOffset returns the byte offset of the PTC section.
func (h *DutiesHeader) ptcOffset() int64 {
	return DutiesHeaderSize + int64(h.ValidatorCount)*int64(h.IndexWidth)
}

// splitOffset mirrors duties.SplitOffset: the start index of chunk `index`
// when listSize items are split into `chunks` contiguous committees. This MUST
// stay identical to indexer/beacon/duties.SplitOffset.
func splitOffset(listSize, chunks, index uint64) uint64 {
	return (listSize * index) / chunks
}

// AttesterSlotRange returns the byte (offset, length) spanning all committees of
// the given slot in the attester section.
func (h *DutiesHeader) AttesterSlotRange(slotIndex uint64) (offset int64, length int64) {
	committeesCount := h.CommitteesPerSlot * h.SlotsPerEpoch
	if committeesCount == 0 {
		return h.attesterOffset(), 0
	}
	start := splitOffset(h.ValidatorCount, committeesCount, slotIndex*h.CommitteesPerSlot)
	end := splitOffset(h.ValidatorCount, committeesCount, (slotIndex+1)*h.CommitteesPerSlot)
	off := h.attesterOffset() + int64(start)*int64(h.IndexWidth)
	return off, int64(end-start) * int64(h.IndexWidth)
}

// PtcSlotRange returns the byte (offset, length) of the PTC members for the slot.
func (h *DutiesHeader) PtcSlotRange(slotIndex uint64) (offset int64, length int64) {
	w := int64(h.IndexWidth)
	return h.ptcOffset() + int64(slotIndex)*int64(h.PtcSize)*w, int64(h.PtcSize) * w
}

// SplitSlotCommittees splits the raw bytes of a slot's attester span (as returned
// by an AttesterSlotRange read) into per-committee global validator index lists.
func (h *DutiesHeader) SplitSlotCommittees(slotIndex uint64, slotBytes []byte) ([][]uint64, error) {
	committeesCount := h.CommitteesPerSlot * h.SlotsPerEpoch
	if committeesCount == 0 {
		return nil, nil
	}
	w := uint64(h.IndexWidth)
	base := splitOffset(h.ValidatorCount, committeesCount, slotIndex*h.CommitteesPerSlot)
	committees := make([][]uint64, h.CommitteesPerSlot)
	for c := uint64(0); c < h.CommitteesPerSlot; c++ {
		start := splitOffset(h.ValidatorCount, committeesCount, slotIndex*h.CommitteesPerSlot+c) - base
		end := splitOffset(h.ValidatorCount, committeesCount, slotIndex*h.CommitteesPerSlot+c+1) - base
		if end*w > uint64(len(slotBytes)) {
			return nil, fmt.Errorf("slot bytes too short: need %d, got %d", end*w, len(slotBytes))
		}
		committees[c] = decodeIndexList(slotBytes[start*w:end*w], h.IndexWidth)
	}
	return committees, nil
}

// DecodeIndexList decodes a tightly packed list of width-byte big-endian indices.
func DecodeIndexList(b []byte, width uint8) []uint64 {
	return decodeIndexList(b, width)
}

func decodeIndexList(b []byte, width uint8) []uint64 {
	w := int(width)
	if w == 0 {
		return nil
	}
	out := make([]uint64, len(b)/w)
	for i := range out {
		out[i] = getUint48(b[i*w:], width)
	}
	return out
}

// putUint48 writes the low DutiesIndexWidth bytes of v as big-endian.
func putUint48(b []byte, v uint64) {
	for i := int(DutiesIndexWidth) - 1; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
}

// getUint48 reads a width-byte big-endian value.
func getUint48(b []byte, width uint8) uint64 {
	var v uint64
	for i := 0; i < int(width); i++ {
		v = (v << 8) | uint64(b[i])
	}
	return v
}
