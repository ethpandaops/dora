package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElBlocks(blocks []*dbtypes.ElBlock, dbTx *sqlx.Tx) error {
	if len(blocks) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_blocks ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_blocks ",
		}),
		"(block_uid, status, events, transactions, transfers)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 5

	args := make([]any, len(blocks)*fieldCount)
	for i, block := range blocks {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprint(&sql, "(")
		for f := range fieldCount {
			if f > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", argIdx+f+1)
		}
		fmt.Fprint(&sql, ")")

		args[argIdx+0] = block.BlockUid
		args[argIdx+1] = block.Status
		args[argIdx+2] = block.Events
		args[argIdx+3] = block.Transactions
		args[argIdx+4] = block.Transfers
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_uid) DO UPDATE SET status = excluded.status, events = excluded.events, transactions = excluded.transactions, transfers = excluded.transfers",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElBlock(blockUid uint64) (*dbtypes.ElBlock, error) {
	block := &dbtypes.ElBlock{}
	err := ReaderDb.Get(block, "SELECT block_uid, status, events, transactions, transfers FROM el_blocks WHERE block_uid = $1", blockUid)
	if err != nil {
		return nil, err
	}
	return block, nil
}

func GetElBlocksByUids(blockUids []uint64) ([]*dbtypes.ElBlock, error) {
	if len(blockUids) == 0 {
		return []*dbtypes.ElBlock{}, nil
	}

	var sql strings.Builder
	args := make([]any, len(blockUids))
	fmt.Fprint(&sql, "SELECT block_uid, status, events, transactions, transfers FROM el_blocks WHERE block_uid IN (")
	for i, uid := range blockUids {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprintf(&sql, "$%v", i+1)
		args[i] = uid
	}
	fmt.Fprint(&sql, ")")

	blocks := []*dbtypes.ElBlock{}
	err := ReaderDb.Select(&blocks, sql.String(), args...)
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

func DeleteElBlock(blockUid uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_blocks WHERE block_uid = $1", blockUid)
	return err
}
