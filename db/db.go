package db

import (
	"embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/types"
	"github.com/pk910/light-beaconchain-explorer/utils"

	"github.com/jackc/pgx/v4/pgxpool"
	_ "github.com/jackc/pgx/v4/stdlib"
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

func GetExplorerState(key string, returnValue interface{}) (interface{}, error) {
	entry := dbtypes.ExplorerState{}
	err := ReaderDb.Get(&entry, `SELECT key, value FROM explorer_state WHERE Key = $1`, key)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(entry.Value), returnValue)
	if err != nil {
		return nil, err
	}
	return returnValue, nil
}

func SetExplorerState(key string, value interface{}, tx *sqlx.Tx) error {
	valueMarshal, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
	INSERT INTO explorer_state (Key, Value)
	VALUES ($1, $2)
	ON CONFLICT (Key) DO UPDATE SET
		value = excluded.value
	`, key, valueMarshal)
	if err != nil {
		return err
	}
	return nil
}

func IsEpochSynchronized(epoch uint64) bool {
	var count uint64
	err := ReaderDb.Get(&count, `SELECT COUNT(*) FROM epochs WHERE epoch = $1`, epoch)
	if err != nil {
		return false
	}
	return count > 0
}

func InsertBlock(block *dbtypes.Block, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
	INSERT INTO blocks (
		root, slot, parent_root, state_root, orphaned, proposer, graffiti, 
		attestation_count, deposit_count, exit_count, attester_slashing_count, proposer_slashing_count, 
		bls_change_count, eth_transaction_count, sync_participation
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	ON CONFLICT (root) DO NOTHING`,
		block.Root, block.Slot, block.ParentRoot, block.StateRoot, block.Orphaned, block.Proposer, block.Graffiti,
		block.AttestationCount, block.DepositCount, block.ExitCount, block.AttesterSlashingCount, block.ProposerSlashingCount,
		block.BLSChangeCount, block.EthTransactionCount, block.SyncParticipation)
	if err != nil {
		return err
	}
	return nil
}

func InsertEpoch(epoch *dbtypes.Epoch, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
	INSERT INTO epochs (
		epoch, validator_count, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
		attestation_count, deposit_count, exit_count, attester_slashing_count, proposer_slashing_count, 
		bls_change_count, eth_transaction_count, sync_participation
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	ON CONFLICT (Epoch) DO UPDATE SET
		validator_count = excluded.validator_count,
		eligible = excluded.eligible,
		voted_target = excluded.voted_target,
		voted_head = excluded.voted_head, 
		voted_total = excluded.voted_total, 
		block_count = excluded.block_count,
		orphaned_count = excluded.orphaned_count,
		attestation_count = excluded.attestation_count, 
		deposit_count = excluded.deposit_count, 
		exit_count = excluded.exit_count, 
		attester_slashing_count = excluded.attester_slashing_count, 
		proposer_slashing_count = excluded.proposer_slashing_count, 
		bls_change_count = excluded.bls_change_count, 
		eth_transaction_count = excluded.eth_transaction_count, 
		sync_participation = excluded.sync_participation
	`,
		epoch.Epoch, epoch.ValidatorCount, epoch.Eligible, epoch.VotedTarget, epoch.VotedHead, epoch.VotedTotal, epoch.BlockCount, epoch.OrphanedCount,
		epoch.AttestationCount, epoch.DepositCount, epoch.ExitCount, epoch.AttesterSlashingCount, epoch.ProposerSlashingCount, epoch.BLSChangeCount,
		epoch.EthTransactionCount, epoch.SyncParticipation)
	if err != nil {
		return err
	}
	return nil
}

func InsertOrphanedBlock(block *dbtypes.OrphanedBlock, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
	INSERT INTO orphaned_blocks (
		root, header, block
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	ON CONFLICT (Root) DO NOTHING`,
		block.Root, block.Header, block.Block)
	if err != nil {
		return err
	}
	return nil
}
