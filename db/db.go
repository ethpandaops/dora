package db

import (
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/types"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

//go:embed migrations/*.sql
var EmbedMigrations embed.FS

var DBPGX *pgxpool.Conn

// DB is a pointer to the explorer-database
var WriterDb *sqlx.DB
var ReaderDb *sqlx.DB

var logger = logrus.StandardLogger().WithField("module", "db")

func dbTestConnection(dbConn *sqlx.DB, dataBaseName string) {
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

func mustInitDB(writer *types.DatabaseConfig, reader *types.DatabaseConfig) (*sqlx.DB, *sqlx.DB) {

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

	logger.Infof("initializing writer db connection to %v with %v/%v conn limit", writer.Host, writer.MaxIdleConns, writer.MaxOpenConns)
	dbConnWriter, err := sqlx.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", writer.Username, writer.Password, writer.Host, writer.Port, writer.Name))
	if err != nil {
		utils.LogFatal(err, "error getting Connection Writer database", 0)
	}

	dbTestConnection(dbConnWriter, "database")
	dbConnWriter.SetConnMaxIdleTime(time.Second * 30)
	dbConnWriter.SetConnMaxLifetime(time.Second * 60)
	dbConnWriter.SetMaxOpenConns(writer.MaxOpenConns)
	dbConnWriter.SetMaxIdleConns(writer.MaxIdleConns)

	if reader == nil {
		return dbConnWriter, dbConnWriter
	}

	logger.Infof("initializing reader db connection to %v with %v/%v conn limit", writer.Host, reader.MaxIdleConns, reader.MaxOpenConns)
	dbConnReader, err := sqlx.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", reader.Username, reader.Password, reader.Host, reader.Port, reader.Name))
	if err != nil {
		utils.LogFatal(err, "error getting Connection Reader database", 0)
	}

	dbTestConnection(dbConnReader, "read replica database")
	dbConnReader.SetConnMaxIdleTime(time.Second * 30)
	dbConnReader.SetConnMaxLifetime(time.Second * 60)
	dbConnReader.SetMaxOpenConns(reader.MaxOpenConns)
	dbConnReader.SetMaxIdleConns(reader.MaxIdleConns)
	return dbConnWriter, dbConnReader
}

func MustInitDB(writer *types.DatabaseConfig, reader *types.DatabaseConfig) {
	WriterDb, ReaderDb = mustInitDB(writer, reader)
}

func ApplyEmbeddedDbSchema(version int64) error {
	goose.SetBaseFS(EmbedMigrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	if version == -2 {
		if err := goose.Up(WriterDb.DB, "migrations"); err != nil {
			return err
		}
	} else if version == -1 {
		if err := goose.UpByOne(WriterDb.DB, "migrations"); err != nil {
			return err
		}
	} else {
		if err := goose.UpTo(WriterDb.DB, "migrations", version); err != nil {
			return err
		}
	}

	return nil
}
