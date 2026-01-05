package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertElTxEvents(events []*dbtypes.ElTxEvent, dbTx *sqlx.Tx) error {
	if len(events) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_tx_events ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_tx_events ",
		}),
		"(block_uid, tx_hash, event_index, source, topic1, topic2, topic3, topic4, topic5, data)",
		" VALUES ",
	)
	argIdx := 0
	fieldCount := 10

	args := make([]any, len(events)*fieldCount)
	for i, event := range events {
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

		args[argIdx+0] = event.BlockUid
		args[argIdx+1] = event.TxHash
		args[argIdx+2] = event.EventIndex
		args[argIdx+3] = event.Source
		args[argIdx+4] = event.Topic1
		args[argIdx+5] = event.Topic2
		args[argIdx+6] = event.Topic3
		args[argIdx+7] = event.Topic4
		args[argIdx+8] = event.Topic5
		args[argIdx+9] = event.Data
		argIdx += fieldCount
	}
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (block_uid, tx_hash, event_index) DO UPDATE SET source = excluded.source, topic1 = excluded.topic1, topic2 = excluded.topic2, topic3 = excluded.topic3, topic4 = excluded.topic4, topic5 = excluded.topic5, data = excluded.data",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func GetElTxEvent(blockUid uint64, txHash []byte, eventIndex uint32) (*dbtypes.ElTxEvent, error) {
	event := &dbtypes.ElTxEvent{}
	err := ReaderDb.Get(event, "SELECT block_uid, tx_hash, event_index, source, topic1, topic2, topic3, topic4, topic5, data FROM el_tx_events WHERE block_uid = $1 AND tx_hash = $2 AND event_index = $3", blockUid, txHash, eventIndex)
	if err != nil {
		return nil, err
	}
	return event, nil
}

func GetElTxEventsByTxHash(txHash []byte) ([]*dbtypes.ElTxEvent, error) {
	events := []*dbtypes.ElTxEvent{}
	err := ReaderDb.Select(&events, "SELECT block_uid, tx_hash, event_index, source, topic1, topic2, topic3, topic4, topic5, data FROM el_tx_events WHERE tx_hash = $1 ORDER BY block_uid DESC, event_index ASC", txHash)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func GetElTxEventsByBlockUid(blockUid uint64) ([]*dbtypes.ElTxEvent, error) {
	events := []*dbtypes.ElTxEvent{}
	err := ReaderDb.Select(&events, "SELECT block_uid, tx_hash, event_index, source, topic1, topic2, topic3, topic4, topic5, data FROM el_tx_events WHERE block_uid = $1 ORDER BY tx_hash ASC, event_index ASC", blockUid)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func GetElTxEventsByBlockUidAndTxHash(blockUid uint64, txHash []byte) ([]*dbtypes.ElTxEvent, error) {
	events := []*dbtypes.ElTxEvent{}
	err := ReaderDb.Select(&events, "SELECT block_uid, tx_hash, event_index, source, topic1, topic2, topic3, topic4, topic5, data FROM el_tx_events WHERE block_uid = $1 AND tx_hash = $2 ORDER BY event_index ASC", blockUid, txHash)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func GetElTxEventsByTopic1(topic1 []byte, offset uint64, limit uint32) ([]*dbtypes.ElTxEvent, uint64, error) {
	var sql strings.Builder
	args := []any{topic1}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, event_index, source, topic1, topic2, topic3, topic4, topic5, data
		FROM el_tx_events
		WHERE topic1 = $1
	)`)

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		null AS tx_hash,
		0 AS event_index,
		null AS source,
		null AS topic1,
		null AS topic2,
		null AS topic3,
		null AS topic4,
		null AS topic5,
		null AS data
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, tx_hash DESC, event_index ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	events := []*dbtypes.ElTxEvent{}
	err := ReaderDb.Select(&events, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching el tx events by topic1: %v", err)
		return nil, 0, err
	}

	if len(events) == 0 {
		return []*dbtypes.ElTxEvent{}, 0, nil
	}

	count := events[0].BlockUid
	return events[1:], count, nil
}

func GetElTxEventsFiltered(offset uint64, limit uint32, filter *dbtypes.ElTxEventFilter) ([]*dbtypes.ElTxEvent, uint64, error) {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
	WITH cte AS (
		SELECT block_uid, tx_hash, event_index, source, topic1, topic2, topic3, topic4, topic5, data
		FROM el_tx_events
	`)

	filterOp := "WHERE"
	if len(filter.TxHash) > 0 {
		args = append(args, filter.TxHash)
		fmt.Fprintf(&sql, " %v tx_hash = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.Source) > 0 {
		args = append(args, filter.Source)
		fmt.Fprintf(&sql, " %v source = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.Topic1) > 0 {
		args = append(args, filter.Topic1)
		fmt.Fprintf(&sql, " %v topic1 = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.Topic2) > 0 {
		args = append(args, filter.Topic2)
		fmt.Fprintf(&sql, " %v topic2 = $%v", filterOp, len(args))
		filterOp = "AND"
	}
	if len(filter.Topic3) > 0 {
		args = append(args, filter.Topic3)
		fmt.Fprintf(&sql, " %v topic3 = $%v", filterOp, len(args))
		filterOp = "AND"
	}

	fmt.Fprint(&sql, ")")

	args = append(args, limit)
	fmt.Fprintf(&sql, `
	SELECT
		count(*) AS block_uid,
		null AS tx_hash,
		0 AS event_index,
		null AS source,
		null AS topic1,
		null AS topic2,
		null AS topic3,
		null AS topic4,
		null AS topic5,
		null AS data
	FROM cte
	UNION ALL SELECT * FROM (
	SELECT * FROM cte
	ORDER BY block_uid DESC, tx_hash DESC, event_index ASC
	LIMIT $%v`, len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}
	fmt.Fprint(&sql, ") AS t1")

	events := []*dbtypes.ElTxEvent{}
	err := ReaderDb.Select(&events, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching filtered el tx events: %v", err)
		return nil, 0, err
	}

	if len(events) == 0 {
		return []*dbtypes.ElTxEvent{}, 0, nil
	}

	count := events[0].BlockUid
	return events[1:], count, nil
}

func DeleteElTxEvent(blockUid uint64, txHash []byte, eventIndex uint32, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_tx_events WHERE block_uid = $1 AND tx_hash = $2 AND event_index = $3", blockUid, txHash, eventIndex)
	return err
}

func DeleteElTxEventsByBlockUid(blockUid uint64, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_tx_events WHERE block_uid = $1", blockUid)
	return err
}

func DeleteElTxEventsByTxHash(txHash []byte, dbTx *sqlx.Tx) error {
	_, err := dbTx.Exec("DELETE FROM el_tx_events WHERE tx_hash = $1", txHash)
	return err
}
