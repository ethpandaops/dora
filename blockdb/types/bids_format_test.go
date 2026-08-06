package types

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
)

// buildTestSlotBids constructs a SlotBids with the given per-bid observations
// (client table index -> first-seen offset).
func buildTestSlotBids(slot uint64, clients []string, observations []map[int]int32) *SlotBids {
	s := &SlotBids{
		Slot:    slot,
		Clients: clients,
		Bids:    make([]*SlotBidsEntry, 0, len(observations)),
	}
	for i, seen := range observations {
		mask, times := NewSeenObservations(seen, len(clients))
		entry := &SlotBidsEntry{
			Bid: &dbtypes.BlockBid{
				ParentRoot:   bytes.Repeat([]byte{byte(i + 1)}, 32),
				ParentHash:   bytes.Repeat([]byte{byte(i + 2)}, 32),
				BlockHash:    bytes.Repeat([]byte{byte(i + 3)}, 32),
				FeeRecipient: bytes.Repeat([]byte{byte(i + 4)}, 20),
				BuilderIndex: int64(i) - 1, // include the -1 self-built sentinel
				GasLimit:     30_000_000 + uint64(i),
				Value:        1_000_000 + uint64(i),
				ElPayment:    2_000_000 + uint64(i),
				Slot:         slot,
			},
			SeenMask:  mask,
			SeenTimes: times,
		}
		s.Bids = append(s.Bids, entry)
	}
	return s
}

func TestSlotBidsRoundTrip(t *testing.T) {
	tests := []struct {
		name         string
		clients      []string
		observations []map[int]int32
	}{
		{
			name:         "empty object",
			clients:      []string{},
			observations: []map[int]int32{},
		},
		{
			name:         "bid without observations",
			clients:      []string{"client-1", "client-2"},
			observations: []map[int]int32{{}},
		},
		{
			name:    "multiple bids and clients",
			clients: []string{"lighthouse-1", "prysm-1", "teku-1", "nimbus-1", "lodestar-1", "grandine-1", "gossamer-1", "zeam-1", "extra-9"},
			observations: []map[int]int32{
				{0: 120, 1: -350, 8: 4000},
				{2: 0, 3: 65_000},
				{0: 1, 1: 2, 2: 3, 3: 4, 4: 5, 5: 6, 6: 7, 7: 8, 8: 9},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := buildTestSlotBids(12345, tt.clients, tt.observations)

			data, err := EncodeSlotBids(src)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}

			decoded, err := DecodeSlotBids(data)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}

			if decoded.Slot != src.Slot {
				t.Errorf("slot mismatch: %d != %d", decoded.Slot, src.Slot)
			}
			if len(decoded.Clients) != len(src.Clients) {
				t.Fatalf("client count mismatch: %d != %d", len(decoded.Clients), len(src.Clients))
			}
			if len(src.Clients) > 0 && !reflect.DeepEqual(decoded.Clients, src.Clients) {
				t.Errorf("clients mismatch: %v != %v", decoded.Clients, src.Clients)
			}
			if len(decoded.Bids) != len(src.Bids) {
				t.Fatalf("bid count mismatch: %d != %d", len(decoded.Bids), len(src.Bids))
			}
			for i, entry := range decoded.Bids {
				srcEntry := src.Bids[i]
				if !reflect.DeepEqual(entry.SeenByClientIndex(), srcEntry.SeenByClientIndex()) {
					t.Errorf("bid %d observations mismatch: %v != %v", i, entry.SeenByClientIndex(), srcEntry.SeenByClientIndex())
				}

				// All bid fields must round-trip; the decoder derives the seen
				// counters, so align them before comparing.
				wantBid := *srcEntry.Bid
				wantBid.SeenCount = uint32(srcEntry.SeenCount())
				wantBid.SeenTotal = uint32(len(src.Clients))
				if wantBid.SeenCount > wantBid.SeenTotal {
					wantBid.SeenTotal = wantBid.SeenCount
				}
				if !reflect.DeepEqual(*entry.Bid, wantBid) {
					t.Errorf("bid %d fields mismatch: %+v != %+v", i, *entry.Bid, wantBid)
				}
			}
		})
	}
}

func TestSlotBidsMerge(t *testing.T) {
	// Stored object: clients A/B, one bid seen by both.
	stored := buildTestSlotBids(100, []string{"client-a", "client-b"}, []map[int]int32{
		{0: 500, 1: 700},
	})
	// Live object: clients B/C, the same bid (same key tuple: index 0) seen by
	// B (earlier) and C, plus a new bid.
	live := buildTestSlotBids(100, []string{"client-b", "client-c"}, []map[int]int32{
		{0: 300, 1: 900},
		{1: 1200},
	})

	merged := MergeSlotBids(stored, live)

	if !reflect.DeepEqual(merged.Clients, []string{"client-a", "client-b", "client-c"}) {
		t.Fatalf("unexpected merged client table: %v", merged.Clients)
	}
	if len(merged.Bids) != 2 {
		t.Fatalf("unexpected merged bid count: %d", len(merged.Bids))
	}

	// First bid: A@500, B@min(700,300)=300, C@900.
	want := map[int]int32{0: 500, 1: 300, 2: 900}
	if got := merged.Bids[0].SeenByClientIndex(); !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected merged observations: %v != %v", got, want)
	}

	// Second bid only exists in the live object: C@1200 (index 2 in merged table).
	want = map[int]int32{2: 1200}
	if got := merged.Bids[1].SeenByClientIndex(); !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected merged observations for live-only bid: %v != %v", got, want)
	}

	// For bids present in both objects the newer (live) bid fields win.
	if merged.Bids[0].Bid != live.Bids[0].Bid {
		t.Error("merged bid fields should come from the newer object")
	}

	// Nil handling.
	if MergeSlotBids(nil, live) != live || MergeSlotBids(stored, nil) != stored {
		t.Error("nil merge should return the non-nil object")
	}

	// Merged object must round-trip.
	data, err := EncodeSlotBids(merged)
	if err != nil {
		t.Fatalf("encode of merged object failed: %v", err)
	}
	if _, err := DecodeSlotBids(data); err != nil {
		t.Fatalf("decode of merged object failed: %v", err)
	}
}
