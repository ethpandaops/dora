package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// insertValidatorNameRangesBatchSize limits the parameters per insert statement
// (4 args per row) to stay well below the engines' bind variable limits.
const insertValidatorNameRangesBatchSize = 5000

func GetValidatorNameSnapshots(ctx context.Context) ([]*dbtypes.ValidatorNameSnapshot, error) {
	snapshots := []*dbtypes.ValidatorNameSnapshot{}
	err := ReaderDb.SelectContext(ctx, &snapshots, `
		SELECT "start_slot", "end_slot", "start_time", "end_time", "ranges_hash"
		FROM validator_name_snapshots
		ORDER BY "start_slot" ASC`)
	if err != nil {
		return nil, fmt.Errorf("error fetching validator name snapshots: %w", err)
	}
	return snapshots, nil
}

// UpsertValidatorNameSnapshot replaces one snapshot period and all its name ranges.
// Ranges are fully rewritten so a retroactively amended snapshot never leaves stale rows.
func UpsertValidatorNameSnapshot(ctx context.Context, tx *sqlx.Tx, snapshot *dbtypes.ValidatorNameSnapshot, ranges []*dbtypes.ValidatorNameRange) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM validator_name_ranges WHERE "snapshot_slot" = $1`, snapshot.StartSlot)
	if err != nil {
		return fmt.Errorf("error clearing validator name ranges for snapshot %v: %w", snapshot.StartSlot, err)
	}

	for batchStart := 0; batchStart < len(ranges); batchStart += insertValidatorNameRangesBatchSize {
		batchEnd := batchStart + insertValidatorNameRangesBatchSize
		if batchEnd > len(ranges) {
			batchEnd = len(ranges)
		}
		batch := ranges[batchStart:batchEnd]

		var sql strings.Builder
		fmt.Fprint(&sql, `INSERT INTO validator_name_ranges ("snapshot_slot", "start_index", "end_index", "name") VALUES `)
		args := make([]any, 0, len(batch)*4)
		for i, nameRange := range batch {
			if i > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "($%v, $%v, $%v, $%v)", len(args)+1, len(args)+2, len(args)+3, len(args)+4)
			args = append(args, nameRange.SnapshotSlot, nameRange.StartIndex, nameRange.EndIndex, nameRange.Name)
		}
		if _, err := tx.ExecContext(ctx, sql.String(), args...); err != nil {
			return fmt.Errorf("error inserting validator name ranges for snapshot %v: %w", snapshot.StartSlot, err)
		}
	}

	_, err = tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO validator_name_snapshots ("start_slot", "end_slot", "start_time", "end_time", "ranges_hash")
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT ("start_slot") DO UPDATE SET
				end_slot = excluded.end_slot,
				start_time = excluded.start_time,
				end_time = excluded.end_time,
				ranges_hash = excluded.ranges_hash`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO validator_name_snapshots ("start_slot", "end_slot", "start_time", "end_time", "ranges_hash")
			VALUES ($1, $2, $3, $4, $5)`,
	}), snapshot.StartSlot, snapshot.EndSlot, snapshot.StartTime, snapshot.EndTime, snapshot.RangesHash)
	if err != nil {
		return fmt.Errorf("error upserting validator name snapshot %v: %w", snapshot.StartSlot, err)
	}
	return nil
}

func DeleteValidatorNameSnapshot(ctx context.Context, tx *sqlx.Tx, startSlot uint64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM validator_name_ranges WHERE "snapshot_slot" = $1`, startSlot)
	if err != nil {
		return fmt.Errorf("error deleting validator name ranges for snapshot %v: %w", startSlot, err)
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM validator_name_snapshots WHERE "start_slot" = $1`, startSlot)
	if err != nil {
		return fmt.Errorf("error deleting validator name snapshot %v: %w", startSlot, err)
	}
	return nil
}

// HasValidatorNameMatch reports whether any current or historic validator name matches
// the given pattern.
func HasValidatorNameMatch(ctx context.Context, pattern string) (bool, error) {
	likeOp := EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "ilike",
		dbtypes.DBEngineSqlite: "LIKE",
	})

	var name string
	err := ReaderDb.GetContext(ctx, &name, fmt.Sprintf(`
		SELECT "name" FROM validator_names WHERE "name" %[1]v $1
		UNION
		SELECT "name" FROM validator_name_ranges WHERE "name" %[1]v $2
		LIMIT 1`, likeOp), pattern, pattern)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("error searching validator names: %w", err)
	}
	return true, nil
}

// SearchValidatorNameCounts returns current and historic validator names matching the
// pattern, with the number of slots proposed under each name. Counts follow the same
// semantics as the name filters: slots covered by a history range count towards the
// name valid at the slot, uncovered slots count towards the proposer's current name.
func SearchValidatorNameCounts(ctx context.Context, pattern string, limit uint32) (dbtypes.SearchAheadValidatorNameResult, error) {
	likeOp := EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "ilike",
		dbtypes.DBEngineSqlite: "LIKE",
	})
	coverProbe := validatorNameHistoryProbeSql(`s."proposer"`,
		`vns."start_slot" <= s."slot" AND vns."end_slot" > s."slot"`)

	names := dbtypes.SearchAheadValidatorNameResult{}
	err := ReaderDb.SelectContext(ctx, &names, fmt.Sprintf(`
		SELECT "name", SUM(cnt) AS "count" FROM (
			SELECT vnr."name" AS "name", COUNT(s2."slot") AS cnt
			FROM validator_name_ranges vnr
			JOIN validator_name_snapshots vns2 ON vns2."start_slot" = vnr."snapshot_slot"
			LEFT JOIN slots s2 ON s2."proposer" >= vnr."start_index" AND s2."proposer" <= vnr."end_index"
				AND s2."slot" >= vns2."start_slot" AND s2."slot" < vns2."end_slot"
			WHERE vnr."name" %[1]v $1
			GROUP BY vnr."name"
			UNION ALL
			SELECT vn."name" AS "name", COUNT(s."slot") AS cnt
			FROM validator_names vn
			LEFT JOIN slots s ON s."proposer" = vn."index"
				AND NOT EXISTS (SELECT 1 FROM (%[2]v) vnh WHERE vnh."end_index" >= s."proposer")
			WHERE vn."name" %[1]v $2
			GROUP BY vn."name"
		) combined
		GROUP BY "name"
		ORDER BY "count" DESC
		LIMIT $3`, likeOp, coverProbe), pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("error searching validator name counts: %w", err)
	}
	return names, nil
}

// Caps for the pre-resolved name match set. Above these the set is marked truncated and
// callers must not use it as a candidate restriction (the plain predicate still applies).
// The index cap also bounds the bind variables of the generated IN list.
const (
	maxNameMatchIndexes = 5000
	maxNameMatchRanges  = 1000
)

// ValidatorNameMatchRange is one name-history range matching a searched pattern,
// combined with the slot/time window of its snapshot.
type ValidatorNameMatchRange struct {
	StartIndex uint64 `db:"start_index"`
	EndIndex   uint64 `db:"end_index"`
	StartSlot  uint64 `db:"start_slot"`
	EndSlot    uint64 `db:"end_slot"`
	StartTime  uint64 `db:"start_time"`
	EndTime    uint64 `db:"end_time"`
}

// ValidatorNameMatches is the pre-resolved identity set of a validator-name pattern:
// validator indexes whose current name matches and history ranges whose recorded name
// matches. It is a superset of the rows the precise name predicate can accept, so it can
// be used as a redundant, index-friendly candidate restriction. Truncated marks a set
// that hit a resolver cap and therefore must not be used as a restriction.
type ValidatorNameMatches struct {
	Indexes   []uint64
	Ranges    []*ValidatorNameMatchRange
	Truncated bool
}

// GetValidatorNameMatches resolves the current-name indexes and history ranges matching
// the given pattern.
func GetValidatorNameMatches(ctx context.Context, pattern string) (*ValidatorNameMatches, error) {
	likeOp := EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "ilike",
		dbtypes.DBEngineSqlite: "LIKE",
	})

	matches := &ValidatorNameMatches{}

	err := ReaderDb.SelectContext(ctx, &matches.Indexes, fmt.Sprintf(
		`SELECT "index" FROM validator_names WHERE "name" %v $1 LIMIT %v`,
		likeOp, maxNameMatchIndexes+1), pattern)
	if err != nil {
		return nil, fmt.Errorf("error resolving validator name indexes: %w", err)
	}
	if len(matches.Indexes) > maxNameMatchIndexes {
		matches.Truncated = true
		return matches, nil
	}

	err = ReaderDb.SelectContext(ctx, &matches.Ranges, fmt.Sprintf(`
		SELECT vnr."start_index", vnr."end_index",
			vns."start_slot", vns."end_slot", vns."start_time", vns."end_time"
		FROM validator_name_ranges vnr
		JOIN validator_name_snapshots vns ON vns."start_slot" = vnr."snapshot_slot"
		WHERE vnr."name" %v $1 LIMIT %v`,
		likeOp, maxNameMatchRanges+1), pattern)
	if err != nil {
		return nil, fmt.Errorf("error resolving validator name ranges: %w", err)
	}
	if len(matches.Ranges) > maxNameMatchRanges {
		matches.Truncated = true
	}

	return matches, nil
}

// IsEmpty reports whether the pattern matched no current or historic name at all.
func (m *ValidatorNameMatches) IsEmpty() bool {
	return !m.Truncated && len(m.Indexes) == 0 && len(m.Ranges) == 0
}

// AppendValidatorNameCandidatePredicate appends the candidate restriction derived from a
// resolved (non-truncated, non-empty) match set and returns the predicate SQL plus the
// extended args. The predicate is a superset of the precise history-aware name predicate
// built by AppendValidatorNameHistoryFilter (non-inverted): rows matching via a history
// range fall in that range's index/window bounds, rows matching via the legacy current
// name fall in the index list. History windows are bounded by slotExpr or timeExpr
// (exactly one must be non-empty), matching the bound used by the precise predicate.
func AppendValidatorNameCandidatePredicate(args []any, matches *ValidatorNameMatches, indexExpr string, slotExpr string, timeExpr string) (string, []any) {
	conds := make([]string, 0, 1+len(matches.Ranges))

	if len(matches.Indexes) > 0 {
		var cond strings.Builder
		fmt.Fprintf(&cond, `%v IN (`, indexExpr)
		appendDollarPlaceholders(&cond, len(args)+1, len(matches.Indexes), ", ")
		fmt.Fprint(&cond, `)`)
		for _, index := range matches.Indexes {
			args = append(args, index)
		}
		conds = append(conds, cond.String())
	}

	for _, nameRange := range matches.Ranges {
		var windowExpr string
		var windowStart, windowEnd uint64
		if slotExpr != "" {
			windowExpr = slotExpr
			windowStart, windowEnd = nameRange.StartSlot, nameRange.EndSlot
		} else {
			windowExpr = timeExpr
			windowStart, windowEnd = nameRange.StartTime, nameRange.EndTime
		}
		conds = append(conds, fmt.Sprintf(
			`(%[1]v >= $%[2]v AND %[1]v <= $%[3]v AND %[4]v >= $%[5]v AND %[4]v < $%[6]v)`,
			indexExpr, len(args)+1, len(args)+2, windowExpr, len(args)+3, len(args)+4))
		args = append(args, nameRange.StartIndex, nameRange.EndIndex, windowStart, windowEnd)
	}

	return "(" + strings.Join(conds, " OR ") + ")", args
}

// validatorNameHistoryProbeSql builds the correlated top-1 probe subquery selecting the
// single history range that can cover the row's validator index within the snapshot
// active at the row's slot/time. The snapshot is resolved via a scalar subquery so the
// planner descends the (snapshot_slot, start_index) primary key with an equality prefix;
// because ranges within a snapshot are disjoint, ORDER BY start_index DESC LIMIT 1 is a
// single backward index descent per row regardless of the number of history ranges.
func validatorNameHistoryProbeSql(indexExpr string, boundCond string) string {
	return fmt.Sprintf(`SELECT vnr."end_index", vnr."name"
		FROM validator_name_ranges vnr
		WHERE vnr."snapshot_slot" = (
			SELECT vns."start_slot" FROM validator_name_snapshots vns
			WHERE %v
			ORDER BY vns."start_slot" DESC
			LIMIT 1
		)
		AND vnr."start_index" <= %v
		ORDER BY vnr."start_index" DESC
		LIMIT 1`, boundCond, indexExpr)
}

// AppendValidatorNameHistoryFilter appends a validator-name filter predicate to args and
// returns the predicate SQL (without leading WHERE/AND) plus the extended args.
//
// The name is resolved historically: rows covered by a name history range match against
// the name valid at the row's slot (slotExpr) or unix time (timeExpr, used by tables that
// only store an EL block time; exactly one of the two must be non-empty). Rows not
// covered by any history range fall back to the current name via legacyNameExpr, which
// must reference an already-joined validator_names column. On networks without name
// history both tables are empty and the predicate degrades to the legacy behavior.
//
// With invert set, the predicate matches rows whose name at the row's slot does NOT
// match the pattern, including unnamed rows.
func AppendValidatorNameHistoryFilter(args []any, indexExpr string, slotExpr string, timeExpr string, legacyNameExpr string, pattern string, invert bool) (string, []any) {
	likeOp := EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "ilike",
		dbtypes.DBEngineSqlite: "LIKE",
	})

	var boundCond string
	if slotExpr != "" {
		boundCond = fmt.Sprintf(`vns."start_slot" <= %[1]v AND vns."end_slot" > %[1]v`, slotExpr)
	} else {
		boundCond = fmt.Sprintf(`vns."start_time" <= %[1]v AND vns."end_time" > %[1]v`, timeExpr)
	}
	probe := validatorNameHistoryProbeSql(indexExpr, boundCond)

	// args appear in SQL in appearance order: history match first, legacy fallback second
	matchArg := len(args) + 1
	legacyArg := len(args) + 2
	args = append(args, pattern, pattern)

	matchSql := fmt.Sprintf(`EXISTS (SELECT 1 FROM (%v) vnh WHERE vnh."end_index" >= %v AND vnh."name" %v $%v)`,
		probe, indexExpr, likeOp, matchArg)
	coverageSql := fmt.Sprintf(`EXISTS (SELECT 1 FROM (%v) vnh WHERE vnh."end_index" >= %v)`,
		probe, indexExpr)

	var predicate string
	if invert {
		predicate = fmt.Sprintf(`(NOT %v AND (%v OR %[3]v IS NULL OR %[3]v = '' OR %[3]v NOT %v $%v))`,
			matchSql, coverageSql, legacyNameExpr, likeOp, legacyArg)
	} else {
		predicate = fmt.Sprintf(`(%v OR (NOT %v AND %v %v $%v))`,
			matchSql, coverageSql, legacyNameExpr, likeOp, legacyArg)
	}
	return predicate, args
}
