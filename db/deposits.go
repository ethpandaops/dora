package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertDepositTxs(depositTxs []*dbtypes.DepositTx, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO deposit_txs ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO deposit_txs ",
		}),
		"(deposit_index, block_number, block_root, publickey, withdrawalcredentials, amount, signature, valid_signature, orphaned, tx_hash, tx_sender, tx_target)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 12

	args := make([]any, len(depositTxs)*fieldCount)
	for i, depositTx := range depositTxs {
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

		args[argIdx+0] = depositTx.Index
		args[argIdx+1] = depositTx.BlockNumber
		args[argIdx+2] = depositTx.BlockRoot
		args[argIdx+3] = depositTx.PublicKey
		args[argIdx+4] = depositTx.WithdrawalCredentials
		args[argIdx+5] = depositTx.Amount
		args[argIdx+6] = depositTx.Signature
		args[argIdx+7] = depositTx.ValidSignature
		args[argIdx+8] = depositTx.Orphaned
		args[argIdx+9] = depositTx.TxHash
		args[argIdx+10] = depositTx.TxSender
		args[argIdx+11] = depositTx.TxTarget
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (deposit_index, block_root) DO UPDATE SET orphaned = excluded.orphaned",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func InsertDeposits(deposits []*dbtypes.Deposit, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO deposits ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO deposits ",
		}),
		"(deposit_index, slot_number, slot_index, slot_root, orphaned, publickey, withdrawalcredentials, amount)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 8

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
		args[argIdx+3] = deposit.SlotRoot
		args[argIdx+4] = deposit.Orphaned
		args[argIdx+5] = deposit.PublicKey
		args[argIdx+6] = deposit.WithdrawalCredentials
		args[argIdx+7] = deposit.Amount
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_index, slot_root) DO UPDATE SET deposit_index = excluded.deposit_index, orphaned = excluded.orphaned",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}
