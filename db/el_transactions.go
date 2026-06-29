package db

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ethpandaops/dora/blockdb"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// elTransactionColumns is the column list for el_transactions queries.
const elTransactionColumns = "tx_uid, block_uid, tx_hash, from_id, to_id, nonce, revert_id, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, eff_gas_price, event_count"

func InsertElTransactions(ctx context.Context, dbTx *sqlx.Tx, txs []*dbtypes.ElTransaction) error {
	if len(txs) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_transactions ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_transactions ",
		}),
		"(tx_uid, block_uid, tx_hash, from_id, to_id, nonce, revert_id, amount, amount_raw, method_id, gas_limit, gas_used, gas_price, tip_price, blob_count, block_number, tx_type, eff_gas_price, event_count)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 19

	args := make([]any, len(txs)*fieldCount)
	for i, tx := range txs {
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

		args[argIdx+0] = tx.TxUid
		args[argIdx+1] = tx.BlockUid
		args[argIdx+2] = tx.TxHash
		args[argIdx+3] = tx.FromID
		args[argIdx+4] = tx.ToID
		args[argIdx+5] = tx.Nonce
		args[argIdx+6] = tx.RevertID
		args[argIdx+7] = tx.Amount
		args[argIdx+8] = tx.AmountRaw
		args[argIdx+9] = tx.MethodID
		args[argIdx+10] = tx.GasLimit
		args[argIdx+11] = tx.GasUsed
		args[argIdx+12] = tx.GasPrice
		args[argIdx+13] = tx.TipPrice
		args[argIdx+14] = tx.BlobCount
		args[argIdx+15] = tx.BlockNumber
		args[argIdx+16] = tx.TxType
		args[argIdx+17] = tx.EffGasPrice
		args[argIdx+18] = tx.EventCount
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (tx_uid) DO UPDATE SET block_uid = excluded.block_uid, tx_hash = excluded.tx_hash, from_id = excluded.from_id, to_id = excluded.to_id, nonce = excluded.nonce, revert_id = excluded.revert_id, amount = excluded.amount, amount_raw = excluded.amount_raw, method_id = excluded.method_id, gas_limit = excluded.gas_limit, gas_used = excluded.gas_used, gas_price = excluded.gas_price, tip_price = excluded.tip_price, blob_count = excluded.blob_count, block_number = excluded.block_number, tx_type = excluded.tx_type, eff_gas_price = excluded.eff_gas_price, event_count = excluded.event_count",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

// GetElTransactionsByHash resolves transactions by full hash via the tx-hash
// index (the dedicated tx_hash column index was dropped). The index returns
// candidate tx_uids for the 10-byte prefix; rows are then filtered by the full
// hash to discard prefix collisions. Falls back to a direct lookup if no index
// is available (e.g. blockdb disabled).
func GetElTransactionsByHash(ctx context.Context, txHash []byte) ([]*dbtypes.ElTransaction, error) {
	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsTxHashIndex() {
		txs := []*dbtypes.ElTransaction{}
		err := ReaderDb.SelectContext(ctx, &txs, "SELECT "+elTransactionColumns+" FROM el_transactions WHERE tx_hash = $1 ORDER BY block_uid DESC", txHash)
		if err != nil {
			return nil, err
		}
		return txs, nil
	}

	uids, err := blockdb.GlobalBlockDb.LookupTxHash(ctx, bdbtypes.HashPrefix(txHash))
	if err != nil {
		return nil, err
	}
	if len(uids) == 0 {
		return []*dbtypes.ElTransaction{}, nil
	}

	rows, err := GetElTransactionsByTxUids(ctx, uids)
	if err != nil {
		return nil, err
	}

	// Collision guard: keep only rows whose full hash matches, ordered by
	// block_uid DESC (canonical first), preserving prior behaviour.
	matched := make([]*dbtypes.ElTransaction, 0, len(rows))
	for _, r := range rows {
		if bytes.Equal(r.TxHash, txHash) {
			matched = append(matched, r)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].BlockUid > matched[j].BlockUid })
	return matched, nil
}

// GetElTransactionsByTxUids returns all transaction records matching the given
// tx_uids. Used by handlers to resolve tx_hash and other fields from dependent
// table results that only store tx_uid.
func GetElTransactionsByTxUids(ctx context.Context, txUids []uint64) ([]*dbtypes.ElTransaction, error) {
	if len(txUids) == 0 {
		return []*dbtypes.ElTransaction{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(txUids))

	fmt.Fprint(&sql, "SELECT "+elTransactionColumns+" FROM el_transactions WHERE tx_uid IN (")
	for i, uid := range txUids {
		args[i] = uid
	}
	appendDollarPlaceholders(&sql, 1, len(txUids), ", ")
	fmt.Fprint(&sql, ")")

	txs := []*dbtypes.ElTransaction{}
	if err := ReaderDb.SelectContext(ctx, &txs, sql.String(), args...); err != nil {
		return nil, err
	}
	return txs, nil
}

func GetElTransactionsByBlockUid(ctx context.Context, blockUid uint64) ([]*dbtypes.ElTransaction, error) {
	txs := []*dbtypes.ElTransaction{}
	err := ReaderDb.SelectContext(ctx, &txs, "SELECT "+elTransactionColumns+" FROM el_transactions WHERE block_uid = $1", blockUid)
	if err != nil {
		return nil, err
	}
	return txs, nil
}

// appendElTransactionFilterConds appends the WHERE conditions for an
// ElTransactionFilter and returns the filterOp ("WHERE"/"AND") to continue with.
// Slot bounds map onto the tx_uid sort key (tx_uid = slot<<32 | ...), so they
// are a free range on the primary index; the rest are predicates.
func appendElTransactionFilterConds(sql *strings.Builder, args *[]any, filter *dbtypes.ElTransactionFilter, filterOp string) string {
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
	if filter.FromID > 0 {
		add("from_id =", filter.FromID)
	}
	if filter.ToID > 0 {
		add("to_id =", filter.ToID)
	}
	if filter.Reverted != nil {
		// revert_id 0 = success; any value > 0 = reverted.
		if *filter.Reverted {
			add("revert_id >", 0)
		} else {
			add("revert_id =", 0)
		}
	}
	if filter.MinGasUsed != nil {
		add("gas_used >=", *filter.MinGasUsed)
	}
	if filter.MaxGasUsed != nil {
		add("gas_used <=", *filter.MaxGasUsed)
	}
	if filter.MinAmount != nil {
		add("amount >=", *filter.MinAmount)
	}
	if filter.MaxAmount != nil {
		add("amount <=", *filter.MaxAmount)
	}
	if filter.MinTip != nil {
		add("tip_price >=", *filter.MinTip)
	}
	if filter.MaxTip != nil {
		add("tip_price <=", *filter.MaxTip)
	}
	if len(filter.TxTypes) > 0 {
		// Match each type both plain and with the create flag set (one extra value).
		placeholders := make([]string, 0, len(filter.TxTypes)*2)
		for _, t := range filter.TxTypes {
			*args = append(*args, t, t|dbtypes.ElTxFlagCreate)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(*args)-1), fmt.Sprintf("$%d", len(*args)))
		}
		fmt.Fprintf(sql, " %s tx_type IN (%s)", filterOp, strings.Join(placeholders, ", "))
		filterOp = "AND"
	}
	return filterOp
}

// GetElTransactionsFiltered returns transactions matching the given filter
// using keyset (boundary) pagination, ordered by tx_uid DESC. Pass
// beforeTxUid = 0 for the first (newest) page, then the tx_uid of the last
// returned row to fetch the next (older) page. Returns the page (up to limit
// rows) and whether more (older) rows exist. Fetches limit+1 rows to detect
// the next page without a separate count scan.
func GetElTransactionsFiltered(ctx context.Context, filter *dbtypes.ElTransactionFilter, beforeTxUid uint64, limit uint32) ([]*dbtypes.ElTransaction, bool, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprintf(&sql, "SELECT %s FROM el_transactions", elTransactionColumns)

	filterOp := appendElTransactionFilterConds(&sql, &args, filter, "WHERE")
	if beforeTxUid > 0 {
		args = append(args, beforeTxUid)
		fmt.Fprintf(&sql, " %v tx_uid < $%v", filterOp, len(args))
	}

	args = append(args, limit+1)
	fmt.Fprintf(&sql, " ORDER BY tx_uid DESC LIMIT $%v", len(args))

	txs := []*dbtypes.ElTransaction{}
	if err := ReaderDb.SelectContext(ctx, &txs, sql.String(), args...); err != nil {
		logger.Errorf("Error while fetching filtered el transactions: %v", err)
		return nil, false, err
	}

	hasMore := false
	if uint32(len(txs)) > limit {
		hasMore = true
		txs = txs[:limit]
	}
	return txs, hasMore, nil
}

// GetElTransactionsPrevAnchor supports backward navigation for the keyset
// transactions list. Given afterTxUid (the tx_uid of the current page's top
// row), it probes for the rows immediately newer (ascending) and returns:
//   - prevBeforeTxUid: the boundary cursor for the previous page (the
//     (limit+1)-th newer row), valid only when atFirst is false;
//   - hasPrev: whether any newer row exists at all (drives enabling First/<);
//   - atFirst: true when fewer than limit+1 newer rows exist, i.e. the previous
//     page is the newest page.
func GetElTransactionsPrevAnchor(ctx context.Context, filter *dbtypes.ElTransactionFilter, afterTxUid uint64, limit uint32) (uint64, bool, bool, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, "SELECT tx_uid FROM el_transactions")

	filterOp := appendElTransactionFilterConds(&sql, &args, filter, "WHERE")
	args = append(args, afterTxUid)
	fmt.Fprintf(&sql, " %v tx_uid > $%v", filterOp, len(args))
	args = append(args, limit+1)
	fmt.Fprintf(&sql, " ORDER BY tx_uid ASC LIMIT $%v", len(args))

	uids := []uint64{}
	if err := ReaderDb.SelectContext(ctx, &uids, sql.String(), args...); err != nil {
		logger.Errorf("Error while probing previous tx anchor: %v", err)
		return 0, false, false, err
	}

	hasPrev := len(uids) > 0
	if uint32(len(uids)) > limit {
		return uids[limit], hasPrev, false, nil
	}
	return 0, hasPrev, true, nil
}

// Account transaction direction: which side of the tx the account is on.
const (
	AccountDirectionAll uint8 = 0
	AccountDirectionOut uint8 = 1 // account is sender (from_id)
	AccountDirectionIn  uint8 = 2 // account is recipient (to_id)
)

// buildElAccountTxQuery builds the keyset query for an account's transactions.
// direction restricts to the from-side, to-side, or both (UNION). The filter's
// FromID/ToID are counterparty filters applied to the opposite side; the other
// filter fields apply uniformly. cursor + reverse drive keyset pagination
// (forward: tx_uid < cursor DESC; reverse/prev-probe: tx_uid > cursor ASC).
// colList selects the projected columns (full row vs just tx_uid for probes).
func buildElAccountTxQuery(accountID uint64, direction uint8, filter *dbtypes.ElTransactionFilter, cursor uint64, limit uint32, reverse bool, colList string) (string, []any) {
	args := []any{}
	cmp, order := "<", "DESC"
	if reverse {
		cmp, order = ">", "ASC"
	}

	var counterFrom, counterTo uint64
	uniform := dbtypes.ElTransactionFilter{}
	if filter != nil {
		counterFrom, counterTo = filter.FromID, filter.ToID
		uniform = *filter
		uniform.FromID, uniform.ToID = 0, 0
	}

	branch := func(sql *strings.Builder, accountIsFrom bool) {
		fmt.Fprintf(sql, "SELECT %s FROM el_transactions WHERE ", colList)
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
		appendElTransactionFilterConds(sql, &args, &uniform, "AND")
		if cursor > 0 {
			args = append(args, cursor)
			fmt.Fprintf(sql, " AND tx_uid %s $%d", cmp, len(args))
		}
		args = append(args, limit+1)
		fmt.Fprintf(sql, " ORDER BY block_uid %s NULLS LAST LIMIT $%d", order, len(args))
	}

	var sql strings.Builder
	switch direction {
	case AccountDirectionOut, AccountDirectionIn:
		fmt.Fprintf(&sql, "SELECT %s FROM (", colList)
		branch(&sql, direction == AccountDirectionOut)
		args = append(args, limit+1)
		fmt.Fprintf(&sql, ") AS x ORDER BY tx_uid %s LIMIT $%d", order, len(args))
	default:
		fmt.Fprintf(&sql, "SELECT %s FROM (SELECT * FROM (", colList)
		branch(&sql, true)
		fmt.Fprint(&sql, ") AS a UNION ALL SELECT * FROM (")
		branch(&sql, false)
		args = append(args, limit+1)
		fmt.Fprintf(&sql, ") AS b) combined ORDER BY tx_uid %s LIMIT $%d", order, len(args))
	}
	return sql.String(), args
}

// GetElTransactionsByAccount returns the account's transactions with keyset
// pagination, restricted by direction and filter. Returns the page (up to
// limit) and whether more (older) rows exist.
func GetElTransactionsByAccount(ctx context.Context, accountID uint64, direction uint8, filter *dbtypes.ElTransactionFilter, beforeTxUid uint64, limit uint32) ([]*dbtypes.ElTransaction, bool, error) {
	sqlStr, args := buildElAccountTxQuery(accountID, direction, filter, beforeTxUid, limit, false, elTransactionColumns)
	txs := []*dbtypes.ElTransaction{}
	if err := ReaderDb.SelectContext(ctx, &txs, sqlStr, args...); err != nil {
		logger.Errorf("Error while fetching el transactions by account: %v", err)
		return nil, false, err
	}
	hasMore := false
	if uint32(len(txs)) > limit {
		hasMore = true
		txs = txs[:limit]
	}
	return txs, hasMore, nil
}

// GetElTransactionsByAccountPrevAnchor probes for rows newer than afterTxUid
// (the current page's top) to drive the First/< controls. Returns the previous
// page's boundary cursor (when atFirst is false), whether any newer row exists,
// and whether the previous page is the newest page.
func GetElTransactionsByAccountPrevAnchor(ctx context.Context, accountID uint64, direction uint8, filter *dbtypes.ElTransactionFilter, afterTxUid uint64, limit uint32) (uint64, bool, bool, error) {
	sqlStr, args := buildElAccountTxQuery(accountID, direction, filter, afterTxUid, limit, true, "tx_uid")
	uids := []uint64{}
	if err := ReaderDb.SelectContext(ctx, &uids, sqlStr, args...); err != nil {
		logger.Errorf("Error while probing previous account tx anchor: %v", err)
		return 0, false, false, err
	}
	hasPrev := len(uids) > 0
	if uint32(len(uids)) > limit {
		return uids[limit], hasPrev, false, nil
	}
	return 0, hasPrev, true, nil
}
