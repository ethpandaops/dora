package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/types"
)

// testDatabaseConfig returns the database the tests run against: a PostgreSQL
// database if DORA_TEST_PGSQL_HOST (with _PORT, _USER, _PASSWORD, _NAME) is
// set, otherwise a sqlite file in a temp dir.
func testDatabaseConfig(t *testing.T) *types.DatabaseConfig {
	t.Helper()

	if host := os.Getenv("DORA_TEST_PGSQL_HOST"); host != "" {
		pgsql := &types.PgsqlDatabaseConfig{
			Host:     host,
			Port:     os.Getenv("DORA_TEST_PGSQL_PORT"),
			Username: os.Getenv("DORA_TEST_PGSQL_USER"),
			Password: os.Getenv("DORA_TEST_PGSQL_PASSWORD"),
			Name:     os.Getenv("DORA_TEST_PGSQL_NAME"),
		}
		return &types.DatabaseConfig{
			Engine:      "pgsql",
			Pgsql:       pgsql,
			PgsqlWriter: (*types.PgsqlWriterDatabaseConfig)(pgsql),
		}
	}

	return &types.DatabaseConfig{
		Engine: "sqlite",
		Sqlite: &types.SqliteDatabaseConfig{
			File: filepath.Join(t.TempDir(), "test.sqlite"),
		},
	}
}

// newTestDB spins up an isolated, fully-migrated database for a test and
// wires it into the package globals the way MustInitDB does at runtime. A
// PostgreSQL test database is reset to an empty schema first.
func newTestDB(t *testing.T) {
	t.Helper()

	cfg := testDatabaseConfig(t)
	MustInitDB(cfg)
	if cfg.Engine == "pgsql" {
		if err := RunDBTransaction(func(tx *sqlx.Tx) error {
			_, err := tx.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public")
			return err
		}); err != nil {
			t.Fatalf("reset schema: %v", err)
		}
	}
	if err := ApplyEmbeddedDbSchema(-2); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(MustCloseDB)
}
