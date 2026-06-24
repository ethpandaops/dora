package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertBuilderExits(ctx context.Context, tx *sqlx.Tx, exits []*dbtypes.BuilderExit) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO builder_exits ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO builder_exits ",
		}),
		"(slot_number, slot_root, slot_index, orphaned, fork_id, source_address, public_key, builder_index, tx_hash, block_number, result)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 11

	args := make([]interface{}, len(exits)*fieldCount)
	for i, exit := range exits {
		if i > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		fmt.Fprintf(&sql, "(")
		for f := 0; f < fieldCount; f++ {
			if f > 0 {
				fmt.Fprintf(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", argIdx+f+1)
		}
		fmt.Fprintf(&sql, ")")

		args[argIdx+0] = exit.SlotNumber
		args[argIdx+1] = exit.SlotRoot
		args[argIdx+2] = exit.SlotIndex
		args[argIdx+3] = exit.Orphaned
		args[argIdx+4] = exit.ForkId
		args[argIdx+5] = exit.SourceAddress
		args[argIdx+6] = exit.PublicKey
		args[argIdx+7] = exit.BuilderIndex
		args[argIdx+8] = exit.TxHash
		args[argIdx+9] = exit.BlockNumber
		args[argIdx+10] = exit.Result
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_root, slot_index) DO UPDATE SET orphaned = excluded.orphaned, fork_id = excluded.fork_id, builder_index = excluded.builder_index, result = excluded.result",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetBuilderExitsFiltered(ctx context.Context, offset uint64, limit uint32, canonicalForkIds []uint64, filter *dbtypes.BuilderExitFilter) ([]*dbtypes.BuilderExit, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			slot_number, slot_root, slot_index, orphaned, fork_id, source_address, public_key, builder_index, tx_hash, block_number, result
		FROM builder_exits
	`)

	filterOp := "WHERE"
	if filter.MinSlot > 0 {
		args = append(args, filter.MinSlot)
		fmt.Fprintf(&sql, " %v slot_number >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxSlot > 0 {
		args = append(args, filter.MaxSlot)
		fmt.Fprintf(&sql, " %v slot_number <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.PublicKey) > 0 {
		args = append(args, filter.PublicKey)
		fmt.Fprintf(&sql, " %v public_key = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.SourceAddress) > 0 {
		args = append(args, filter.SourceAddress)
		fmt.Fprintf(&sql, " %v source_address = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinIndex > 0 {
		args = append(args, filter.MinIndex)
		fmt.Fprintf(&sql, " %v builder_index >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxIndex > 0 {
		args = append(args, filter.MaxIndex)
		fmt.Fprintf(&sql, " %v builder_index <= $%v", filterOp, len(args))
		filterOp = "AND"
	}

	appendWithOrphanedFilter(&sql, &args, &filterOp, filter.WithOrphaned, canonicalForkIds, "fork_id")

	args = append(args, limit)
	fmt.Fprintf(&sql, `)
	SELECT
		count(*) AS slot_number,
		null AS slot_root,
		0 AS slot_index,
		false AS orphaned,
		0 AS fork_id,
		null AS source_address,
		null AS public_key,
		null AS builder_index,
		null AS tx_hash,
		0 AS block_number,
		0 AS result
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY slot_number DESC, slot_index DESC
	LIMIT $%v
	`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v ", len(args))
	}
	fmt.Fprintf(&sql, ") AS t1")

	exits := []*dbtypes.BuilderExit{}
	err := ReaderDb.SelectContext(ctx, &exits, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered builder exits: %v", err)
		return nil, 0, err
	}

	return exits[1:], exits[0].SlotNumber, nil
}

func GetBuilderExitsByElBlockRange(ctx context.Context, firstBlock uint64, lastBlock uint64) []*dbtypes.BuilderExit {
	exits := []*dbtypes.BuilderExit{}

	err := ReaderDb.SelectContext(ctx, &exits, `
		SELECT builder_exits.*
		FROM builder_exits
		WHERE block_number >= $1 AND block_number <= $2
		ORDER BY block_number ASC, slot_index ASC
	`, firstBlock, lastBlock)
	if err != nil {
		logger.Errorf("Error while fetching builder exits: %v", err)
		return nil
	}

	return exits
}

func UpdateBuilderExitTxHash(ctx context.Context, tx *sqlx.Tx, slotRoot []byte, slotIndex uint64, txHash []byte) error {
	_, err := tx.ExecContext(ctx, `UPDATE builder_exits SET tx_hash = $1 WHERE slot_root = $2 AND slot_index = $3`, txHash, slotRoot, slotIndex)
	if err != nil {
		return err
	}
	return nil
}
