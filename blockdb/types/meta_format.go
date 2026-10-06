// Package types: per-slot meta object format.
//
// One object stores the gossip-side meta data of a single slot: all execution
// payload bids of the slot together with their gossip observations (which
// clients saw each bid and when), and the inclusion lists published in the
// slot together with their evaluation (see inclusionlists_format.go). The object is self-sufficient (full bid fields,
// not just key tuples), so historic bid data could be served from the blockdb
// alone without the relational block_bids table. Clients are identified by
// name via a per-object client table, so objects stay decodable across client
// config changes. Observations are stored as a bitmask over the client table
// plus one first-seen offset (ms from slot start) per set bit.
//
// Object layout (version 2):
//
//	HEADER (20 bytes)
//	├── Magic:        [4]byte = "META"
//	├── Version:      uint16
//	├── Flags:        uint8   (reserved, 0)
//	├── Reserved:     uint8
//	├── Slot:         uint64
//	├── SectionCount: uint16
//	└── Reserved:     uint16
//	SECTION DIRECTORY: per section (12 bytes):
//	├── Type:   uint16 (BidsSection* constants)
//	├── Flags:  uint16 (BidsSectionFlag* constants)
//	├── Offset: uint32 (from object start)
//	└── Length: uint32 (stored length)
//	SECTION DATA
//
// Sections are independent, so a reader can load just the ones it needs: the
// directory sits in a fixed-size prefix of the object (MetaPrefixSize, which
// also covers the small sections stored first) and tells where each section
// lives. A decoder skips section types it does not know and keeps their raw
// bytes, so they survive a decode / merge / encode cycle of a build that
// predates them. Records inside a section carry a length prefix, so
// fields can be appended to a record without a new format version; decoders
// ignore trailing record bytes they do not know.
//
// CLIENTS section: uint16 count, per client: uint8 name length + name bytes
// BIDS section: uint16 count, per bid: uint16 record length + record:
//
//	├── ParentRoot:   32 bytes
//	├── ParentHash:   32 bytes
//	├── BlockHash:    32 bytes
//	├── FeeRecipient: 20 bytes
//	├── BuilderIndex: uint64 (two's complement int64)
//	├── GasLimit:     uint64
//	├── Value:        uint64
//	├── ElPayment:    uint64
//	├── SeenMask:     ceil(ClientCount/8) bytes (bit i = client table index i)
//	└── SeenTimes:    int32 per set mask bit, in ascending client index order
//
// Version 1 objects carry the magic "BIDS" and have no section directory: a 20
// byte header (ClientCount and BidCount in place of SectionCount and Reserved)
// is followed by the client table and the bid records without length prefixes.
// They are still decoded.
package types

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/golang/snappy"

	"github.com/ethpandaops/dora/dbtypes"
)

// MetaMagic identifies a per-slot meta object.
var MetaMagic = [4]byte{'M', 'E', 'T', 'A'}

// legacyMetaMagic identifies a version 1 object, which only holds bids.
var legacyMetaMagic = [4]byte{'B', 'I', 'D', 'S'}

const (
	// MetaFormatVersion is the current meta object format version.
	MetaFormatVersion uint16 = 2

	// metaFormatVersionV1 is the unsectioned legacy format version.
	metaFormatVersionV1 uint16 = 1

	// MetaHeaderSize is the fixed header size.
	MetaHeaderSize = 20

	// metaDirectoryEntrySize is the size of one section directory entry.
	metaDirectoryEntrySize = 12

	// bidsRecordFixedSize is the fixed part of a bid record (key tuple + bid
	// fields), before the variable seen mask and times.
	bidsRecordFixedSize = 32 + 32 + 32 + 20 + 8 + 8 + 8 + 8
)

// Section types of a meta object.
const (
	MetaSectionClients            uint16 = 1 // client name table
	MetaSectionBids               uint16 = 2 // bid records
	MetaSectionInclusionTxs       uint16 = 3 // raw inclusion list transactions
	MetaSectionInclusionLists     uint16 = 4 // inclusion list records
	MetaSectionInclusionEvals     uint16 = 5 // inclusion list evaluations per target block
	MetaSectionInclusionCommittee uint16 = 6 // inclusion list committee members
	MetaSectionInclusionTxHashes  uint16 = 7 // inclusion list transaction hash table
)

const (
	// MetaSectionFlagSnappy marks a section whose data is snappy compressed.
	MetaSectionFlagSnappy uint16 = 1

	// metaMaxSectionDecodedBytes bounds the decompressed size of a section.
	metaMaxSectionDecodedBytes = 64 << 20

	// MetaPrefixSize is the size of the object prefix a ranged reader loads
	// first. It covers the header, the section directory and usually the small
	// sections stored first.
	MetaPrefixSize = 4096
)

// SlotMetaFlags selects the parts of a meta object to load. The client table
// is always loaded.
type SlotMetaFlags uint8

const (
	// SlotMetaFlagBids selects the bids with their observations.
	SlotMetaFlagBids SlotMetaFlags = 1 << 0
	// SlotMetaFlagInclusionLists selects the inclusion lists with their
	// committee, observations, transaction hashes and evaluations.
	SlotMetaFlagInclusionLists SlotMetaFlags = 1 << 1
	// SlotMetaFlagInclusionTxs selects the raw inclusion list transactions.
	SlotMetaFlagInclusionTxs SlotMetaFlags = 1 << 2
	// SlotMetaFlagAll selects everything, including sections of unknown type.
	// Objects that are written back must be loaded with it.
	SlotMetaFlagAll SlotMetaFlags = 0xff
)

// wantsSection returns whether the flags select a section of the given type.
func (f SlotMetaFlags) wantsSection(sectionType uint16) bool {
	switch sectionType {
	case MetaSectionClients:
		return true
	case MetaSectionBids:
		return f&SlotMetaFlagBids != 0
	case MetaSectionInclusionCommittee, MetaSectionInclusionLists,
		MetaSectionInclusionTxHashes, MetaSectionInclusionEvals:
		return f&SlotMetaFlagInclusionLists != 0
	case MetaSectionInclusionTxs:
		return f&SlotMetaFlagInclusionTxs != 0
	default:
		return f == SlotMetaFlagAll
	}
}

// SlotMetaSection is a raw section of a meta object. It is used to carry
// sections of unknown type through a decode / merge / encode cycle.
type SlotMetaSection struct {
	Type  uint16
	Flags uint16
	Data  []byte // stored bytes (compressed if the snappy flag is set)
}

// SlotMeta holds all bids of one slot with their gossip observations and the
// inclusion lists published in the slot. It is the input to EncodeSlotMeta and
// the result of DecodeSlotMeta.
type SlotMeta struct {
	Slot    uint64
	Clients []string
	Bids    []*SlotMetaBid

	// InclusionLists holds the inclusion lists published in this slot and
	// their evaluations against the blocks of the following slot. Nil if the
	// slot has none.
	InclusionLists *SlotInclusionLists

	// Extra holds sections of a type this build does not know. They are
	// re-emitted unchanged on encode.
	Extra []*SlotMetaSection
}

// SlotMetaBid is one bid with its observations. Bid carries the full bid
// fields; decoded entries derive Bid.Slot from the object and the seen
// counters from the observations (SeenCount = observer count, SeenTotal =
// client table size).
type SlotMetaBid struct {
	Bid *dbtypes.BlockBid
	// SeenMask bit i is set if the client at table index i observed the bid.
	SeenMask []byte
	// SeenTimes holds one first-seen offset (ms from slot start) per set mask
	// bit, in ascending client index order.
	SeenTimes []int32
}

// Key returns the string key identifying this bid across objects, built from
// the bid's dedup tuple (parent root, parent hash, block hash, builder index).
func (e *SlotMetaBid) Key() string {
	var buf bytes.Buffer
	buf.Grow(len(e.Bid.ParentRoot) + len(e.Bid.ParentHash) + len(e.Bid.BlockHash) + 8)
	buf.Write(e.Bid.ParentRoot)
	buf.Write(e.Bid.ParentHash)
	buf.Write(e.Bid.BlockHash)
	_ = binary.Write(&buf, binary.BigEndian, e.Bid.BuilderIndex)
	return buf.String()
}

// SeenBitSet returns whether the client at table index i observed the bid.
func (e *SlotMetaBid) SeenBitSet(i int) bool {
	return seenBitSet(e.SeenMask, i)
}

// SeenCount returns the number of clients that observed the bid.
func (e *SlotMetaBid) SeenCount() int {
	return seenCount(e.SeenMask)
}

// SeenByClientIndex returns a map from client table index to first-seen offset
// (ms from slot start) for all clients that observed the bid.
func (e *SlotMetaBid) SeenByClientIndex() map[int]int32 {
	return seenByClientIndex(e.SeenMask, e.SeenTimes)
}

// seenBitSet returns whether bit i is set in a seen mask.
func seenBitSet(mask []byte, i int) bool {
	if i < 0 || i>>3 >= len(mask) {
		return false
	}
	return mask[i>>3]&(1<<(i&7)) != 0
}

// seenCount returns the number of set bits in a seen mask.
func seenCount(mask []byte) int {
	count := 0
	for _, b := range mask {
		for ; b != 0; b &= b - 1 {
			count++
		}
	}
	return count
}

// seenByClientIndex converts a seen mask and its times to a map from client
// table index to first-seen offset.
func seenByClientIndex(mask []byte, times []int32) map[int]int32 {
	seen := make(map[int]int32, len(times))
	timeIdx := 0
	for i := 0; i < len(mask)*8; i++ {
		if !seenBitSet(mask, i) {
			continue
		}
		var t int32
		if timeIdx < len(times) {
			t = times[timeIdx]
		}
		seen[i] = t
		timeIdx++
	}
	return seen
}

// NewSeenObservations builds SeenMask/SeenTimes from a map of client table
// index to first-seen offset, for a client table of the given size.
func NewSeenObservations(seen map[int]int32, clientCount int) (mask []byte, times []int32) {
	mask = make([]byte, (clientCount+7)/8)
	times = make([]int32, 0, len(seen))
	for i := range clientCount {
		t, ok := seen[i]
		if !ok {
			continue
		}
		mask[i>>3] |= 1 << (i & 7)
		times = append(times, t)
	}
	return mask, times
}

// appendSeen appends a seen mask and its times to buf after validating them
// against the client table size.
func appendSeen(buf *bytes.Buffer, mask []byte, times []int32, maskLen int) error {
	if len(mask) != maskLen {
		return fmt.Errorf("invalid seen mask length: %d != %d", len(mask), maskLen)
	}
	if len(times) != seenCount(mask) {
		return fmt.Errorf("seen times count %d != seen count %d", len(times), seenCount(mask))
	}

	buf.Write(mask)
	for _, t := range times {
		_ = binary.Write(buf, binary.BigEndian, t)
	}

	return nil
}

// readSeen reads a seen mask and its times from data at pos. Returns the new
// position.
func readSeen(data []byte, pos int, maskLen int) (mask []byte, times []int32, newPos int, err error) {
	if pos+maskLen > len(data) {
		return nil, nil, pos, fmt.Errorf("truncated seen mask")
	}
	mask = bytes.Clone(data[pos : pos+maskLen])
	pos += maskLen

	count := seenCount(mask)
	if pos+4*count > len(data) {
		return nil, nil, pos, fmt.Errorf("truncated seen times")
	}
	times = make([]int32, 0, count)
	for range count {
		times = append(times, int32(binary.BigEndian.Uint32(data[pos:pos+4])))
		pos += 4
	}

	return mask, times, pos, nil
}

// EncodeSlotMeta packs a SlotMeta into a single meta object.
func EncodeSlotMeta(s *SlotMeta) ([]byte, error) {
	if len(s.Clients) > 0xffff {
		return nil, fmt.Errorf("too many clients: %d", len(s.Clients))
	}
	if len(s.Bids) > 0xffff {
		return nil, fmt.Errorf("too many bids: %d", len(s.Bids))
	}

	sections := make([]*SlotMetaSection, 0, 7+len(s.Extra))
	sections = append(sections, &SlotMetaSection{
		Type: MetaSectionClients,
		Data: encodeClientsSection(s.Clients),
	})

	bidsData, err := encodeBidsSection(s)
	if err != nil {
		return nil, err
	}
	sections = append(sections, &SlotMetaSection{Type: MetaSectionBids, Data: bidsData})

	// The large raw transaction section goes last, so the sections before it
	// stay within reach of a prefix read.
	var txSection *SlotMetaSection
	if s.InclusionLists != nil {
		var ilSections []*SlotMetaSection
		ilSections, txSection, err = encodeInclusionListSections(s.InclusionLists, len(s.Clients))
		if err != nil {
			return nil, err
		}
		sections = append(sections, ilSections...)
	}

	sections = append(sections, s.Extra...)
	if txSection != nil {
		sections = append(sections, txSection)
	}
	if len(sections) > 0xffff {
		return nil, fmt.Errorf("too many sections: %d", len(sections))
	}

	dataOffset := MetaHeaderSize + metaDirectoryEntrySize*len(sections)
	totalSize := dataOffset
	for _, section := range sections {
		totalSize += len(section.Data)
	}
	if totalSize > 0xffffffff {
		return nil, fmt.Errorf("meta object too large: %d bytes", totalSize)
	}

	buf := bytes.NewBuffer(make([]byte, 0, totalSize))
	buf.Write(MetaMagic[:])
	_ = binary.Write(buf, binary.BigEndian, MetaFormatVersion)
	buf.WriteByte(0) // flags
	buf.WriteByte(0) // reserved
	_ = binary.Write(buf, binary.BigEndian, s.Slot)
	_ = binary.Write(buf, binary.BigEndian, uint16(len(sections)))
	_ = binary.Write(buf, binary.BigEndian, uint16(0)) // reserved

	offset := dataOffset
	for _, section := range sections {
		_ = binary.Write(buf, binary.BigEndian, section.Type)
		_ = binary.Write(buf, binary.BigEndian, section.Flags)
		_ = binary.Write(buf, binary.BigEndian, uint32(offset))
		_ = binary.Write(buf, binary.BigEndian, uint32(len(section.Data)))
		offset += len(section.Data)
	}
	for _, section := range sections {
		buf.Write(section.Data)
	}

	return buf.Bytes(), nil
}

// encodeClientsSection encodes the client name table.
func encodeClientsSection(clients []string) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(clients)))
	for _, name := range clients {
		if len(name) > 0xff {
			name = name[:0xff]
		}
		buf.WriteByte(uint8(len(name)))
		buf.WriteString(name)
	}
	return buf.Bytes()
}

// encodeBidsSection encodes the bid records of an object.
func encodeBidsSection(s *SlotMeta) ([]byte, error) {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(s.Bids)))

	maskLen := (len(s.Clients) + 7) / 8
	var record bytes.Buffer
	for _, entry := range s.Bids {
		record.Reset()
		if err := appendBidRecord(&record, entry, maskLen); err != nil {
			return nil, err
		}
		if record.Len() > 0xffff {
			return nil, fmt.Errorf("bid record too large: %d bytes", record.Len())
		}
		_ = binary.Write(&buf, binary.BigEndian, uint16(record.Len()))
		buf.Write(record.Bytes())
	}

	return buf.Bytes(), nil
}

// appendBidRecord appends one bid record (without length prefix) to buf.
func appendBidRecord(buf *bytes.Buffer, entry *SlotMetaBid, maskLen int) error {
	bid := entry.Bid
	if len(bid.ParentRoot) != 32 || len(bid.ParentHash) != 32 || len(bid.BlockHash) != 32 {
		return fmt.Errorf("invalid bid key tuple lengths")
	}
	if len(bid.FeeRecipient) != 20 {
		return fmt.Errorf("invalid fee recipient length: %d", len(bid.FeeRecipient))
	}

	buf.Write(bid.ParentRoot)
	buf.Write(bid.ParentHash)
	buf.Write(bid.BlockHash)
	buf.Write(bid.FeeRecipient)
	_ = binary.Write(buf, binary.BigEndian, bid.BuilderIndex)
	_ = binary.Write(buf, binary.BigEndian, bid.GasLimit)
	_ = binary.Write(buf, binary.BigEndian, bid.Value)
	_ = binary.Write(buf, binary.BigEndian, bid.ElPayment)

	return appendSeen(buf, entry.SeenMask, entry.SeenTimes, maskLen)
}

// DecodeSlotMeta decodes a complete meta object of any supported version.
func DecodeSlotMeta(data []byte) (*SlotMeta, error) {
	return DecodeSlotMetaSections(data, SlotMetaFlagAll)
}

// DecodeSlotMetaSections decodes the selected parts of a complete meta object.
func DecodeSlotMetaSections(data []byte, flags SlotMetaFlags) (*SlotMeta, error) {
	return ReadSlotMeta(flags, func(offset int64, length int64) ([]byte, error) {
		if offset >= int64(len(data)) {
			return []byte{}, nil
		}
		end := int64(len(data))
		if length > 0 && offset+length < end {
			end = offset + length
		}
		return data[offset:end], nil
	})
}

// ReadSlotMeta loads the selected parts of a meta object through a ranged
// reader, so sections that are not selected are never fetched. read returns
// up to length bytes of the object starting at offset (fewer at the object
// end, everything up to the end if length is 0) and nil if the object does not
// exist. Returns nil, nil if the object does not exist.
func ReadSlotMeta(flags SlotMetaFlags, read func(offset int64, length int64) ([]byte, error)) (*SlotMeta, error) {
	prefix, err := read(0, MetaPrefixSize)
	if err != nil {
		return nil, err
	}
	if prefix == nil {
		return nil, nil
	}
	if len(prefix) < MetaHeaderSize {
		return nil, fmt.Errorf("meta object too short: %d bytes", len(prefix))
	}
	if !bytes.Equal(prefix[0:4], MetaMagic[:]) && !bytes.Equal(prefix[0:4], legacyMetaMagic[:]) {
		return nil, fmt.Errorf("invalid slot meta magic")
	}

	// A prefix shorter than requested is the whole object.
	complete := len(prefix) < MetaPrefixSize

	switch version := binary.BigEndian.Uint16(prefix[4:6]); version {
	case metaFormatVersionV1:
		// Version 1 objects have no directory and are decoded as a whole.
		if !complete {
			if prefix, err = read(0, 0); err != nil {
				return nil, err
			}
		}
		return decodeSlotMetaV1(prefix, flags)
	case MetaFormatVersion:
	default:
		return nil, fmt.Errorf("unsupported slot meta version: %d", version)
	}

	sectionCount := int(binary.BigEndian.Uint16(prefix[16:18]))
	directoryEnd := MetaHeaderSize + metaDirectoryEntrySize*sectionCount
	if directoryEnd > len(prefix) {
		if complete {
			return nil, fmt.Errorf("truncated section directory")
		}
		if prefix, err = read(0, int64(directoryEnd)); err != nil {
			return nil, err
		}
		if directoryEnd > len(prefix) {
			return nil, fmt.Errorf("truncated section directory")
		}
	}

	// Collect the selected sections and the byte range of those the prefix
	// does not cover.
	type location struct {
		section *SlotMetaSection
		offset  int
		length  int
	}
	locations := make([]*location, 0, sectionCount)
	rangeStart, rangeEnd := -1, -1
	for i := range sectionCount {
		entry := prefix[MetaHeaderSize+metaDirectoryEntrySize*i:]
		loc := &location{
			section: &SlotMetaSection{
				Type:  binary.BigEndian.Uint16(entry[0:2]),
				Flags: binary.BigEndian.Uint16(entry[2:4]),
			},
			offset: int(binary.BigEndian.Uint32(entry[4:8])),
			length: int(binary.BigEndian.Uint32(entry[8:12])),
		}
		if !flags.wantsSection(loc.section.Type) {
			continue
		}
		locations = append(locations, loc)

		if loc.offset+loc.length <= len(prefix) {
			continue
		}
		if complete {
			return nil, fmt.Errorf("section %d exceeds object size", i)
		}
		if rangeStart < 0 || loc.offset < rangeStart {
			rangeStart = loc.offset
		}
		if loc.offset+loc.length > rangeEnd {
			rangeEnd = loc.offset + loc.length
		}
	}

	var rangeData []byte
	if rangeStart >= 0 {
		if rangeData, err = read(int64(rangeStart), int64(rangeEnd-rangeStart)); err != nil {
			return nil, err
		}
		if len(rangeData) < rangeEnd-rangeStart {
			return nil, fmt.Errorf("truncated meta object: range %d-%d", rangeStart, rangeEnd)
		}
	}

	sections := make([]*SlotMetaSection, 0, len(locations))
	for _, loc := range locations {
		if loc.offset+loc.length <= len(prefix) {
			loc.section.Data = prefix[loc.offset : loc.offset+loc.length]
		} else {
			loc.section.Data = rangeData[loc.offset-rangeStart : loc.offset-rangeStart+loc.length]
		}
		sections = append(sections, loc.section)
	}

	return decodeSlotMetaSections(binary.BigEndian.Uint64(prefix[8:16]), sections)
}

// decodeSlotMetaV1 decodes the selected parts of an unsectioned version 1 object.
func decodeSlotMetaV1(data []byte, flags SlotMetaFlags) (*SlotMeta, error) {
	if len(data) < MetaHeaderSize {
		return nil, fmt.Errorf("meta object too short: %d bytes", len(data))
	}

	s := &SlotMeta{
		Slot: binary.BigEndian.Uint64(data[8:16]),
		Bids: []*SlotMetaBid{},
	}
	clientCount := int(binary.BigEndian.Uint16(data[16:18]))
	bidCount := int(binary.BigEndian.Uint16(data[18:20]))

	clients, pos, err := readClientTable(data, MetaHeaderSize, clientCount)
	if err != nil {
		return nil, err
	}
	s.Clients = clients

	if !flags.wantsSection(MetaSectionBids) {
		return s, nil
	}

	maskLen := (clientCount + 7) / 8
	s.Bids = make([]*SlotMetaBid, 0, bidCount)
	for range bidCount {
		entry, newPos, err := readBidRecord(data, pos, s.Slot, clientCount, maskLen)
		if err != nil {
			return nil, err
		}
		pos = newPos
		s.Bids = append(s.Bids, entry)
	}

	return s, nil
}

// decodeSlotMetaSections decodes the given sections of a version 2 object.
func decodeSlotMetaSections(slot uint64, sections []*SlotMetaSection) (*SlotMeta, error) {
	s := &SlotMeta{
		Slot: slot,
	}

	// The client table sizes the seen masks of all other sections, so it is
	// decoded first.
	for _, section := range sections {
		if section.Type != MetaSectionClients {
			continue
		}
		payload, err := section.payload()
		if err != nil {
			return nil, err
		}
		if len(payload) < 2 {
			return nil, fmt.Errorf("truncated clients section")
		}
		clients, _, err := readClientTable(payload, 2, int(binary.BigEndian.Uint16(payload[0:2])))
		if err != nil {
			return nil, err
		}
		s.Clients = clients
	}
	if s.Clients == nil {
		s.Clients = []string{}
	}

	clientCount := len(s.Clients)
	maskLen := (clientCount + 7) / 8
	s.Bids = []*SlotMetaBid{}

	var ilSections []*SlotMetaSection
	for _, section := range sections {
		switch section.Type {
		case MetaSectionClients:
		case MetaSectionBids:
			payload, err := section.payload()
			if err != nil {
				return nil, err
			}
			bids, err := decodeBidsSection(payload, s.Slot, clientCount, maskLen)
			if err != nil {
				return nil, err
			}
			s.Bids = bids
		case MetaSectionInclusionCommittee, MetaSectionInclusionLists,
			MetaSectionInclusionTxHashes, MetaSectionInclusionEvals, MetaSectionInclusionTxs:
			ilSections = append(ilSections, section)
		default:
			s.Extra = append(s.Extra, &SlotMetaSection{
				Type:  section.Type,
				Flags: section.Flags,
				Data:  bytes.Clone(section.Data),
			})
		}
	}

	if len(ilSections) > 0 {
		lists, err := decodeInclusionListSections(ilSections, maskLen)
		if err != nil {
			return nil, err
		}
		s.InclusionLists = lists
	}

	return s, nil
}

// payload returns the section data, decompressed if the snappy flag is set.
func (section *SlotMetaSection) payload() ([]byte, error) {
	if section.Flags&MetaSectionFlagSnappy == 0 {
		return section.Data, nil
	}

	decodedLen, err := snappy.DecodedLen(section.Data)
	if err != nil {
		return nil, fmt.Errorf("invalid compressed section %d: %w", section.Type, err)
	}
	if decodedLen > metaMaxSectionDecodedBytes {
		return nil, fmt.Errorf("compressed section %d too large: %d bytes", section.Type, decodedLen)
	}

	payload, err := snappy.Decode(nil, section.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress section %d: %w", section.Type, err)
	}

	return payload, nil
}

// readClientTable reads clientCount client names from data at pos. Returns the
// new position.
func readClientTable(data []byte, pos int, clientCount int) ([]string, int, error) {
	clients := make([]string, 0, clientCount)
	for range clientCount {
		if pos >= len(data) {
			return nil, pos, fmt.Errorf("truncated client table")
		}
		nameLen := int(data[pos])
		pos++
		if pos+nameLen > len(data) {
			return nil, pos, fmt.Errorf("truncated client name")
		}
		clients = append(clients, string(data[pos:pos+nameLen]))
		pos += nameLen
	}
	return clients, pos, nil
}

// decodeBidsSection decodes the length-prefixed bid records of a section.
func decodeBidsSection(data []byte, slot uint64, clientCount int, maskLen int) ([]*SlotMetaBid, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("truncated bids section")
	}
	bidCount := int(binary.BigEndian.Uint16(data[0:2]))
	pos := 2

	bids := make([]*SlotMetaBid, 0, bidCount)
	for range bidCount {
		if pos+2 > len(data) {
			return nil, fmt.Errorf("truncated bid record length")
		}
		recordLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if pos+recordLen > len(data) {
			return nil, fmt.Errorf("truncated bid record")
		}

		entry, _, err := readBidRecord(data[pos:pos+recordLen], 0, slot, clientCount, maskLen)
		if err != nil {
			return nil, err
		}
		pos += recordLen
		bids = append(bids, entry)
	}

	return bids, nil
}

// readBidRecord reads one bid record from data at pos. Returns the new position.
func readBidRecord(data []byte, pos int, slot uint64, clientCount int, maskLen int) (*SlotMetaBid, int, error) {
	if pos+bidsRecordFixedSize > len(data) {
		return nil, pos, fmt.Errorf("truncated bid record")
	}
	entry := &SlotMetaBid{
		Bid: &dbtypes.BlockBid{
			ParentRoot:   bytes.Clone(data[pos : pos+32]),
			ParentHash:   bytes.Clone(data[pos+32 : pos+64]),
			BlockHash:    bytes.Clone(data[pos+64 : pos+96]),
			FeeRecipient: bytes.Clone(data[pos+96 : pos+116]),
			BuilderIndex: int64(binary.BigEndian.Uint64(data[pos+116 : pos+124])),
			GasLimit:     binary.BigEndian.Uint64(data[pos+124 : pos+132]),
			Value:        binary.BigEndian.Uint64(data[pos+132 : pos+140]),
			ElPayment:    binary.BigEndian.Uint64(data[pos+140 : pos+148]),
			Slot:         slot,
		},
	}

	mask, times, pos, err := readSeen(data, pos+bidsRecordFixedSize, maskLen)
	if err != nil {
		return nil, pos, fmt.Errorf("bid record: %w", err)
	}
	entry.SeenMask = mask
	entry.SeenTimes = times

	entry.Bid.SeenCount = uint32(len(times))
	entry.Bid.SeenTotal = uint32(clientCount)
	if entry.Bid.SeenCount > entry.Bid.SeenTotal {
		entry.Bid.SeenTotal = entry.Bid.SeenCount
	}

	return entry, pos, nil
}

// MergeSlotMeta merges two meta objects for the same slot, unifying their
// client tables, bid lists and inclusion lists. Observations keep the earliest
// first-seen offset per client; for bids present in both objects the bid
// fields of the second (newer) object win. Sections of unknown type are kept,
// those of the newer object replacing same-typed ones of the older. Either
// argument may be nil.
func MergeSlotMeta(a, b *SlotMeta) *SlotMeta {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	merged := &SlotMeta{
		Slot:    a.Slot,
		Clients: make([]string, 0, len(a.Clients)+len(b.Clients)),
	}
	clientIdx := make(map[string]int, len(a.Clients)+len(b.Clients))
	addClient := func(name string) int {
		if idx, ok := clientIdx[name]; ok {
			return idx
		}
		idx := len(merged.Clients)
		merged.Clients = append(merged.Clients, name)
		clientIdx[name] = idx
		return idx
	}

	// Register all clients from both tables up front so clients that observed
	// nothing stay in the table (they are the "not seen" denominator).
	for _, name := range a.Clients {
		addClient(name)
	}
	for _, name := range b.Clients {
		addClient(name)
	}

	// seen maps bid key -> merged client table index -> earliest offset.
	seen := make(map[string]map[int]int32, len(a.Bids)+len(b.Bids))
	bids := make(map[string]*dbtypes.BlockBid, len(a.Bids)+len(b.Bids))
	bidOrder := make([]string, 0, len(a.Bids)+len(b.Bids))

	for _, src := range []*SlotMeta{a, b} {
		for _, entry := range src.Bids {
			key := entry.Key()
			obs, exists := seen[key]
			if !exists {
				obs = make(map[int]int32, entry.SeenCount())
				seen[key] = obs
				bidOrder = append(bidOrder, key)
			}
			// Later sources overwrite the bid fields (b is the newer object).
			bids[key] = entry.Bid

			mergeSeen(obs, entry.SeenByClientIndex(), src.Clients, clientIdx)
		}
	}

	merged.Bids = make([]*SlotMetaBid, 0, len(bidOrder))
	for _, key := range bidOrder {
		mask, times := NewSeenObservations(seen[key], len(merged.Clients))
		merged.Bids = append(merged.Bids, &SlotMetaBid{
			Bid:       bids[key],
			SeenMask:  mask,
			SeenTimes: times,
		})
	}

	merged.InclusionLists = mergeSlotInclusionLists(
		a.InclusionLists, a.Clients, b.InclusionLists, b.Clients, clientIdx, len(merged.Clients),
	)

	extraIdx := make(map[uint16]int, len(a.Extra)+len(b.Extra))
	for _, src := range []*SlotMeta{a, b} {
		for _, section := range src.Extra {
			if idx, ok := extraIdx[section.Type]; ok {
				merged.Extra[idx] = section
				continue
			}
			extraIdx[section.Type] = len(merged.Extra)
			merged.Extra = append(merged.Extra, section)
		}
	}

	return merged
}

// mergeSeen merges the observations of a source object (indexed by its client
// table) into dst (indexed by the merged client table), keeping the earliest
// offset per client.
func mergeSeen(dst map[int]int32, src map[int]int32, srcClients []string, clientIdx map[string]int) {
	for srcIdx, t := range src {
		if srcIdx >= len(srcClients) {
			continue
		}
		idx, ok := clientIdx[srcClients[srcIdx]]
		if !ok {
			continue
		}
		if existing, ok := dst[idx]; !ok || t < existing {
			dst[idx] = t
		}
	}
}
