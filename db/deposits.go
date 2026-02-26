package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/jmoiron/sqlx"
)

func InsertDeposits(ctx context.Context, tx *sqlx.Tx, deposits []*dbtypes.Deposit) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO deposits ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO deposits ",
		}),
		"(deposit_index, slot_number, slot_index, slot_root, orphaned, publickey, withdrawalcredentials, amount, fork_id)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 9

	args := make([]any, len(deposits)*fieldCount)
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

		args[argIdx+0] = deposit.Index
		args[argIdx+1] = deposit.SlotNumber
		args[argIdx+2] = deposit.SlotIndex
		args[argIdx+3] = deposit.SlotRoot[:]
		args[argIdx+4] = deposit.Orphaned
		args[argIdx+5] = deposit.PublicKey[:]
		args[argIdx+6] = deposit.WithdrawalCredentials[:]
		args[argIdx+7] = deposit.Amount
		args[argIdx+8] = deposit.ForkId
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_index, slot_root) DO UPDATE SET deposit_index = excluded.deposit_index, orphaned = excluded.orphaned, fork_id = excluded.fork_id",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetDepositsFiltered(ctx context.Context, offset uint64, limit uint32, canonicalForkIds []uint64, filter *dbtypes.DepositFilter, txFilter *dbtypes.DepositTxFilter) ([]*dbtypes.DepositWithTx, uint64, error) {
	var sql strings.Builder
	args := []any{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			deposits.deposit_index, deposits.slot_number, deposits.slot_index, deposits.slot_root, deposits.orphaned, deposits.publickey, deposits.withdrawalcredentials, deposits.amount, deposits.fork_id`)

	if txFilter != nil {
		fmt.Fprint(&sql, `,
			deposit_txs.block_number, deposit_txs.block_time, deposit_txs.block_root, deposit_txs.valid_signature, deposit_txs.tx_hash, deposit_txs.tx_sender, deposit_txs.tx_target
		`)
	}

	fmt.Fprint(&sql, `
		FROM deposits
	`)

	if txFilter != nil {
		fmt.Fprint(&sql, `
			LEFT JOIN deposit_txs ON deposit_txs.deposit_index = deposits.deposit_index AND deposit_txs.publickey = deposits.publickey
		`)
	}

	filterOp := "WHERE"
	if filter.MinIndex > 0 {
		args = append(args, filter.MinIndex)
		fmt.Fprintf(&sql, " %v deposits.deposit_index >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxIndex > 0 {
		args = append(args, filter.MaxIndex)
		fmt.Fprintf(&sql, " %v deposits.deposit_index <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.PublicKey) > 0 {
		args = append(args, filter.PublicKey)
		fmt.Fprintf(&sql, " %v deposits.publickey = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinAmount > 0 {
		args = append(args, filter.MinAmount*utils.GWEI.Uint64())
		fmt.Fprintf(&sql, " %v deposits.amount >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxAmount > 0 {
		args = append(args, filter.MaxAmount*utils.GWEI.Uint64())
		fmt.Fprintf(&sql, " %v deposits.amount <= $%v", filterOp, len(args))
		filterOp = "AND"
	}

	if filter.WithOrphaned != 1 {
		forkIdStr := make([]string, len(canonicalForkIds))
		for i, forkId := range canonicalForkIds {
			forkIdStr[i] = fmt.Sprintf("%v", forkId)
		}
		if len(forkIdStr) == 0 {
			forkIdStr = append(forkIdStr, "0")
		}

		if filter.WithOrphaned == 0 {
			fmt.Fprintf(&sql, " %v deposits.fork_id IN (%v)", filterOp, strings.Join(forkIdStr, ","))
			filterOp = "AND"
		} else if filter.WithOrphaned == 2 {
			fmt.Fprintf(&sql, " %v deposits.fork_id NOT IN (%v)", filterOp, strings.Join(forkIdStr, ","))
			filterOp = "AND"
		}
	}

	if txFilter != nil {
		if txFilter.WithValid == 0 {
			fmt.Fprintf(&sql, " %v deposit_txs.valid_signature IN (1, 2)", filterOp)
			filterOp = "AND"
		} else if txFilter.WithValid == 2 {
			fmt.Fprintf(&sql, " %v deposit_txs.valid_signature = 0", filterOp)
			filterOp = "AND"
		}

		if len(txFilter.WithdrawalAddress) > 0 {
			// 0x01 = ETH1, 0x02 = compounding, 0x03 = builder deposit
			wdcreds1 := make([]byte, 32)
			wdcreds1[0] = 0x01
			copy(wdcreds1[12:], txFilter.WithdrawalAddress)
			wdcreds2 := make([]byte, 32)
			wdcreds2[0] = 0x02
			copy(wdcreds2[12:], txFilter.WithdrawalAddress)
			wdcreds3 := make([]byte, 32)
			wdcreds3[0] = 0x03
			copy(wdcreds3[12:], txFilter.WithdrawalAddress)
			args = append(args, wdcreds1, wdcreds2, wdcreds3)
			fmt.Fprintf(&sql, " %v (deposits.withdrawalcredentials = $%v OR deposits.withdrawalcredentials = $%v OR deposits.withdrawalcredentials = $%v)", filterOp, len(args)-2, len(args)-1, len(args))
			filterOp = "AND"
		}

		if len(txFilter.Address) > 0 {
			args = append(args, txFilter.Address)
			fmt.Fprintf(&sql, " %v deposit_txs.tx_sender = $%v", filterOp, len(args))
			filterOp = "AND"
		}

		if len(txFilter.TargetAddress) > 0 {
			args = append(args, txFilter.TargetAddress)
			fmt.Fprintf(&sql, " %v deposit_txs.tx_target = $%v", filterOp, len(args))
			filterOp = "AND"
		}
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, `) 
	SELECT 
		0 AS deposit_index, 
		count(*) AS slot_number, 
		0 AS slot_index, 
		null AS slot_root,
		false AS orphaned,
		null AS publickey, 
		null AS withdrawalcredentials,
		0 AS amount,
		0 AS fork_id`)

	if txFilter != nil {
		fmt.Fprint(&sql, `,
			0 AS block_number,
			0 AS block_time,
			null AS block_root,
			0 AS valid_signature,
			null AS tx_hash,
			null AS tx_sender,
			null AS tx_target
		`)
	}

	fmt.Fprintf(&sql, `
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY slot_number DESC, deposit_index DESC 
	LIMIT $%v 
	`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v ", len(args))
	}
	fmt.Fprintf(&sql, ") AS t1")

	deposits := []*dbtypes.DepositWithTx{}
	err := ReaderDb.SelectContext(ctx, &deposits, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered deposits: %v", err)
		return nil, 0, err
	}

	return deposits[1:], deposits[0].SlotNumber, nil
}
