package api

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"
)

// APIClientsPeerScoresResponse is the response body of the peer scores API.
//
// The shape will be filled out alongside the real data wiring in a
// follow-up change. The current implementation returns an empty data
// array so that consumers can start integrating against the endpoint.
type APIClientsPeerScoresResponse struct {
	Data []any `json:"data"`
}

// APIClientsPeerScores returns peer score information for the connected
// consensus clients as JSON. The endpoint is only registered when
// PeerScoresConfig.Enabled is true.
//
// @Summary Get peer scores for connected consensus clients
// @Description Placeholder endpoint - returns an empty data array. The real
// @Description payload (per-peer scores collected from the connected
// @Description consensus clients) will be added in a follow-up change.
// @Tags clients
// @Accept json
// @Produce json
// @Success 200 {object} APIClientsPeerScoresResponse
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/clients/peer_scores [get]
// @ID getClientsPeerScores
func APIClientsPeerScores(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := APIClientsPeerScoresResponse{
		Data: []any{},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode peer scores response")
		http.Error(w, `{"error": "failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
