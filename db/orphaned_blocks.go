package db

import (
	"context"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertOrphanedBlock(ctx context.Context, tx *sqlx.Tx, block *dbtypes.OrphanedBlock) error {
	_, err := tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO orphaned_blocks (
				root, header_ver, header_ssz, block_ver, block_ssz, block_uid
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (root) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE INTO orphaned_blocks (
				root, header_ver, header_ssz, block_ver, block_ssz, block_uid
			) VALUES ($1, $2, $3, $4, $5, $6)`,
	}),
		block.Root, block.HeaderVer, block.HeaderSSZ, block.BlockVer, block.BlockSSZ, block.BlockUid)
	if err != nil {
		return err
	}
	return nil
}

func GetOrphanedBlock(ctx context.Context, root []byte) *dbtypes.OrphanedBlock {
	block := dbtypes.OrphanedBlock{}
	err := ReaderDb.GetContext(ctx, &block, `
	SELECT root, header_ver, header_ssz, block_ver, block_ssz, block_uid
	FROM orphaned_blocks
	WHERE root = $1
	`, root)
	if err != nil {
		return nil
	}
	return &block
}
