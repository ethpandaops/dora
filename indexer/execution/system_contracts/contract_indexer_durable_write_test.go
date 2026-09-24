package system_contracts

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	exectx "github.com/ethpandaops/dora/indexer/execution"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

type durableWriteTestTx struct{}

func newDurableWriteTestIndexer(t *testing.T) *contractIndexer[durableWriteTestTx] {
	t.Helper()

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
	executionPool := execution.NewPool(ctx, logger)
	beaconIndexer := beacon.NewIndexer(ctx, logger, consensusPool)
	indexerCtx := exectx.NewIndexerCtx(ctx, logger, executionPool, consensusPool, beaconIndexer)

	return &contractIndexer[durableWriteTestTx]{
		indexer: indexerCtx,
		logger:  logger,
		options: &contractIndexerOptions[durableWriteTestTx]{
			stateKey: "test.state",
			persistTxs: func(tx *sqlx.Tx, txs []*durableWriteTestTx) error {
				return nil
			},
		},
		state: &contractIndexerState{
			FinalBlock:    100,
			FinalQueueLen: 5,
			ForkStates:    map[beacon.ForkKey]*contractIndexerForkState{},
		},
	}
}

// TestPersistFinalizedRequestTxs_FailedWriteRollsBackState verifies that when the persist
// transaction fails, ci.state.FinalBlock/FinalQueueLen are rolled back to their prior
// values instead of staying advanced. Before the fix, the in-memory cursor was mutated
// inside the RunDBTransaction closure before persistState's commit was confirmed, and
// never rolled back on failure - permanently skipping the block range for the rest of the
// process's uptime. Fails without the fix, passes with it.
func TestPersistFinalizedRequestTxs_FailedWriteRollsBackState(t *testing.T) {
	ci := newDurableWriteTestIndexer(t)

	if _, err := db.ReaderDb.Exec(`DROP TABLE explorer_state`); err != nil {
		t.Fatalf("drop explorer_state table: %v", err)
	}

	err := ci.persistFinalizedRequestTxs(999999, 42, nil)
	if err == nil {
		t.Fatalf("expected persistFinalizedRequestTxs to report the failed persist, got nil error")
	}

	if ci.state.FinalBlock != 100 {
		t.Fatalf("ci.state.FinalBlock = %v, want 100 (rolled back) after the failed transaction", ci.state.FinalBlock)
	}
	if ci.state.FinalQueueLen != 5 {
		t.Fatalf("ci.state.FinalQueueLen = %v, want 5 (rolled back) after the failed transaction", ci.state.FinalQueueLen)
	}
}

// TestPersistFinalizedRequestTxs_SuccessfulWriteAdvancesState is the happy-path mirror:
// a successful persist must still advance the in-memory cursor.
func TestPersistFinalizedRequestTxs_SuccessfulWriteAdvancesState(t *testing.T) {
	ci := newDurableWriteTestIndexer(t)

	err := ci.persistFinalizedRequestTxs(999999, 42, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ci.state.FinalBlock != 999999 {
		t.Fatalf("ci.state.FinalBlock = %v, want 999999", ci.state.FinalBlock)
	}
	if ci.state.FinalQueueLen != 42 {
		t.Fatalf("ci.state.FinalQueueLen = %v, want 42", ci.state.FinalQueueLen)
	}
}

// TestPersistRecentRequestTxs_FailedWriteRollsBackNewForkState verifies the same rollback
// for the fork-scoped state map, covering the case where the fork didn't have an entry
// before the call (the entry must be removed entirely on failure, not left behind).
func TestPersistRecentRequestTxs_FailedWriteRollsBackNewForkState(t *testing.T) {
	ci := newDurableWriteTestIndexer(t)

	if _, err := db.ReaderDb.Exec(`DROP TABLE explorer_state`); err != nil {
		t.Fatalf("drop explorer_state table: %v", err)
	}

	forkId := beacon.ForkKey(7)
	err := ci.persistRecentRequestTxs(forkId, 555, 3, nil)
	if err == nil {
		t.Fatalf("expected persistRecentRequestTxs to report the failed persist, got nil error")
	}

	if _, exists := ci.state.ForkStates[forkId]; exists {
		t.Fatalf("ci.state.ForkStates[%v] still present after the failed transaction - should have been rolled back to absent", forkId)
	}
}

// TestPersistRecentRequestTxs_FailedWriteRestoresPriorForkState covers the case where the
// fork already had a state entry - a failed write must restore the OLD value, not just
// delete it.
func TestPersistRecentRequestTxs_FailedWriteRestoresPriorForkState(t *testing.T) {
	ci := newDurableWriteTestIndexer(t)

	forkId := beacon.ForkKey(7)
	ci.state.ForkStates[forkId] = &contractIndexerForkState{Block: 111, QueueLen: 1}

	if _, err := db.ReaderDb.Exec(`DROP TABLE explorer_state`); err != nil {
		t.Fatalf("drop explorer_state table: %v", err)
	}

	err := ci.persistRecentRequestTxs(forkId, 555, 3, nil)
	if err == nil {
		t.Fatalf("expected persistRecentRequestTxs to report the failed persist, got nil error")
	}

	got, exists := ci.state.ForkStates[forkId]
	if !exists {
		t.Fatalf("ci.state.ForkStates[%v] missing after the failed transaction - should have been restored to its prior value", forkId)
	}
	if got.Block != 111 || got.QueueLen != 1 {
		t.Fatalf("ci.state.ForkStates[%v] = %+v, want {Block:111 QueueLen:1} (restored)", forkId, got)
	}
}

// TestPersistState_DirectCall_FailedWriteRestoresPrunedForkStates covers persistState's
// own internal cleanup in isolation: it removes fork states whose block is behind the
// finalized block before attempting the write. finalizedBlockNumber is forced above zero
// by inserting a matching slot row, so the cleanup loop actually has something to remove.
func TestPersistState_DirectCall_FailedWriteRestoresPrunedForkStates(t *testing.T) {
	ci := newDurableWriteTestIndexer(t)

	// getFinalizedBlockNumber() resolves the finalized root via
	// indexer.BeaconIndexer.GetBlockByRoot (empty cache -> miss here) and then falls
	// back to db.GetSlotByRoot. A fresh ChainState's GetFinalizedCheckpoint() returns
	// (0, NullRoot) - consensus.NullRoot is the all-zero phase0.Root - so seeding a slot
	// at that root with a known eth block number gives getFinalizedBlockNumber()
	// something to find without needing access to ChainState's unexported setter.
	ethBlockNumber := uint64(500)
	if err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertSlot(context.Background(), tx, &dbtypes.Slot{
			Root:               consensus.NullRoot[:],
			Slot:               1,
			ParentRoot:         consensus.NullRoot[:],
			StateRoot:          consensus.NullRoot[:],
			Status:             dbtypes.Canonical,
			EthBlockNumber:     &ethBlockNumber,
			EthBlockHash:       []byte{},
			EthBlockParentHash: []byte{},
			EthBlockExtra:      []byte{},
			EthFeeRecipient:    []byte{},
		})
	}); err != nil {
		t.Fatalf("seed finalized slot: %v", err)
	}

	staleForkId := beacon.ForkKey(1)
	liveForkId := beacon.ForkKey(2)
	ci.state.ForkStates[staleForkId] = &contractIndexerForkState{Block: 1, QueueLen: 0}  // < 500: eligible for cleanup
	ci.state.ForkStates[liveForkId] = &contractIndexerForkState{Block: 600, QueueLen: 0} // >= 500: not eligible

	if _, err := db.ReaderDb.Exec(`DROP TABLE explorer_state`); err != nil {
		t.Fatalf("drop explorer_state table: %v", err)
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return ci.persistState(tx)
	})
	if err == nil {
		t.Fatalf("expected persistState to report the failed write, got nil error")
	}

	if _, exists := ci.state.ForkStates[staleForkId]; !exists {
		t.Fatalf("ci.state.ForkStates[%v] was removed by persistState's internal cleanup and never restored after the failed write", staleForkId)
	}
	if _, exists := ci.state.ForkStates[liveForkId]; !exists {
		t.Fatalf("ci.state.ForkStates[%v] should never have been touched", liveForkId)
	}
}
