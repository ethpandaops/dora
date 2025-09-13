package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
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

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate database between different engines",
	Long:  "Migrate data from one database engine to another (SQLite <-> PostgreSQL)",
	RunE:  runMigrate,
}

func init() {
	rootCmd.AddCommand(migrateCmd)
	
	// Source database flags
	migrateCmd.Flags().String("source-engine", "", "Source database engine (sqlite/pgsql)")
	migrateCmd.Flags().String("source-sqlite-path", "", "Source SQLite database path")
	migrateCmd.Flags().String("source-pgsql-host", "", "Source PostgreSQL host")
	migrateCmd.Flags().String("source-pgsql-port", "5432", "Source PostgreSQL port")
	migrateCmd.Flags().String("source-pgsql-user", "", "Source PostgreSQL user")
	migrateCmd.Flags().String("source-pgsql-pass", "", "Source PostgreSQL password")
	migrateCmd.Flags().String("source-pgsql-db", "", "Source PostgreSQL database name")
	
	// Target database flags
	migrateCmd.Flags().String("target-engine", "", "Target database engine (sqlite/pgsql)")
	migrateCmd.Flags().String("target-sqlite-path", "", "Target SQLite database path")
	migrateCmd.Flags().String("target-pgsql-host", "", "Target PostgreSQL host")
	migrateCmd.Flags().String("target-pgsql-port", "5432", "Target PostgreSQL port")
	migrateCmd.Flags().String("target-pgsql-user", "", "Target PostgreSQL user")
	migrateCmd.Flags().String("target-pgsql-pass", "", "Target PostgreSQL password")
	migrateCmd.Flags().String("target-pgsql-db", "", "Target PostgreSQL database name")
	
	// Additional options
	migrateCmd.Flags().String("limit-tables", "", "Limit tables to migrate (comma separated list)")
	migrateCmd.Flags().BoolP("debug", "d", false, "Enable debug mode")
	
	// Mark required flags
	migrateCmd.MarkFlagRequired("source-engine")
	migrateCmd.MarkFlagRequired("target-engine")
}

func runMigrate(cmd *cobra.Command, args []string) error {
	debug, _ := cmd.Flags().GetBool("debug")
	sourceEngine, _ := cmd.Flags().GetString("source-engine")
	targetEngine, _ := cmd.Flags().GetString("target-engine")
	limitTablesStr, _ := cmd.Flags().GetString("limit-tables")
	
	// Source database flags
	sourceSqlitePath, _ := cmd.Flags().GetString("source-sqlite-path")
	sourcePgsqlHost, _ := cmd.Flags().GetString("source-pgsql-host")
	sourcePgsqlPort, _ := cmd.Flags().GetString("source-pgsql-port")
	sourcePgsqlUser, _ := cmd.Flags().GetString("source-pgsql-user")
	sourcePgsqlPass, _ := cmd.Flags().GetString("source-pgsql-pass")
	sourcePgsqlDb, _ := cmd.Flags().GetString("source-pgsql-db")
	
	// Target database flags
	targetSqlitePath, _ := cmd.Flags().GetString("target-sqlite-path")
	targetPgsqlHost, _ := cmd.Flags().GetString("target-pgsql-host")
	targetPgsqlPort, _ := cmd.Flags().GetString("target-pgsql-port")
	targetPgsqlUser, _ := cmd.Flags().GetString("target-pgsql-user")
	targetPgsqlPass, _ := cmd.Flags().GetString("target-pgsql-pass")
	targetPgsqlDb, _ := cmd.Flags().GetString("target-pgsql-db")

	if debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if sourceEngine == "" || targetEngine == "" {
		return fmt.Errorf("source-engine and target-engine are required")
	}

	limitTables := make([]string, 0)
	if limitTablesStr != "" {
		limitTables = strings.Split(limitTablesStr, ",")
	}

	sourceConfig := DbConfig{Engine: sourceEngine}
	targetConfig := DbConfig{Engine: targetEngine}

	if sourceEngine == "sqlite" {
		sourceConfig.Sqlite.File = sourceSqlitePath
	} else {
		sourceConfig.Pgsql.Host = sourcePgsqlHost
		sourceConfig.Pgsql.Port = sourcePgsqlPort
		sourceConfig.Pgsql.Username = sourcePgsqlUser
		sourceConfig.Pgsql.Password = sourcePgsqlPass
		sourceConfig.Pgsql.Name = sourcePgsqlDb
	}

	if targetEngine == "sqlite" {
		targetConfig.Sqlite.File = targetSqlitePath
	} else {
		targetConfig.Pgsql.Host = targetPgsqlHost
		targetConfig.Pgsql.Port = targetPgsqlPort
		targetConfig.Pgsql.Username = targetPgsqlUser
		targetConfig.Pgsql.Password = targetPgsqlPass
		targetConfig.Pgsql.Name = targetPgsqlDb
	}

	if err := migrateDatabase(sourceConfig, targetConfig, limitTables); err != nil {
		return fmt.Errorf("migration failed: %v", err)
	}
	
	logrus.Info("Migration completed successfully")
	return nil
}

func migrateDatabase(source, target DbConfig, limitTables []string) error {
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

	// Filter tables if limitTables is provided
	if len(limitTables) > 0 {
		filteredTables := make([]string, 0)
		for _, table := range tables {
			if slices.Contains(limitTables, table) {
				filteredTables = append(filteredTables, table)
			}
		}
		tables = filteredTables
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
