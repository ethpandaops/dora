package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/jmoiron/sqlx"
)

func InsertDepositTxs(depositTxs []*dbtypes.DepositTx, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO deposit_txs ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO deposit_txs ",
		}),
		"(deposit_index, block_number, block_time, block_root, publickey, withdrawalcredentials, amount, signature, valid_signature, orphaned, tx_hash, tx_sender, tx_target, fork_id)",
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

		args[argIdx+0] = depositTx.Index
		args[argIdx+1] = depositTx.BlockNumber
		args[argIdx+2] = depositTx.BlockTime
		args[argIdx+3] = depositTx.BlockRoot
		args[argIdx+4] = depositTx.PublicKey
		args[argIdx+5] = depositTx.WithdrawalCredentials
		args[argIdx+6] = depositTx.Amount
		args[argIdx+7] = depositTx.Signature
		args[argIdx+8] = depositTx.ValidSignature
		args[argIdx+9] = depositTx.Orphaned
		args[argIdx+10] = depositTx.TxHash
		args[argIdx+11] = depositTx.TxSender
		args[argIdx+12] = depositTx.TxTarget
		args[argIdx+13] = depositTx.ForkId
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (deposit_index, block_root) DO UPDATE SET orphaned = excluded.orphaned, fork_id = excluded.fork_id",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetDepositTxs(firstIndex uint64, limit uint32) []*dbtypes.DepositTx {
	var sql strings.Builder
	args := []any{}
	fmt.Fprint(&sql, `
	SELECT
		deposit_index, block_number, block_time, block_root, publickey, withdrawalcredentials, amount, signature, valid_signature, orphaned, tx_hash, tx_sender, tx_target, fork_id
	FROM deposit_txs
	`)
	if firstIndex > 0 {
		args = append(args, firstIndex)
		fmt.Fprintf(&sql, " WHERE deposit_index <= $%v ", len(args))
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	ORDER BY deposit_index DESC
	LIMIT $%v
	`, len(args))

	depositTxs := []*dbtypes.DepositTx{}
	err := ReaderDb.Select(&depositTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching deposit txs: %v", err)
		return nil
	}
	return depositTxs
}

func GetDepositTxsByIndexes(indexes []uint64) []*dbtypes.DepositTx {
	depositTxs := []*dbtypes.DepositTx{}
	if len(indexes) == 0 {
		return depositTxs
	}

	var sql strings.Builder
	args := []interface{}{}

	fmt.Fprint(&sql, `SELECT deposit_txs.*
		FROM deposit_txs
		WHERE deposit_index IN (
	`)

	for idx, index := range indexes {
		if idx > 0 {
			fmt.Fprintf(&sql, ", ")
		}
		args = append(args, index)
		fmt.Fprintf(&sql, "$%v", len(args))
	}
	fmt.Fprintf(&sql, ")")

	err := ReaderDb.Select(&depositTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching deposit txs by indexes: %v", err)
		return nil
	}

	return depositTxs
}

func GetDepositTxsFiltered(offset uint64, limit uint32, canonicalForkIds []uint64, filter *dbtypes.DepositTxFilter) ([]*dbtypes.DepositTx, uint64, error) {
	var sql strings.Builder
	args := []any{}
	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT
			deposit_index, block_number, block_time, block_root, publickey, withdrawalcredentials, amount, signature, valid_signature, orphaned, tx_hash, tx_sender, tx_target, fork_id
		FROM deposit_txs
	`)

	filterOp := "WHERE"
	if filter.MinIndex > 0 {
		args = append(args, filter.MinIndex)
		fmt.Fprintf(&sql, " %v deposit_index >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxIndex > 0 {
		args = append(args, filter.MaxIndex)
		fmt.Fprintf(&sql, " %v deposit_index <= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.Address) > 0 {
		args = append(args, filter.Address)
		fmt.Fprintf(&sql, " %v tx_sender = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.TargetAddress) > 0 {
		args = append(args, filter.TargetAddress)
		fmt.Fprintf(&sql, " %v tx_target = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.PublicKey) > 0 {
		args = append(args, filter.PublicKey)
		fmt.Fprintf(&sql, " %v publickey = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.PublicKeys) > 0 {
		fmt.Fprintf(&sql, " %v publickey IN (", filterOp)
		for i, pubKey := range filter.PublicKeys {
			if i > 0 {
				fmt.Fprintf(&sql, ", ")
			}
			args = append(args, pubKey)
			fmt.Fprintf(&sql, "$%v", len(args))
		}
		fmt.Fprintf(&sql, ")")
		filterOp = "AND"
	}
	if len(filter.WithdrawalAddress) > 0 {
		wdcreds1 := make([]byte, 32)
		wdcreds1[0] = 0x01
		copy(wdcreds1[12:], filter.WithdrawalAddress)
		wdcreds2 := make([]byte, 32)
		wdcreds2[0] = 0x02
		copy(wdcreds2[12:], filter.WithdrawalAddress)
		args = append(args, wdcreds1, wdcreds2)
		fmt.Fprintf(&sql, " %v (withdrawalcredentials = $%v OR withdrawalcredentials = $%v)", filterOp, len(args)-1, len(args))
		filterOp = "AND"
	}
	if filter.MinAmount > 0 {
		args = append(args, filter.MinAmount*utils.GWEI.Uint64())
		fmt.Fprintf(&sql, " %v amount >= $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if filter.MaxAmount > 0 {
		args = append(args, filter.MaxAmount*utils.GWEI.Uint64())
		fmt.Fprintf(&sql, " %v amount <= $%v", filterOp, len(args))
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

	if filter.WithValid == 0 {
		fmt.Fprintf(&sql, " %v valid_signature IN (1, 2)", filterOp)
		filterOp = "AND"
	} else if filter.WithValid == 2 {
		fmt.Fprintf(&sql, " %v valid_signature = 0", filterOp)
		filterOp = "AND"
	}

	args = append(args, limit)
	fmt.Fprintf(&sql, `) 
	SELECT 
		count(*) AS deposit_index, 
		0 AS block_number, 
		0 AS block_time, 
		null AS block_root, 
		null AS publickey, 
		null AS withdrawalcredentials,
		0 AS amount, 
		null AS signature, 
		false AS valid_signature, 
		false AS orphaned, 
		null AS tx_hash, 
		null AS tx_sender, 
		null AS tx_target,
		0 AS fork_id
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY deposit_index DESC 
	LIMIT $%v 
	`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v ", len(args))
	}
	fmt.Fprintf(&sql, ") AS t1")

	depositTxs := []*dbtypes.DepositTx{}
	err := ReaderDb.Select(&depositTxs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered deposit txs: %v", err)
		return nil, 0, err
	}

	return depositTxs[1:], depositTxs[0].Index, nil
}
