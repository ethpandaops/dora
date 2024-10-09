package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertWithdrawalRequests(elRequests []*dbtypes.WithdrawalRequest, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO withdrawal_requests ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO withdrawal_requests ",
		}),
		"(slot_number, slot_root, slot_index, orphaned, fork_id, source_address, validator_index, validator_pubkey, amount, tx_hash, block_number)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 11

	args := make([]any, len(elRequests)*fieldCount)
	for i, elRequest := range elRequests {
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

		args[argIdx+0] = elRequest.SlotNumber
		args[argIdx+1] = elRequest.SlotRoot
		args[argIdx+2] = elRequest.SlotIndex
		args[argIdx+3] = elRequest.Orphaned
		args[argIdx+4] = elRequest.ForkId
		args[argIdx+5] = elRequest.SourceAddress
		args[argIdx+6] = elRequest.ValidatorIndex
		args[argIdx+7] = elRequest.ValidatorPubkey
		args[argIdx+8] = elRequest.Amount
		args[argIdx+9] = elRequest.TxHash
		args[argIdx+10] = elRequest.BlockNumber
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_root, slot_index) DO UPDATE SET orphaned = excluded.orphaned, tx_hash = excluded.tx_hash",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetWithdrawalRequestsFiltered(offset uint64, limit uint32, finalizedBlock uint64, filter *dbtypes.WithdrawalRequestFilter) ([]*dbtypes.WithdrawalRequest, uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			slot_number, slot_index, slot_root, orphaned, fork_id, source_address, validator_index, validator_pubkey, amount, tx_hash, block_number
		FROM withdrawal_requests
	`)

	if filter.ValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS source_names ON source_names."index" = withdrawal_requests.validator_index 
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
		args = append(args, *filter.MinAmount)
		fmt.Fprintf(&sql, " %v amount >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxAmount != nil {
		args = append(args, *filter.MaxAmount)
		fmt.Fprintf(&sql, " %v amount <= $%v", filterOp, len(args))
		filterOp = "AND"
	}

	if filter.WithOrphaned == 0 {
		args = append(args, finalizedBlock)
		fmt.Fprintf(&sql, " %v (slot_number > $%v OR orphaned = false)", filterOp, len(args))
		filterOp = "AND"
	} else if filter.WithOrphaned == 2 {
		args = append(args, finalizedBlock)
		fmt.Fprintf(&sql, " %v (slot_number > $%v OR orphaned = true)", filterOp, len(args))
		filterOp = "AND"
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, `) 
	SELECT 
		count(*) AS slot_number, 
		0 AS slot_index,
		null AS slot_root,
		false AS orphaned, 
		0 AS fork_id,
		null AS source_address,
		0 AS validator_index,
		null AS validator_pubkey,
		0 AS amount,
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

	withdrawalRequests := []*dbtypes.WithdrawalRequest{}
	err := ReaderDb.Select(&withdrawalRequests, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered withdrawal requests: %v", err)
		return nil, 0, err
	}

	return withdrawalRequests[1:], withdrawalRequests[0].SlotNumber, nil
}

func GetWithdrawalRequestsByElBlockRange(firstSlot uint64, lastSlot uint64) []*dbtypes.WithdrawalRequest {
	withdrawalRequests := []*dbtypes.WithdrawalRequest{}

	err := ReaderDb.Select(&withdrawalRequests, `
		SELECT withdrawal_requests.*
		FROM withdrawal_requests
		WHERE block_number >= $1 AND block_number <= $2
		ORDER BY block_number ASC, slot_index ASC
	`, firstSlot, lastSlot)
	if err != nil {
		logger.Errorf("Error while fetching withdrawal requests: %v", err)
		return nil
	}

	return withdrawalRequests
}

func UpdateWithdrawalRequestTxHash(slotRoot []byte, slotIndex uint64, txHash []byte, tx *sqlx.Tx) error {
	_, err := tx.Exec(`UPDATE withdrawal_requests SET tx_hash = $1 WHERE slot_root = $2 AND slot_index = $3`, txHash, slotRoot, slotIndex)
	if err != nil {
		return err
	}
	return nil
}
