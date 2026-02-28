package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElTokenTransfers(ctx context.Context, dbTx *sqlx.Tx, transfers []*dbtypes.ElTokenTransfer) error {
	if len(transfers) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_token_transfers ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_token_transfers ",
		}),
		"(block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 11

	args := make([]any, len(transfers)*fieldCount)
	for i, transfer := range transfers {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprint(&sql, "(")
		for f := 0; f < fieldCount; f++ {
			if f > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", argIdx+f+1)
		}
		fmt.Fprint(&sql, ")")

		args[argIdx+0] = transfer.BlockUid
		args[argIdx+1] = transfer.TxHash
		args[argIdx+2] = transfer.TxPos
		args[argIdx+3] = transfer.TxIdx
		args[argIdx+4] = transfer.TokenID
		args[argIdx+5] = transfer.TokenType
		args[argIdx+6] = transfer.TokenIndex
		args[argIdx+7] = transfer.FromID
		args[argIdx+8] = transfer.ToID
		args[argIdx+9] = transfer.Amount
		args[argIdx+10] = transfer.AmountRaw
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_uid, tx_hash, tx_idx) DO UPDATE SET tx_pos = excluded.tx_pos, token_id = excluded.token_id, token_type = excluded.token_type, token_index = excluded.token_index, from_id = excluded.from_id, to_id = excluded.to_id, amount = excluded.amount, amount_raw = excluded.amount_raw",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElTokenTransfer(ctx context.Context, blockUid uint64, txHash []byte, txIdx uint32) (*dbtypes.ElTokenTransfer, error) {
	transfer := &dbtypes.ElTokenTransfer{}
	err := ReaderDb.GetContext(ctx, transfer, "SELECT block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw FROM el_token_transfers WHERE block_uid = $1 AND tx_hash = $2 AND tx_idx = $3", blockUid, txHash, txIdx)
	if err != nil {
		return nil, err
	}
	return transfer, nil
}

func GetElTokenTransfersByTxHash(ctx context.Context, txHash []byte) ([]*dbtypes.ElTokenTransfer, error) {
	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.SelectContext(ctx, &transfers, "SELECT block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw FROM el_token_transfers WHERE tx_hash = $1 ORDER BY block_uid DESC, tx_pos DESC, tx_idx DESC", txHash)
	if err != nil {
		return nil, err
	}
	return transfers, nil
}

// GetElTokenTransferCountByBlockUidAndTxHash returns the number of token
// transfers for a given block UID and transaction hash.
func GetElTokenTransferCountByBlockUidAndTxHash(ctx context.Context, blockUid uint64, txHash []byte) (uint64, error) {
	var count uint64
	err := ReaderDb.GetContext(ctx, &count,
		"SELECT COUNT(*) FROM el_token_transfers WHERE block_uid = $1 AND tx_hash = $2",
		blockUid, txHash,
	)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func GetElTokenTransfersByBlockUid(ctx context.Context, blockUid uint64) ([]*dbtypes.ElTokenTransfer, error) {
	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.SelectContext(ctx, &transfers, "SELECT block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw FROM el_token_transfers WHERE block_uid = $1 ORDER BY tx_pos ASC, tx_idx ASC", blockUid)
	if err != nil {
		return nil, err
	}
	return transfers, nil
}

func GetElTokenTransfersByBlockUidAndTxHash(ctx context.Context, blockUid uint64, txHash []byte) ([]*dbtypes.ElTokenTransfer, error) {
	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.SelectContext(ctx, &transfers, "SELECT block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw FROM el_token_transfers WHERE block_uid = $1 AND tx_hash = $2 ORDER BY tx_idx DESC", blockUid, txHash)
	if err != nil {
		return nil, err
	}
	return transfers, nil
}

func GetElTokenTransfersByTokenID(ctx context.Context, tokenID uint64, offset uint64, limit uint32) ([]*dbtypes.ElTokenTransfer, uint64, error) {
	var sql strings.Builder
	args := []any{tokenID}

	// Use window function for count - avoids double scan
	// NULLS LAST matches the composite index definition to enable index scans.
	fmt.Fprint(&sql, `
		SELECT
			block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index,
			from_id, to_id, amount, amount_raw,
			COUNT(*) OVER() AS total_count
		FROM el_token_transfers
		WHERE token_id = $1
		ORDER BY block_uid DESC NULLS LAST, tx_pos DESC, tx_idx DESC
		LIMIT $2`)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	type resultRow struct {
		dbtypes.ElTokenTransfer
		TotalCount uint64 `db:"total_count"`
	}

	rows := []resultRow{}
	err := ReaderDb.SelectContext(ctx, &rows, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el token transfers by token id: %v", err)
		return nil, 0, err
	}

	if len(rows) == 0 {
		return []*dbtypes.ElTokenTransfer{}, 0, nil
	}

	transfers := make([]*dbtypes.ElTokenTransfer, len(rows))
	var totalCount uint64
	for i, row := range rows {
		transfers[i] = &row.ElTokenTransfer
		if i == 0 {
			totalCount = row.TotalCount
		}
	}

	return transfers, totalCount, nil
}

func GetElTokenTransfersByAccountID(ctx context.Context, accountID uint64, isFrom bool, offset uint64, limit uint32) ([]*dbtypes.ElTokenTransfer, uint64, error) {
	var sql strings.Builder
	args := []any{accountID}

	column := "to_id"
	if isFrom {
		column = "from_id"
	}

	// Use window function for count - avoids double scan
	// NULLS LAST matches the composite index definition to enable index scans.
	fmt.Fprintf(&sql, `
		SELECT
			block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index,
			from_id, to_id, amount, amount_raw,
			COUNT(*) OVER() AS total_count
		FROM el_token_transfers
		WHERE %s = $1
		ORDER BY block_uid DESC NULLS LAST, tx_pos DESC, tx_idx DESC
		LIMIT $2`, column)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	type resultRow struct {
		dbtypes.ElTokenTransfer
		TotalCount uint64 `db:"total_count"`
	}

	rows := []resultRow{}
	err := ReaderDb.SelectContext(ctx, &rows, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el token transfers by account id: %v", err)
		return nil, 0, err
	}

	if len(rows) == 0 {
		return []*dbtypes.ElTokenTransfer{}, 0, nil
	}

	transfers := make([]*dbtypes.ElTokenTransfer, len(rows))
	var totalCount uint64
	for i, row := range rows {
		transfers[i] = &row.ElTokenTransfer
		if i == 0 {
			totalCount = row.TotalCount
		}
	}

	return transfers, totalCount, nil
}

func GetElTokenTransfersFiltered(ctx context.Context, offset uint64, limit uint32, filter *dbtypes.ElTokenTransferFilter) ([]*dbtypes.ElTokenTransfer, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw
		FROM el_token_transfers
	`)

	filterOp := "WHERE"
	if filter.TokenID != nil {
		args = append(args, *filter.TokenID)
		fmt.Fprintf(&sql, " %v token_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.FromID > 0 {
		args = append(args, filter.FromID)
		fmt.Fprintf(&sql, " %v from_id = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.ToID > 0 {
		args = append(args, filter.ToID)
		fmt.Fprintf(&sql, " %v to_id = $%v", filterOp, len(args))
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

	fmt.Fprint(&sql, ")")

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		null AS tx_hash,
		0 AS tx_pos,
		0 AS tx_idx,
		0 AS token_id,
		0 AS token_type,
		null AS token_index,
		0 AS from_id,
		0 AS to_id,
		0 AS amount,
		null AS amount_raw
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC NULLS LAST, tx_pos DESC, tx_idx DESC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.SelectContext(ctx, &transfers, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el token transfers: %v", err)
		return nil, 0, err
	}

	if len(transfers) == 0 {
		return []*dbtypes.ElTokenTransfer{}, 0, nil
	}

	count := transfers[0].BlockUid
	return transfers[1:], count, nil
}

// MaxAccountTokenTransferCount is the maximum count returned for address token transfer queries.
// If the actual count exceeds this, the query returns this limit and sets the "more" flag.
const MaxAccountTokenTransferCount = 100000

// GetElTokenTransfersByAccountIDCombined returns all token transfers where the account is either sender or receiver.
// Results are sorted by block_uid DESC, tx_pos DESC, tx_idx DESC.
// tokenTypes filters by token type (empty = all types).
// Returns transfers, total count (capped at MaxAccountTokenTransferCount), whether count is capped, and error.
func GetElTokenTransfersByAccountIDCombined(ctx context.Context, accountID uint64, tokenTypes []uint8, offset uint64, limit uint32) ([]*dbtypes.ElTokenTransfer, uint64, bool, error) {
	// Use UNION ALL instead of OR for better index usage.
	// The second query excludes rows where from_id = accountID to avoid duplicates.
	// Push LIMIT into each UNION branch so PG can use composite indexes efficiently.
	// NULLS LAST is required to match the index definition and enable index scans.
	var sql strings.Builder
	innerLimit := offset + uint64(limit)
	args := []any{accountID, accountID, accountID}

	// Build token type filter clause
	tokenTypeFilter := ""
	if len(tokenTypes) > 0 {
		var tokenTypeArgs strings.Builder
		for i, tt := range tokenTypes {
			if i > 0 {
				fmt.Fprint(&tokenTypeArgs, ", ")
			}
			args = append(args, tt)
			fmt.Fprintf(&tokenTypeArgs, "$%v", len(args))
		}
		tokenTypeFilter = fmt.Sprintf(" AND token_type IN (%s)", tokenTypeArgs.String())
	}

	args = append(args, innerLimit)
	innerLimitIdx := len(args)

	fmt.Fprintf(&sql, `
		SELECT block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index,
			from_id, to_id, amount, amount_raw
		FROM (
			SELECT * FROM (SELECT block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index,
				from_id, to_id, amount, amount_raw
			FROM el_token_transfers WHERE from_id = $1%s
			ORDER BY block_uid DESC NULLS LAST
			LIMIT $%d)
			UNION ALL
			SELECT * FROM (SELECT block_uid, tx_hash, tx_pos, tx_idx, token_id, token_type, token_index,
				from_id, to_id, amount, amount_raw
			FROM el_token_transfers WHERE to_id = $2 AND from_id != $3%s
			ORDER BY block_uid DESC NULLS LAST
			LIMIT $%d)
		) combined
		ORDER BY block_uid DESC, tx_pos DESC, tx_idx DESC
		LIMIT $%d`, tokenTypeFilter, innerLimitIdx, tokenTypeFilter, innerLimitIdx, len(args)+1)
	args = append(args, limit)

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.SelectContext(ctx, &transfers, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el token transfers by account id combined: %v", err)
		return nil, 0, false, err
	}

	// Get count with a separate query, capped at MaxAccountTokenTransferCount
	// Build count query with same token type filter
	countArgs := []any{accountID, accountID, accountID}
	countTokenTypeFilter := ""
	if len(tokenTypes) > 0 {
		var tokenTypeArgs strings.Builder
		for i, tt := range tokenTypes {
			if i > 0 {
				fmt.Fprint(&tokenTypeArgs, ", ")
			}
			countArgs = append(countArgs, tt)
			fmt.Fprintf(&tokenTypeArgs, "$%v", len(countArgs))
		}
		countTokenTypeFilter = fmt.Sprintf(" AND token_type IN (%s)", tokenTypeArgs.String())
	}

	countSQL := fmt.Sprintf(`
		SELECT COUNT(*) FROM (
			SELECT 1 FROM el_token_transfers WHERE from_id = $1%s
			UNION ALL
			SELECT 1 FROM el_token_transfers WHERE to_id = $2 AND from_id != $3%s
			LIMIT $%d
		) limited`, countTokenTypeFilter, countTokenTypeFilter, len(countArgs)+1)
	countArgs = append(countArgs, MaxAccountTokenTransferCount)

	var totalCount uint64
	err = ReaderDb.GetContext(ctx, &totalCount, countSQL, countArgs...)
	if err != nil {
		logger.Errorf("Error while counting el token transfers by account id combined: %v", err)
		return nil, 0, false, err
	}

	moreAvailable := totalCount >= MaxAccountTokenTransferCount

	return transfers, totalCount, moreAvailable, nil
}

func DeleteElTokenTransfer(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64, txHash []byte, txIdx uint32) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM el_token_transfers WHERE block_uid = $1 AND tx_hash = $2 AND tx_idx = $3", blockUid, txHash, txIdx)
	return err
}

func DeleteElTokenTransfersByBlockUid(ctx context.Context, dbTx *sqlx.Tx, blockUid uint64) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM el_token_transfers WHERE block_uid = $1", blockUid)
	return err
}

func DeleteElTokenTransfersByTxHash(ctx context.Context, dbTx *sqlx.Tx, txHash []byte) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM el_token_transfers WHERE tx_hash = $1", txHash)
	return err
}

func DeleteElTokenTransfersByTokenID(ctx context.Context, dbTx *sqlx.Tx, tokenID uint64) error {
	_, err := dbTx.ExecContext(ctx, "DELETE FROM el_token_transfers WHERE token_id = $1", tokenID)
	return err
}
