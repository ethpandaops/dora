package dbtypes

// ClPeerScoreSample is one row of cl_peer_score_samples. It captures
// what reporter saw for a single target at a single observed_at tick.
type ClPeerScoreSample struct {
	ObservedAt         int64   `db:"observed_at" json:"observed_at"`
	ReporterPeerID     string  `db:"reporter_peer_id" json:"reporter_peer_id"`
	ReporterClientType int16   `db:"reporter_client_type" json:"reporter_client_type"`
	TargetPeerID       string  `db:"target_peer_id" json:"target_peer_id"`
	TargetClientType   *int16  `db:"target_client_type" json:"target_client_type"`
	Score              float64 `db:"score" json:"score"`
	ScoreNormalized    float64 `db:"score_normalized" json:"score_normalized"`
	ScoreState         string  `db:"score_state" json:"score_state"`
	ComponentsJSON     *string `db:"components_json" json:"components_json,omitempty"`
	LastEventJSON      *string `db:"last_event_json" json:"last_event_json,omitempty"`
}

// ClPeerScoreEvent is one row of cl_peer_score_events. Events are
// inserted only when the reporter observes a fresh downscore (or
// upscore) reason for a target since the last poll tick.
type ClPeerScoreEvent struct {
	ObservedAt     int64    `db:"observed_at" json:"observed_at"`
	ReporterPeerID string   `db:"reporter_peer_id" json:"reporter_peer_id"`
	TargetPeerID   string   `db:"target_peer_id" json:"target_peer_id"`
	ReasonCode     string   `db:"reason_code" json:"reason_code"`
	NativeReason   string   `db:"native_reason" json:"native_reason"`
	Category       string   `db:"category" json:"category"`
	Delta          *float64 `db:"delta" json:"delta"`
	Topic          *string  `db:"topic" json:"topic"`
	Direction      *string  `db:"direction" json:"direction"`
}
