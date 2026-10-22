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
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v4"

	"github.com/ethpandaops/dora/dbtypes"
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

// PostgreSQL row counts are taken from the planner estimate instead of an exact count(*) when
// counting would be expensive: an exact count has to scan the whole filtered set, which takes
// tens of seconds on multi-million-row tables and is repeated for every page view. The planner
// estimate is only trusted for large sets, as small estimates are regularly off by orders of
// magnitude (multi-column joins in particular), so a small estimated set is still counted
// exactly unless even that scan is too expensive.
const (
	// countExactCostLimit is the planner cost up to which a count is always exact.
	countExactCostLimit = 10000
	// countEstimateMinRows is the planner row estimate below which the estimate is not trusted.
	countEstimateMinRows = 10000
	// countSmallSetCostLimit is the planner cost up to which a small estimated set is counted
	// exactly instead of showing the untrusted estimate.
	countSmallSetCostLimit = 100000
)

// countFilteredRows returns the total number of rows matched by fromClause (the FROM/JOIN/WHERE
// part of a query) for a page query that returned fetched rows at offset with the given limit.
// A page that is not full ends the result set, so the total is known without a count query.
// On PostgreSQL the planner is asked first and its row estimate is used when the exact count
// would be expensive; the exact count is used otherwise and always on SQLite.
func countFilteredRows(ctx context.Context, fromClause string, args []any, offset uint64, fetched int, limit uint32) (uint64, error) {
	fetchedEnd := offset + uint64(fetched)
	if uint32(fetched) < limit {
		return fetchedEnd, nil
	}

	if DbEngine == dbtypes.DBEnginePgsql {
		// The simple protocol inlines the arguments, so the planner estimates with the actual
		// values instead of a generic plan for the prepared statement.
		explainArgs := append([]any{pgx.QuerySimpleProtocol(true)}, args...)

		var planJson string
		err := ReaderDb.GetContext(ctx, &planJson, "EXPLAIN (FORMAT JSON) SELECT 1 "+fromClause, explainArgs...)
		if err != nil {
			return 0, err
		}

		var plans []struct {
			Plan struct {
				TotalCost float64 `json:"Total Cost"`
				PlanRows  float64 `json:"Plan Rows"`
			} `json:"Plan"`
		}
		if err := json.Unmarshal([]byte(planJson), &plans); err != nil {
			return 0, fmt.Errorf("failed to parse query plan: %w", err)
		}

		if len(plans) > 0 && plans[0].Plan.TotalCost > countExactCostLimit &&
			(plans[0].Plan.PlanRows >= countEstimateMinRows || plans[0].Plan.TotalCost > countSmallSetCostLimit) {
			count := uint64(plans[0].Plan.PlanRows)
			if count < fetchedEnd {
				count = fetchedEnd
			}
			return count, nil
		}
	}

	var count uint64
	err := ReaderDb.GetContext(ctx, &count, "SELECT count(*) "+fromClause, args...)
	if err != nil {
		return 0, err
	}
	if count < fetchedEnd {
		count = fetchedEnd
	}
	return count, nil
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
