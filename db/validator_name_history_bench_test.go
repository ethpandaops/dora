package db

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types"
	"github.com/jmoiron/sqlx"
)

// Benchmarks the proposer-name filter on a mature-devnet-sized slots table
// (200k slots, 100 nodes x 1000 validators, every validator reassigned once),
// with and without name history rows. The no-match filters force a full scan,
// probing the history per scanned row - the worst case.
func BenchmarkGetFilteredSlotsProposerName(b *testing.B) {
	cfg := &types.DatabaseConfig{
		Engine: "sqlite",
		Sqlite: &types.SqliteDatabaseConfig{File: filepath.Join(b.TempDir(), "bench.sqlite")},
	}
	MustInitDB(cfg)
	defer MustCloseDB()
	if err := ApplyEmbeddedDbSchema(-2); err != nil {
		b.Fatalf("apply schema: %v", err)
	}

	const slotCount = 200000
	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		stmt, err := tx.Prepare(`INSERT INTO slots (slot, proposer, status, root, parent_root, state_root, graffiti, graffiti_text, exec_times, block_uid, fork_id, builder_index) VALUES (?,?,?,?,?,?,?,?,?,?,0,-1)`)
		if err != nil {
			return err
		}
		for i := 0; i < slotCount; i++ {
			root := []byte(fmt.Sprintf("root-%08d", i))
			if _, err := stmt.Exec(i, i%100000, 1, root, root, root, []byte{}, "", []byte{}, i<<16); err != nil {
				return err
			}
		}

		names := make([]*dbtypes.ValidatorName, 0, 100000)
		for i := uint64(0); i < 100000; i++ {
			names = append(names, &dbtypes.ValidatorName{Index: i, Name: fmt.Sprintf("node-%d", i/1000)})
		}
		for start := 0; start < len(names); start += 5000 {
			if err := InsertValidatorNames(context.Background(), tx, names[start:start+5000]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatalf("seed: %v", err)
	}

	seedHistory := func(b *testing.B) {
		err := RunDBTransaction(func(tx *sqlx.Tx) error {
			for s := uint64(0); s < 2; s++ {
				endSlot := (s + 1) * 100000
				if s == 1 {
					endSlot = uint64(math.MaxInt64)
				}
				ranges := make([]*dbtypes.ValidatorNameRange, 0, 100)
				for r := uint64(0); r < 100; r++ {
					ranges = append(ranges, &dbtypes.ValidatorNameRange{
						SnapshotSlot: s * 100000,
						StartIndex:   r * 1000, EndIndex: r*1000 + 999,
						Name: fmt.Sprintf("node-%d-%d", s, r),
					})
				}
				if err := UpsertValidatorNameSnapshot(context.Background(), tx, &dbtypes.ValidatorNameSnapshot{
					StartSlot: s * 100000, EndSlot: endSlot,
					StartTime: s * 1200000, EndTime: uint64(math.MaxInt64),
					RangesHash: fmt.Sprintf("hash-%d", s),
				}, ranges); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			b.Fatalf("seed history: %v", err)
		}
	}

	run := func(b *testing.B, name string, invert bool, wantMatches bool) {
		filter := &dbtypes.BlockFilter{ProposerName: name, InvertProposer: invert, WithMissing: 1, WithOrphaned: 1}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			slots := GetFilteredSlots(context.Background(), filter, slotCount, 0, 25)
			if wantMatches && len(slots) == 0 {
				b.Fatal("expected matches")
			}
			if !wantMatches && len(slots) != 0 {
				b.Fatalf("expected no matches, got %d", len(slots))
			}
		}
	}

	// empty history tables: the behavior every non-history network gets
	b.Run("legacy-nomatch", func(b *testing.B) { run(b, "no-such-node", false, false) })
	b.Run("legacy-match", func(b *testing.B) { run(b, "node-42", false, true) })

	seedHistory(b)
	b.Run("history-nomatch", func(b *testing.B) { run(b, "no-such-node", false, false) })
	b.Run("history-match", func(b *testing.B) { run(b, "node-1-42", false, true) })
	b.Run("history-invert", func(b *testing.B) { run(b, "node-1-42", true, true) })
}
