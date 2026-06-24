package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertBuilderExitTxs(ctx context.Context, tx *sqlx.Tx, exitTxs []*dbtypes.BuilderExitTx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO builder_exit_request_txs ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO builder_exit_request_txs ",
		}),
		"(block_number, block_index, block_time, block_root, fork_id, source_address, public_key, builder_index, tx_hash, tx_sender, tx_target, dequeue_block)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 12

	args := make([]any, len(exitTxs)*fieldCount)
	for i, exitTx := range exitTxs {
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

		args[argIdx+0] = exitTx.BlockNumber
		args[argIdx+1] = exitTx.BlockIndex
		args[argIdx+2] = exitTx.BlockTime
		args[argIdx+3] = exitTx.BlockRoot
		args[argIdx+4] = exitTx.ForkId
		args[argIdx+5] = exitTx.SourceAddress
		args[argIdx+6] = exitTx.PublicKey
		args[argIdx+7] = exitTx.BuilderIndex
		args[argIdx+8] = exitTx.TxHash
		args[argIdx+9] = exitTx.TxSender
		args[argIdx+10] = exitTx.TxTarget
		args[argIdx+11] = exitTx.DequeueBlock
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

func GetBuilderExitTxsByDequeueRange(ctx context.Context, dequeueFirst uint64, dequeueLast uint64) []*dbtypes.BuilderExitTx {
	exitTxs := []*dbtypes.BuilderExitTx{}

	err := ReaderDb.SelectContext(ctx, &exitTxs, `SELECT builder_exit_request_txs.*
		FROM builder_exit_request_txs
		WHERE dequeue_block >= $1 AND dequeue_block <= $2
		ORDER BY dequeue_block ASC, block_number ASC, block_index ASC
	`, dequeueFirst, dequeueLast)
	if err != nil {
		logger.Errorf("Error while fetching builder exit txs: %v", err)
		return nil
	}

	return exitTxs
}

func GetBuilderExitTxsByTxHashes(ctx context.Context, txHashes [][]byte) []*dbtypes.BuilderExitTx {
	var sql strings.Builder
	args := make([]any, len(txHashes))

	fmt.Fprint(&sql, `SELECT builder_exit_request_txs.*
		FROM builder_exit_request_txs
		WHERE tx_hash IN (
	`)

	for idx, txHash := range txHashes {
		args[idx] = txHash
	}
	appendDollarPlaceholders(&sql, 1, len(txHashes), ", ")
	fmt.Fprintf(&sql, ")")

	exitTxs := []*dbtypes.BuilderExitTx{}
	err := ReaderDb.SelectContext(ctx, &exitTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching builder exit txs: %v", err)
		return nil
	}

	return exitTxs
}

func GetBuilderExitTxsFiltered(ctx context.Context, offset uint64, limit uint32, filter *dbtypes.BuilderExitTxFilter) ([]*dbtypes.BuilderExitTx, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			block_number, block_index, block_time, block_root, fork_id, source_address, public_key, builder_index, tx_hash, tx_sender, tx_target, dequeue_block
		FROM builder_exit_request_txs
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
		null AS public_key,
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

	exitTxs := []*dbtypes.BuilderExitTx{}
	err := ReaderDb.SelectContext(ctx, &exitTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered builder exit txs: %v", err)
		return nil, 0, err
	}

	return exitTxs[1:], exitTxs[0].BlockNumber, nil
}
