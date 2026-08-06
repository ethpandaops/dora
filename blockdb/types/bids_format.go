// Package types: per-slot bids object format (BIDS).
//
// One object stores all execution payload bids of a single slot together with
// their gossip observations: which clients saw each bid and when. The object
// is self-sufficient (full bid fields, not just key tuples), so historic bid
// data could be served from the blockdb alone without the relational
// block_bids table. Clients are identified by name via a per-object client
// table, so objects stay decodable across client config changes. Per-bid
// observations are stored as a bitmask over the client table plus one
// first-seen offset (ms from slot start) per set bit.
//
// Object layout:
//
//	HEADER (20 bytes)
//	├── Magic:       [4]byte = "BIDS"
//	├── Version:     uint16
//	├── Flags:       uint8   (reserved, 0)
//	├── Reserved:    uint8
//	├── Slot:        uint64
//	├── ClientCount: uint16
//	└── BidCount:    uint16
//	CLIENT TABLE: per client: uint8 name length + name bytes
//	BID RECORDS: per bid:
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
package types

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/ethpandaops/dora/dbtypes"
)

// BidsMagic identifies a per-slot bids object.
var BidsMagic = [4]byte{'B', 'I', 'D', 'S'}

const (
	// BidsFormatVersion is the current bids object format version.
	BidsFormatVersion uint16 = 1

	// BidsHeaderSize is the fixed header size for version 1.
	BidsHeaderSize = 20

	// bidsRecordFixedSize is the fixed part of a bid record (key tuple + bid
	// fields), before the variable seen mask and times.
	bidsRecordFixedSize = 32 + 32 + 32 + 20 + 8 + 8 + 8 + 8
)

// SlotBids holds all bids of one slot with their gossip observations. It is
// the input to EncodeSlotBids and the result of DecodeSlotBids.
type SlotBids struct {
	Slot    uint64
	Clients []string
	Bids    []*SlotBidsEntry
}

// SlotBidsEntry is one bid with its observations. Bid carries the full bid
// fields; decoded entries derive Bid.Slot from the object and the seen
// counters from the observations (SeenCount = observer count, SeenTotal =
// client table size).
type SlotBidsEntry struct {
	Bid *dbtypes.BlockBid
	// SeenMask bit i is set if the client at table index i observed the bid.
	SeenMask []byte
	// SeenTimes holds one first-seen offset (ms from slot start) per set mask
	// bit, in ascending client index order.
	SeenTimes []int32
}

// Key returns the string key identifying this bid across objects, built from
// the bid's dedup tuple (parent root, parent hash, block hash, builder index).
func (e *SlotBidsEntry) Key() string {
	var buf bytes.Buffer
	buf.Grow(len(e.Bid.ParentRoot) + len(e.Bid.ParentHash) + len(e.Bid.BlockHash) + 8)
	buf.Write(e.Bid.ParentRoot)
	buf.Write(e.Bid.ParentHash)
	buf.Write(e.Bid.BlockHash)
	_ = binary.Write(&buf, binary.BigEndian, e.Bid.BuilderIndex)
	return buf.String()
}

// SeenBitSet returns whether the client at table index i observed the bid.
func (e *SlotBidsEntry) SeenBitSet(i int) bool {
	if i < 0 || i>>3 >= len(e.SeenMask) {
		return false
	}
	return e.SeenMask[i>>3]&(1<<(i&7)) != 0
}

// SeenCount returns the number of clients that observed the bid.
func (e *SlotBidsEntry) SeenCount() int {
	count := 0
	for _, b := range e.SeenMask {
		for ; b != 0; b &= b - 1 {
			count++
		}
	}
	return count
}

// SeenByClientIndex returns a map from client table index to first-seen offset
// (ms from slot start) for all clients that observed the bid.
func (e *SlotBidsEntry) SeenByClientIndex() map[int]int32 {
	seen := make(map[int]int32, len(e.SeenTimes))
	timeIdx := 0
	for i := 0; i < len(e.SeenMask)*8; i++ {
		if !e.SeenBitSet(i) {
			continue
		}
		var t int32
		if timeIdx < len(e.SeenTimes) {
			t = e.SeenTimes[timeIdx]
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

// EncodeSlotBids packs a SlotBids into a single BIDS object.
func EncodeSlotBids(s *SlotBids) ([]byte, error) {
	if len(s.Clients) > 0xffff {
		return nil, fmt.Errorf("too many clients: %d", len(s.Clients))
	}
	if len(s.Bids) > 0xffff {
		return nil, fmt.Errorf("too many bids: %d", len(s.Bids))
	}

	var buf bytes.Buffer
	buf.Write(BidsMagic[:])
	_ = binary.Write(&buf, binary.BigEndian, BidsFormatVersion)
	buf.WriteByte(0) // flags
	buf.WriteByte(0) // reserved
	_ = binary.Write(&buf, binary.BigEndian, s.Slot)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(s.Clients)))
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(s.Bids)))

	for _, name := range s.Clients {
		if len(name) > 0xff {
			name = name[:0xff]
		}
		buf.WriteByte(uint8(len(name)))
		buf.WriteString(name)
	}

	maskLen := (len(s.Clients) + 7) / 8
	for _, entry := range s.Bids {
		bid := entry.Bid
		if len(bid.ParentRoot) != 32 || len(bid.ParentHash) != 32 || len(bid.BlockHash) != 32 {
			return nil, fmt.Errorf("invalid bid key tuple lengths")
		}
		if len(bid.FeeRecipient) != 20 {
			return nil, fmt.Errorf("invalid fee recipient length: %d", len(bid.FeeRecipient))
		}
		if len(entry.SeenMask) != maskLen {
			return nil, fmt.Errorf("invalid seen mask length: %d != %d", len(entry.SeenMask), maskLen)
		}
		if len(entry.SeenTimes) != entry.SeenCount() {
			return nil, fmt.Errorf("seen times count %d != seen count %d", len(entry.SeenTimes), entry.SeenCount())
		}

		buf.Write(bid.ParentRoot)
		buf.Write(bid.ParentHash)
		buf.Write(bid.BlockHash)
		buf.Write(bid.FeeRecipient)
		_ = binary.Write(&buf, binary.BigEndian, bid.BuilderIndex)
		_ = binary.Write(&buf, binary.BigEndian, bid.GasLimit)
		_ = binary.Write(&buf, binary.BigEndian, bid.Value)
		_ = binary.Write(&buf, binary.BigEndian, bid.ElPayment)
		buf.Write(entry.SeenMask)
		for _, t := range entry.SeenTimes {
			_ = binary.Write(&buf, binary.BigEndian, t)
		}
	}

	return buf.Bytes(), nil
}

// DecodeSlotBids decodes a BIDS object.
func DecodeSlotBids(data []byte) (*SlotBids, error) {
	if len(data) < BidsHeaderSize {
		return nil, fmt.Errorf("bids object too short: %d bytes", len(data))
	}
	if !bytes.Equal(data[0:4], BidsMagic[:]) {
		return nil, fmt.Errorf("invalid bids magic")
	}
	if version := binary.BigEndian.Uint16(data[4:6]); version != BidsFormatVersion {
		return nil, fmt.Errorf("unsupported bids version: %d", version)
	}

	s := &SlotBids{
		Slot: binary.BigEndian.Uint64(data[8:16]),
	}
	clientCount := int(binary.BigEndian.Uint16(data[16:18]))
	bidCount := int(binary.BigEndian.Uint16(data[18:20]))

	pos := BidsHeaderSize
	s.Clients = make([]string, 0, clientCount)
	for range clientCount {
		if pos >= len(data) {
			return nil, fmt.Errorf("truncated client table")
		}
		nameLen := int(data[pos])
		pos++
		if pos+nameLen > len(data) {
			return nil, fmt.Errorf("truncated client name")
		}
		s.Clients = append(s.Clients, string(data[pos:pos+nameLen]))
		pos += nameLen
	}

	maskLen := (clientCount + 7) / 8
	s.Bids = make([]*SlotBidsEntry, 0, bidCount)
	for range bidCount {
		if pos+bidsRecordFixedSize+maskLen > len(data) {
			return nil, fmt.Errorf("truncated bid record")
		}
		entry := &SlotBidsEntry{
			Bid: &dbtypes.BlockBid{
				ParentRoot:   bytes.Clone(data[pos : pos+32]),
				ParentHash:   bytes.Clone(data[pos+32 : pos+64]),
				BlockHash:    bytes.Clone(data[pos+64 : pos+96]),
				FeeRecipient: bytes.Clone(data[pos+96 : pos+116]),
				BuilderIndex: int64(binary.BigEndian.Uint64(data[pos+116 : pos+124])),
				GasLimit:     binary.BigEndian.Uint64(data[pos+124 : pos+132]),
				Value:        binary.BigEndian.Uint64(data[pos+132 : pos+140]),
				ElPayment:    binary.BigEndian.Uint64(data[pos+140 : pos+148]),
				Slot:         s.Slot,
			},
			SeenMask: bytes.Clone(data[pos+bidsRecordFixedSize : pos+bidsRecordFixedSize+maskLen]),
		}
		pos += bidsRecordFixedSize + maskLen

		seenCount := entry.SeenCount()
		if pos+4*seenCount > len(data) {
			return nil, fmt.Errorf("truncated bid seen times")
		}
		entry.SeenTimes = make([]int32, 0, seenCount)
		for range seenCount {
			entry.SeenTimes = append(entry.SeenTimes, int32(binary.BigEndian.Uint32(data[pos:pos+4])))
			pos += 4
		}

		entry.Bid.SeenCount = uint32(seenCount)
		entry.Bid.SeenTotal = uint32(clientCount)
		if entry.Bid.SeenCount > entry.Bid.SeenTotal {
			entry.Bid.SeenTotal = entry.Bid.SeenCount
		}

		s.Bids = append(s.Bids, entry)
	}

	return s, nil
}

// MergeSlotBids merges two bids objects for the same slot, unifying their
// client tables and bid lists. Observations keep the earliest first-seen
// offset per client; for bids present in both objects the bid fields of the
// second (newer) object win. Either argument may be nil.
func MergeSlotBids(a, b *SlotBids) *SlotBids {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	merged := &SlotBids{
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

	for _, src := range []*SlotBids{a, b} {
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

			for srcIdx, t := range entry.SeenByClientIndex() {
				if srcIdx >= len(src.Clients) {
					continue
				}
				idx := addClient(src.Clients[srcIdx])
				if existing, ok := obs[idx]; !ok || t < existing {
					obs[idx] = t
				}
			}
		}
	}

	merged.Bids = make([]*SlotBidsEntry, 0, len(bidOrder))
	for _, key := range bidOrder {
		mask, times := NewSeenObservations(seen[key], len(merged.Clients))
		merged.Bids = append(merged.Bids, &SlotBidsEntry{
			Bid:       bids[key],
			SeenMask:  mask,
			SeenTimes: times,
		})
	}

	return merged
}
