package db

// IMPORTANT — $N placeholder ordering (SQLite vs PostgreSQL):
//
// PostgreSQL treats $N as a positional parameter: $1 is always the 1st arg,
// $4 the 4th, regardless of where it appears in the query text.
//
// SQLite (mattn/go-sqlite3) treats $N as a *named* parameter. It assigns the
// binding index by the order each distinct $N first appears in the query text,
// NOT by the digit N. So a query that references e.g. $1, $4, $2, $3, $5 binds
// the positional args in that appearance order, scrambling the values.
//
// Consequence: in queries that must run on both engines, placeholders MUST
// appear in strict ascending order and a $N must not be reused out of position.
// Give each value its own in-order placeholder (duplicating the arg if needed)
// rather than reusing a lower number after a higher one.

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

func appendDollarPlaceholders(sql *strings.Builder, start, count int, separator string) {
	for i := 0; i < count; i++ {
		if i > 0 {
			sql.WriteString(separator)
		}
		sql.WriteByte('$')
		sql.WriteString(strconv.Itoa(start + i))
	}
}

func byteSliceMapKey(value []byte) string {
	return hex.EncodeToString(value)
}

func appendUint64ListPlaceholders(sql *strings.Builder, args *[]any, values []uint64, separator string) {
	start := len(*args) + 1
	if len(values) == 0 {
		*args = append(*args, uint64(0))
		appendDollarPlaceholders(sql, start, 1, separator)
		return
	}
	for _, value := range values {
		*args = append(*args, value)
	}
	appendDollarPlaceholders(sql, start, len(values), separator)
}

func appendWithOrphanedFilter(sql *strings.Builder, args *[]any, filterOp *string, withOrphaned uint8, canonicalForkIds []uint64, column string) {
	if withOrphaned == 1 {
		return
	}

	switch withOrphaned {
	case 0:
		fmt.Fprintf(sql, " %v %s IN (", *filterOp, column)
		appendUint64ListPlaceholders(sql, args, canonicalForkIds, ",")
		fmt.Fprint(sql, ")")
		*filterOp = "AND"
	case 2:
		fmt.Fprintf(sql, " %v %s NOT IN (", *filterOp, column)
		appendUint64ListPlaceholders(sql, args, canonicalForkIds, ",")
		fmt.Fprint(sql, ")")
		*filterOp = "AND"
	}
}

// maxBindParams bounds the placeholder count of a single INSERT statement. PostgreSQL's
// extended protocol allows 65535 bind parameters per statement and SQLite's default
// SQLITE_MAX_VARIABLE_NUMBER is 32766, so the lower of the two is applied to both engines.
const maxBindParams = 32766

// insertChunks splits items into slices small enough that fieldCount placeholders per item
// stay below maxBindParams and runs insert for each of them. Bulk inserts whose size is
// driven by chain data rather than by a per-block limit (e.g. the one-time Gloas builder
// onboarding, which produces one row per queued builder deposit) exceed the limit in a
// single statement and would fail the whole transaction.
func insertChunks[T any](items []T, fieldCount int, insert func(chunk []T) error) error {
	if len(items) == 0 {
		return nil
	}

	chunkSize := maxBindParams / fieldCount
	if chunkSize < 1 {
		chunkSize = 1
	}

	for start := 0; start < len(items); start += chunkSize {
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}

		if err := insert(items[start:end]); err != nil {
			return err
		}
	}

	return nil
}
