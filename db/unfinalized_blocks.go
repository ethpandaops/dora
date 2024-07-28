package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/jmoiron/sqlx"
)

func InsertUnfinalizedBlock(block *dbtypes.UnfinalizedBlock, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO unfinalized_blocks (
				root, slot, header_ver, header_ssz, block_ver, block_ssz
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (root) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE INTO unfinalized_blocks (
				root, slot, header_ver, header_ssz, block_ver, block_ssz
			) VALUES ($1, $2, $3, $4, $5, $6)`,
	}),
		block.Root, block.Slot, block.HeaderVer, block.HeaderSSZ, block.BlockVer, block.BlockSSZ)
	if err != nil {
		return err
	}
	return nil
}

func GetUnfinalizedBlocks(filter *dbtypes.UnfinalizedBlockFilter) []*dbtypes.UnfinalizedBlock {
	blockRefs := []*dbtypes.UnfinalizedBlock{}

	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `SELECT root, slot, header_ver, header_ssz`)

	if filter == nil || filter.WithBody {
		fmt.Fprint(&sql, `, block_ver, block_ssz`)
	}
	fmt.Fprint(&sql, `
	FROM unfinalized_blocks
	`)

	if filter != nil {
		filterOp := "WHERE"

		if filter.MinSlot > 0 {
			args = append(args, filter.MinSlot)
			fmt.Fprintf(&sql, " %v slot >= $%v", filterOp, len(args))
			filterOp = "AND"
		}

		if filter.MaxSlot > 0 {
			args = append(args, filter.MaxSlot)
			fmt.Fprintf(&sql, " %v slot <= $%v", filterOp, len(args))
			filterOp = "AND"
		}
	}

	err := ReaderDb.Select(&blockRefs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized blocks: %v", err)
		return nil
	}
	return blockRefs
}

func GetUnfinalizedBlock(root []byte) *dbtypes.UnfinalizedBlock {
	block := dbtypes.UnfinalizedBlock{}
	err := ReaderDb.Get(&block, `
	SELECT root, slot, header_ver, header_ssz, block_ver, block_ssz
	FROM unfinalized_blocks
	WHERE root = $1
	`, root)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized block 0x%x: %v", root, err)
		return nil
	}
	return &block
}

func DeleteUnfinalizedBefore(slot uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`DELETE FROM unfinalized_blocks WHERE slot < $1`, slot)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM unfinalized_epochs WHERE epoch < $1`, utils.EpochOfSlot(slot))
	if err != nil {
		return err
	}
	return nil
}
