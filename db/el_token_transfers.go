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
		"(tx_uid, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 9

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

		args[argIdx+0] = transfer.TxUid
		args[argIdx+1] = transfer.TxIdx
		args[argIdx+2] = transfer.TokenID
		args[argIdx+3] = transfer.TokenType
		args[argIdx+4] = transfer.TokenIndex
		args[argIdx+5] = transfer.FromID
		args[argIdx+6] = transfer.ToID
		args[argIdx+7] = transfer.Amount
		args[argIdx+8] = transfer.AmountRaw
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (tx_uid, tx_idx) DO UPDATE SET token_id = excluded.token_id, token_type = excluded.token_type, token_index = excluded.token_index, from_id = excluded.from_id, to_id = excluded.to_id, amount = excluded.amount, amount_raw = excluded.amount_raw",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

// GetElTokenTransferCountByTxUid returns the number of token
// transfers for a given transaction UID.
func GetElTokenTransferCountByTxUid(ctx context.Context, txUid uint64) (uint64, error) {
	var count uint64
	err := ReaderDb.GetContext(ctx, &count,
		"SELECT COUNT(*) FROM el_token_transfers WHERE tx_uid = $1",
		txUid,
	)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func GetElTokenTransfersByTxUid(ctx context.Context, txUid uint64) ([]*dbtypes.ElTokenTransfer, error) {
	transfers := []*dbtypes.ElTokenTransfer{}
	err := ReaderDb.SelectContext(ctx, &transfers, "SELECT tx_uid, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw FROM el_token_transfers WHERE tx_uid = $1 ORDER BY tx_idx DESC", txUid)
	if err != nil {
		return nil, err
	}
	return transfers, nil
}

// elTokenTransferColumns is the column list for el_token_transfers queries.
const elTokenTransferColumns = "tx_uid, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw"

// appendElTokenTransferFilterConds appends the WHERE conditions for an
// ElTokenTransferFilter and returns the filterOp ("WHERE"/"AND") to continue
// with. Slot bounds map onto the tx_uid sort key (free range on the index);
// token_id / from_id / to_id use existing composite indexes; the rest are
// predicates.
func appendElTokenTransferFilterConds(sql *strings.Builder, args *[]any, filter *dbtypes.ElTokenTransferFilter, filterOp string) string {
	if filter == nil {
		return filterOp
	}
	add := func(cond string, val any) {
		*args = append(*args, val)
		fmt.Fprintf(sql, " %s %s $%d", filterOp, cond, len(*args))
		filterOp = "AND"
	}
	if filter.MinSlot != nil {
		add("tx_uid >=", *filter.MinSlot<<32)
	}
	if filter.MaxSlot != nil {
		add("tx_uid <", (*filter.MaxSlot+1)<<32)
	}
	if filter.TokenID != nil {
		add("token_id =", *filter.TokenID)
	}
	if filter.FromID > 0 {
		add("from_id =", filter.FromID)
	}
	if filter.ToID > 0 {
		add("to_id =", filter.ToID)
	}
	if filter.MinAmount != nil {
		add("amount >=", *filter.MinAmount)
	}
	if filter.MaxAmount != nil {
		add("amount <=", *filter.MaxAmount)
	}
	if len(filter.TokenTypes) > 0 {
		placeholders := make([]string, len(filter.TokenTypes))
		for i, t := range filter.TokenTypes {
			*args = append(*args, t)
			placeholders[i] = fmt.Sprintf("$%d", len(*args))
		}
		fmt.Fprintf(sql, " %s token_type IN (%s)", filterOp, strings.Join(placeholders, ", "))
		filterOp = "AND"
	}
	return filterOp
}

// GetElTokenTransfersPrevAnchor supports backward navigation for the keyset
// token-transfers list (global or token-scoped via filter.TokenID). Given the
// current page's top cursor (afterTxUid, afterTxIdx) it probes the rows
// immediately newer (ascending) and returns the previous page's boundary
// cursor, whether any newer row exists (hasPrev), and whether the previous page
// is the newest page (atFirst).
func GetElTokenTransfersPrevAnchor(ctx context.Context, filter *dbtypes.ElTokenTransferFilter, afterTxUid uint64, afterTxIdx uint32, limit uint32) (uint64, uint32, bool, bool, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, "SELECT tx_uid, tx_idx FROM el_token_transfers")

	filterOp := appendElTokenTransferFilterConds(&sql, &args, filter, "WHERE")
	args = append(args, afterTxUid, afterTxIdx)
	fmt.Fprintf(&sql, " %v (tx_uid > $%v OR (tx_uid = $%v AND tx_idx > $%v))", filterOp, len(args)-1, len(args)-1, len(args))
	args = append(args, limit+1)
	fmt.Fprintf(&sql, " ORDER BY tx_uid ASC, tx_idx ASC LIMIT $%v", len(args))

	type cursorRow struct {
		TxUid uint64 `db:"tx_uid"`
		TxIdx uint32 `db:"tx_idx"`
	}
	rows := []cursorRow{}
	if err := ReaderDb.SelectContext(ctx, &rows, sql.String(), args...); err != nil {
		logger.Errorf("Error while probing previous transfer anchor: %v", err)
		return 0, 0, false, false, err
	}

	hasPrev := len(rows) > 0
	if uint32(len(rows)) > limit {
		return rows[limit].TxUid, rows[limit].TxIdx, hasPrev, false, nil
	}
	return 0, 0, hasPrev, true, nil
}

// GetElTokenTransfersFiltered returns token transfers matching the given filter
// using keyset pagination, ordered by (tx_uid, tx_idx) DESC. Pass
// beforeTxUid = 0 for the first (newest) page, then the (tx_uid, tx_idx) of the
// last returned row. Returns the page (up to limit rows) and whether more
// (older) rows exist.
func GetElTokenTransfersFiltered(ctx context.Context, filter *dbtypes.ElTokenTransferFilter, beforeTxUid uint64, beforeTxIdx uint32, limit uint32) ([]*dbtypes.ElTokenTransfer, bool, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprintf(&sql, "SELECT %s FROM el_token_transfers", elTokenTransferColumns)

	filterOp := appendElTokenTransferFilterConds(&sql, &args, filter, "WHERE")
	if beforeTxUid > 0 {
		args = append(args, beforeTxUid, beforeTxIdx)
		fmt.Fprintf(&sql, " %v (tx_uid < $%v OR (tx_uid = $%v AND tx_idx < $%v))", filterOp, len(args)-1, len(args)-1, len(args))
	}

	args = append(args, limit+1)
	fmt.Fprintf(&sql, " ORDER BY tx_uid DESC, tx_idx DESC LIMIT $%v", len(args))

	transfers := []*dbtypes.ElTokenTransfer{}
	if err := ReaderDb.SelectContext(ctx, &transfers, sql.String(), args...); err != nil {
		logger.Errorf("Error while fetching filtered el token transfers: %v", err)
		return nil, false, err
	}

	hasMore := false
	if uint32(len(transfers)) > limit {
		hasMore = true
		transfers = transfers[:limit]
	}
	return transfers, hasMore, nil
}

// buildElAccountTransferQuery builds the keyset query for an account's token
// transfers, mirroring buildElAccountTxQuery but with the (tx_uid, tx_idx)
// composite cursor. direction restricts to the from-side, to-side, or both; the
// filter's FromID/ToID are counterparty filters applied to the opposite side.
func buildElAccountTransferQuery(accountID uint64, direction uint8, filter *dbtypes.ElTokenTransferFilter, curUid uint64, curIdx uint32, limit uint32, reverse bool, colList string) (string, []any) {
	args := []any{}
	cmp, order := "<", "DESC"
	if reverse {
		cmp, order = ">", "ASC"
	}

	var counterFrom, counterTo uint64
	uniform := dbtypes.ElTokenTransferFilter{}
	if filter != nil {
		counterFrom, counterTo = filter.FromID, filter.ToID
		uniform = *filter
		uniform.FromID, uniform.ToID = 0, 0
	}

	branch := func(sql *strings.Builder, accountIsFrom bool) {
		fmt.Fprintf(sql, "SELECT %s FROM el_token_transfers WHERE ", colList)
		if accountIsFrom {
			args = append(args, accountID)
			fmt.Fprintf(sql, "from_id = $%d", len(args))
			if counterTo > 0 {
				args = append(args, counterTo)
				fmt.Fprintf(sql, " AND to_id = $%d", len(args))
			}
		} else {
			args = append(args, accountID, accountID)
			fmt.Fprintf(sql, "to_id = $%d AND from_id != $%d", len(args)-1, len(args))
			if counterFrom > 0 {
				args = append(args, counterFrom)
				fmt.Fprintf(sql, " AND from_id = $%d", len(args))
			}
		}
		appendElTokenTransferFilterConds(sql, &args, &uniform, "AND")
		if curUid > 0 {
			args = append(args, curUid, curUid, curIdx)
			fmt.Fprintf(sql, " AND (tx_uid %s $%d OR (tx_uid = $%d AND tx_idx %s $%d))", cmp, len(args)-2, len(args)-1, cmp, len(args))
		}
		args = append(args, limit+1)
		fmt.Fprintf(sql, " ORDER BY tx_uid %s, tx_idx %s LIMIT $%d", order, order, len(args))
	}

	var sql strings.Builder
	switch direction {
	case AccountDirectionOut, AccountDirectionIn:
		fmt.Fprintf(&sql, "SELECT %s FROM (", colList)
		branch(&sql, direction == AccountDirectionOut)
		args = append(args, limit+1)
		fmt.Fprintf(&sql, ") AS x ORDER BY tx_uid %s, tx_idx %s LIMIT $%d", order, order, len(args))
	default:
		fmt.Fprintf(&sql, "SELECT %s FROM (SELECT * FROM (", colList)
		branch(&sql, true)
		fmt.Fprint(&sql, ") AS a UNION ALL SELECT * FROM (")
		branch(&sql, false)
		args = append(args, limit+1)
		fmt.Fprintf(&sql, ") AS b) combined ORDER BY tx_uid %s, tx_idx %s LIMIT $%d", order, order, len(args))
	}
	return sql.String(), args
}

// GetElTokenTransfersByAccount returns the account's token transfers with keyset
// pagination, restricted by direction and filter. Returns the page (up to limit)
// and whether more (older) rows exist.
func GetElTokenTransfersByAccount(ctx context.Context, accountID uint64, direction uint8, filter *dbtypes.ElTokenTransferFilter, beforeTxUid uint64, beforeTxIdx uint32, limit uint32) ([]*dbtypes.ElTokenTransfer, bool, error) {
	sqlStr, args := buildElAccountTransferQuery(accountID, direction, filter, beforeTxUid, beforeTxIdx, limit, false, elTokenTransferColumns)
	transfers := []*dbtypes.ElTokenTransfer{}
	if err := ReaderDb.SelectContext(ctx, &transfers, sqlStr, args...); err != nil {
		logger.Errorf("Error while fetching el token transfers by account: %v", err)
		return nil, false, err
	}
	hasMore := false
	if uint32(len(transfers)) > limit {
		hasMore = true
		transfers = transfers[:limit]
	}
	return transfers, hasMore, nil
}

// GetElTokenTransfersByAccountPrevAnchor probes for rows newer than the current
// page's top to drive the First/< controls. Returns the previous page's boundary
// cursor (when not atFirst), whether any newer row exists, and whether the
// previous page is the newest page.
func GetElTokenTransfersByAccountPrevAnchor(ctx context.Context, accountID uint64, direction uint8, filter *dbtypes.ElTokenTransferFilter, afterTxUid uint64, afterTxIdx uint32, limit uint32) (uint64, uint32, bool, bool, error) {
	sqlStr, args := buildElAccountTransferQuery(accountID, direction, filter, afterTxUid, afterTxIdx, limit, true, "tx_uid, tx_idx")
	rows := []struct {
		TxUid uint64 `db:"tx_uid"`
		TxIdx uint32 `db:"tx_idx"`
	}{}
	if err := ReaderDb.SelectContext(ctx, &rows, sqlStr, args...); err != nil {
		logger.Errorf("Error while probing previous account transfer anchor: %v", err)
		return 0, 0, false, false, err
	}
	hasPrev := len(rows) > 0
	if uint32(len(rows)) > limit {
		return rows[limit].TxUid, rows[limit].TxIdx, hasPrev, false, nil
	}
	return 0, 0, hasPrev, true, nil
}
