package db

import (
	"context"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// TestGetFilteredSlotsProposerNameHistory checks the time-aware proposer name filter:
// with ProposerNameHistory the name must match the assignment valid at each slot,
// without it the current mapping applies to all slots (legacy behavior).
func TestGetFilteredSlotsProposerNameHistory(t *testing.T) {
	newTestDB(t)

	// validator 5 was "old-node" for slots [0, 100), then "new-node"; only the
	// differing interval gets a history row, [100, max) resolves via COALESCE fallback
	insertTestSlot(t, 50, 5)
	insertTestSlot(t, 150, 5)

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		if err := InsertValidatorNames(context.Background(), tx, []*dbtypes.ValidatorName{{Index: 5, Name: "new-node"}}); err != nil {
			return err
		}
		return ReplaceValidatorNameHistory(context.Background(), tx, []*dbtypes.ValidatorNameHistory{
			{Index: 5, StartSlot: 0, EndSlot: 100, Name: "old-node"},
		})
	})
	if err != nil {
		t.Fatalf("insert names: %v", err)
	}

	cases := []struct {
		name      string
		filter    *dbtypes.BlockFilter
		wantSlots []uint64
	}{
		{
			name:      "history-aware match on old name",
			filter:    &dbtypes.BlockFilter{ProposerName: "old-node", ProposerNameHistory: true, WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{50},
		},
		{
			name:      "history-aware match on new name",
			filter:    &dbtypes.BlockFilter{ProposerName: "new-node", ProposerNameHistory: true, WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{150},
		},
		{
			name:      "history-aware inverted match",
			filter:    &dbtypes.BlockFilter{ProposerName: "old-node", InvertProposer: true, ProposerNameHistory: true, WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{150},
		},
		{
			name:      "legacy filter matches current name everywhere",
			filter:    &dbtypes.BlockFilter{ProposerName: "new-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{150, 50},
		},
		{
			name:      "legacy filter finds nothing for old name",
			filter:    &dbtypes.BlockFilter{ProposerName: "old-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{},
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

func insertTestSlot(t *testing.T, slot uint64, proposer uint64) {
	t.Helper()
	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		return InsertSlot(context.Background(), tx, &dbtypes.Slot{
			Slot:       slot,
			Proposer:   proposer,
			Status:     dbtypes.Canonical,
			Root:       []byte{byte(slot), 0x01},
			ParentRoot: []byte{byte(slot), 0x02},
			StateRoot:  []byte{byte(slot), 0x03},
			Graffiti:   []byte{},
			ExecTimes:  []byte{},
			BlockUid:   slot << 16,
		})
	})
	if err != nil {
		t.Fatalf("insert slot %d: %v", slot, err)
	}
}
