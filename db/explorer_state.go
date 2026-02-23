package db

import (
	"context"
	"encoding/json"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func GetExplorerState(ctx context.Context, key string, returnValue interface{}) (interface{}, error) {
	entry := dbtypes.ExplorerState{}
	err := ReaderDb.GetContext(ctx, &entry, `SELECT key, value FROM explorer_state WHERE key = $1`, key)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(entry.Value), returnValue)
	if err != nil {
		return nil, err
	}
	return returnValue, nil
}

func SetExplorerState(ctx context.Context, tx *sqlx.Tx, key string, value interface{}) error {
	valueMarshal, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO explorer_state (key, value)
			VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET
				value = excluded.value`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO explorer_state (key, value)
			VALUES ($1, $2)`,
	}), key, valueMarshal)
	if err != nil {
		return err
	}
	return nil
}
