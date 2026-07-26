package system_contracts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/indexer/execution"
	"github.com/ethpandaops/dora/types"
)

func newTestDB(t *testing.T) {
	t.Helper()
	cfg := &types.DatabaseConfig{
		Engine: "sqlite",
		Sqlite: &types.SqliteDatabaseConfig{File: filepath.Join(t.TempDir(), "test.sqlite")},
	}
	db.MustInitDB(cfg)
	if err := db.ApplyEmbeddedDbSchema(-2); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(db.MustCloseDB)
}

type stubMatch struct{}

func newTestMatcher(persist func(tx *sqlx.Tx, matches []*stubMatch) error, startHeight uint64) *transactionMatcher[stubMatch] {
	return &transactionMatcher[stubMatch]{
		indexer: &execution.IndexerCtx{Ctx: context.Background()},
		logger:  logrus.New(),
		options: &transactionMatcherOptions[stubMatch]{
			stateKey:       "test.matcher",
			persistMatches: persist,
		},
		state: &transactionMatcherState{MatchHeight: startHeight},
	}
}

func storedHeight(t *testing.T) uint64 {
	t.Helper()
	state := transactionMatcherState{}
	if _, err := db.GetExplorerState(context.Background(), "test.matcher", &state); err != nil {
		return 0 // no state persisted yet
	}
	return state.MatchHeight
}

// TestPersistMatchRange checks that a failed match persist rolls the height back
// so the range is retried, and that a successful persist advances and stores it.
func TestPersistMatchRange(t *testing.T) {
	newTestDB(t)

	// Failure: persistMatches errors, so the height must revert and nothing persist.
	failing := newTestMatcher(func(tx *sqlx.Tx, matches []*stubMatch) error {
		return errors.New("transient error")
	}, 100)
	if err := failing.persistMatchRange([]*stubMatch{{}}, 200); err == nil {
		t.Fatal("expected error from persistMatchRange")
	}
	if failing.state.MatchHeight != 100 {
		t.Errorf("match height after failure = %d, want 100 (reverted)", failing.state.MatchHeight)
	}
	if h := storedHeight(t); h != 0 {
		t.Errorf("stored height after failure = %d, want 0 (nothing persisted)", h)
	}

	// Success: persistMatches ok, so the height advances and is stored.
	ok := newTestMatcher(func(tx *sqlx.Tx, matches []*stubMatch) error {
		return nil
	}, 100)
	if err := ok.persistMatchRange([]*stubMatch{{}}, 200); err != nil {
		t.Fatalf("persistMatchRange: %v", err)
	}
	if ok.state.MatchHeight != 200 {
		t.Errorf("match height after success = %d, want 200", ok.state.MatchHeight)
	}
	if h := storedHeight(t); h != 200 {
		t.Errorf("stored height after success = %d, want 200", h)
	}
}
