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
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_root, block_index) DO UPDATE SET source_index = excluded.source_index, target_index = excluded.target_index, fork_id = excluded.fork_id",
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

	err := ReaderDb.Select(&consolidationTxs, `SELECT consolidation_request_txs.*
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

func GetConsolidationRequestTxsByTxHashes(txHashes [][]byte) []*dbtypes.ConsolidationRequestTx {
	var sql strings.Builder
	args := []interface{}{}

	fmt.Fprint(&sql, `SELECT consolidation_request_txs.*
		FROM consolidation_request_txs
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

	consolidationTxs := []*dbtypes.ConsolidationRequestTx{}
	err := ReaderDb.Select(&consolidationTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching consolidation request txs: %v", err)
		return nil
	}

	return consolidationTxs
}

func GetConsolidationRequestTxsFiltered(offset uint64, limit uint32, canonicalForkIds []uint64, filter *dbtypes.ConsolidationRequestTxFilter) ([]*dbtypes.ConsolidationRequestTx, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			block_number, block_index, block_time, block_root, fork_id, source_address, source_pubkey, source_index, target_pubkey, target_index, tx_hash, tx_sender, tx_target, dequeue_block
		FROM consolidation_request_txs
	`)

	if filter.SrcValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS source_names ON source_names."index" = consolidation_request_txs.source_index 
		`)
	}
	if filter.TgtValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS target_names ON target_names."index" = consolidation_request_txs.target_index 
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
		fmt.Fprintf(&sql, " %v (source_pubkey = $%v OR target_pubkey = $%v) ", filterOp, len(args), len(args))
		filterOp = "AND"
	}
	if len(filter.SourceAddress) > 0 {
		args = append(args, filter.SourceAddress)
		fmt.Fprintf(&sql, " %v source_address = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinSrcIndex > 0 {
		args = append(args, filter.MinSrcIndex)
		fmt.Fprintf(&sql, " %v source_index >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxSrcIndex > 0 {
		args = append(args, filter.MaxSrcIndex)
		fmt.Fprintf(&sql, " %v source_index <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinTgtIndex > 0 {
		args = append(args, filter.MinTgtIndex)
		fmt.Fprintf(&sql, " %v target_index >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxTgtIndex > 0 {
		args = append(args, filter.MaxTgtIndex)
		fmt.Fprintf(&sql, " %v target_index <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.SrcValidatorName != "" {
		args = append(args, "%"+filter.SrcValidatorName+"%")
		fmt.Fprintf(&sql, " %v ", filterOp)
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` source_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` source_names.name LIKE $%v `,
		}), len(args))
		filterOp = "AND"
	}
	if filter.TgtValidatorName != "" {
		args = append(args, "%"+filter.TgtValidatorName+"%")
		fmt.Fprintf(&sql, " %v ", filterOp)
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` target_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` target_names.name LIKE $%v `,
		}), len(args))
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
		null AS source_pubkey,
		0 AS source_index,
		null AS target_pubkey,
		0 AS target_index,
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

	consolidationRequestTxs := []*dbtypes.ConsolidationRequestTx{}
	err := ReaderDb.Select(&consolidationRequestTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered consolidation request txs: %v", err)
		return nil, 0, err
	}

	return consolidationRequestTxs[1:], consolidationRequestTxs[0].BlockNumber, nil
}
