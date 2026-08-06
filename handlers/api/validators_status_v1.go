package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

// APIValidatorsStatusResponse represents the response structure for bulk validator status lookup
type APIValidatorsStatusResponse struct {
	Status string                   `json:"status"`
	Data   *APIValidatorsStatusData `json:"data"`
}

// APIValidatorsStatusData contains the validator status data
type APIValidatorsStatusData struct {
	Validators []*APIValidatorStatusInfo `json:"validators"`
	Count      uint64                    `json:"count"`
}

// APIValidatorStatusInfo represents the status of a single validator.
// Unknown validator indices are omitted from the response.
type APIValidatorStatusInfo struct {
	Index             uint64 `json:"index"`
	Status            string `json:"status"`
	Slashed           bool   `json:"slashed"`
	ActivationEpoch   uint64 `json:"activation_epoch"`
	ExitEpoch         uint64 `json:"exit_epoch"`
	WithdrawableEpoch uint64 `json:"withdrawable_epoch"`
	EffectiveBalance  uint64 `json:"effective_balance"`
}

// APIValidatorsStatusRequest represents the request body for POST requests
type APIValidatorsStatusRequest struct {
	Indices []uint64 `json:"indices"`
}

// APIValidatorsStatusV1 returns the status of validators in bulk
// Supports both GET (with query params) and POST (with JSON body) requests
// @Summary Get validator status in bulk
// @Description Returns status, slashed flag and lifecycle epochs for up to 10000 validators by index. Supports GET with query params or POST with JSON body for large lists. Unknown indices are omitted from the response.
// @Tags validators
// @Accept json
// @Produce json
// @Param indices query string false "Comma-separated list of validator indices (GET only)"
// @Param body body APIValidatorsStatusRequest false "Request body for POST requests with indices array"
// @Success 200 {object} APIValidatorsStatusResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/validators/status [get]
// @Router /v1/validators/status [post]
// @ID getValidatorsStatus
// @Security BearerAuth
func APIValidatorsStatusV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var indices []uint64

	// Handle different request methods
	switch r.Method {
	case "GET":
		// Parse query parameters
		indicesStr := r.URL.Query().Get("indices")
		if indicesStr == "" {
			http.Error(w, `{"status": "ERROR: indices parameter must be provided"}`, http.StatusBadRequest)
			return
		}

		for _, indexStr := range strings.Split(indicesStr, ",") {
			indexStr = strings.TrimSpace(indexStr)
			if indexStr == "" {
				continue
			}
			index, err := strconv.ParseUint(indexStr, 10, 64)
			if err != nil {
				http.Error(w, `{"status": "ERROR: invalid validator index: `+indexStr+`"}`, http.StatusBadRequest)
				return
			}
			indices = append(indices, index)
		}

	case "POST":
		// Parse JSON body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"status": "ERROR: failed to read request body"}`, http.StatusBadRequest)
			return
		}
		defer func() {
			_ = r.Body.Close()
		}()

		var req APIValidatorsStatusRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, `{"status": "ERROR: invalid JSON body"}`, http.StatusBadRequest)
			return
		}

		indices = req.Indices

	default:
		http.Error(w, `{"status": "ERROR: method not allowed, use GET or POST"}`, http.StatusMethodNotAllowed)
		return
	}

	if len(indices) == 0 {
		http.Error(w, `{"status": "ERROR: no valid indices provided"}`, http.StatusBadRequest)
		return
	}
	if len(indices) > 10000 {
		http.Error(w, `{"status": "ERROR: maximum 10000 indices allowed"}`, http.StatusBadRequest)
		return
	}

	validators := make([]*APIValidatorStatusInfo, 0, len(indices))
	for _, index := range indices {
		validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(index), false)
		if validator == nil || validator.Validator == nil {
			// Unknown validator index, omit from results
			continue
		}

		validators = append(validators, &APIValidatorStatusInfo{
			Index:             uint64(validator.Index),
			Status:            validator.Status.String(),
			Slashed:           validator.Validator.Slashed,
			ActivationEpoch:   uint64(validator.Validator.ActivationEpoch),
			ExitEpoch:         uint64(validator.Validator.ExitEpoch),
			WithdrawableEpoch: uint64(validator.Validator.WithdrawableEpoch),
			EffectiveBalance:  uint64(validator.Validator.EffectiveBalance),
		})
	}

	response := APIValidatorsStatusResponse{
		Status: "OK",
		Data: &APIValidatorsStatusData{
			Validators: validators,
			Count:      uint64(len(validators)),
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode validators status response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
