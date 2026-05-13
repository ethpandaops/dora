package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/handlers"
)

// APIClientsPeerScores returns the current snapshot of the peer-score
// matrix as JSON: reporters, targets, matrix and reason histogram. The
// payload mirrors what the /clients/peer_scores HTML page renders.
//
// @Summary Get current peer-score matrix snapshot
// @Description Returns the live in-memory snapshot of per-client peer
// @Description scores. Same data the /clients/peer_scores page renders.
// @Tags clients
// @Accept json
// @Produce json
// @Success 200 {object} models.ClientsPeerScoresPageData
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/clients/peer_scores [get]
// @ID getClientsPeerScores
func APIClientsPeerScores(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	pageData := handlers.PeerScoresPageData()
	if err := json.NewEncoder(w).Encode(pageData); err != nil {
		logrus.WithError(err).Error("failed to encode peer scores response")
		http.Error(w, `{"error": "failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

// APIPeerScoresEventsResponse wraps the list returned by the events
// endpoint.
type APIPeerScoresEventsResponse struct {
	Data []*dbtypes.ClPeerScoreEvent `json:"data"`
}

// APIClientsPeerScoresEvents returns recent peer-score events from the
// database, filtered by reporter / target / since.
//
// Query parameters:
//   - reporter (optional): reporter peer ID
//   - target   (optional): target peer ID
//   - since    (optional): unix milliseconds floor (default: now - 1h)
//   - limit    (optional): max rows (default: 50, max: 500)
//
// @Summary Get recent peer-score events
// @Tags clients
// @Produce json
// @Success 200 {object} APIPeerScoresEventsResponse
// @Router /v1/clients/peer_scores/events [get]
// @ID getClientsPeerScoresEvents
func APIClientsPeerScoresEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if db.ReaderDb == nil {
		writeJSON(w, &APIPeerScoresEventsResponse{Data: []*dbtypes.ClPeerScoreEvent{}})
		return
	}

	q := r.URL.Query()
	reporter := q.Get("reporter")
	target := q.Get("target")
	since := parseInt64(q.Get("since"), time.Now().Add(-time.Hour).UnixMilli())
	limit := parseInt(q.Get("limit"), 50)
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	events, err := db.GetPeerScoreEvents(r.Context(), reporter, target, since, limit)
	if err != nil {
		logrus.WithError(err).Error("failed to load peer-score events")
		http.Error(w, `{"error":"failed to load events"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, &APIPeerScoresEventsResponse{Data: events})
}

// APIPeerScoresReasonsResponse wraps the reason histogram payload.
type APIPeerScoresReasonsResponse struct {
	Data map[string]int `json:"data"`
}

// APIClientsPeerScoresReasons returns reason_code -> count over the
// given window. If reporter is supplied the histogram is reporter-scoped.
//
// @Summary Get peer-score reason histogram
// @Tags clients
// @Produce json
// @Success 200 {object} APIPeerScoresReasonsResponse
// @Router /v1/clients/peer_scores/reasons [get]
// @ID getClientsPeerScoresReasons
func APIClientsPeerScoresReasons(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if db.ReaderDb == nil {
		writeJSON(w, &APIPeerScoresReasonsResponse{Data: map[string]int{}})
		return
	}

	q := r.URL.Query()
	reporter := q.Get("reporter")
	since := parseInt64(q.Get("since"), time.Now().Add(-time.Hour).UnixMilli())

	counts, err := db.GetPeerScoreReasonCounts(r.Context(), reporter, since)
	if err != nil {
		logrus.WithError(err).Error("failed to load peer-score reason counts")
		http.Error(w, `{"error":"failed to load reasons"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, &APIPeerScoresReasonsResponse{Data: counts})
}

func writeJSON(w http.ResponseWriter, payload any) {
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logrus.WithError(err).Error("failed to encode peer-scores response")
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func parseInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

func parseInt64(s string, fallback int64) int64 {
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fallback
	}
	return v
}
