package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertBlob(blob *dbtypes.Blob, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
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

func InsertBlobAssignment(blobAssignment *dbtypes.BlobAssignment, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
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

func GetBlob(commitment []byte, withData bool) *dbtypes.Blob {
	blob := dbtypes.Blob{}
	var sql strings.Builder
	fmt.Fprintf(&sql, `SELECT commitment, proof, size`)
	if withData {
		fmt.Fprintf(&sql, `, blob`)
	}
	fmt.Fprintf(&sql, ` FROM blobs WHERE commitment = $1`)
	err := ReaderDb.Get(&blob, sql.String(), commitment)
	if err != nil {
		return nil
	}
	return &blob
}

func GetLatestBlobAssignment(commitment []byte) *dbtypes.BlobAssignment {
	blobAssignment := dbtypes.BlobAssignment{}
	err := ReaderDb.Get(&blobAssignment, "SELECT root, commitment, slot FROM blob_assignments WHERE commitment = $1 ORDER BY slot DESC LIMIT 1", commitment)
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

func GetBlobStatistics(currentSlot uint64) (*BlobStatistics, error) {
	stats := &BlobStatistics{}

	var err error
	slot1h := uint64(0)
	if currentSlot > 300 {
		slot1h = currentSlot - 300
	}
	err = ReaderDb.Get(&stats.BlobsLast1h, `
		SELECT COALESCE(SUM(blob_count), 0)
		FROM slots
		WHERE status != 0 AND slot >= $1
	`, slot1h)
	if err != nil {
		return nil, fmt.Errorf("failed to get blobs last 1h: %w", err)
	}

	slot24h := uint64(0)
	if currentSlot > 7200 {
		slot24h = currentSlot - 7200
	}
	err = ReaderDb.Get(&stats.BlobsLast24h, `
		SELECT COALESCE(SUM(blob_count), 0)
		FROM slots
		WHERE status != 0 AND slot >= $1
	`, slot24h)
	if err != nil {
		return nil, fmt.Errorf("failed to get blobs last 24h: %w", err)
	}

	slot7d := uint64(0)
	if currentSlot > 50400 {
		slot7d = currentSlot - 50400
	}
	err = ReaderDb.Get(&stats.BlobsLast7d, `
		SELECT COALESCE(SUM(blob_count), 0)
		FROM slots
		WHERE status != 0 AND slot >= $1
	`, slot7d)
	if err != nil {
		return nil, fmt.Errorf("failed to get blobs last 7d: %w", err)
	}

	slot18d := uint64(0)
	retentionSlots := uint64(4096 * 32)
	if currentSlot > retentionSlots {
		slot18d = currentSlot - retentionSlots
	}
	err = ReaderDb.Get(&stats.BlobsLast18d, `
		SELECT COALESCE(SUM(blob_count), 0)
		FROM slots
		WHERE status != 0 AND slot >= $1
	`, slot18d)
	if err != nil {
		return nil, fmt.Errorf("failed to get blobs last 18d: %w", err)
	}

	err = ReaderDb.Get(&stats.BlocksWithBlobsLast1h, `
		SELECT COUNT(*)
		FROM slots
		WHERE status != 0 AND blob_count > 0 AND slot >= $1
	`, slot1h)
	if err != nil {
		return nil, fmt.Errorf("failed to get blocks with blobs last 1h: %w", err)
	}

	err = ReaderDb.Get(&stats.BlocksWithBlobsLast24h, `
		SELECT COUNT(*)
		FROM slots
		WHERE status != 0 AND blob_count > 0 AND slot >= $1
	`, slot24h)
	if err != nil {
		return nil, fmt.Errorf("failed to get blocks with blobs last 24h: %w", err)
	}

	err = ReaderDb.Get(&stats.BlocksWithBlobsLast7d, `
		SELECT COUNT(*)
		FROM slots
		WHERE status != 0 AND blob_count > 0 AND slot >= $1
	`, slot7d)
	if err != nil {
		return nil, fmt.Errorf("failed to get blocks with blobs last 7d: %w", err)
	}

	err = ReaderDb.Get(&stats.BlocksWithBlobsLast18d, `
		SELECT COUNT(*)
		FROM slots
		WHERE status != 0 AND blob_count > 0 AND slot >= $1
	`, slot18d)
	if err != nil {
		return nil, fmt.Errorf("failed to get blocks with blobs last 18d: %w", err)
	}

	err = ReaderDb.Get(&stats.BlobGasLast1h, `
		SELECT COALESCE(SUM(eth_gas_used), 0)
		FROM slots
		WHERE status != 0 AND blob_count > 0 AND slot >= $1
	`, slot1h)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob gas last 1h: %w", err)
	}

	err = ReaderDb.Get(&stats.BlobGasLast24h, `
		SELECT COALESCE(SUM(eth_gas_used), 0)
		FROM slots
		WHERE status != 0 AND blob_count > 0 AND slot >= $1
	`, slot24h)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob gas last 24h: %w", err)
	}

	err = ReaderDb.Get(&stats.BlobGasLast7d, `
		SELECT COALESCE(SUM(eth_gas_used), 0)
		FROM slots
		WHERE status != 0 AND blob_count > 0 AND slot >= $1
	`, slot7d)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob gas last 7d: %w", err)
	}

	err = ReaderDb.Get(&stats.BlobGasLast18d, `
		SELECT COALESCE(SUM(eth_gas_used), 0)
		FROM slots
		WHERE status != 0 AND blob_count > 0 AND slot >= $1
	`, slot18d)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob gas last 18d: %w", err)
	}

	return stats, nil
}
