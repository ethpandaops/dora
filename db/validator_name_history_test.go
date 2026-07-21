package db

import (
	"context"
	"math"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// seedNameHistory writes two snapshot periods:
// slots [0, 100) / time [1000, 2200): validators 0-9 = "old-node"
// slots [100, max) / time [2200, max): validators 0-9 = "new-node"
func seedNameHistory(t *testing.T) {
	t.Helper()

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		if err := UpsertValidatorNameSnapshot(context.Background(), tx, &dbtypes.ValidatorNameSnapshot{
			StartSlot: 0, EndSlot: 100, StartTime: 1000, EndTime: 2200, RangesHash: "hash-a",
		}, []*dbtypes.ValidatorNameRange{
			{SnapshotSlot: 0, StartIndex: 0, EndIndex: 9, Name: "old-node"},
		}); err != nil {
			return err
		}
		return UpsertValidatorNameSnapshot(context.Background(), tx, &dbtypes.ValidatorNameSnapshot{
			StartSlot: 100, EndSlot: uint64(math.MaxInt64), StartTime: 2200, EndTime: uint64(math.MaxInt64), RangesHash: "hash-b",
		}, []*dbtypes.ValidatorNameRange{
			{SnapshotSlot: 100, StartIndex: 0, EndIndex: 9, Name: "new-node"},
		})
	})
	if err != nil {
		t.Fatalf("seed name history: %v", err)
	}
}

func TestValidatorNameSnapshotRoundtrip(t *testing.T) {
	newTestDB(t)
	seedNameHistory(t)

	snapshots, err := GetValidatorNameSnapshots(context.Background())
	if err != nil {
		t.Fatalf("get snapshots: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %v", len(snapshots))
	}
	if snapshots[0].StartSlot != 0 || snapshots[0].EndSlot != 100 || snapshots[0].RangesHash != "hash-a" {
		t.Errorf("unexpected snapshot 0: %+v", snapshots[0])
	}
	if snapshots[1].StartSlot != 100 || snapshots[1].EndSlot != uint64(math.MaxInt64) {
		t.Errorf("unexpected snapshot 1: %+v", snapshots[1])
	}

	// amend snapshot 0: ranges are fully rewritten
	err = RunDBTransaction(func(tx *sqlx.Tx) error {
		return UpsertValidatorNameSnapshot(context.Background(), tx, &dbtypes.ValidatorNameSnapshot{
			StartSlot: 0, EndSlot: 100, StartTime: 1000, EndTime: 2200, RangesHash: "hash-a2",
		}, []*dbtypes.ValidatorNameRange{
			{SnapshotSlot: 0, StartIndex: 5, EndIndex: 9, Name: "amended-node"},
		})
	})
	if err != nil {
		t.Fatalf("amend snapshot: %v", err)
	}

	var rangeCount int
	if err := ReaderDb.Get(&rangeCount, `SELECT COUNT(*) FROM validator_name_ranges WHERE snapshot_slot = 0`); err != nil {
		t.Fatalf("count ranges: %v", err)
	}
	if rangeCount != 1 {
		t.Errorf("expected 1 range after amendment, got %v", rangeCount)
	}

	// delete snapshot 1: meta and ranges are removed
	err = RunDBTransaction(func(tx *sqlx.Tx) error {
		return DeleteValidatorNameSnapshot(context.Background(), tx, 100)
	})
	if err != nil {
		t.Fatalf("delete snapshot: %v", err)
	}
	snapshots, err = GetValidatorNameSnapshots(context.Background())
	if err != nil {
		t.Fatalf("get snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot after delete, got %v", len(snapshots))
	}
	if err := ReaderDb.Get(&rangeCount, `SELECT COUNT(*) FROM validator_name_ranges WHERE snapshot_slot = 100`); err != nil {
		t.Fatalf("count ranges: %v", err)
	}
	if rangeCount != 0 {
		t.Errorf("expected 0 ranges for deleted snapshot, got %v", rangeCount)
	}
}

// TestGetFilteredSlotsHistoryNames checks the history-aware proposer name filter:
// rows covered by a history range match the name valid at the row's slot, uncovered
// rows fall back to the legacy current-name join, and inverted filters include
// unnamed and non-matching rows.
func TestGetFilteredSlotsHistoryNames(t *testing.T) {
	newTestDB(t)
	seedNameHistory(t)

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		// current names: validator 20 = "legacy-node" (uncovered by history)
		if err := InsertValidatorNames(context.Background(), tx, []*dbtypes.ValidatorName{
			{Index: 20, Name: "legacy-node"},
		}); err != nil {
			return err
		}

		// slot 50: proposer 5 covered by history -> "old-node"
		// slot 150: proposer 5 covered by history -> "new-node"
		// slot 250: proposer 20 uncovered -> legacy "legacy-node"
		// slot 350: proposer 30 uncovered and unnamed
		for _, s := range []struct {
			slot     uint64
			proposer uint64
		}{
			{50, 5},
			{150, 5},
			{250, 20},
			{350, 30},
		} {
			root := []byte{byte(s.slot), 0x01}
			if err := InsertSlot(context.Background(), tx, &dbtypes.Slot{
				Slot: s.slot, Proposer: s.proposer, Status: dbtypes.Canonical,
				Root: root, ParentRoot: root, StateRoot: root,
				Graffiti: []byte{}, ExecTimes: []byte{},
				BlockUid: s.slot << 16,
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
			name:      "history match at old assignment",
			filter:    &dbtypes.BlockFilter{ProposerName: "old-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{50},
		},
		{
			name:      "history match at current assignment",
			filter:    &dbtypes.BlockFilter{ProposerName: "new-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{150},
		},
		{
			name:      "legacy fallback for uncovered proposer",
			filter:    &dbtypes.BlockFilter{ProposerName: "legacy-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{250},
		},
		{
			name:      "no match",
			filter:    &dbtypes.BlockFilter{ProposerName: "unknown-node", WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{},
		},
		{
			name:      "inverted match includes unnamed, legacy and non-matching history rows",
			filter:    &dbtypes.BlockFilter{ProposerName: "old-node", InvertProposer: true, WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{350, 250, 150},
		},
		{
			name:      "inverted match against current assignment keeps old assignment rows",
			filter:    &dbtypes.BlockFilter{ProposerName: "new-node", InvertProposer: true, WithMissing: 1, WithOrphaned: 1},
			wantSlots: []uint64{350, 250, 50},
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

// TestSearchValidatorNames checks that search matches historic names and that name
// suggestion counts are time-scoped: covered slots count towards the name valid at the
// slot, uncovered slots count towards the proposer's current name, without double
// counting validators whose current name mirrors the open history snapshot.
func TestSearchValidatorNames(t *testing.T) {
	newTestDB(t)
	seedNameHistory(t)

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		// current names: validator 5 mirrors the open history snapshot,
		// validator 20 is uncovered by history
		if err := InsertValidatorNames(context.Background(), tx, []*dbtypes.ValidatorName{
			{Index: 5, Name: "new-node"},
			{Index: 20, Name: "legacy-node"},
		}); err != nil {
			return err
		}

		for _, s := range []struct {
			slot     uint64
			proposer uint64
		}{
			{50, 5},   // covered -> "old-node"
			{150, 5},  // covered -> "new-node"
			{250, 20}, // uncovered -> "legacy-node"
		} {
			root := []byte{byte(s.slot), 0x01}
			if err := InsertSlot(context.Background(), tx, &dbtypes.Slot{
				Slot: s.slot, Proposer: s.proposer, Status: dbtypes.Canonical,
				Root: root, ParentRoot: root, StateRoot: root,
				Graffiti: []byte{}, ExecTimes: []byte{},
				BlockUid: s.slot << 16,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	for pattern, want := range map[string]bool{
		"%old-node%":    true, // historic name only
		"%legacy-node%": true, // current name only
		"%new-node%":    true,
		"%unknown%":     false,
	} {
		match, err := HasValidatorNameMatch(context.Background(), pattern)
		if err != nil {
			t.Fatalf("HasValidatorNameMatch(%q): %v", pattern, err)
		}
		if match != want {
			t.Errorf("HasValidatorNameMatch(%q) = %v, want %v", pattern, match, want)
		}
	}

	names, err := SearchValidatorNameCounts(context.Background(), "%node%", 10)
	if err != nil {
		t.Fatalf("SearchValidatorNameCounts: %v", err)
	}
	counts := make(map[string]uint64, len(names))
	for _, entry := range names {
		counts[entry.Name] = entry.Count
	}
	expected := map[string]uint64{"old-node": 1, "new-node": 1, "legacy-node": 1}
	if len(counts) != len(expected) {
		t.Fatalf("got names %v, want %v", counts, expected)
	}
	for name, count := range expected {
		if counts[name] != count {
			t.Errorf("count for %q = %v, want %v (all: %v)", name, counts[name], count, counts)
		}
	}
}

// TestConsolidationRequestTxsHistoryNames checks the time-keyed variant of the name
// filter used by tables that only store an EL block time.
func TestConsolidationRequestTxsHistoryNames(t *testing.T) {
	newTestDB(t)
	seedNameHistory(t)

	srcIndex := uint64(5)
	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		return InsertConsolidationRequestTxs(context.Background(), tx, []*dbtypes.ConsolidationRequestTx{
			{
				BlockNumber: 10, BlockIndex: 0, BlockTime: 1600, BlockRoot: []byte{0x01}, ForkId: 0,
				SourceAddress: []byte{0x02}, SourcePubkey: []byte{0x03}, SourceIndex: &srcIndex,
				TargetPubkey: []byte{0x04}, TxHash: []byte{0x05}, TxSender: []byte{0x06}, TxTarget: []byte{0x07},
				DequeueBlock: 20,
			},
			{
				BlockNumber: 11, BlockIndex: 0, BlockTime: 3000, BlockRoot: []byte{0x11}, ForkId: 0,
				SourceAddress: []byte{0x12}, SourcePubkey: []byte{0x13}, SourceIndex: &srcIndex,
				TargetPubkey: []byte{0x14}, TxHash: []byte{0x15}, TxSender: []byte{0x16}, TxTarget: []byte{0x17},
				DequeueBlock: 21,
			},
		})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name       string
		filterName string
		wantBlocks []uint64
	}{
		{"old assignment matches only the older tx", "old-node", []uint64{10}},
		{"current assignment matches only the newer tx", "new-node", []uint64{11}},
		{"unknown name matches nothing", "unknown-node", []uint64{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			txs, _, err := GetConsolidationRequestTxsFiltered(context.Background(), 0, 100, []uint64{0}, &dbtypes.ConsolidationRequestTxFilter{
				SrcValidatorName: c.filterName,
				WithOrphaned:     1,
			})
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			gotBlocks := make([]uint64, 0, len(txs))
			for _, tx := range txs {
				gotBlocks = append(gotBlocks, tx.BlockNumber)
			}
			if len(gotBlocks) != len(c.wantBlocks) {
				t.Fatalf("got blocks %v, want %v", gotBlocks, c.wantBlocks)
			}
			for i := range gotBlocks {
				if gotBlocks[i] != c.wantBlocks[i] {
					t.Fatalf("got blocks %v, want %v", gotBlocks, c.wantBlocks)
				}
			}
		})
	}
}
