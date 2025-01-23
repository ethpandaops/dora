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
		"(block_number, block_index, block_time, block_root, fork_id, source_address, validator_pubkey, validator_index, amount, tx_hash, tx_sender, tx_target, dequeue_block)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 13

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
		args[argIdx+7] = withdrawalTx.ValidatorIndex
		args[argIdx+8] = withdrawalTx.Amount
		args[argIdx+9] = withdrawalTx.TxHash
		args[argIdx+10] = withdrawalTx.TxSender
		args[argIdx+11] = withdrawalTx.TxTarget
		args[argIdx+12] = withdrawalTx.DequeueBlock
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

	err := ReaderDb.Select(&withdrawalTxs, `SELECT withdrawal_request_txs.*
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

func GetWithdrawalRequestTxsByTxHashes(txHashes [][]byte) []*dbtypes.WithdrawalRequestTx {
	var sql strings.Builder
	args := []interface{}{}

	fmt.Fprint(&sql, `SELECT withdrawal_request_txs.*
		FROM withdrawal_request_txs
		WHERE tx_hash IN (
	`)

	for idx, txHash := range txHashes {
		if idx > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		args = append(args, txHash)
		fmt.Fprintf(&sql, "$%v", len(args))
	}
	fmt.Fprintf(&sql, ")")

	withdrawalTxs := []*dbtypes.WithdrawalRequestTx{}
	err := ReaderDb.Select(&withdrawalTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching withdrawal request txs: %v", err)
		return nil
	}

	return withdrawalTxs
}

func GetWithdrawalRequestTxsFiltered(offset uint64, limit uint32, canonicalForkIds []uint64, filter *dbtypes.WithdrawalRequestTxFilter) ([]*dbtypes.WithdrawalRequestTx, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			block_number, block_index, block_time, block_root, fork_id, source_address, validator_pubkey, validator_index, CAST(amount AS BIGINT), tx_hash, tx_sender, tx_target, dequeue_block
		FROM withdrawal_request_txs
	`)

	if filter.ValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS source_names ON source_names."index" = withdrawal_request_txs.validator_index 
		`)
	}

	filterOp := "WHERE"
	if filter.MinDequeue > 0 {
		args = append(args, filter.MinDequeue)
		fmt.Fprintf(&sql, " %v dequeue_block >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxDequeue > 0 {
		args = append(args, filter.MaxDequeue)
		fmt.Fprintf(&sql, " %v dequeue_block <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.PublicKey) > 0 {
		args = append(args, filter.PublicKey)
		fmt.Fprintf(&sql, " %v validator_pubkey = $%v ", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.SourceAddress) > 0 {
		args = append(args, filter.SourceAddress)
		fmt.Fprintf(&sql, " %v source_address = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinIndex > 0 {
		args = append(args, filter.MinIndex)
		fmt.Fprintf(&sql, " %v validator_index >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxIndex > 0 {
		args = append(args, filter.MaxIndex)
		fmt.Fprintf(&sql, " %v validator_index <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.ValidatorName != "" {
		args = append(args, "%"+filter.ValidatorName+"%")
		fmt.Fprintf(&sql, " %v ", filterOp)
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` source_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` source_names.name LIKE $%v `,
		}), len(args))
		filterOp = "AND"
	}
	if filter.MinAmount != nil {
		args = append(args, ConvertUint64ToInt64(*filter.MinAmount))
		fmt.Fprintf(&sql, " %v amount >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxAmount != nil {
		args = append(args, ConvertUint64ToInt64(*filter.MaxAmount))
		fmt.Fprintf(&sql, " %v amount <= $%v", filterOp, len(args))
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
			fmt.Fprintf(&sql, " %v fork_id IN (%v)", filterOp, strings.Join(forkIdStr, ","))
			filterOp = "AND"
		} else if filter.WithOrphaned == 2 {
			fmt.Fprintf(&sql, " %v fork_id NOT IN (%v)", filterOp, strings.Join(forkIdStr, ","))
			filterOp = "AND"
		}
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, `) 
	SELECT 
		count(*) AS block_number, 
		0 AS block_index,
		0 AS block_time,
		null AS block_root,
		0 AS fork_id,
		null AS source_address,
		null AS validator_pubkey,
		0 AS validator_index,
		CAST(0 AS BIGINT) AS amount,
		null AS tx_hash,
		null AS tx_sender,
		null AS tx_target,
		0 AS dequeue_block
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_time DESC, block_index DESC
	LIMIT $%v 
	`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v ", len(args))
	}
	fmt.Fprintf(&sql, ") AS t1")

	withdrawalRequestTxs := []*dbtypes.WithdrawalRequestTx{}
	err := ReaderDb.Select(&withdrawalRequestTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered withdrawal request txs: %v", err)
		return nil, 0, err
	}

	return withdrawalRequestTxs[1:], withdrawalRequestTxs[0].BlockNumber, nil
}
