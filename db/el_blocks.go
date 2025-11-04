package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// GetElBlock retrieves a block by its hash
func GetElBlock(hash []byte) (*dbtypes.ElBlock, error) {
	block := &dbtypes.ElBlock{}
	err := ReaderDb.Get(block, `
		SELECT *
		FROM el_blocks
		WHERE hash = $1
	`, hash)
	if err != nil {
		return nil, err
	}
	return block, nil
}

// GetElBlockByNumber retrieves a block by its number (returns canonical block based on fork IDs)
func GetElBlockByNumber(number uint64, forkIds []uint64) (*dbtypes.ElBlock, error) {
	block := &dbtypes.ElBlock{}

	var sql strings.Builder
	args := []interface{}{number}

	fmt.Fprint(&sql, `
		SELECT *
		FROM el_blocks
		WHERE number = $1 AND orphaned = false
	`)

	if len(forkIds) > 0 {
		args = append(args, forkIds)
		fmt.Fprintf(&sql, " AND fork_id = ANY($%v)", len(args))
	}

	fmt.Fprint(&sql, " ORDER BY fork_id DESC LIMIT 1")

	err := ReaderDb.Get(block, sql.String(), args...)
	if err != nil {
		return nil, err
	}
	return block, nil
}

// GetElBlocksFiltered retrieves blocks with optional filtering and pagination
func GetElBlocksFiltered(offset uint, limit uint, forkIds []uint64, filter *dbtypes.ElBlockFilter) ([]*dbtypes.ElBlock, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_blocks
	`)

	filterOp := "WHERE"

	// Always filter by fork IDs if provided
	if len(forkIds) > 0 {
		args = append(args, forkIds)
		fmt.Fprintf(&sql, " %v fork_id = ANY($%v)", filterOp, len(args))
		filterOp = "AND"
	}

	if filter != nil {
		if filter.MinNumber != nil {
			args = append(args, *filter.MinNumber)
			fmt.Fprintf(&sql, " %v number >= $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.MaxNumber != nil {
			args = append(args, *filter.MaxNumber)
			fmt.Fprintf(&sql, " %v number <= $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.MinTimestamp != nil {
			args = append(args, *filter.MinTimestamp)
			fmt.Fprintf(&sql, " %v timestamp >= $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.MaxTimestamp != nil {
			args = append(args, *filter.MaxTimestamp)
			fmt.Fprintf(&sql, " %v timestamp <= $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.FeeRecipient != nil {
			args = append(args, filter.FeeRecipient)
			fmt.Fprintf(&sql, " %v fee_recipient = $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.MinGasUsed != nil {
			args = append(args, *filter.MinGasUsed)
			fmt.Fprintf(&sql, " %v gas_used >= $%v", filterOp, len(args))
			filterOp = "AND"
		}
		if filter.WithOrphaned == 1 {
			fmt.Fprintf(&sql, " %v orphaned = true", filterOp)
			filterOp = "AND"
		} else if filter.WithOrphaned == 2 {
			fmt.Fprintf(&sql, " %v orphaned = false", filterOp)
			filterOp = "AND"
		}
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY number DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	// Count total
	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	// Get results (rebuild args without offset for SELECT)
	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	blocks := []*dbtypes.ElBlock{}
	err := ReaderDb.Select(&blocks, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return blocks, totalCount, nil
}

// InsertElBlock inserts a new block
func InsertElBlock(block *dbtypes.ElBlock, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_blocks ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_blocks ",
		}),
		"(number, hash, parent_hash, timestamp, fee_recipient, state_root, receipts_root, logs_bloom, ",
		"difficulty, total_difficulty, size, gas_used, gas_limit, base_fee_per_gas, extra_data, nonce, ",
		"transaction_count, internal_tx_count, event_count, withdrawal_count, total_fees, burnt_fees, orphaned, fork_id) ",
		"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24) ",
	)
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			ON CONFLICT (hash) DO UPDATE SET
				orphaned = EXCLUDED.orphaned,
				transaction_count = EXCLUDED.transaction_count,
				internal_tx_count = EXCLUDED.internal_tx_count,
				event_count = EXCLUDED.event_count
		`,
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(),
		block.Number,
		block.Hash,
		block.ParentHash,
		block.Timestamp,
		block.FeeRecipient,
		block.StateRoot,
		block.ReceiptsRoot,
		block.LogsBloom,
		block.Difficulty,
		block.TotalDifficulty,
		block.Size,
		block.GasUsed,
		block.GasLimit,
		block.BaseFeePerGas,
		block.ExtraData,
		block.Nonce,
		block.TransactionCount,
		block.InternalTxCount,
		block.EventCount,
		block.WithdrawalCount,
		block.TotalFees,
		block.BurntFees,
		block.Orphaned,
		block.ForkId,
	)
	return err
}

// UpdateElBlockOrphanedStatus marks a block as orphaned or canonical
func UpdateElBlockOrphanedStatus(hash []byte, orphaned bool, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		UPDATE el_blocks
		SET orphaned = $1
		WHERE hash = $2
	`, orphaned, hash)
	return err
}
