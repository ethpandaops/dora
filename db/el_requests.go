package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// slot_number, slot_index, slot_root, orphaned, request_type, source_address, source_index, source_pubkey, target_index, target_pubkey, amount

func InsertElRequests(elRequests []*dbtypes.ElRequest, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_requests ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_requests ",
		}),
		"(slot_number, slot_index, slot_root, orphaned, request_type, source_address, source_index, source_pubkey, target_index, target_pubkey, amount)",
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
		args[argIdx+1] = elRequest.SlotIndex
		args[argIdx+2] = elRequest.SlotRoot
		args[argIdx+3] = elRequest.Orphaned
		args[argIdx+4] = elRequest.RequestType
		args[argIdx+5] = elRequest.SourceAddress
		args[argIdx+6] = elRequest.SourceIndex
		args[argIdx+7] = elRequest.SourcePubkey
		args[argIdx+8] = elRequest.TargetIndex
		args[argIdx+9] = elRequest.TargetPubkey
		args[argIdx+10] = elRequest.Amount
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (slot_root, slot_index) DO UPDATE SET orphaned = excluded.orphaned",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElRequestsForValidator(validator uint64) []*dbtypes.ElRequest {
	var sql strings.Builder
	args := []any{
		validator,
		validator,
	}
	fmt.Fprint(&sql, `
	SELECT
		slot_number, slot_index, slot_root, orphaned, request_type, source_address, source_index, source_pubkey, target_index, target_pubkey, amount
	FROM el_requests
	WHERE source_index = $1 OR target_index = $2
	`)

	elRequests := []*dbtypes.ElRequest{}
	err := ReaderDb.Select(&elRequests, sql.String(), args...)
	if err != nil {
		return nil
	}
	return elRequests
}

func GetElRequestsFiltered(offset uint64, limit uint32, finalizedBlock uint64, filter *dbtypes.ElRequestFilter) ([]*dbtypes.ElRequest, uint64, error) {
	var sql strings.Builder
	args := []any{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			slot_number, slot_index, slot_root, orphaned, request_type, source_address, source_index, source_pubkey, target_index, target_pubkey, amount
		FROM el_requests
	`)

	if filter.SourceValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS source_names ON source_names."index" = el_requests.source_index 
		`)
	}
	if filter.TargetValidatorName != "" {
		fmt.Fprint(&sql, `
		LEFT JOIN validator_names AS target_names ON target_names."index" = el_requests.target_index 
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
	if filter.RequestType > 0 {
		args = append(args, filter.RequestType)
		fmt.Fprintf(&sql, " %v request_type = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.SourceAddress) > 0 {
		args = append(args, filter.SourceAddress)
		fmt.Fprintf(&sql, " %v source_address = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MinSourceIndex > 0 {
		args = append(args, filter.MinSourceIndex)
		fmt.Fprintf(&sql, " %v source_index >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxSourceIndex > 0 {
		args = append(args, filter.MaxSourceIndex)
		fmt.Fprintf(&sql, " %v source_index <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.SourceValidatorName != "" {
		args = append(args, "%"+filter.SourceValidatorName+"%")
		fmt.Fprintf(&sql, " %v ", filterOp)
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` source_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` source_names.name LIKE $%v `,
		}), len(args))
		filterOp = "AND"
	}
	if filter.MinTargetIndex > 0 {
		args = append(args, filter.MinTargetIndex)
		fmt.Fprintf(&sql, " %v target_index >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxTargetIndex > 0 {
		args = append(args, filter.MaxTargetIndex)
		fmt.Fprintf(&sql, " %v target_index <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.TargetValidatorName != "" {
		args = append(args, "%"+filter.TargetValidatorName+"%")
		fmt.Fprintf(&sql, " %v ", filterOp)
		fmt.Fprintf(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  ` target_names.name ilike $%v `,
			dbtypes.DBEngineSqlite: ` target_names.name LIKE $%v `,
		}), len(args))
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
		0 AS request_type,
		null AS source_address,
		0 AS source_index,
		null AS source_pubkey,
		0 AS target_index,
		null AS target_pubkey,
		0 AS amount
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

	elRequests := []*dbtypes.ElRequest{}
	err := ReaderDb.Select(&elRequests, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered voluntary exits: %v", err)
		return nil, 0, err
	}

	return elRequests[1:], elRequests[0].SlotNumber, nil
}
