package db

import (
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
	"github.com/mitchellh/mapstructure"
)

func InsertSlot(slot *dbtypes.Slot, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO slots (
				slot, proposer, status, root, parent_root, state_root, graffiti, graffiti_text,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, 
				eth_block_extra, eth_block_extra_text, sync_participation, fork_id, payload_status
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
			ON CONFLICT (slot, root) DO UPDATE SET
				status = excluded.status,
				eth_block_extra = excluded.eth_block_extra,
				eth_block_extra_text = excluded.eth_block_extra_text,
				fork_id = excluded.fork_id,
				payload_status = excluded.payload_status`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO slots (
				slot, proposer, status, root, parent_root, state_root, graffiti, graffiti_text,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, 
				eth_block_extra, eth_block_extra_text, sync_participation, fork_id, payload_status
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`,
	}),
		slot.Slot, slot.Proposer, slot.Status, slot.Root, slot.ParentRoot, slot.StateRoot, slot.Graffiti, slot.GraffitiText,
		slot.AttestationCount, slot.DepositCount, slot.ExitCount, slot.WithdrawCount, slot.WithdrawAmount, slot.AttesterSlashingCount,
		slot.ProposerSlashingCount, slot.BLSChangeCount, slot.EthTransactionCount, slot.EthBlockNumber, slot.EthBlockHash,
		slot.EthBlockExtra, slot.EthBlockExtraText, slot.SyncParticipation, slot.ForkId, slot.PayloadStatus)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM slots WHERE slot = $1 AND proposer = $2 AND status = 0", slot.Slot, slot.Proposer)
	if err != nil {
		return err
	}

	return nil
}

func InsertMissingSlot(block *dbtypes.SlotHeader, tx *sqlx.Tx) error {
	var blockCount int
	err := ReaderDb.Get(&blockCount, `
		SELECT
			COUNT(*)
		FROM slots
		WHERE slot = $1 AND proposer = $2
	`, block.Slot, block.Proposer)
	if err != nil {
		return err
	}

	if blockCount > 0 {
		return nil
	}

	_, err = tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO slots (
				slot, proposer, status, root
			) VALUES ($1, $2, $3, '0x')
			ON CONFLICT (slot, root) DO UPDATE SET
			proposer = excluded.proposer`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO slots (
				slot, proposer, status, root
			) VALUES ($1, $2, $3, '0x')`,
	}),
		block.Slot, block.Proposer, block.Status)
	if err != nil {
		return err
	}
	return nil
}

func GetSlotsRange(firstSlot uint64, lastSlot uint64, withMissing bool, withOrphaned bool) []*dbtypes.AssignedSlot {
	var sql strings.Builder
	fmt.Fprintf(&sql, `SELECT slots.slot, slots.proposer`)
	blockFields := []string{
		"state_root", "root", "slot", "proposer", "status", "parent_root", "graffiti", "graffiti_text",
		"attestation_count", "deposit_count", "exit_count", "withdraw_count", "withdraw_amount", "attester_slashing_count",
		"proposer_slashing_count", "bls_change_count", "eth_transaction_count", "eth_block_number", "eth_block_hash",
		"eth_block_extra", "eth_block_extra_text", "sync_participation", "fork_id", "payload_status",
	}
	for _, blockField := range blockFields {
		fmt.Fprintf(&sql, ", slots.%v AS \"block.%v\"", blockField, blockField)
	}
	fmt.Fprintf(&sql, ` FROM slots `)
	fmt.Fprintf(&sql, ` WHERE slot <= $1 AND slot >= $2 `)

	if !withMissing {
		fmt.Fprintf(&sql, ` AND slots.status != 0 `)
	}
	if !withOrphaned {
		fmt.Fprintf(&sql, ` AND slots.status != 2 `)
	}
	fmt.Fprintf(&sql, ` ORDER BY slot DESC `)

	rows, err := ReaderDb.Query(sql.String(), firstSlot, lastSlot)
	if err != nil {
		logger.WithError(err).Errorf("Error while fetching slots range: %v", sql.String())
		return nil
	}

	return parseAssignedSlots(rows, blockFields, 2)
}

func GetSlotsByParentRoot(parentRoot []byte) []*dbtypes.Slot {
	slots := []*dbtypes.Slot{}
	err := ReaderDb.Select(&slots, `
	SELECT
		slot, proposer, status, root, parent_root, state_root, graffiti, graffiti_text,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, 
		eth_block_extra, eth_block_extra_text, sync_participation, fork_id, payload_status
	FROM slots
	WHERE parent_root = $1
	ORDER BY slot DESC
	`, parentRoot)
	if err != nil {
		logger.Errorf("Error while fetching slots by parent root: %v", err)
		return nil
	}
	return slots
}

func GetSlotByRoot(root []byte) *dbtypes.Slot {
	block := dbtypes.Slot{}
	err := ReaderDb.Get(&block, `
	SELECT
		root, slot, parent_root, state_root, status, proposer, graffiti, graffiti_text,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash,
		eth_block_extra, eth_block_extra_text, sync_participation, fork_id, payload_status
	FROM slots
	WHERE root = $1
	`, root)
	if err != nil {
		//logger.Errorf("Error while fetching block by root 0x%x: %v", root, err)
		return nil
	}
	return &block
}

func GetSlotsByRoots(roots [][]byte) map[phase0.Root]*dbtypes.Slot {
	argIdx := 0
	args := make([]any, len(roots))
	plcList := make([]string, len(roots))
	for i, root := range roots {
		plcList[i] = fmt.Sprintf("$%v", argIdx+1)
		args[argIdx] = root
		argIdx += 1
	}

	sql := fmt.Sprintf(
		`SELECT
			root, slot, parent_root, state_root, status, proposer, graffiti, graffiti_text,
			attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
			proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash,
			eth_block_extra, eth_block_extra_text, sync_participation, fork_id, payload_status
		FROM slots
		WHERE root IN (%v)
		ORDER BY slot DESC`,
		strings.Join(plcList, ", "),
	)

	slots := []*dbtypes.Slot{}
	err := ReaderDb.Select(&slots, sql, args...)
	if err != nil {
		logger.Errorf("Error while fetching block by roots: %v", err)
		return nil
	}

	slotMap := make(map[phase0.Root]*dbtypes.Slot)
	for _, slot := range slots {
		slotMap[phase0.Root(slot.Root)] = slot
	}

	return slotMap
}

func GetBlockHeadByRoot(root []byte) *dbtypes.BlockHead {
	blockHead := dbtypes.BlockHead{}
	err := ReaderDb.Get(&blockHead, `
	SELECT
		root, slot, parent_root, fork_id
	FROM slots
	WHERE root = $1
	`, root)
	if err != nil {
		return nil
	}
	return &blockHead
}

func GetSlotsByBlockHash(blockHash []byte) []*dbtypes.Slot {
	slots := []*dbtypes.Slot{}
	err := ReaderDb.Select(&slots, `
	SELECT
		slot, proposer, status, root, parent_root, state_root, graffiti, graffiti_text,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
		proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, 
		eth_block_extra, eth_block_extra_text, sync_participation, fork_id, payload_status
	FROM slots
	WHERE eth_block_hash = $1
	ORDER BY slot DESC
	`, blockHash)
	if err != nil {
		logger.Errorf("Error while fetching slots by block hash: %v", err)
		return nil
	}
	return slots
}

func parseAssignedSlots(rows *sql.Rows, fields []string, fieldsOffset int) []*dbtypes.AssignedSlot {
	blockAssignments := []*dbtypes.AssignedSlot{}

	scanArgs := make([]interface{}, len(fields)+fieldsOffset)
	for rows.Next() {
		scanVals := make([]interface{}, len(fields)+fieldsOffset)
		for i := range scanArgs {
			scanArgs[i] = &scanVals[i]
		}
		err := rows.Scan(scanArgs...)
		if err != nil {
			logger.Errorf("Error while parsing assigned block: %v", err)
			continue
		}

		blockAssignment := dbtypes.AssignedSlot{}
		blockAssignment.Slot = uint64(scanVals[0].(int64))
		blockAssignment.Proposer = uint64(scanVals[1].(int64))

		if scanVals[2] != nil {
			blockValMap := map[string]interface{}{}
			for idx, fName := range fields {
				blockValMap[fName] = scanVals[idx+fieldsOffset]
			}
			var block dbtypes.Slot
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

func GetFilteredSlots(filter *dbtypes.BlockFilter, firstSlot uint64, offset uint64, limit uint32) []*dbtypes.AssignedSlot {
	var sql strings.Builder
	fmt.Fprintf(&sql, `SELECT slots.slot, slots.proposer`)
	blockFields := []string{
		"state_root", "root", "slot", "proposer", "status", "parent_root", "graffiti", "graffiti_text",
		"attestation_count", "deposit_count", "exit_count", "withdraw_count", "withdraw_amount", "attester_slashing_count",
		"proposer_slashing_count", "bls_change_count", "eth_transaction_count", "eth_block_number", "eth_block_hash",
		"eth_block_extra", "eth_block_extra_text", "sync_participation", "fork_id", "payload_status",
	}
	for _, blockField := range blockFields {
		fmt.Fprintf(&sql, ", slots.%v AS \"block.%v\"", blockField, blockField)
	}
	fmt.Fprintf(&sql, ` FROM slots `)
	if filter.ProposerName != "" {
		fmt.Fprintf(&sql, ` LEFT JOIN validator_names ON validator_names."index" = slots.proposer `)
	}

	argIdx := 0
	args := make([]any, 0)

	argIdx++
	fmt.Fprintf(&sql, ` WHERE slots.slot < $%v `, argIdx)
	args = append(args, firstSlot)

	if filter.WithMissing == 0 {
		fmt.Fprintf(&sql, ` AND slots.status != 0 `)
	} else if filter.WithMissing == 2 {
		fmt.Fprintf(&sql, ` AND slots.status = 0 `)
	}
	if filter.WithOrphaned == 0 {
		fmt.Fprintf(&sql, ` AND slots.status != 2 `)
	} else if filter.WithOrphaned == 2 {
		fmt.Fprintf(&sql, ` AND slots.status = 2 `)
	}
	if filter.ProposerIndex != nil {
		argIdx++
		fmt.Fprintf(&sql, ` AND slots.proposer = $%v `, argIdx)
		args = append(args, *filter.ProposerIndex)
	}
	if filter.Graffiti != "" {
		argIdx++
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` AND slots.graffiti_text ilike $%v `,
			dbtypes.DBEngineSqlite: ` AND slots.graffiti_text LIKE $%v `,
		}), argIdx)
		args = append(args, "%"+filter.Graffiti+"%")
	}
	if filter.ExtraData != "" {
		argIdx++
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` AND slots.eth_block_extra_text ilike $%v `,
			dbtypes.DBEngineSqlite: ` AND slots.eth_block_extra_text LIKE $%v `,
		}), argIdx)
		args = append(args, "%"+filter.ExtraData+"%")
	}
	if filter.ProposerName != "" {
		argIdx++
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` AND validator_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` AND validator_names.name LIKE $%v `,
		}), argIdx)
		args = append(args, "%"+filter.ProposerName+"%")
	}

	fmt.Fprintf(&sql, `	ORDER BY slots.slot DESC `)
	fmt.Fprintf(&sql, ` LIMIT $%v OFFSET $%v `, argIdx+1, argIdx+2)
	argIdx += 2
	args = append(args, limit)
	args = append(args, offset)

	//fmt.Printf("sql: %v, args: %v\n", sql.String(), args)
	rows, err := ReaderDb.Query(sql.String(), args...)
	if err != nil {
		logger.WithError(err).Errorf("Error while fetching filtered slots: %v", sql.String())
		return nil
	}

	return parseAssignedSlots(rows, blockFields, 2)
}

func GetSlotStatus(blockRoots [][]byte) []*dbtypes.BlockStatus {
	orphanedRefs := []*dbtypes.BlockStatus{}
	if len(blockRoots) == 0 {
		return orphanedRefs
	}
	var sql strings.Builder
	fmt.Fprintf(&sql, `
	SELECT
		root, status
	FROM slots
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
		logger.Errorf("Error while fetching orphaned status: %v", err)
		return nil
	}
	return orphanedRefs
}

func GetHighestRootBeforeSlot(slot uint64, withOrphaned bool) []byte {
	var result []byte
	statusFilter := ""
	if !withOrphaned {
		statusFilter = "AND status != 2"
	}

	err := ReaderDb.Get(&result, `
	SELECT root FROM slots WHERE slot < $1 `+statusFilter+` AND status != 0 ORDER BY slot DESC LIMIT 1
	`, slot)
	if err != nil {
		return nil
	}
	return result
}

func GetSlotAssignment(slot uint64) uint64 {
	proposer := uint64(math.MaxInt64)
	err := ReaderDb.Get(&proposer, `
	SELECT
		proposer
	FROM slots
	WHERE slot = $1 AND status IN (0, 1)
	`, slot)
	if err != nil {
		return math.MaxInt64
	}
	return proposer
}
