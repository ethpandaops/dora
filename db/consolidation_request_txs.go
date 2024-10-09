package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertConsolidationRequestTxs(consolidationTxs []*dbtypes.ConsolidationRequestTx, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO consolidation_request_txs ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO consolidation_request_txs ",
		}),
		"(block_number, block_index, block_time, block_root, fork_id, source_address, source_pubkey, source_index, target_pubkey, target_index, tx_hash, tx_sender, tx_target, dequeue_block)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 14

	args := make([]any, len(consolidationTxs)*fieldCount)
	for i, consolidationTx := range consolidationTxs {
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

		args[argIdx+0] = consolidationTx.BlockNumber
		args[argIdx+1] = consolidationTx.BlockIndex
		args[argIdx+2] = consolidationTx.BlockTime
		args[argIdx+3] = consolidationTx.BlockRoot
		args[argIdx+4] = consolidationTx.ForkId
		args[argIdx+5] = consolidationTx.SourceAddress
		args[argIdx+6] = consolidationTx.SourcePubkey
		args[argIdx+7] = consolidationTx.SourceIndex
		args[argIdx+8] = consolidationTx.TargetPubkey
		args[argIdx+9] = consolidationTx.TargetIndex
		args[argIdx+10] = consolidationTx.TxHash
		args[argIdx+11] = consolidationTx.TxSender
		args[argIdx+12] = consolidationTx.TxTarget
		args[argIdx+13] = consolidationTx.DequeueBlock
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_number, block_index) DO UPDATE SET source_index = excluded.source_index, target_index = excluded.target_index, fork_id = excluded.fork_id",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetConsolidationRequestTxsByDequeueRange(dequeueFirst uint64, dequeueLast uint64) []*dbtypes.ConsolidationRequestTx {
	consolidationTxs := []*dbtypes.ConsolidationRequestTx{}

	err := ReaderDb.Select(&consolidationTxs, `SELECT block_number, block_index, block_time, block_root, fork_id, source_address, source_pubkey, source_index, target_pubkey, target_index, tx_hash, tx_sender, tx_target, dequeue_block
		FROM consolidation_request_txs
		WHERE dequeue_block >= $1 AND dequeue_block <= $2
		ORDER BY dequeue_block ASC, block_number ASC, block_index ASC
	`, dequeueFirst, dequeueLast)
	if err != nil {
		logger.Errorf("Error while fetching consolidation request txs: %v", err)
		return nil
	}

	return consolidationTxs
}
