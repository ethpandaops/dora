package db

import (
	"context"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func GetValidatorNameDictEntries(ctx context.Context) ([]*dbtypes.ValidatorNameDictEntry, error) {
	entries := []*dbtypes.ValidatorNameDictEntry{}
	err := ReaderDb.SelectContext(ctx, &entries, `SELECT "id", "name" FROM validator_names_dict ORDER BY "id" ASC`)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// InsertValidatorNameDictEntry inserts a distinct name and returns its stable id.
// Idempotent: an existing name returns its previously assigned id.
func InsertValidatorNameDictEntry(ctx context.Context, tx *sqlx.Tx, name string) (uint64, error) {
	_, err := tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  `INSERT INTO validator_names_dict ("name") VALUES ($1) ON CONFLICT ("name") DO NOTHING`,
		dbtypes.DBEngineSqlite: `INSERT OR IGNORE INTO validator_names_dict ("name") VALUES ($1)`,
	}), name)
	if err != nil {
		return 0, err
	}

	var id uint64
	err = tx.GetContext(ctx, &id, `SELECT "id" FROM validator_names_dict WHERE "name" = $1`, name)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetSlotsWithoutNameId returns slot rows that have not been stamped with a
// proposer name id yet, newest first so recent pages heal before deep history.
func GetSlotsWithoutNameId(ctx context.Context, limit uint32) []*dbtypes.SlotNameStamp {
	stamps := []*dbtypes.SlotNameStamp{}
	err := ReaderDb.SelectContext(ctx, &stamps, `
		SELECT "slot", "root", "proposer" FROM slots
		WHERE "proposer_name_id" IS NULL
		ORDER BY "slot" DESC
		LIMIT $1`, limit)
	if err != nil {
		logger.Errorf("Error while fetching slots without name id: %v", err)
		return nil
	}
	return stamps
}

func UpdateSlotNameIds(ctx context.Context, tx *sqlx.Tx, stamps []*dbtypes.SlotNameStamp) error {
	stmt, err := tx.PreparexContext(ctx, `UPDATE slots SET proposer_name_id = $1 WHERE slot = $2 AND root = $3`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, stamp := range stamps {
		if _, err := stmt.ExecContext(ctx, stamp.ProposerNameId, stamp.Slot, stamp.Root); err != nil {
			return err
		}
	}
	return nil
}
