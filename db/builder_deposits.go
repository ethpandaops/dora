package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertBuilderDeposits(ctx context.Context, tx *sqlx.Tx, deposits []*dbtypes.BuilderDeposit) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO builder_deposits ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO builder_deposits ",
		}),
		"(slot_number, slot_root, slot_index, orphaned, fork_id, public_key, withdrawal_credentials, amount, signature, builder_index, tx_hash, block_number, result)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 13

	args := make([]interface{}, len(deposits)*fieldCount)
	for i, deposit := range deposits {
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

		args[argIdx+0] = deposit.SlotNumber
		args[argIdx+1] = deposit.SlotRoot
		args[argIdx+2] = deposit.SlotIndex
		args[argIdx+3] = deposit.Orphaned
		args[argIdx+4] = deposit.ForkId
		args[argIdx+5] = deposit.PublicKey
		args[argIdx+6] = deposit.WithdrawalCredentials
		args[argIdx+7] = deposit.Amount
		args[argIdx+8] = deposit.Signature
		args[argIdx+9] = deposit.BuilderIndex
		args[argIdx+10] = deposit.TxHash
		args[argIdx+11] = deposit.BlockNumber
		args[argIdx+12] = deposit.Result
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

func GetBuilderDepositsFiltered(ctx context.Context, offset uint64, limit uint32, canonicalForkIds []uint64, filter *dbtypes.BuilderDepositFilter) ([]*dbtypes.BuilderDeposit, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			slot_number, slot_root, slot_index, orphaned, fork_id, public_key, withdrawal_credentials, amount, signature, builder_index, tx_hash, block_number, result
		FROM builder_deposits
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
	if filter.MinAmount != nil {
		args = append(args, *filter.MinAmount)
		fmt.Fprintf(&sql, " %v amount >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxAmount != nil {
		args = append(args, *filter.MaxAmount)
		fmt.Fprintf(&sql, " %v amount <= $%v", filterOp, len(args))
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
		null AS public_key,
		null AS withdrawal_credentials,
		0 AS amount,
		null AS signature,
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

	deposits := []*dbtypes.BuilderDeposit{}
	err := ReaderDb.SelectContext(ctx, &deposits, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered builder deposits: %v", err)
		return nil, 0, err
	}

	return deposits[1:], deposits[0].SlotNumber, nil
}

func GetBuilderDepositsByElBlockRange(ctx context.Context, firstBlock uint64, lastBlock uint64) []*dbtypes.BuilderDeposit {
	deposits := []*dbtypes.BuilderDeposit{}

	err := ReaderDb.SelectContext(ctx, &deposits, `
		SELECT builder_deposits.*
		FROM builder_deposits
		WHERE block_number >= $1 AND block_number <= $2
		ORDER BY block_number ASC, slot_index ASC
	`, firstBlock, lastBlock)
	if err != nil {
		logger.Errorf("Error while fetching builder deposits: %v", err)
		return nil
	}

	return deposits
}

func UpdateBuilderDepositTxHash(ctx context.Context, tx *sqlx.Tx, slotRoot []byte, slotIndex uint64, txHash []byte) error {
	_, err := tx.ExecContext(ctx, `UPDATE builder_deposits SET tx_hash = $1 WHERE slot_root = $2 AND slot_index = $3`, txHash, slotRoot, slotIndex)
	if err != nil {
		return err
	}
	return nil
}
