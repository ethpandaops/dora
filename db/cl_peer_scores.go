package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/dbtypes"
)

// InsertPeerScoreSamples bulk-inserts peer score samples. The
// (observed_at, reporter_peer_id, target_peer_id) primary key dedupes
// rows on conflict so a brief overlap on poll cycles never errors.
func InsertPeerScoreSamples(ctx context.Context, tx *sqlx.Tx, samples []*dbtypes.ClPeerScoreSample) error {
	if len(samples) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO cl_peer_score_samples ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO cl_peer_score_samples ",
		}),
		"(observed_at, reporter_peer_id, reporter_client_type, target_peer_id, target_client_type, score, score_normalized, score_state, components_json, last_event_json)",
		" VALUES ",
	)

	const fieldCount = 10
	args := make([]any, 0, len(samples)*fieldCount)
	argIdx := 0

	for i, s := range samples {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprint(&sql, "(")
		for f := 0; f < fieldCount; f++ {
			if f > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%d", argIdx+f+1)
		}
		fmt.Fprint(&sql, ")")

		args = append(args,
			s.ObservedAt,
			s.ReporterPeerID,
			s.ReporterClientType,
			s.TargetPeerID,
			s.TargetClientType,
			s.Score,
			s.ScoreNormalized,
			s.ScoreState,
			s.ComponentsJSON,
			s.LastEventJSON,
		)
		argIdx += fieldCount
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (observed_at, reporter_peer_id, target_peer_id) DO NOTHING",
		dbtypes.DBEngineSqlite: "",
	}))

	if _, err := tx.ExecContext(ctx, sql.String(), args...); err != nil {
		return fmt.Errorf("insert peer score samples: %w", err)
	}
	return nil
}

// InsertPeerScoreEvents bulk-inserts peer score events. The composite
// primary key dedupes overlapping observations.
func InsertPeerScoreEvents(ctx context.Context, tx *sqlx.Tx, events []*dbtypes.ClPeerScoreEvent) error {
	if len(events) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO cl_peer_score_events ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO cl_peer_score_events ",
		}),
		"(observed_at, reporter_peer_id, target_peer_id, reason_code, native_reason, category, delta, topic, direction)",
		" VALUES ",
	)

	const fieldCount = 9
	args := make([]any, 0, len(events)*fieldCount)
	argIdx := 0

	for i, e := range events {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprint(&sql, "(")
		for f := 0; f < fieldCount; f++ {
			if f > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%d", argIdx+f+1)
		}
		fmt.Fprint(&sql, ")")

		args = append(args,
			e.ObservedAt,
			e.ReporterPeerID,
			e.TargetPeerID,
			e.ReasonCode,
			e.NativeReason,
			e.Category,
			e.Delta,
			e.Topic,
			e.Direction,
		)
		argIdx += fieldCount
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (observed_at, reporter_peer_id, target_peer_id, reason_code) DO NOTHING",
		dbtypes.DBEngineSqlite: "",
	}))

	if _, err := tx.ExecContext(ctx, sql.String(), args...); err != nil {
		return fmt.Errorf("insert peer score events: %w", err)
	}
	return nil
}

// GetRecentPeerScoreSamples returns all peer score samples with
// observed_at >= sinceMs. Used by the API to power the live matrix on
// page reloads when the cached in-memory snapshot isn't enough.
func GetRecentPeerScoreSamples(ctx context.Context, sinceMs int64) ([]*dbtypes.ClPeerScoreSample, error) {
	samples := []*dbtypes.ClPeerScoreSample{}
	err := ReaderDb.SelectContext(ctx, &samples, `
		SELECT
			observed_at, reporter_peer_id, reporter_client_type, target_peer_id, target_client_type,
			score, score_normalized, score_state, components_json, last_event_json
		FROM cl_peer_score_samples
		WHERE observed_at >= $1
		ORDER BY observed_at DESC
	`, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("get recent peer score samples: %w", err)
	}
	return samples, nil
}

// GetEventsForPair returns the most recent events between a specific
// reporter and target, newest first. Used by the detail modal.
func GetEventsForPair(ctx context.Context, reporter, target string, limit int) ([]*dbtypes.ClPeerScoreEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	events := []*dbtypes.ClPeerScoreEvent{}
	err := ReaderDb.SelectContext(ctx, &events, `
		SELECT
			observed_at, reporter_peer_id, target_peer_id, reason_code, native_reason,
			category, delta, topic, direction
		FROM cl_peer_score_events
		WHERE reporter_peer_id = $1 AND target_peer_id = $2
		ORDER BY observed_at DESC
		LIMIT $3
	`, reporter, target, limit)
	if err != nil {
		return nil, fmt.Errorf("get events for pair: %w", err)
	}
	return events, nil
}

// GetPeerScoreEvents returns recent peer-score events filtered by any
// combination of reporter, target and a sinceMs floor. Newest-first.
// Empty reporter/target means "any". Drives the API endpoint that
// powers both the detail modal and the global event log.
func GetPeerScoreEvents(ctx context.Context, reporter, target string, sinceMs int64, limit int) ([]*dbtypes.ClPeerScoreEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT
			observed_at, reporter_peer_id, target_peer_id, reason_code, native_reason,
			category, delta, topic, direction
		FROM cl_peer_score_events
		WHERE observed_at >= $1
	`
	args := []any{sinceMs}
	argIdx := 1
	if reporter != "" {
		argIdx++
		query += fmt.Sprintf(" AND reporter_peer_id = $%d", argIdx)
		args = append(args, reporter)
	}
	if target != "" {
		argIdx++
		query += fmt.Sprintf(" AND target_peer_id = $%d", argIdx)
		args = append(args, target)
	}
	argIdx++
	query += fmt.Sprintf(" ORDER BY observed_at DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	events := []*dbtypes.ClPeerScoreEvent{}
	if err := ReaderDb.SelectContext(ctx, &events, query, args...); err != nil {
		return nil, fmt.Errorf("get peer-score events: %w", err)
	}
	return events, nil
}

// GetPeerScoreReasonCounts returns a histogram of reason_code -> count
// over the window [sinceMs, now]. When reporter is non-empty the
// histogram is scoped to that reporter; otherwise it covers all
// reporters. Drives the reason histogram on the peer-scores tab.
func GetPeerScoreReasonCounts(ctx context.Context, reporter string, sinceMs int64) (map[string]int, error) {
	query := `
		SELECT reason_code, COUNT(*) AS c
		FROM cl_peer_score_events
		WHERE observed_at >= $1
	`
	args := []any{sinceMs}
	if reporter != "" {
		query += " AND reporter_peer_id = $2"
		args = append(args, reporter)
	}
	query += " GROUP BY reason_code"

	rows, err := ReaderDb.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get peer-score reason counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int, 16)
	for rows.Next() {
		var code string
		var c int
		if err := rows.Scan(&code, &c); err != nil {
			return nil, fmt.Errorf("scan reason count: %w", err)
		}
		counts[code] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reason counts: %w", err)
	}
	return counts, nil
}

// GetReasonCountsForPair returns a histogram of reason_code -> count
// for a specific (reporter, target) pair since sinceMs. Drives the
// reason histogram on the peer-scores tab.
func GetReasonCountsForPair(ctx context.Context, reporter, target string, sinceMs int64) (map[string]int, error) {
	rows, err := ReaderDb.QueryContext(ctx, `
		SELECT reason_code, COUNT(*) AS c
		FROM cl_peer_score_events
		WHERE reporter_peer_id = $1 AND target_peer_id = $2 AND observed_at >= $3
		GROUP BY reason_code
	`, reporter, target, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("get reason counts for pair: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int, 16)
	for rows.Next() {
		var code string
		var c int
		if err := rows.Scan(&code, &c); err != nil {
			return nil, fmt.Errorf("scan reason count: %w", err)
		}
		counts[code] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reason counts: %w", err)
	}
	return counts, nil
}
