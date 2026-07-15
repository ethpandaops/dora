package db

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types"
	"github.com/jmoiron/sqlx"
)

// Benchmarks the proposer-name filter on a mature-devnet-sized slots table
// (200k slots, 100 nodes x 1000 validators), legacy vs history-aware SQL.
// The filter name matches nothing, forcing a full scan - the worst case.
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

		// worst case: every validator was reassigned twice within the slot window,
		// so every (index, snapshot) pair gets a history row (200k rows)
		history := make([]*dbtypes.ValidatorNameHistory, 0, 200000)
		for s := uint64(0); s < 2; s++ {
			for i := uint64(0); i < 100000; i++ {
				history = append(history, &dbtypes.ValidatorNameHistory{
					Index:     i,
					StartSlot: s * 100000, EndSlot: (s + 1) * 100000,
					Name: fmt.Sprintf("node-%d-%d", s, i/1000),
				})
			}
		}
		return ReplaceValidatorNameHistory(context.Background(), tx, history)
	})
	if err != nil {
		b.Fatalf("seed: %v", err)
	}

	run := func(b *testing.B, withHistory bool) {
		filter := &dbtypes.BlockFilter{ProposerName: "no-such-node", ProposerNameHistory: withHistory, WithMissing: 1, WithOrphaned: 1}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			slots := GetFilteredSlots(context.Background(), filter, slotCount, 0, 25)
			if len(slots) != 0 {
				b.Fatalf("expected no matches, got %d", len(slots))
			}
		}
	}

	b.Run("legacy", func(b *testing.B) { run(b, false) })
	b.Run("history", func(b *testing.B) { run(b, true) })

	// selective match: LIMIT satisfied quickly, the common interactive case
	runMatch := func(b *testing.B, withHistory bool, name string) {
		filter := &dbtypes.BlockFilter{ProposerName: name, ProposerNameHistory: withHistory, WithMissing: 1, WithOrphaned: 1}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			slots := GetFilteredSlots(context.Background(), filter, slotCount, 0, 25)
			if len(slots) == 0 {
				b.Fatal("expected matches")
			}
		}
	}
	b.Run("legacy-match", func(b *testing.B) { runMatch(b, false, "node-42") })
	b.Run("history-match", func(b *testing.B) { runMatch(b, true, "node-1-42") })
}
