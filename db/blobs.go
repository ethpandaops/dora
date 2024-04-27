package db

import (
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
