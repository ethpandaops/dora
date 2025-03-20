package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/types"
)

type DbConfig struct {
	Engine string
	Sqlite types.SqliteDatabaseConfig
	Pgsql  types.PgsqlDatabaseConfig
}

func migrate() {
	flags := flag.NewFlagSet("migrate", flag.ExitOnError)
	sourceEngine := flags.String("source-engine", "", "Source database engine (sqlite/pgsql)")
	sourceSqlitePath := flags.String("source-sqlite-path", "", "Source SQLite database path")
	sourcePgsqlHost := flags.String("source-pgsql-host", "", "Source PostgreSQL host")
	sourcePgsqlPort := flags.String("source-pgsql-port", "5432", "Source PostgreSQL port")
	sourcePgsqlUser := flags.String("source-pgsql-user", "", "Source PostgreSQL user")
	sourcePgsqlPass := flags.String("source-pgsql-pass", "", "Source PostgreSQL password")
	sourcePgsqlDb := flags.String("source-pgsql-db", "", "Source PostgreSQL database name")

	targetEngine := flags.String("target-engine", "", "Target database engine (sqlite/pgsql)")
	targetSqlitePath := flags.String("target-sqlite-path", "", "Target SQLite database path")
	targetPgsqlHost := flags.String("target-pgsql-host", "", "Target PostgreSQL host")
	targetPgsqlPort := flags.String("target-pgsql-port", "5432", "Target PostgreSQL port")
	targetPgsqlUser := flags.String("target-pgsql-user", "", "Target PostgreSQL user")
	targetPgsqlPass := flags.String("target-pgsql-pass", "", "Target PostgreSQL password")
	targetPgsqlDb := flags.String("target-pgsql-db", "", "Target PostgreSQL database name")

	debug := flags.Bool("debug", false, "Enable debug mode")

	flags.Parse(os.Args[1:])

	if *debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if *sourceEngine == "" || *targetEngine == "" {
		flag.Usage()
		os.Exit(1)
	}

	sourceConfig := DbConfig{Engine: *sourceEngine}
	targetConfig := DbConfig{Engine: *targetEngine}

	if *sourceEngine == "sqlite" {
		sourceConfig.Sqlite.File = *sourceSqlitePath
	} else {
		sourceConfig.Pgsql.Host = *sourcePgsqlHost
		sourceConfig.Pgsql.Port = *sourcePgsqlPort
		sourceConfig.Pgsql.Username = *sourcePgsqlUser
		sourceConfig.Pgsql.Password = *sourcePgsqlPass
		sourceConfig.Pgsql.Name = *sourcePgsqlDb
	}

	if *targetEngine == "sqlite" {
		targetConfig.Sqlite.File = *targetSqlitePath
	} else {
		targetConfig.Pgsql.Host = *targetPgsqlHost
		targetConfig.Pgsql.Port = *targetPgsqlPort
		targetConfig.Pgsql.Username = *targetPgsqlUser
		targetConfig.Pgsql.Password = *targetPgsqlPass
		targetConfig.Pgsql.Name = *targetPgsqlDb
	}

	if err := migrateDatabase(sourceConfig, targetConfig); err != nil {
		logrus.Fatalf("Migration failed: %v", err)
	}
}

func migrateDatabase(source, target DbConfig) error {
	var sourceDb *sqlx.DB
	var err error

	// Initialize source connection
	if source.Engine == "sqlite" {
		sourceDb, err = sqlx.Open("sqlite", source.Sqlite.File)
	} else {
		sourceDb, err = sqlx.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			source.Pgsql.Username, source.Pgsql.Password, source.Pgsql.Host, source.Pgsql.Port, source.Pgsql.Name))
	}
	if err != nil {
		return fmt.Errorf("failed to connect to source db: %v", err)
	}
	defer sourceDb.Close()

	// Initialize target connection
	targetDbConfig := types.DatabaseConfig{
		Engine: target.Engine,
	}
	if target.Engine == "sqlite" {
		targetDbConfig.Sqlite = &types.SqliteDatabaseConfig{
			File: target.Sqlite.File,
		}
	} else {
		targetDbConfig.Pgsql = &types.PgsqlDatabaseConfig{
			Username: target.Pgsql.Username,
			Password: target.Pgsql.Password,
			Host:     target.Pgsql.Host,
			Port:     target.Pgsql.Port,
			Name:     target.Pgsql.Name,
		}
	}

	// Initialize schema on target
	db.MustInitDB(&targetDbConfig)
	if err := db.ApplyEmbeddedDbSchema(-2); err != nil {
		return fmt.Errorf("failed to initialize target schema: %v", err)
	}

	// Get all table names
	var tables []string
	if source.Engine == "sqlite" {
		err = sourceDb.Select(&tables, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'goose_%'")
	} else {
		err = sourceDb.Select(&tables, "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_name NOT LIKE 'goose_%'")
	}
	if err != nil {
		return fmt.Errorf("failed to get table names: %v", err)
	}

	// Migrate each table
	for _, table := range tables {
		logrus.Printf("Migrating table: %s", table)

		// Get total row count
		var count int
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
		if err := sourceDb.Get(&count, countQuery); err != nil {
			return fmt.Errorf("failed to get row count for table %s: %v", table, err)
		}

		// Get all rows
		rows, err := sourceDb.Queryx(fmt.Sprintf("SELECT * FROM %s", table))
		if err != nil {
			return fmt.Errorf("failed to read from source table %s: %v", table, err)
		}

		processed := 0
		lastProgress := 0
		err = db.RunDBTransaction(func(tx *sqlx.Tx) error {
			for rows.Next() {
				// Get row data as map
				row := make(map[string]interface{})
				if err := rows.MapScan(row); err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to scan row: %v", err)
				}

				// Build insert query
				cols := make([]string, 0, len(row))
				vals := make([]string, 0, len(row))
				args := make([]interface{}, 0, len(row))
				i := 1
				for col, val := range row {
					cols = append(cols, fmt.Sprintf("\"%s\"", col))
					if target.Engine == "sqlite" {
						vals = append(vals, "?")
					} else {
						vals = append(vals, fmt.Sprintf("$%d", i))
					}
					args = append(args, val)
					i++
				}

				query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
					table,
					strings.Join(cols, ","),
					strings.Join(vals, ","))

				if _, err := tx.Exec(query, args...); err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to insert into target table %s: %v", table, err)
				}

				processed++
				progress := (processed * 100) / count
				if progress/10 > lastProgress/10 {
					fmt.Print(".")
				}
				lastProgress = progress
			}
			return nil
		})

		rows.Close()
		fmt.Println() // New line after dots

		if err != nil {
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
	}

	logrus.Printf("Migration completed successfully")
	return nil
}
