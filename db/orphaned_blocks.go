package db

import (
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertOrphanedBlock(block *dbtypes.OrphanedBlock, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO orphaned_blocks (
				root, header_ver, header_ssz, block_ver, block_ssz, payload_ver, payload_ssz
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (root) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE INTO orphaned_blocks (
				root, header_ver, header_ssz, block_ver, block_ssz, payload_ver, payload_ssz
			) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
	}),
		block.Root, block.HeaderVer, block.HeaderSSZ, block.BlockVer, block.BlockSSZ, block.PayloadVer, block.PayloadSSZ)
	if err != nil {
		return err
	}
	return nil
}

func GetOrphanedBlock(root []byte) *dbtypes.OrphanedBlock {
	block := dbtypes.OrphanedBlock{}
	err := ReaderDb.Get(&block, `
	SELECT root, header_ver, header_ssz, block_ver, block_ssz, payload_ver, payload_ssz
	FROM orphaned_blocks
	WHERE root = $1
	`, root)
	if err != nil {
		return nil
	}
	return &block
}
