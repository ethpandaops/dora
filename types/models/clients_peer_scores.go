package models

// ClientsPeerScoresPageData holds the data rendered on the
// /clients/peer_scores page.
//
// The page is currently a placeholder: data wiring (peer score
// collection from the connected CL clients, normalization and
// rendering) will be added in a follow-up change.
type ClientsPeerScoresPageData struct {
	PollIntervalSeconds int
}
