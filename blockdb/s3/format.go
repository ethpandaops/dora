package s3

import (
	"encoding/binary"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec"

	"github.com/ethpandaops/dora/blockdb/types"
)

// Object format versions:
// v1: header + body (pre-gloas blocks)
// v2: header + body + payload + bal (gloas+ blocks, payload/BAL introduced in same fork)
//
// Note: Both payload and BAL may be empty (length 0), but body is always required.

// Metadata sizes by version
const (
	metadataSizeV1 = 16 // 4 (version) + 4 (headerLen) + 4 (bodyVer) + 4 (bodyLen)
	metadataSizeV2 = 32 // v1 + 4 (payloadVer) + 4 (payloadLen) + 4 (balVer) + 4 (balLen)

	// Maximum metadata size for initial read
	maxMetadataSize = 64
)

// objectMetadata represents the metadata for all format versions.
type objectMetadata struct {
	ObjVersion uint32

	// Header (always present)
	HeaderLength uint32

	// Body (always required)
	BodyVersion uint32
	BodyLength  uint32

	// Payload (v2+, may be empty)
	PayloadVersion uint32
	PayloadLength  uint32

	// BAL (v2+, may be empty)
	BalVersion uint32
	BalLength  uint32
}

// metadataSize returns the metadata size for this object.
func (m *objectMetadata) metadataSize() int {
	switch m.ObjVersion {
	case 1:
		return metadataSizeV1
	case 2:
		return metadataSizeV2
	default:
		return metadataSizeV2
	}
}

// headerOffset returns the byte offset of the header data.
func (m *objectMetadata) headerOffset() int {
	return m.metadataSize()
}

// bodyOffset returns the byte offset of the body data.
func (m *objectMetadata) bodyOffset() int {
	return m.metadataSize() + int(m.HeaderLength)
}

// payloadOffset returns the byte offset of the payload data.
func (m *objectMetadata) payloadOffset() int {
	return m.metadataSize() + int(m.HeaderLength) + int(m.BodyLength)
}

// balOffset returns the byte offset of the BAL data.
func (m *objectMetadata) balOffset() int {
	return m.metadataSize() + int(m.HeaderLength) + int(m.BodyLength) + int(m.PayloadLength)
}

// storedFlags returns which components are stored in this object.
func (m *objectMetadata) storedFlags() types.BlockDataFlags {
	var flags types.BlockDataFlags

	if m.HeaderLength > 0 {
		flags |= types.BlockDataFlagHeader
	}
	if m.BodyLength > 0 {
		flags |= types.BlockDataFlagBody
	}
	if m.PayloadLength > 0 && m.ObjVersion >= 2 {
		flags |= types.BlockDataFlagPayload
	}
	if m.BalLength > 0 && m.ObjVersion >= 2 {
		flags |= types.BlockDataFlagBal
	}

	return flags
}

// readObjectMetadata reads metadata from any format version.
func readObjectMetadata(data []byte) (*objectMetadata, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("data too short for metadata version")
	}

	version := binary.BigEndian.Uint32(data[:4])
	meta := &objectMetadata{ObjVersion: version}

	switch version {
	case 1:
		if len(data) < metadataSizeV1 {
			return nil, fmt.Errorf("data too short for v1 metadata: need %d, got %d", metadataSizeV1, len(data))
		}
		meta.HeaderLength = binary.BigEndian.Uint32(data[4:8])
		meta.BodyVersion = binary.BigEndian.Uint32(data[8:12])
		meta.BodyLength = binary.BigEndian.Uint32(data[12:16])

	case 2:
		if len(data) < metadataSizeV2 {
			return nil, fmt.Errorf("data too short for v2 metadata: need %d, got %d", metadataSizeV2, len(data))
		}
		meta.HeaderLength = binary.BigEndian.Uint32(data[4:8])
		meta.BodyVersion = binary.BigEndian.Uint32(data[8:12])
		meta.BodyLength = binary.BigEndian.Uint32(data[12:16])
		meta.PayloadVersion = binary.BigEndian.Uint32(data[16:20])
		meta.PayloadLength = binary.BigEndian.Uint32(data[20:24])
		meta.BalVersion = binary.BigEndian.Uint32(data[24:28])
		meta.BalLength = binary.BigEndian.Uint32(data[28:32])

	default:
		return nil, fmt.Errorf("unsupported object version: %d", version)
	}

	return meta, nil
}

// writeObjectMetadata creates metadata bytes for the given BlockData.
// Uses v1 format for pre-gloas blocks, v2 for gloas+ blocks.
func writeObjectMetadata(data *types.BlockData) []byte {
	// Use v2 format only for gloas+ blocks (which can have payload/BAL)
	if data.BodyVersion >= uint64(spec.DataVersionGloas) {
		meta := make([]byte, metadataSizeV2)
		binary.BigEndian.PutUint32(meta[0:4], 2)
		binary.BigEndian.PutUint32(meta[4:8], uint32(len(data.HeaderData)))
		binary.BigEndian.PutUint32(meta[8:12], uint32(data.BodyVersion))
		binary.BigEndian.PutUint32(meta[12:16], uint32(len(data.BodyData)))
		binary.BigEndian.PutUint32(meta[16:20], uint32(data.PayloadVersion))
		binary.BigEndian.PutUint32(meta[20:24], uint32(len(data.PayloadData)))
		binary.BigEndian.PutUint32(meta[24:28], uint32(data.BalVersion))
		binary.BigEndian.PutUint32(meta[28:32], uint32(len(data.BalData)))
		return meta
	}

	// Use v1 format for pre-gloas blocks
	meta := make([]byte, metadataSizeV1)
	binary.BigEndian.PutUint32(meta[0:4], 1)
	binary.BigEndian.PutUint32(meta[4:8], uint32(len(data.HeaderData)))
	binary.BigEndian.PutUint32(meta[8:12], uint32(data.BodyVersion))
	binary.BigEndian.PutUint32(meta[12:16], uint32(len(data.BodyData)))
	return meta
}

// getDataRange calculates the single byte range spanning all requested components.
// Returns (start, end) where end is inclusive. Returns (-1, -1) if no data to fetch.
func (m *objectMetadata) getDataRange(flags types.BlockDataFlags) (int64, int64) {
	var start int64 = -1
	var end int64 = -1

	// Check each component in order (they're stored sequentially)
	if flags.Has(types.BlockDataFlagHeader) && m.HeaderLength > 0 {
		start = int64(m.headerOffset())
		end = start + int64(m.HeaderLength) - 1
	}

	if flags.Has(types.BlockDataFlagBody) && m.BodyLength > 0 {
		bodyStart := int64(m.bodyOffset())
		bodyEnd := bodyStart + int64(m.BodyLength) - 1
		if start < 0 {
			start = bodyStart
		}
		end = bodyEnd
	}

	if flags.Has(types.BlockDataFlagPayload) && m.PayloadLength > 0 && m.ObjVersion >= 2 {
		payloadStart := int64(m.payloadOffset())
		payloadEnd := payloadStart + int64(m.PayloadLength) - 1
		if start < 0 {
			start = payloadStart
		}
		end = payloadEnd
	}

	if flags.Has(types.BlockDataFlagBal) && m.BalLength > 0 && m.ObjVersion >= 2 {
		balStart := int64(m.balOffset())
		balEnd := balStart + int64(m.BalLength) - 1
		if start < 0 {
			start = balStart
		}
		end = balEnd
	}

	return start, end
}
