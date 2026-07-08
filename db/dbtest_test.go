package db

import (
	"path/filepath"
	"testing"

	"github.com/ethpandaops/dora/types"
)

// newTestDB spins up an isolated, fully-migrated sqlite database for a test and
// wires it into the package globals the way MustInitDB does at runtime.
func newTestDB(t *testing.T) {
	t.Helper()

	cfg := &types.DatabaseConfig{
		Engine: "sqlite",
		Sqlite: &types.SqliteDatabaseConfig{
			File: filepath.Join(t.TempDir(), "test.sqlite"),
		},
	}

	MustInitDB(cfg)
	if err := ApplyEmbeddedDbSchema(-2); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(MustCloseDB)
}
