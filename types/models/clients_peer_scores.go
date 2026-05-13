package models

// ClientsPeerScoresPageData holds the data rendered on the
// /clients/peer_scores page.
type ClientsPeerScoresPageData struct {
	PollIntervalSeconds int                                   `json:"poll_interval_seconds"`
	Reporters           []*PeerScoresReporter                 `json:"reporters"`
	Targets             []*PeerScoresTarget                   `json:"targets"`
	Matrix              map[string]map[string]*PeerScoresCell `json:"matrix"`
	ReasonHistogram     []*PeerScoresReasonCount              `json:"reason_histogram"`
	ScrapedAtMs         int64                                 `json:"scraped_at_ms"`
}

// PeerScoresReporter describes one of dora's locally connected
// consensus clients - the side of the matrix that polls peer scores.
type PeerScoresReporter struct {
	PeerID     string `json:"peer_id"`
	Name       string `json:"name"`
	ClientType int8   `json:"client_type"`
	ClientName string `json:"client_name"`
	Supported  bool   `json:"supported"`
	FetchedAt  int64  `json:"fetched_at"`
}

// PeerScoresTarget describes one peer observed by at least one
// reporter. KnownClientType > 0 means we recognise the peer as one of
// our own reporters (cross-reference matched).
type PeerScoresTarget struct {
	PeerID          string `json:"peer_id"`
	PeerIDShort     string `json:"peer_id_short"`
	AgentVersion    string `json:"agent_version"`
	KnownClientType int8   `json:"known_client_type"`
	KnownAs         string `json:"known_as"`
}

// PeerScoresCell is a single (reporter, target) entry in the score
// matrix.
type PeerScoresCell struct {
	Score               float64            `json:"score"`
	ScoreNormalized     float64            `json:"score_normalized"`
	ScoreState          string             `json:"score_state"`
	LastEventReason     string             `json:"last_event_reason,omitempty"`
	LastEventNative     string             `json:"last_event_native,omitempty"`
	LastEventSecondsAgo uint64             `json:"last_event_seconds_ago,omitempty"`
	Components          map[string]float64 `json:"components,omitempty"`
}

// PeerScoresReasonCount is one bar of the reason histogram.
type PeerScoresReasonCount struct {
	Reason   string `json:"reason"`
	Category string `json:"category"`
	Count    int    `json:"count"`
}
