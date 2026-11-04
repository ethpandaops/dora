package beacon

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

// TestProcessEpochPruning_FailedWriteDoesNotAdvanceState verifies that a failed pruning
// persist transaction is surfaced as an error and does not advance lastPrunedEpoch. Before
// the fix, RunDBTransaction's result was discarded, lastPrunedEpoch advanced
// unconditionally, and the function always returned a nil error - so a transient write
// failure silently and permanently lost that epoch's pruned data. Fails without the fix,
// passes with it.
func TestProcessEpochPruning_FailedWriteDoesNotAdvanceState(t *testing.T) {
	utils.Config = &types.Config{}

	cfg := &types.DatabaseConfig{
		Engine: "sqlite",
		Sqlite: &types.SqliteDatabaseConfig{File: filepath.Join(t.TempDir(), "test.sqlite")},
	}
	db.MustInitDB(cfg)
	if err := db.ApplyEmbeddedDbSchema(-2); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(db.MustCloseDB)

	// Force updatePruningState's db.SetExplorerState write to fail deterministically -
	// a stand-in for any transient write failure on the explorer_state table.
	if _, err := db.ReaderDb.Exec(`DROP TABLE explorer_state`); err != nil {
		t.Fatalf("drop explorer_state table: %v", err)
	}

	ctx := context.Background()
	logger := logrus.StandardLogger()
	consensusPool := consensus.NewPool(ctx, logger)
	indexer := NewIndexer(ctx, logger, consensusPool)

	// No blocks were added to the cache, so processEpochPruning(0) has nothing to persist
	// except the prune-state checkpoint itself - which is exactly the write broken above.
	_, _, err := indexer.processEpochPruning(0)

	if err == nil {
		t.Fatalf("expected processEpochPruning to report the failed persist, got nil error")
	}

	var pruneState dbtypes.IndexerPruneState
	if _, dbErr := db.GetExplorerState(ctx, "indexer.prunestate", &pruneState); dbErr == nil {
		t.Fatalf("expected GetExplorerState to fail (nothing should have been durably persisted), but it succeeded with epoch=%v", pruneState.Epoch)
	}

	if indexer.lastPrunedEpoch != 0 {
		t.Fatalf("lastPrunedEpoch advanced to %v despite the persist transaction failing - epoch 0 will never be retried", indexer.lastPrunedEpoch)
	}
}

// TestProcessEpochPruning_SuccessfulWriteAdvancesState is the mirror-image happy path:
// a successful persist must still advance lastPrunedEpoch and report no error.
func TestProcessEpochPruning_SuccessfulWriteAdvancesState(t *testing.T) {
	utils.Config = &types.Config{}

	cfg := &types.DatabaseConfig{
		Engine: "sqlite",
		Sqlite: &types.SqliteDatabaseConfig{File: filepath.Join(t.TempDir(), "test.sqlite")},
	}
	db.MustInitDB(cfg)
	if err := db.ApplyEmbeddedDbSchema(-2); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(db.MustCloseDB)

	ctx := context.Background()
	logger := logrus.StandardLogger()
	consensusPool := consensus.NewPool(ctx, logger)
	indexer := NewIndexer(ctx, logger, consensusPool)

	_, _, err := indexer.processEpochPruning(0)
	if err != nil {
		t.Fatalf("unexpected error from processEpochPruning: %v", err)
	}

	if indexer.lastPrunedEpoch != 1 {
		t.Fatalf("lastPrunedEpoch = %v, want 1", indexer.lastPrunedEpoch)
	}

	var pruneState dbtypes.IndexerPruneState
	if _, dbErr := db.GetExplorerState(ctx, "indexer.prunestate", &pruneState); dbErr != nil {
		t.Fatalf("expected prune state to be durably persisted, GetExplorerState failed: %v", dbErr)
	}
	if pruneState.Epoch != 1 {
		t.Fatalf("persisted prune state epoch = %v, want 1", pruneState.Epoch)
	}
}
