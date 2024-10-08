package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertWithdrawalRequestTxs(withdrawalTxs []*dbtypes.WithdrawalRequestTx, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO withdrawal_request_txs ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO withdrawal_request_txs ",
		}),
		"(block_number, block_index, block_time, block_root, fork_id, source_address, validator_pubkey, amount, tx_hash, tx_sender, tx_target, dequeue_block)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 12

	args := make([]any, len(withdrawalTxs)*fieldCount)
	for i, withdrawalTx := range withdrawalTxs {
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

		args[argIdx+0] = withdrawalTx.BlockNumber
		args[argIdx+1] = withdrawalTx.BlockIndex
		args[argIdx+2] = withdrawalTx.BlockTime
		args[argIdx+3] = withdrawalTx.BlockRoot
		args[argIdx+4] = withdrawalTx.ForkId
		args[argIdx+5] = withdrawalTx.SourceAddress
		args[argIdx+6] = withdrawalTx.ValidatorPubkey
		args[argIdx+7] = withdrawalTx.Amount
		args[argIdx+8] = withdrawalTx.TxHash
		args[argIdx+9] = withdrawalTx.TxSender
		args[argIdx+10] = withdrawalTx.TxTarget
		args[argIdx+11] = withdrawalTx.DequeueBlock
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_root, block_index) DO UPDATE SET fork_id = excluded.fork_id",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetWithdrawalRequestTxsByDequeueRange(dequeueFirst uint64, dequeueLast uint64) []*dbtypes.WithdrawalRequestTx {
	withdrawalTxs := []*dbtypes.WithdrawalRequestTx{}

	err := ReaderDb.Select(&withdrawalTxs, `SELECT block_number, block_index, block_time, block_root, fork_id, source_address, validator_pubkey, amount, tx_hash, tx_sender, tx_target, dequeue_block
		FROM withdrawal_request_txs
		WHERE dequeue_block >= $1 AND dequeue_block <= $2
		ORDER BY dequeue_block ASC, block_number ASC, block_index ASC
	`, dequeueFirst, dequeueLast)
	if err != nil {
		logger.Errorf("Error while fetching withdrawal request transactions: %v", err)
		return nil
	}

	return withdrawalTxs
}
