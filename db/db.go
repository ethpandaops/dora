package db

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/types"
	"github.com/pk910/light-beaconchain-explorer/utils"

	"github.com/jackc/pgx/v4/pgxpool"
	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/mitchellh/mapstructure"
)

//go:embed schema/pgsql/*.sql
var EmbedPgsqlSchema embed.FS

//go:embed schema/sqlite/*.sql
var EmbedSqliteSchema embed.FS

var DBPGX *pgxpool.Conn

// DB is a pointer to the explorer-database
var DbEngine dbtypes.DBEngineType
var WriterDb *sqlx.DB
var ReaderDb *sqlx.DB

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
	dbConn, err := sqlx.Open("sqlite3", fmt.Sprintf("%s?cache=shared", config.File))
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
		WriterDb, ReaderDb = mustInitSqlite(sqliteConfig)
	} else if utils.Config.Database.Engine == "pgsql" {
		readerConfig := (*types.PgsqlDatabaseConfig)(&utils.Config.Database.Pgsql)
		writerConfig := (*types.PgsqlDatabaseConfig)(&utils.Config.Database.PgsqlWriter)
		if writerConfig.Host == "" {
			writerConfig = readerConfig
		}
		DbEngine = dbtypes.DBEnginePgsql
		WriterDb, ReaderDb = mustInitPgsql(writerConfig, readerConfig)
	} else {
		logger.Fatalf("unknown database engine type: %s", utils.Config.Database.Engine)
	}
}

func MustCloseDB() {
	err := WriterDb.Close()
	if err != nil {
		logger.Errorf("Error closing writer db connection: %v", err)
	}
	err = ReaderDb.Close()
	if err != nil {
		logger.Errorf("Error closing reader db connection: %v", err)
	}
}

func ApplyEmbeddedDbSchema(version int64) error {
	var engineDialect string
	var schemaDirectory string
	switch DbEngine {
	case dbtypes.DBEnginePgsql:
		goose.SetBaseFS(EmbedPgsqlSchema)
		engineDialect = "postgres"
		schemaDirectory = "schema/pgsql"
		break
	case dbtypes.DBEngineSqlite:
		goose.SetBaseFS(EmbedSqliteSchema)
		engineDialect = "sqlite3"
		schemaDirectory = "schema/sqlite"
		break
	default:
		logger.Fatalf("unknown database engine")
	}

	fmt.Printf(engineDialect)
	if err := goose.SetDialect(engineDialect); err != nil {
		return err
	}

	if version == -2 {
		if err := goose.Up(WriterDb.DB, schemaDirectory); err != nil {
			return err
		}
	} else if version == -1 {
		if err := goose.UpByOne(WriterDb.DB, schemaDirectory); err != nil {
			return err
		}
	} else {
		if err := goose.UpTo(WriterDb.DB, schemaDirectory, version); err != nil {
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

func GetExplorerState(key string, returnValue interface{}) (interface{}, error) {
	entry := dbtypes.ExplorerState{}
	err := ReaderDb.Get(&entry, `SELECT key, value FROM explorer_state WHERE key = $1`, key)
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
	_, err = tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO explorer_state (key, value)
			VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET
				value = excluded.value`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO explorer_state (key, value)
			VALUES ($1, $2)`,
	}), key, valueMarshal)
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
	fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "INSERT INTO slot_assignments (slot, proposer) VALUES ",
		dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO slot_assignments (slot, proposer) VALUES ",
	}))
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
	fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot) DO UPDATE SET proposer = excluded.proposer",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func InsertBlock(block *dbtypes.Block, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO blocks (
				root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
			ON CONFLICT (root) DO UPDATE SET
				orphaned = excluded.orphaned`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO blocks (
				root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
	}),
		block.Root, block.Slot, block.ParentRoot, block.StateRoot, block.Orphaned, block.Proposer, block.Graffiti, block.GraffitiText,
		block.AttestationCount, block.DepositCount, block.ExitCount, block.WithdrawCount, block.WithdrawAmount, block.AttesterSlashingCount,
		block.ProposerSlashingCount, block.BLSChangeCount, block.EthTransactionCount, block.EthBlockNumber, block.EthBlockHash, block.SyncParticipation)
	if err != nil {
		return err
	}
	return nil
}

func InsertEpoch(epoch *dbtypes.Epoch, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
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
				sync_participation = excluded.sync_participation`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO epochs (
				epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
	}),
		epoch.Epoch, epoch.ValidatorCount, epoch.ValidatorBalance, epoch.Eligible, epoch.VotedTarget, epoch.VotedHead, epoch.VotedTotal, epoch.BlockCount, epoch.OrphanedCount,
		epoch.AttestationCount, epoch.DepositCount, epoch.ExitCount, epoch.WithdrawCount, epoch.WithdrawAmount, epoch.AttesterSlashingCount, epoch.ProposerSlashingCount,
		epoch.BLSChangeCount, epoch.EthTransactionCount, epoch.SyncParticipation)
	if err != nil {
		return err
	}
	return nil
}

func InsertOrphanedBlock(block *dbtypes.OrphanedBlock, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO orphaned_blocks (
				root, header, block
			) VALUES ($1, $2, $3)
			ON CONFLICT (root) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE orphaned_blocks (
				root, header, block
			) VALUES ($1, $2, $3)`,
	}),
		block.Root, block.Header, block.Block)
	if err != nil {
		return err
	}
	return nil
}

func GetOrphanedBlock(root []byte) *dbtypes.OrphanedBlock {
	block := dbtypes.OrphanedBlock{}
	err := ReaderDb.Get(&block, `
	SELECT root, header, block
	FROM orphaned_blocks
	WHERE root = $1
	`, root)
	if err != nil {
		return nil
	}
	return &block
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
		logger.Errorf("Error while fetching blocks for slot: %v", err)
		return nil
	}
	return blocks
}

func GetBlocksByParentRoot(parentRoot []byte) []*dbtypes.Block {
	blocks := []*dbtypes.Block{}
	err := ReaderDb.Select(&blocks, `
	SELECT
		root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
	FROM blocks
	WHERE parent_root = $1
	ORDER BY slot DESC
	`, parentRoot)
	if err != nil {
		logger.Errorf("Error while fetching blocks by parent root: %v", err)
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
	err := ReaderDb.Select(&blocks, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			SELECT
				root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
			FROM blocks
			WHERE graffiti_text ilike $1 AND slot < $2 ` + orphanedLimit + `
			ORDER BY slot DESC
			LIMIT $3 OFFSET $4`,
		dbtypes.DBEngineSqlite: `
			SELECT
				root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
			FROM blocks
			WHERE graffiti_text LIKE $1 AND slot < $2 ` + orphanedLimit + `
			ORDER BY slot DESC
			LIMIT $3 OFFSET $4`,
	}), "%"+graffiti+"%", firstSlot, limit, offset)
	if err != nil {
		logger.Errorf("Error while fetching blocks with graffiti: %v", err)
		return nil
	}
	return blocks
}

func GetAssignedBlocks(proposer uint64, firstSlot uint64, offset uint64, limit uint32, withOrphaned bool) []*dbtypes.AssignedBlock {
	blockAssignments := []*dbtypes.AssignedBlock{}
	orphanedLimit := ""
	if !withOrphaned {
		orphanedLimit = "AND NOT orphaned"
	}
	var sql strings.Builder
	fmt.Fprintf(&sql, `SELECT slot_assignments.slot, slot_assignments.proposer`)
	blockFields := []string{
		"root", "slot", "parent_root", "state_root", "orphaned", "proposer", "graffiti", "graffiti_text",
		"attestation_count", "deposit_count", "exit_count", "withdraw_count", "withdraw_amount", "attester_slashing_count",
		"proposer_slashing_count", "bls_change_count", "eth_transaction_count", "eth_block_number", "eth_block_hash", "sync_participation",
	}
	for _, blockField := range blockFields {
		fmt.Fprintf(&sql, ", blocks.%v AS \"block.%v\"", blockField, blockField)
	}
	fmt.Fprintf(&sql, `
	FROM slot_assignments
	LEFT JOIN blocks ON blocks.slot = slot_assignments.slot
	WHERE (slot_assignments.proposer = $1 OR blocks.proposer = $1) AND slot_assignments.slot < $2 `+orphanedLimit+`
	ORDER BY slot_assignments.slot DESC
	LIMIT $3 OFFSET $4
	`)
	rows, err := ReaderDb.Query(sql.String(), proposer, firstSlot, limit, offset)
	if err != nil {
		logger.Errorf("Error while fetching assigned blocks: %v", err)
		return nil
	}

	scanArgs := make([]interface{}, len(blockFields)+2)
	for rows.Next() {
		scanVals := make([]interface{}, len(blockFields)+2)
		for i := range scanArgs {
			scanArgs[i] = &scanVals[i]
		}
		err := rows.Scan(scanArgs...)
		if err != nil {
			logger.Errorf("Error while parsing assigned block: %v", err)
			continue
		}

		blockAssignment := dbtypes.AssignedBlock{}
		blockAssignment.Slot = uint64(scanVals[0].(int64))
		blockAssignment.Proposer = uint64(scanVals[1].(int64))

		if scanVals[2] != nil {
			blockValMap := map[string]interface{}{}
			for idx, fName := range blockFields {
				blockValMap[fName] = scanVals[idx+2]
			}
			var block dbtypes.Block
			cfg := &mapstructure.DecoderConfig{
				Metadata: nil,
				Result:   &block,
				TagName:  "db",
			}
			decoder, _ := mapstructure.NewDecoder(cfg)
			decoder.Decode(blockValMap)
			blockAssignment.Block = &block
		}

		blockAssignments = append(blockAssignments, &blockAssignment)
	}

	return blockAssignments
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

func GetBlockOrphanedRefs(blockRoots [][]byte) []*dbtypes.BlockOrphanedRef {
	orphanedRefs := []*dbtypes.BlockOrphanedRef{}
	if len(blockRoots) == 0 {
		return orphanedRefs
	}
	var sql strings.Builder
	fmt.Fprintf(&sql, `
	SELECT
		root, orphaned
	FROM blocks
	WHERE root in (`)
	argIdx := 0
	args := make([]any, len(blockRoots))
	for i, root := range blockRoots {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%v", argIdx+1)
		args[argIdx] = root
		argIdx += 1
	}
	fmt.Fprintf(&sql, ")")
	err := ReaderDb.Select(&orphanedRefs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching blocks: %v", err)
		return nil
	}
	return orphanedRefs
}

func GetHighestRootBeforeSlot(slot uint64, withOrphaned bool) []byte {
	var result []byte
	orphanedLimit := ""
	if !withOrphaned {
		orphanedLimit = "AND NOT orphaned"
	}

	err := ReaderDb.Get(&result, `
	SELECT root FROM blocks WHERE slot < $1 `+orphanedLimit+` ORDER BY slot DESC LIMIT 1
	`, slot)
	if err != nil {
		logger.Errorf("Error while fetching highest root before %v: %v", slot, err)
		return nil
	}
	return result
}

func InsertUnfinalizedBlock(block *dbtypes.UnfinalizedBlock, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO unfinalized_blocks (
				root, slot, header, block
			) VALUES ($1, $2, $3, $4)
			ON CONFLICT (root) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE INTO unfinalized_blocks (
				root, slot, header, block
			) VALUES ($1, $2, $3, $4)`,
	}),
		block.Root, block.Slot, block.Header, block.Block)
	if err != nil {
		return err
	}
	return nil
}

func GetUnfinalizedBlockHeader() []*dbtypes.UnfinalizedBlockHeader {
	blockRefs := []*dbtypes.UnfinalizedBlockHeader{}
	err := ReaderDb.Select(&blockRefs, `
	SELECT
		root, slot, header
	FROM unfinalized_blocks
	`)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized block refs: %v", err)
		return nil
	}
	return blockRefs
}

func GetUnfinalizedBlock(root []byte) *dbtypes.UnfinalizedBlock {
	block := dbtypes.UnfinalizedBlock{}
	err := ReaderDb.Get(&block, `
	SELECT root, slot, header, block
	FROM unfinalized_blocks
	WHERE root = $1
	`, root)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized block 0x%x: %v", root, err)
		return nil
	}
	return &block
}

func InsertUnfinalizedEpochDuty(epochDuty *dbtypes.UnfinalizedEpochDuty, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO unfinalized_duties (
				epoch, dependent_root, duties
			) VALUES ($1, $2, $3)
			ON CONFLICT (root) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE INTO unfinalized_duties (
				epoch, dependent_root, duties
			) VALUES ($1, $2, $3)`,
	}),
		epochDuty.Epoch, epochDuty.DependentRoot, epochDuty.Duties)
	if err != nil {
		return err
	}
	return nil
}

func GetUnfinalizedEpochDutyRefs() []*dbtypes.UnfinalizedEpochDutyRef {
	dutyRefs := []*dbtypes.UnfinalizedEpochDutyRef{}
	err := ReaderDb.Select(&dutyRefs, `
	SELECT
		epoch, dependent_root
	FROM unfinalized_duties
	`)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized duty refs: %v", err)
		return nil
	}
	return dutyRefs
}

func GetUnfinalizedDuty(epoch uint64, dependentRoot []byte) *dbtypes.UnfinalizedEpochDuty {
	epochDuty := dbtypes.UnfinalizedEpochDuty{}
	err := ReaderDb.Get(&epochDuty, `
	SELECT epoch, dependent_root, duties
	FROM unfinalized_duties
	WHERE epoch = $1 AND dependent_root = $2
	`, epoch, dependentRoot)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized duty %v/0x%x: %v", epoch, dependentRoot, err)
		return nil
	}
	return &epochDuty
}

func DeleteUnfinalizedBefore(slot uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`DELETE FROM unfinalized_blocks WHERE slot < $1`, slot)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM unfinalized_duties WHERE epoch < $1`, utils.EpochOfSlot(slot))
	if err != nil {
		return err
	}
	return nil
}
