package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertBuilderDepositTxs(ctx context.Context, tx *sqlx.Tx, depositTxs []*dbtypes.BuilderDepositTx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO builder_deposit_request_txs ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO builder_deposit_request_txs ",
		}),
		"(block_number, block_index, block_time, block_root, fork_id, public_key, withdrawal_credentials, amount, signature, builder_index, tx_hash, tx_sender, tx_target, dequeue_block)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 14

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

		args[argIdx+0] = depositTx.BlockNumber
		args[argIdx+1] = depositTx.BlockIndex
		args[argIdx+2] = depositTx.BlockTime
		args[argIdx+3] = depositTx.BlockRoot
		args[argIdx+4] = depositTx.ForkId
		args[argIdx+5] = depositTx.PublicKey
		args[argIdx+6] = depositTx.WithdrawalCredentials
		args[argIdx+7] = depositTx.Amount
		args[argIdx+8] = depositTx.Signature
		args[argIdx+9] = depositTx.BuilderIndex
		args[argIdx+10] = depositTx.TxHash
		args[argIdx+11] = depositTx.TxSender
		args[argIdx+12] = depositTx.TxTarget
		args[argIdx+13] = depositTx.DequeueBlock
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_root, block_index) DO UPDATE SET builder_index = excluded.builder_index, dequeue_block = excluded.dequeue_block, fork_id = excluded.fork_id",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetBuilderDepositTxsByDequeueRange(ctx context.Context, dequeueFirst uint64, dequeueLast uint64) []*dbtypes.BuilderDepositTx {
	depositTxs := []*dbtypes.BuilderDepositTx{}

	err := ReaderDb.SelectContext(ctx, &depositTxs, `SELECT builder_deposit_request_txs.*
		FROM builder_deposit_request_txs
		WHERE dequeue_block >= $1 AND dequeue_block <= $2
		ORDER BY dequeue_block ASC, block_number ASC, block_index ASC
	`, dequeueFirst, dequeueLast)
	if err != nil {
		logger.Errorf("Error while fetching builder deposit txs: %v", err)
		return nil
	}

	return depositTxs
}

func GetBuilderDepositTxsByTxHashes(ctx context.Context, txHashes [][]byte) []*dbtypes.BuilderDepositTx {
	var sql strings.Builder
	args := make([]any, len(txHashes))

	fmt.Fprint(&sql, `SELECT builder_deposit_request_txs.*
		FROM builder_deposit_request_txs
		WHERE tx_hash IN (
	`)

	for idx, txHash := range txHashes {
		args[idx] = txHash
	}
	appendDollarPlaceholders(&sql, 1, len(txHashes), ", ")
	fmt.Fprintf(&sql, ")")

	depositTxs := []*dbtypes.BuilderDepositTx{}
	err := ReaderDb.SelectContext(ctx, &depositTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching builder deposit txs: %v", err)
		return nil
	}

	return depositTxs
}

func GetBuilderDepositTxsFiltered(ctx context.Context, offset uint64, limit uint32, filter *dbtypes.BuilderDepositTxFilter) ([]*dbtypes.BuilderDepositTx, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			block_number, block_index, block_time, block_root, fork_id, public_key, withdrawal_credentials, amount, signature, builder_index, tx_hash, tx_sender, tx_target, dequeue_block
		FROM builder_deposit_request_txs
	`)

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
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, `)
	SELECT
		count(*) AS block_number,
		0 AS block_index,
		0 AS block_time,
		null AS block_root,
		0 AS fork_id,
		null AS public_key,
		null AS withdrawal_credentials,
		0 AS amount,
		null AS signature,
		null AS builder_index,
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

	depositTxs := []*dbtypes.BuilderDepositTx{}
	err := ReaderDb.SelectContext(ctx, &depositTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered builder deposit txs: %v", err)
		return nil, 0, err
	}

	return depositTxs[1:], depositTxs[0].BlockNumber, nil
}
