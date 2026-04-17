package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// InsertElEventIndices inserts event index entries in batch.
func InsertElEventIndices(ctx context.Context, dbTx *sqlx.Tx, entries []*dbtypes.ElEventIndex) error {
	if len(entries) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_event_index ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_event_index ",
		}),
		"(tx_uid, event_index, source_id, topic1)",
		" VALUES ",
	)

	argIdx := 0
	fieldCount := 4
	args := make([]any, len(entries)*fieldCount)

	for i, entry := range entries {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprint(&sql, "(")
		for f := 0; f < fieldCount; f++ {
			if f > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", argIdx+f+1)
		}
		fmt.Fprint(&sql, ")")

		args[argIdx+0] = entry.TxUid
		args[argIdx+1] = entry.EventIndex
		args[argIdx+2] = entry.SourceID
		args[argIdx+3] = entry.Topic1
		argIdx += fieldCount
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (tx_uid, event_index) DO UPDATE SET" +
			" source_id = excluded.source_id," +
			" topic1 = excluded.topic1",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	return err
}

// GetElEventIndicesBySource returns event index entries for a given
// source contract, ordered by tx_uid DESC.
func GetElEventIndicesBySource(
	ctx context.Context,
	sourceID uint64,
	offset uint64,
	limit uint32,
) ([]*dbtypes.ElEventIndex, uint64, error) {
	var sql strings.Builder
	args := []any{sourceID}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT tx_uid, event_index, source_id, topic1
		FROM el_event_index
		WHERE source_id = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS tx_uid,
		0 AS event_index,
		0 AS source_id,
		null AS topic1
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY tx_uid DESC NULLS LAST, event_index ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	entries := []*dbtypes.ElEventIndex{}
	err := ReaderDb.SelectContext(ctx, &entries, sql.String(), args...)
	if err != nil {
		return nil, 0, err
	}

	if len(entries) == 0 {
		return []*dbtypes.ElEventIndex{}, 0, nil
	}

	count := entries[0].TxUid
	return entries[1:], count, nil
}

// GetElEventIndicesByTopic1 returns event index entries for a given
// event signature (topic1), ordered by tx_uid DESC.
func GetElEventIndicesByTopic1(
	ctx context.Context,
	topic1 []byte,
	offset uint64,
	limit uint32,
) ([]*dbtypes.ElEventIndex, uint64, error) {
	var sql strings.Builder
	args := []any{topic1}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT tx_uid, event_index, source_id, topic1
		FROM el_event_index
		WHERE topic1 = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS tx_uid,
		0 AS event_index,
		0 AS source_id,
		null AS topic1
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY tx_uid DESC NULLS LAST, event_index ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	entries := []*dbtypes.ElEventIndex{}
	err := ReaderDb.SelectContext(ctx, &entries, sql.String(), args...)
	if err != nil {
		return nil, 0, err
	}

	if len(entries) == 0 {
		return []*dbtypes.ElEventIndex{}, 0, nil
	}

	count := entries[0].TxUid
	return entries[1:], count, nil
}

// GetElEventIndexCountByTxUid returns the number of event index entries
// for a given transaction UID.
func GetElEventIndexCountByTxUid(ctx context.Context, txUid uint64) (uint64, error) {
	var count uint64
	err := ReaderDb.GetContext(ctx, &count,
		"SELECT COUNT(*) FROM el_event_index WHERE tx_uid = $1",
		txUid,
	)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GetElEventIndicesByTxUid returns all event index entries for a
// given transaction UID.
func GetElEventIndicesByTxUid(ctx context.Context, txUid uint64) ([]*dbtypes.ElEventIndex, error) {
	entries := []*dbtypes.ElEventIndex{}
	err := ReaderDb.SelectContext(ctx, &entries,
		"SELECT tx_uid, event_index, source_id, topic1"+
			" FROM el_event_index WHERE tx_uid = $1 ORDER BY event_index ASC",
		txUid,
	)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
