package db

import (
	"embed"
	"fmt"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"

	"github.com/jackc/pgx/v4/pgxpool"
	_ "github.com/jackc/pgx/v4/stdlib"
)

//go:embed schema/pgsql/*.sql
var EmbedPgsqlSchema embed.FS

//go:embed schema/sqlite/*.sql
var EmbedSqliteSchema embed.FS

var DBPGX *pgxpool.Conn

// DB is a pointer to the explorer-database
var DbEngine dbtypes.DBEngineType
var ReaderDb *sqlx.DB
var writerDb *sqlx.DB
var writerMutex sync.Mutex

var logger = logrus.StandardLogger().WithField("module", "db")

func checkDbConn(dbConn *sqlx.DB, dataBaseName string) {
	// The golang sql driver does not properly implement PingContext
	// therefore we use a timer to catch db connection timeouts
	dbConnectionTimeout := time.NewTimer(15 * time.Second)

	go func() {
		<-dbConnectionTimeout.C
		logger.Fatalf("timeout while connecting to %s", dataBaseName)
	}()

	err := dbConn.Ping()
	if err != nil {
		logger.Fatalf("unable to Ping %s: %s", dataBaseName, err)
	}

	dbConnectionTimeout.Stop()
}

func mustInitSqlite(config *types.SqliteDatabaseConfig) (*sqlx.DB, *sqlx.DB) {
	if config.MaxOpenConns == 0 {
		config.MaxOpenConns = 50
	}
	if config.MaxIdleConns == 0 {
		config.MaxIdleConns = 10
	}
	if config.MaxOpenConns < config.MaxIdleConns {
		config.MaxIdleConns = config.MaxOpenConns
	}

	logger.Infof("initializing sqlite connection to %v with %v/%v conn limit", config.File, config.MaxIdleConns, config.MaxOpenConns)
	dbConn, err := sqlx.Open("sqlite", fmt.Sprintf("%s?_pragma=journal_mode(WAL)", config.File))
	if err != nil {
		utils.LogFatal(err, "error opening sqlite database", 0)
	}

	checkDbConn(dbConn, "database")
	dbConn.SetConnMaxIdleTime(0)
	dbConn.SetConnMaxLifetime(0)
	dbConn.SetMaxOpenConns(config.MaxOpenConns)
	dbConn.SetMaxIdleConns(config.MaxIdleConns)

	dbConn.MustExec("PRAGMA journal_mode = WAL")

	return dbConn, dbConn
}

func mustInitPgsql(writer *types.PgsqlDatabaseConfig, reader *types.PgsqlDatabaseConfig) (*sqlx.DB, *sqlx.DB) {
	if writer.MaxOpenConns == 0 {
		writer.MaxOpenConns = 50
	}
	if writer.MaxIdleConns == 0 {
		writer.MaxIdleConns = 10
	}
	if writer.MaxOpenConns < writer.MaxIdleConns {
		writer.MaxIdleConns = writer.MaxOpenConns
	}

	if reader.MaxOpenConns == 0 {
		reader.MaxOpenConns = 50
	}
	if reader.MaxIdleConns == 0 {
		reader.MaxIdleConns = 10
	}
	if reader.MaxOpenConns < reader.MaxIdleConns {
		reader.MaxIdleConns = reader.MaxOpenConns
	}

	logger.Infof("initializing pgsql writer connection to %v with %v/%v conn limit", writer.Host, writer.MaxIdleConns, writer.MaxOpenConns)
	dbConnWriter, err := sqlx.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", writer.Username, writer.Password, writer.Host, writer.Port, writer.Name))
	if err != nil {
		utils.LogFatal(err, "error getting pgsql writer database", 0)
	}

	checkDbConn(dbConnWriter, "database")
	dbConnWriter.SetConnMaxIdleTime(time.Second * 30)
	dbConnWriter.SetConnMaxLifetime(time.Second * 60)
	dbConnWriter.SetMaxOpenConns(writer.MaxOpenConns)
	dbConnWriter.SetMaxIdleConns(writer.MaxIdleConns)

	logger.Infof("initializing pgsql reader connection to %v with %v/%v conn limit", writer.Host, reader.MaxIdleConns, reader.MaxOpenConns)
	dbConnReader, err := sqlx.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", reader.Username, reader.Password, reader.Host, reader.Port, reader.Name))
	if err != nil {
		utils.LogFatal(err, "error getting pgsql reader database", 0)
	}

	checkDbConn(dbConnReader, "read replica database")
	dbConnReader.SetConnMaxIdleTime(time.Second * 30)
	dbConnReader.SetConnMaxLifetime(time.Second * 60)
	dbConnReader.SetMaxOpenConns(reader.MaxOpenConns)
	dbConnReader.SetMaxIdleConns(reader.MaxIdleConns)
	return dbConnWriter, dbConnReader
}

func MustInitDB() {
	if utils.Config.Database.Engine == "sqlite" {
		sqliteConfig := (*types.SqliteDatabaseConfig)(&utils.Config.Database.Sqlite)
		DbEngine = dbtypes.DBEngineSqlite
		writerDb, ReaderDb = mustInitSqlite(sqliteConfig)
	} else if utils.Config.Database.Engine == "pgsql" {
		readerConfig := (*types.PgsqlDatabaseConfig)(&utils.Config.Database.Pgsql)
		writerConfig := (*types.PgsqlDatabaseConfig)(&utils.Config.Database.PgsqlWriter)
		if writerConfig.Host == "" {
			writerConfig = readerConfig
		}
		DbEngine = dbtypes.DBEnginePgsql
		writerDb, ReaderDb = mustInitPgsql(writerConfig, readerConfig)
	} else {
		logger.Fatalf("unknown database engine type: %s", utils.Config.Database.Engine)
	}
}

func MustCloseDB() {
	err := writerDb.Close()
	if err != nil {
		logger.Errorf("Error closing writer db connection: %v", err)
	}
	err = ReaderDb.Close()
	if err != nil {
		logger.Errorf("Error closing reader db connection: %v", err)
	}
}

func RunDBTransaction(handler func(tx *sqlx.Tx) error) error {
	if DbEngine == dbtypes.DBEngineSqlite {
		writerMutex.Lock()
		defer writerMutex.Unlock()
	}

	tx, err := writerDb.Beginx()
	if err != nil {
		return fmt.Errorf("error starting db transactions: %v", err)
	}

	defer tx.Rollback()

	err = handler(tx)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("error committing db transaction: %v", err)
	}

	return nil
}

func ApplyEmbeddedDbSchema(version int64) error {
	var engineDialect string
	var schemaDirectory string
	switch DbEngine {
	case dbtypes.DBEnginePgsql:
		goose.SetBaseFS(EmbedPgsqlSchema)
		engineDialect = "postgres"
		schemaDirectory = "schema/pgsql"
	case dbtypes.DBEngineSqlite:
		goose.SetBaseFS(EmbedSqliteSchema)
		engineDialect = "sqlite3"
		schemaDirectory = "schema/sqlite"
	default:
		logger.Fatalf("unknown database engine")
	}
	if err := goose.SetDialect(engineDialect); err != nil {
		return err
	}

	if version == -2 {
		if err := goose.Up(writerDb.DB, schemaDirectory, goose.WithAllowMissing()); err != nil {
			return err
		}
	} else if version == -1 {
		if err := goose.UpByOne(writerDb.DB, schemaDirectory, goose.WithAllowMissing()); err != nil {
			return err
		}
	} else {
		if err := goose.UpTo(writerDb.DB, schemaDirectory, version, goose.WithAllowMissing()); err != nil {
			return err
		}
	}

	return nil
}

func EngineQuery(queryMap map[dbtypes.DBEngineType]string) string {
	if queryMap[DbEngine] != "" {
		return queryMap[DbEngine]
	}
	return queryMap[dbtypes.DBEngineAny]
}
