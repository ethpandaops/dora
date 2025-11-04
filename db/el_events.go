package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// GetElEventsByTransaction retrieves all events for a transaction hash
func GetElEventsByTransaction(txHash []byte) ([]*dbtypes.ElEvent, error) {
	events := []*dbtypes.ElEvent{}
	err := ReaderDb.Select(&events, `
		SELECT *
		FROM el_events
		WHERE transaction_hash = $1
		ORDER BY log_index ASC
	`, txHash)
	return events, err
}

// GetElEventsByAddress retrieves events emitted by an address
func GetElEventsByAddress(address []byte, offset uint, limit uint) ([]*dbtypes.ElEvent, uint64, error) {
	var sql strings.Builder
	args := []interface{}{address}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_events
			WHERE address = $1
	`)

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY block_timestamp DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	events := []*dbtypes.ElEvent{}
	err := ReaderDb.Select(&events, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return events, totalCount, nil
}

// GetElEventsByTopic0 retrieves events by topic0 (event signature)
func GetElEventsByTopic0(topic0 []byte, offset uint, limit uint) ([]*dbtypes.ElEvent, uint64, error) {
	var sql strings.Builder
	args := []interface{}{topic0}

	fmt.Fprint(&sql, `
		WITH cte AS (
			SELECT *
			FROM el_events
			WHERE topic0 = $1
	`)

	args = append(args, limit)
	fmt.Fprintf(&sql, " ORDER BY block_timestamp DESC LIMIT $%v", len(args))

	if offset > 0 {
		args = append(args, offset)
		fmt.Fprintf(&sql, " OFFSET $%v", len(args))
	}

	fmt.Fprint(&sql, `) SELECT COUNT(*) AS total_count FROM cte`)

	var totalCount uint64
	row := ReaderDb.QueryRow(sql.String(), args...)
	if err := row.Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	resultArgs := args
	if offset > 0 {
		resultArgs = args[:len(args)-1]
	}

	events := []*dbtypes.ElEvent{}
	err := ReaderDb.Select(&events, strings.Replace(sql.String(), `) SELECT COUNT(*) AS total_count FROM cte`, ``, 1), resultArgs...)
	if err != nil {
		return nil, 0, err
	}

	return events, totalCount, nil
}

// InsertElEvents inserts multiple events
func InsertElEvents(events []*dbtypes.ElEvent, tx *sqlx.Tx) error {
	if len(events) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_events ",
			dbtypes.DBEngineSqlite: "INSERT INTO el_events ",
		}),
		"(transaction_hash, block_number, block_timestamp, log_index, address, ",
		"topic0, topic1, topic2, topic3, data, event_name, decoded_data) ",
		"VALUES ",
	)

	argIdx := 0
	fieldCount := 12
	args := make([]interface{}, len(events)*fieldCount)

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

		args[argIdx+0] = event.TransactionHash
		args[argIdx+1] = event.BlockNumber
		args[argIdx+2] = event.BlockTimestamp
		args[argIdx+3] = event.LogIndex
		args[argIdx+4] = event.Address
		args[argIdx+5] = event.Topic0
		args[argIdx+6] = event.Topic1
		args[argIdx+7] = event.Topic2
		args[argIdx+8] = event.Topic3
		args[argIdx+9] = event.Data
		args[argIdx+10] = event.EventName
		args[argIdx+11] = event.DecodedData
		argIdx += fieldCount
	}

	_, err := tx.Exec(sql.String(), args...)
	return err
}

// DeleteElEventsOlderThan deletes events older than a given timestamp
func DeleteElEventsOlderThan(timestamp uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		DELETE FROM el_events
		WHERE block_timestamp < $1
	`, timestamp)
	return err
}

// GetElMethodSignature retrieves a method signature by its 4-byte ID
func GetElMethodSignature(signature []byte) (*dbtypes.ElMethodSignature, error) {
	methodSig := &dbtypes.ElMethodSignature{}
	err := ReaderDb.Get(methodSig, `
		SELECT *
		FROM el_method_signatures
		WHERE signature = $1
	`, signature)
	if err != nil {
		return nil, err
	}
	return methodSig, nil
}

// GetElEventSignature retrieves an event signature by its topic0 hash
func GetElEventSignature(signature []byte) (*dbtypes.ElEventSignature, error) {
	eventSig := &dbtypes.ElEventSignature{}
	err := ReaderDb.Get(eventSig, `
		SELECT *
		FROM el_event_signatures
		WHERE signature = $1
	`, signature)
	if err != nil {
		return nil, err
	}
	return eventSig, nil
}
