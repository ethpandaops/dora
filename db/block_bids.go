package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertBids(bids []*dbtypes.BlockBid, tx *sqlx.Tx) error {
	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO block_bids ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO block_bids ",
		}),
		"(parent_root, parent_hash, block_hash, fee_recipient, gas_limit, builder_index, slot, value, el_payment)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 9

	args := make([]any, len(bids)*fieldCount)
	for i, bid := range bids {
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

		args[argIdx+0] = bid.ParentRoot
		args[argIdx+1] = bid.ParentHash
		args[argIdx+2] = bid.BlockHash
		args[argIdx+3] = bid.FeeRecipient
		args[argIdx+4] = bid.GasLimit
		args[argIdx+5] = bid.BuilderIndex
		args[argIdx+6] = bid.Slot
		args[argIdx+7] = bid.Value
		args[argIdx+8] = bid.ElPayment
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (parent_root, parent_hash, block_hash, builder_index) DO UPDATE SET " +
			"fee_recipient = excluded.fee_recipient, " +
			"gas_limit = excluded.gas_limit, " +
			"slot = excluded.slot, " +
			"value = excluded.value, " +
			"el_payment = excluded.el_payment",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetBidsForBlockRoot(blockRoot []byte) []*dbtypes.BlockBid {
	var sql strings.Builder
	args := []any{
		blockRoot,
	}
	fmt.Fprint(&sql, `
	SELECT
		parent_root, parent_hash, block_hash, fee_recipient, gas_limit, builder_index, slot, value, el_payment
	FROM block_bids
	WHERE parent_root = $1
	ORDER BY value DESC
	`)

	bids := []*dbtypes.BlockBid{}
	err := ReaderDb.Select(&bids, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching bids for block root: %v", err)
		return nil
	}
	return bids
}

func GetBidsForSlotRange(minSlot uint64) []*dbtypes.BlockBid {
	var sql strings.Builder
	args := []any{
		minSlot,
	}
	fmt.Fprint(&sql, `
	SELECT
		parent_root, parent_hash, block_hash, fee_recipient, gas_limit, builder_index, slot, value, el_payment
	FROM block_bids
	WHERE slot >= $1
	ORDER BY slot DESC, value DESC
	`)

	bids := []*dbtypes.BlockBid{}
	err := ReaderDb.Select(&bids, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching bids for slot range: %v", err)
		return nil
	}
	return bids
}

func DeleteBidsBeforeSlot(minSlot uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`DELETE FROM block_bids WHERE slot < $1`, minSlot)
	return err
}
