package beacon

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types"
)

func newBidTestDB(t *testing.T) {
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

// makeBidCache builds a cache whose slot span (0..20) exceeds the flush threshold,
// so checkAndFlush flushes every bid below the cutoff (maxSlot - retain = 10).
func makeBidCache() *blockBidCache {
	cache := &blockBidCache{
		indexer: &Indexer{logger: logrus.New()},
		bids:    make(map[bidCacheKey]*dbtypes.BlockBid, 64),
	}
	for slot := 0; slot <= 20; slot++ {
		bid := &dbtypes.BlockBid{
			ParentRoot:   []byte{1},
			ParentHash:   []byte{2},
			BlockHash:    []byte{byte(slot)}, // unique per slot -> unique primary key
			FeeRecipient: []byte{3},
			Slot:         uint64(slot),
		}
		cache.bids[bidCacheKey{BlockHash: [32]byte{byte(slot)}}] = bid
	}
	cache.minSlot = 0
	cache.maxSlot = 20
	return cache
}

// TestCheckAndFlush verifies that bids are dropped from the cache only after a
// successful db write, and kept for retry when the write fails.
func TestCheckAndFlush(t *testing.T) {
	newBidTestDB(t)

	// Success: slots 0..9 are below the cutoff and flushed; slots 10..20 stay.
	ok := makeBidCache()
	if err := ok.checkAndFlush(); err != nil {
		t.Fatalf("checkAndFlush: %v", err)
	}
	if len(ok.bids) != 11 {
		t.Errorf("cached bids after flush = %d, want 11 (slots >= cutoff retained)", len(ok.bids))
	}

	// Failure: drop the table so the write fails; the bids must be kept.
	if err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		_, err := tx.Exec("DROP TABLE block_bids")
		return err
	}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	fail := makeBidCache()
	before := len(fail.bids)
	if err := fail.checkAndFlush(); err == nil {
		t.Fatal("expected error from checkAndFlush when the db write fails")
	}
	if len(fail.bids) != before {
		t.Errorf("cached bids after failed flush = %d, want %d (retained)", len(fail.bids), before)
	}
}
