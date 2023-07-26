package db

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
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

func InsertSlotAssignments(slotAssignments []*dbtypes.SlotAssignment, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprintf(&sql, "INSERT INTO slot_assignments (slot, proposer) VALUES ")
	argIdx := 0
	args := make([]any, len(slotAssignments)*2)
	for i, slotAssignment := range slotAssignments {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "($%v, $%v)", argIdx+1, argIdx+2)
		args[argIdx] = slotAssignment.Slot
		args[argIdx+1] = slotAssignment.Proposer
		argIdx += 2
	}
	fmt.Fprintf(&sql, " ON CONFLICT (slot) DO UPDATE SET proposer = excluded.proposer")
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func InsertBlock(block *dbtypes.Block, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
	INSERT INTO blocks (
		root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	ON CONFLICT (root) DO UPDATE SET
		orphaned = excluded.orphaned
	`,
		block.Root, block.Slot, block.ParentRoot, block.StateRoot, block.Orphaned, block.Proposer, block.Graffiti, block.GraffitiText,
		block.AttestationCount, block.DepositCount, block.ExitCount, block.WithdrawCount, block.WithdrawAmount, block.AttesterSlashingCount,
		block.ProposerSlashingCount, block.BLSChangeCount, block.EthTransactionCount, block.EthBlockNumber, block.EthBlockHash, block.SyncParticipation)
	if err != nil {
		return err
	}
	return nil
}

func InsertEpoch(epoch *dbtypes.Epoch, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
	INSERT INTO epochs (
		epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	ON CONFLICT (epoch) DO UPDATE SET
		validator_count = excluded.validator_count,
		validator_balance = excluded.validator_balance,
		eligible = excluded.eligible,
		voted_target = excluded.voted_target,
		voted_head = excluded.voted_head, 
		voted_total = excluded.voted_total, 
		block_count = excluded.block_count,
		orphaned_count = excluded.orphaned_count,
		attestation_count = excluded.attestation_count, 
		deposit_count = excluded.deposit_count, 
		exit_count = excluded.exit_count, 
		withdraw_count = excluded.withdraw_count, 
		withdraw_amount = excluded.withdraw_amount, 
		attester_slashing_count = excluded.attester_slashing_count, 
		proposer_slashing_count = excluded.proposer_slashing_count, 
		bls_change_count = excluded.bls_change_count, 
		eth_transaction_count = excluded.eth_transaction_count, 
		sync_participation = excluded.sync_participation
	`,
		epoch.Epoch, epoch.ValidatorCount, epoch.ValidatorBalance, epoch.Eligible, epoch.VotedTarget, epoch.VotedHead, epoch.VotedTotal, epoch.BlockCount, epoch.OrphanedCount,
		epoch.AttestationCount, epoch.DepositCount, epoch.ExitCount, epoch.WithdrawCount, epoch.WithdrawAmount, epoch.AttesterSlashingCount, epoch.ProposerSlashingCount,
		epoch.BLSChangeCount, epoch.EthTransactionCount, epoch.SyncParticipation)
	if err != nil {
		return err
	}
	return nil
}

func InsertOrphanedBlock(block *dbtypes.OrphanedBlock, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
	INSERT INTO orphaned_blocks (
		root, header, block
	) VALUES ($1, $2, $3)
	ON CONFLICT (root) DO NOTHING`,
		block.Root, block.Header, block.Block)
	if err != nil {
		return err
	}
	return nil
}

func GetEpochs(firstEpoch uint64, limit uint32) []*dbtypes.Epoch {
	epochs := []*dbtypes.Epoch{}
	err := ReaderDb.Select(&epochs, `
	SELECT
		epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count,
		proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation
	FROM epochs
	WHERE epoch <= $1
	ORDER BY epoch DESC
	LIMIT $2
	`, firstEpoch, limit)
	if err != nil {
		logger.Errorf("Error while fetching epochs: %v", err)
		return nil
	}
	return epochs
}

func GetBlocks(firstBlock uint64, limit uint32, withOrphaned bool) []*dbtypes.Block {
	blocks := []*dbtypes.Block{}
	orphanedLimit := ""
	if !withOrphaned {
		orphanedLimit = "AND NOT orphaned"
	}
	err := ReaderDb.Select(&blocks, `
	SELECT
		root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
	FROM blocks
	WHERE slot <= $1 `+orphanedLimit+`
	ORDER BY slot DESC
	LIMIT $2
	`, firstBlock, limit)
	if err != nil {
		logger.Errorf("Error while fetching blocks: %v", err)
		return nil
	}
	return blocks
}

func GetBlocksForSlots(firstSlot uint64, lastSlot uint64, withOrphaned bool) []*dbtypes.Block {
	blocks := []*dbtypes.Block{}
	orphanedLimit := ""
	if !withOrphaned {
		orphanedLimit = "AND NOT orphaned"
	}
	err := ReaderDb.Select(&blocks, `
	SELECT
		root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
	FROM blocks
	WHERE slot <= $1 AND slot >= $2 `+orphanedLimit+`
	ORDER BY slot DESC
	`, firstSlot, lastSlot)
	if err != nil {
		logger.Errorf("Error while fetching blocks: %v", err)
		return nil
	}
	return blocks
}

func GetBlocksWithGraffiti(graffiti string, firstSlot uint64, offset uint64, limit uint32, withOrphaned bool) []*dbtypes.Block {
	blocks := []*dbtypes.Block{}
	orphanedLimit := ""
	if !withOrphaned {
		orphanedLimit = "AND NOT orphaned"
	}
	err := ReaderDb.Select(&blocks, `
	SELECT
		root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
	FROM blocks
	WHERE graffiti_text ilike $1 AND slot < $2 `+orphanedLimit+`
	ORDER BY slot DESC
	LIMIT $3 OFFSET $4
	`, "%"+graffiti+"%", firstSlot, limit, offset)
	if err != nil {
		logger.Errorf("Error while fetching blocks: %v", err)
		return nil
	}
	return blocks
}

func GetSlotAssignmentsForSlots(firstSlot uint64, lastSlot uint64) []*dbtypes.SlotAssignment {
	assignments := []*dbtypes.SlotAssignment{}
	err := ReaderDb.Select(&assignments, `
	SELECT
		slot, proposer
	FROM slot_assignments
	WHERE slot <= $1 AND slot >= $2 
	`, firstSlot, lastSlot)
	if err != nil {
		logger.Errorf("Error while fetching blocks: %v", err)
		return nil
	}
	return assignments
}
