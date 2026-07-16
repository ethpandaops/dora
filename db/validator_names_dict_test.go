package db

import (
	"context"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func TestValidatorNameDictEntryIdempotency(t *testing.T) {
	newTestDB(t)

	var firstId, secondId, otherId uint64
	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		var err error
		if firstId, err = InsertValidatorNameDictEntry(context.Background(), tx, "lighthouse-geth-1"); err != nil {
			return err
		}
		if secondId, err = InsertValidatorNameDictEntry(context.Background(), tx, "lighthouse-geth-1"); err != nil {
			return err
		}
		otherId, err = InsertValidatorNameDictEntry(context.Background(), tx, "prysm-besu-2")
		return err
	})
	if err != nil {
		t.Fatalf("insert dict entries: %v", err)
	}

	if firstId == 0 {
		t.Errorf("id 0 is reserved, got %v for first entry", firstId)
	}
	if firstId != secondId {
		t.Errorf("same name got different ids: %v != %v", firstId, secondId)
	}
	if otherId == firstId {
		t.Errorf("different names share id %v", otherId)
	}

	entries, err := GetValidatorNameDictEntries(context.Background())
	if err != nil {
		t.Fatalf("get dict entries: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 dict entries, got %v", len(entries))
	}
}

// TestGetFilteredSlotsStampedNames checks the name filter over stamped rows:
// stamped rows match by dictionary id, unstamped rows fall back to the legacy
// current-name join, and id 0 (resolved, no name) only matches inverted filters.
func TestGetFilteredSlotsStampedNames(t *testing.T) {
	newTestDB(t)

	var oldNodeId, newNodeId uint64
	noNameId := uint64(0)

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		var err error
		if oldNodeId, err = InsertValidatorNameDictEntry(context.Background(), tx, "old-node"); err != nil {
			return err
		}
		if newNodeId, err = InsertValidatorNameDictEntry(context.Background(), tx, "new-node"); err != nil {
			return err
		}
		// current mapping: validator 5 = "new-node" (for the legacy fallback arm)
		if err := InsertValidatorNames(context.Background(), tx, []*dbtypes.ValidatorName{{Index: 5, Name: "new-node"}}); err != nil {
			return err
		}

		// slot 50: stamped with the old assignment
		// slot 150: stamped with the current assignment
		// slot 250: unstamped (repair pending) -> legacy fallback matches current name
		// slot 350: stamped "no name"
		for _, s := range []struct {
			slot   uint64
			nameId *uint64
		}{
			{50, &oldNodeId},
			{150, &newNodeId},
			{250, nil},
			{350, &noNameId},
		} {
			root := []byte{byte(s.slot), 0x01}
			if err := InsertSlot(context.Background(), tx, &dbtypes.Slot{
				Slot: s.slot, Proposer: 5, Status: dbtypes.Canonical,
				Root: root, ParentRoot: root, StateRoot: root,
				Graffiti: []byte{}, ExecTimes: []byte{},
				BlockUid: s.slot << 16, ProposerNameId: s.nameId,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name      string
		filter    *dbtypes.BlockFilter
		wantSlots []uint64
	}{
		{
			name:      "stamped id match",
			filter:    &dbtypes.BlockFilter{ProposerName: "old-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{50},
		},
		{
			name:      "stamped id match plus legacy fallback for unstamped row",
			filter:    &dbtypes.BlockFilter{ProposerName: "new-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{250, 150},
		},
		{
			name:      "no matching ids and no legacy match",
			filter:    &dbtypes.BlockFilter{ProposerName: "unknown-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{},
		},
		{
			name:      "inverted match includes no-name, non-matching stamps and legacy fallback",
			filter:    &dbtypes.BlockFilter{ProposerName: "old-node", InvertProposer: true, WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{350, 250, 150},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			slots := GetFilteredSlots(context.Background(), c.filter, 1000, 0, 100)
			gotSlots := make([]uint64, 0, len(slots))
			for _, slot := range slots {
				gotSlots = append(gotSlots, slot.Slot)
			}
			if len(gotSlots) != len(c.wantSlots) {
				t.Fatalf("got slots %v, want %v", gotSlots, c.wantSlots)
			}
			for i := range gotSlots {
				if gotSlots[i] != c.wantSlots[i] {
					t.Fatalf("got slots %v, want %v", gotSlots, c.wantSlots)
				}
			}
		})
	}
}

func TestSlotNameIdRepairRoundtrip(t *testing.T) {
	newTestDB(t)

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		root := []byte{0x01}
		return InsertSlot(context.Background(), tx, &dbtypes.Slot{
			Slot: 10, Proposer: 7, Status: dbtypes.Canonical,
			Root: root, ParentRoot: root, StateRoot: root,
			Graffiti: []byte{}, ExecTimes: []byte{}, BlockUid: 10 << 16,
		})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	stamps := GetSlotsWithoutNameId(context.Background(), 100)
	if len(stamps) != 1 || stamps[0].Slot != 10 || stamps[0].Proposer != 7 {
		t.Fatalf("unexpected unstamped slots: %+v", stamps)
	}

	nameId := uint64(3)
	stamps[0].ProposerNameId = &nameId
	err = RunDBTransaction(func(tx *sqlx.Tx) error {
		return UpdateSlotNameIds(context.Background(), tx, stamps)
	})
	if err != nil {
		t.Fatalf("update stamps: %v", err)
	}

	if remaining := GetSlotsWithoutNameId(context.Background(), 100); len(remaining) != 0 {
		t.Errorf("expected no unstamped slots after repair, got %v", len(remaining))
	}
}
