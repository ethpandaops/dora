package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertConsolidationRequests(consolidations []*dbtypes.ConsolidationRequest, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO consolidation_requests ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO consolidation_requests ",
		}),
		"(slot_number, slot_root, slot_index, orphaned, fork_id, source_address, source_index, source_pubkey, target_index, target_pubkey, tx_hash, block_number)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 12

	args := make([]interface{}, len(consolidations)*fieldCount)
	for i, consolidation := range consolidations {
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

		args[argIdx+0] = consolidation.SlotNumber
		args[argIdx+1] = consolidation.SlotRoot[:]
		args[argIdx+2] = consolidation.SlotIndex
		args[argIdx+3] = consolidation.Orphaned
		args[argIdx+4] = consolidation.ForkId
		args[argIdx+5] = consolidation.SourceAddress[:]
		args[argIdx+6] = consolidation.SourceIndex
		args[argIdx+7] = consolidation.SourcePubkey[:]
		args[argIdx+8] = consolidation.TargetIndex
		args[argIdx+9] = consolidation.TargetPubkey[:]
		args[argIdx+10] = consolidation.TxHash[:]
		args[argIdx+11] = consolidation.BlockNumber
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_root, slot_index) DO UPDATE SET orphaned = excluded.orphaned, fork_id = excluded.fork_id",
		dbtypes.DBEngineSqlite: "",
	}))
	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetConsolidationRequestsFiltered(offset uint64, limit uint32, canonicalForkIds []uint64, filter *dbtypes.ConsolidationRequestFilter) ([]*dbtypes.ConsolidationRequest, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			slot_number, slot_root, slot_index, orphaned, fork_id, source_address, source_index, source_pubkey, target_index, target_pubkey, tx_hash, block_number
		FROM consolidation_requests
	`)

	if filter.SrcValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS source_names ON source_names."index" = consolidation_requests.source_index 
		`)
	}
	if filter.TgtValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS target_names ON target_names."index" = consolidation_requests.target_index 
		`)
	}

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
		count(*) AS slot_number,
		null AS slot_root,
		0 AS slot_index,
		false AS orphaned, 
		0 AS fork_id,
		null AS source_address,
		0 AS source_index,
		null AS source_pubkey,
		0 AS target_index,
		null AS target_pubkey,
		null AS tx_hash,
		0 AS block_number
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

	consolidationRequests := []*dbtypes.ConsolidationRequest{}
	err := ReaderDb.Select(&consolidationRequests, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered consolidation requests: %v", err)
		return nil, 0, err
	}

	return consolidationRequests[1:], consolidationRequests[0].SlotNumber, nil
}

func GetConsolidationRequestsByElBlockRange(firstSlot uint64, lastSlot uint64) []*dbtypes.ConsolidationRequest {
	consolidationRequests := []*dbtypes.ConsolidationRequest{}

	err := ReaderDb.Select(&consolidationRequests, `
		SELECT consolidation_requests.*
		FROM consolidation_requests
		WHERE block_number >= $1 AND block_number <= $2
		ORDER BY block_number ASC, slot_index ASC
	`, firstSlot, lastSlot)
	if err != nil {
		logger.Errorf("Error while fetching consolidation requests: %v", err)
		return nil
	}

	return consolidationRequests
}

func UpdateConsolidationRequestTxHash(slotRoot []byte, slotIndex uint64, txHash []byte, tx *sqlx.Tx) error {
	_, err := tx.Exec(`UPDATE consolidation_requests SET tx_hash = $1 WHERE slot_root = $2 AND slot_index = $3`, txHash, slotRoot, slotIndex)
	if err != nil {
		return err
	}
	return nil
}
