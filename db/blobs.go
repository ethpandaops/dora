package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertBlob(ctx context.Context, tx *sqlx.Tx, blob *dbtypes.Blob) error {
	_, err := tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO blobs (
				commitment, proof, size, blob
			) VALUES ($1, $2, $3, $4)
			ON CONFLICT (commitment) DO UPDATE SET
				size = excluded.size,
				blob = excluded.blob`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO blobs (
				commitment, proof, size, blob
			) VALUES ($1, $2, $3, $4)`,
	}),
		blob.Commitment, blob.Proof, blob.Size, blob.Blob)
	if err != nil {
		return err
	}
	return nil
}

func InsertBlobAssignment(ctx context.Context, tx *sqlx.Tx, blobAssignment *dbtypes.BlobAssignment) error {
	_, err := tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO blob_assignments (
				root, commitment, slot
			) VALUES ($1, $2, $3)
			ON CONFLICT (root, commitment) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO blob_assignments (
				root, commitment, slot
			) VALUES ($1, $2, $3)`,
	}),
		blobAssignment.Root, blobAssignment.Commitment, blobAssignment.Slot)
	if err != nil {
		return err
	}
	return nil
}

func GetBlob(ctx context.Context, commitment []byte, withData bool) *dbtypes.Blob {
	blob := dbtypes.Blob{}
	var sql strings.Builder
	fmt.Fprintf(&sql, `SELECT commitment, proof, size`)
	if withData {
		fmt.Fprintf(&sql, `, blob`)
	}
	fmt.Fprintf(&sql, ` FROM blobs WHERE commitment = $1`)
	err := ReaderDb.GetContext(ctx, &blob, sql.String(), commitment)
	if err != nil {
		return nil
	}
	return &blob
}

func GetLatestBlobAssignment(ctx context.Context, commitment []byte) *dbtypes.BlobAssignment {
	blobAssignment := dbtypes.BlobAssignment{}
	err := ReaderDb.GetContext(ctx, &blobAssignment, "SELECT root, commitment, slot FROM blob_assignments WHERE commitment = $1 ORDER BY slot DESC LIMIT 1", commitment)
	if err != nil {
		return nil
	}
	return &blobAssignment
}

type BlobStatistics struct {
	BlobsLast1h            uint64
	BlobsLast24h           uint64
	BlobsLast7d            uint64
	BlobsLast18d           uint64
	BlocksWithBlobsLast1h  uint64
	BlocksWithBlobsLast24h uint64
	BlocksWithBlobsLast7d  uint64
	BlocksWithBlobsLast18d uint64
	BlobGasLast1h          uint64
	BlobGasLast24h         uint64
	BlobGasLast7d          uint64
	BlobGasLast18d         uint64
}

func GetBlobStatistics(ctx context.Context, currentSlot uint64) (*BlobStatistics, error) {
	stats := &BlobStatistics{}

	type periodStats struct {
		TotalBlobs      uint64 `db:"total_blobs"`
		BlocksWithBlobs uint64 `db:"blocks_with_blobs"`
		TotalGas        uint64 `db:"total_gas"`
	}

	var err error

	slot1h := uint64(0)
	if currentSlot > 300 {
		slot1h = currentSlot - 300
	}
	var stats1h periodStats
	err = ReaderDb.GetContext(ctx, &stats1h, `
		SELECT
			COALESCE(SUM(blob_count), 0) as total_blobs,
			COUNT(CASE WHEN blob_count > 0 THEN 1 END) as blocks_with_blobs,
			COALESCE(SUM(CASE WHEN blob_count > 0 THEN eth_gas_used ELSE 0 END), 0) as total_gas
		FROM slots
		WHERE status != 0 AND slot >= $1
	`, slot1h)
	if err != nil {
		return nil, fmt.Errorf("failed to get 1h statistics: %w", err)
	}
	stats.BlobsLast1h = stats1h.TotalBlobs
	stats.BlocksWithBlobsLast1h = stats1h.BlocksWithBlobs
	stats.BlobGasLast1h = stats1h.TotalGas

	slot24h := uint64(0)
	if currentSlot > 7200 {
		slot24h = currentSlot - 7200
	}
	var stats24h periodStats
	err = ReaderDb.GetContext(ctx, &stats24h, `
		SELECT
			COALESCE(SUM(blob_count), 0) as total_blobs,
			COUNT(CASE WHEN blob_count > 0 THEN 1 END) as blocks_with_blobs,
			COALESCE(SUM(CASE WHEN blob_count > 0 THEN eth_gas_used ELSE 0 END), 0) as total_gas
		FROM slots
		WHERE status != 0 AND slot >= $1
	`, slot24h)
	if err != nil {
		return nil, fmt.Errorf("failed to get 24h statistics: %w", err)
	}
	stats.BlobsLast24h = stats24h.TotalBlobs
	stats.BlocksWithBlobsLast24h = stats24h.BlocksWithBlobs
	stats.BlobGasLast24h = stats24h.TotalGas

	slot7d := uint64(0)
	if currentSlot > 50400 {
		slot7d = currentSlot - 50400
	}
	var stats7d periodStats
	err = ReaderDb.GetContext(ctx, &stats7d, `
		SELECT
			COALESCE(SUM(blob_count), 0) as total_blobs,
			COUNT(CASE WHEN blob_count > 0 THEN 1 END) as blocks_with_blobs,
			COALESCE(SUM(CASE WHEN blob_count > 0 THEN eth_gas_used ELSE 0 END), 0) as total_gas
		FROM slots
		WHERE status != 0 AND slot >= $1
	`, slot7d)
	if err != nil {
		return nil, fmt.Errorf("failed to get 7d statistics: %w", err)
	}
	stats.BlobsLast7d = stats7d.TotalBlobs
	stats.BlocksWithBlobsLast7d = stats7d.BlocksWithBlobs
	stats.BlobGasLast7d = stats7d.TotalGas

	slot18d := uint64(0)
	retentionSlots := uint64(4096 * 32)
	if currentSlot > retentionSlots {
		slot18d = currentSlot - retentionSlots
	}
	var stats18d periodStats
	err = ReaderDb.GetContext(ctx, &stats18d, `
		SELECT
			COALESCE(SUM(blob_count), 0) as total_blobs,
			COUNT(CASE WHEN blob_count > 0 THEN 1 END) as blocks_with_blobs,
			COALESCE(SUM(CASE WHEN blob_count > 0 THEN eth_gas_used ELSE 0 END), 0) as total_gas
		FROM slots
		WHERE status != 0 AND slot >= $1
	`, slot18d)
	if err != nil {
		return nil, fmt.Errorf("failed to get 18d statistics: %w", err)
	}
	stats.BlobsLast18d = stats18d.TotalBlobs
	stats.BlocksWithBlobsLast18d = stats18d.BlocksWithBlobs
	stats.BlobGasLast18d = stats18d.TotalGas

	return stats, nil
}
