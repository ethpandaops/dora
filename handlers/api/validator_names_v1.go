package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIValidatorNamesResponse represents the response structure for validator names lookup
type APIValidatorNamesResponse struct {
	Status string                 `json:"status"`
	Data   *APIValidatorNamesData `json:"data"`
}

// APIValidatorNamesData contains the validator names data
type APIValidatorNamesData struct {
	ValidatorNames []*APIValidatorNameInfo `json:"validator_names"`
	Count          uint64                  `json:"count"`
}

// APIValidatorNameInfo represents information about a single validator name lookup
type APIValidatorNameInfo struct {
	Index     uint64 `json:"index"`
	Name      string `json:"name,omitempty"`
	PublicKey string `json:"public_key"`
	Found     bool   `json:"found"`
}

// APIValidatorNamesRequest represents the request body for POST requests
type APIValidatorNamesRequest struct {
	Indices []uint64 `json:"indices,omitempty"`
	Pubkeys []string `json:"pubkeys,omitempty"`
}

// APIValidatorNamesV1 returns validator names for given indices or public keys
// Supports both GET (with query params) and POST (with JSON body) requests
// @Summary Get validator names
// @Description Returns validator names for given validator indices or public keys. Supports GET with query params or POST with JSON body for large lists.
// @Tags validator_names
// @Accept json
// @Produce json
// @Param indices query string false "Comma-separated list of validator indices (GET only)"
// @Param pubkeys query string false "Comma-separated list of validator public keys (GET only, with or without 0x prefix)"
// @Param body body APIValidatorNamesRequest false "Request body for POST requests with indices and/or pubkeys arrays"
// @Success 200 {object} APIValidatorNamesResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/validator_names [get]
// @Router /v1/validator_names [post]
// @ID getValidatorNames
func APIValidatorNamesV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var indices []uint64
	var pubkeys []string

	// Handle different request methods
	switch r.Method {
	case "GET":
		// Parse query parameters
		query := r.URL.Query()
		indicesStr := query.Get("indices")
		pubkeysStr := query.Get("pubkeys")

		if indicesStr == "" && pubkeysStr == "" {
			http.Error(w, `{"status": "ERROR: either indices or pubkeys parameter must be provided"}`, http.StatusBadRequest)
			return
		}

		// Parse indices from query string
		if indicesStr != "" {
			indexList := strings.SplitSeq(indicesStr, ",")
			for indexStr := range indexList {
				indexStr = strings.TrimSpace(indexStr)
				if indexStr == "" {
					continue
				}
				if index, err := strconv.ParseUint(indexStr, 10, 64); err == nil {
					indices = append(indices, index)
				}
			}
		}

		// Parse pubkeys from query string
		if pubkeysStr != "" {
			pubkeyList := strings.SplitSeq(pubkeysStr, ",")
			for pubkeyStr := range pubkeyList {
				pubkeyStr = strings.TrimSpace(pubkeyStr)
				if pubkeyStr != "" {
					pubkeys = append(pubkeys, pubkeyStr)
				}
			}
		}

	case "POST":
		// Parse JSON body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"status": "ERROR: failed to read request body"}`, http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var req APIValidatorNamesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, `{"status": "ERROR: invalid JSON body"}`, http.StatusBadRequest)
			return
		}

		if len(req.Indices) == 0 && len(req.Pubkeys) == 0 {
			http.Error(w, `{"status": "ERROR: either indices or pubkeys array must be provided"}`, http.StatusBadRequest)
			return
		}

		indices = req.Indices
		pubkeys = req.Pubkeys

	default:
		http.Error(w, `{"status": "ERROR: method not allowed, use GET or POST"}`, http.StatusMethodNotAllowed)
		return
	}

	// Validate total count (max 100 items combined)
	totalItems := len(indices) + len(pubkeys)
	if totalItems == 0 {
		http.Error(w, `{"status": "ERROR: no valid indices or pubkeys provided"}`, http.StatusBadRequest)
		return
	}
	if totalItems > 100 {
		http.Error(w, `{"status": "ERROR: maximum 100 indices/pubkeys allowed in total"}`, http.StatusBadRequest)
		return
	}

	var validatorNames []*APIValidatorNameInfo

	// Process indices
	for _, index := range indices {
		validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(index), false)
		validatorInfo := &APIValidatorNameInfo{
			Index: index,
			Found: validator != nil,
		}

		if validator != nil {
			validatorInfo.Name = services.GlobalBeaconService.GetValidatorName(index)
			validatorInfo.PublicKey = fmt.Sprintf("0x%x", validator.Validator.PublicKey[:])
		}

		validatorNames = append(validatorNames, validatorInfo)
	}

	// Process pubkeys
	for _, pubkeyStr := range pubkeys {
		// Remove 0x prefix if present
		pubkeyStr = strings.TrimPrefix(pubkeyStr, "0x")

		// Decode hex string
		pubkeyBytes, err := hex.DecodeString(pubkeyStr)
		if err != nil || len(pubkeyBytes) != 48 {
			validatorNames = append(validatorNames, &APIValidatorNameInfo{
				PublicKey: "0x" + pubkeyStr,
				Found:     false,
			})
			continue
		}

		// Convert to BLS public key
		var pubkey phase0.BLSPubKey
		copy(pubkey[:], pubkeyBytes)

		// Look up validator by public key
		validatorIndex, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(pubkey)
		validatorInfo := &APIValidatorNameInfo{
			PublicKey: fmt.Sprintf("0x%x", pubkeyBytes),
			Found:     found,
		}

		if found {
			validatorInfo.Index = uint64(validatorIndex)
			validatorInfo.Name = services.GlobalBeaconService.GetValidatorName(uint64(validatorIndex))
		}

		validatorNames = append(validatorNames, validatorInfo)
	}

	response := APIValidatorNamesResponse{
		Status: "OK",
		Data: &APIValidatorNamesData{
			ValidatorNames: validatorNames,
			Count:          uint64(len(validatorNames)),
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode validator names response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
