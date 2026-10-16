package db

import (
	"context"

	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/dbtypes"
)

// UpsertUnfinalizedMetadata stores or replaces one slot metadata item.
func UpsertUnfinalizedMetadata(ctx context.Context, tx *sqlx.Tx, entry *dbtypes.UnfinalizedMetadata) error {
	_, err := tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO unfinalized_metadata (
				slot, kind, key, data
			) VALUES ($1, $2, $3, $4)
			ON CONFLICT (slot, kind, key) DO UPDATE SET data = excluded.data`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO unfinalized_metadata (
				slot, kind, key, data
			) VALUES ($1, $2, $3, $4)`,
	}), entry.Slot, entry.Kind, entry.Key, entry.Data)
	if err != nil {
		return err
	}
	return nil
}

// StreamUnfinalizedMetadata passes every item from the given slot on to cb, in
// slot and kind order, so the lists of a slot precede their evaluations.
func StreamUnfinalizedMetadata(ctx context.Context, slot uint64, cb func(entry *dbtypes.UnfinalizedMetadata)) error {
	return streamUnfinalizedMetadata(ctx, `SELECT slot, kind, key, data FROM unfinalized_metadata WHERE slot >= $1 ORDER BY slot ASC, kind ASC`, cb, slot)
}

// StreamUnfinalizedSlotMetadata passes the items of one slot and kind on to cb.
func StreamUnfinalizedSlotMetadata(ctx context.Context, slot uint64, kind dbtypes.UnfinalizedMetadataKind, cb func(entry *dbtypes.UnfinalizedMetadata)) error {
	return streamUnfinalizedMetadata(ctx, `SELECT slot, kind, key, data FROM unfinalized_metadata WHERE slot = $1 AND kind = $2`, cb, slot, kind)
}

func streamUnfinalizedMetadata(ctx context.Context, query string, cb func(entry *dbtypes.UnfinalizedMetadata), args ...any) error {
	rows, err := ReaderDb.QueryContext(ctx, query, args...)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized metadata: %v", err)
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		entry := dbtypes.UnfinalizedMetadata{}
		err := rows.Scan(&entry.Slot, &entry.Kind, &entry.Key, &entry.Data)
		if err != nil {
			logger.Errorf("Error while scanning unfinalized metadata: %v", err)
			return err
		}
		cb(&entry)
	}

	return nil
}

// DeleteUnfinalizedMetadata removes the items of the given slots and kinds.
func DeleteUnfinalizedMetadata(ctx context.Context, tx *sqlx.Tx, slots []uint64, kinds []dbtypes.UnfinalizedMetadataKind) error {
	for _, slot := range slots {
		for _, kind := range kinds {
			_, err := tx.ExecContext(ctx, `DELETE FROM unfinalized_metadata WHERE slot = $1 AND kind = $2`, slot, kind)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteUnfinalizedMetadataBefore removes all items of slots before the given slot.
func DeleteUnfinalizedMetadataBefore(ctx context.Context, tx *sqlx.Tx, slot uint64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM unfinalized_metadata WHERE slot < $1`, slot)
	if err != nil {
		return err
	}
	return nil
}
