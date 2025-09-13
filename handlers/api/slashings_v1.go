package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APISlashingsResponse represents the response structure for slashings list
type APISlashingsResponse struct {
	Status string            `json:"status"`
	Data   *APISlashingsData `json:"data"`
}

// APISlashingsData contains the slashings data
type APISlashingsData struct {
	Slashings  []*APISlashingInfo `json:"slashings"`
	Count      uint64             `json:"count"`
	TotalCount uint64             `json:"total_count"`
	PageIndex  uint64             `json:"page_index"`
	TotalPages uint64             `json:"total_pages"`
}

// APISlashingInfo represents information about a single slashing
type APISlashingInfo struct {
	SlotNumber             uint64 `json:"slot_number"`
	SlotRoot               string `json:"slot_root"`
	Time                   int64  `json:"time"`
	Orphaned               bool   `json:"orphaned"`
	Reason                 uint8  `json:"reason"`
	ValidatorIndex         uint64 `json:"validator_index"`
	ValidatorName          string `json:"validator_name,omitempty"`
	ValidatorStatus        string `json:"validator_status"`
	ValidatorBalance       uint64 `json:"validator_balance,omitempty"`
	SlasherIndex           uint64 `json:"slasher_index"`
	SlasherName            string `json:"slasher_name,omitempty"`
	ValidatorLiveness      uint8  `json:"validator_liveness,omitempty"`
	ValidatorLivenessTotal uint8  `json:"validator_liveness_total,omitempty"`
}

// APISlashings returns a list of slashings with filters
// @Summary Get slashings list
// @Description Returns a list of slashings with detailed information and filtering options
// @Tags slashings
// @Accept json
// @Produce json
// @Param limit query int false "Number of slashings to return (max 100)"
// @Param page query int false "Page number (starts at 1)"
// @Param min_slot query int false "Minimum slot number"
// @Param max_slot query int false "Maximum slot number"
// @Param min_index query int false "Minimum validator index"
// @Param max_index query int false "Maximum validator index"
// @Param validator_name query string false "Filter by validator name"
// @Param slasher_name query string false "Filter by slasher name"
// @Param reason query int false "Filter by slashing reason"
// @Param with_orphaned query int false "Include orphaned slashings (0=exclude, 1=include, 2=only orphaned)"
// @Success 200 {object} APISlashingsResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slashings [get]
func APISlashingsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()

	// Parse limit parameter
	limit := uint64(50)
	limitStr := query.Get("limit")
	if limitStr != "" {
		parsedLimit, err := strconv.ParseUint(limitStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid limit parameter"}`, http.StatusBadRequest)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		if parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Parse page parameter
	pageIdx := uint64(1)
	pageStr := query.Get("page")
	if pageStr != "" {
		parsedPage, err := strconv.ParseUint(pageStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid page parameter"}`, http.StatusBadRequest)
			return
		}
		if parsedPage > 0 {
			pageIdx = parsedPage
		}
	}

	// Parse filter parameters
	var minSlot, maxSlot, minIndex, maxIndex uint64
	var validatorName, slasherName string
	var withReason uint8
	var withOrphaned uint8 = 1 // Default to include orphaned

	if minSlotStr := query.Get("min_slot"); minSlotStr != "" {
		if parsed, err := strconv.ParseUint(minSlotStr, 10, 64); err == nil {
			minSlot = parsed
		}
	}

	if maxSlotStr := query.Get("max_slot"); maxSlotStr != "" {
		if parsed, err := strconv.ParseUint(maxSlotStr, 10, 64); err == nil {
			maxSlot = parsed
		}
	}

	if minIndexStr := query.Get("min_index"); minIndexStr != "" {
		if parsed, err := strconv.ParseUint(minIndexStr, 10, 64); err == nil {
			minIndex = parsed
		}
	}

	if maxIndexStr := query.Get("max_index"); maxIndexStr != "" {
		if parsed, err := strconv.ParseUint(maxIndexStr, 10, 64); err == nil {
			maxIndex = parsed
		}
	}

	validatorName = query.Get("validator_name")
	slasherName = query.Get("slasher_name")

	if reasonStr := query.Get("reason"); reasonStr != "" {
		if parsed, err := strconv.ParseUint(reasonStr, 10, 8); err == nil {
			withReason = uint8(parsed)
		}
	}

	if orphanedStr := query.Get("with_orphaned"); orphanedStr != "" {
		if parsed, err := strconv.ParseUint(orphanedStr, 10, 8); err == nil {
			withOrphaned = uint8(parsed)
		}
	}

	// Build slashing filter
	slashingFilter := &dbtypes.SlashingFilter{
		MinSlot:       minSlot,
		MaxSlot:       maxSlot,
		MinIndex:      minIndex,
		MaxIndex:      maxIndex,
		ValidatorName: validatorName,
		SlasherName:   slasherName,
		WithReason:    dbtypes.SlashingReason(withReason),
		WithOrphaned:  withOrphaned,
	}

	// Get slashings from service
	dbSlashings, totalRows := services.GlobalBeaconService.GetSlashingsByFilter(slashingFilter, pageIdx-1, uint32(limit))
	chainState := services.GlobalBeaconService.GetChainState()

	var slashings []*APISlashingInfo
	for _, slashing := range dbSlashings {
		slashingInfo := &APISlashingInfo{
			SlotNumber:     slashing.SlotNumber,
			SlotRoot:       fmt.Sprintf("0x%x", slashing.SlotRoot),
			Time:           chainState.SlotToTime(phase0.Slot(slashing.SlotNumber)).Unix(),
			Orphaned:       slashing.Orphaned,
			Reason:         uint8(slashing.Reason),
			ValidatorIndex: slashing.ValidatorIndex,
			ValidatorName:  services.GlobalBeaconService.GetValidatorName(slashing.ValidatorIndex),
			SlasherIndex:   slashing.SlasherIndex,
			SlasherName:    services.GlobalBeaconService.GetValidatorName(slashing.SlasherIndex),
		}

		// Get validator information
		validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(slashing.ValidatorIndex), false)
		if validator == nil {
			slashingInfo.ValidatorStatus = "Unknown"
		} else {
			slashingInfo.ValidatorBalance = uint64(validator.Balance)

			if strings.HasPrefix(validator.Status.String(), "pending") {
				slashingInfo.ValidatorStatus = "Pending"
			} else if validator.Status == v1.ValidatorStateActiveOngoing {
				slashingInfo.ValidatorStatus = "Active"
				slashingInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				slashingInfo.ValidatorLivenessTotal = 3
			} else if validator.Status == v1.ValidatorStateActiveExiting {
				slashingInfo.ValidatorStatus = "Exiting"
				slashingInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				slashingInfo.ValidatorLivenessTotal = 3
			} else if validator.Status == v1.ValidatorStateActiveSlashed {
				slashingInfo.ValidatorStatus = "Slashed"
				slashingInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				slashingInfo.ValidatorLivenessTotal = 3
			} else if validator.Status == v1.ValidatorStateExitedUnslashed {
				slashingInfo.ValidatorStatus = "Exited"
			} else if validator.Status == v1.ValidatorStateExitedSlashed {
				slashingInfo.ValidatorStatus = "Slashed"
			} else {
				slashingInfo.ValidatorStatus = validator.Status.String()
			}
		}

		slashings = append(slashings, slashingInfo)
	}

	// Calculate pagination info
	totalPages := totalRows / limit
	if totalRows%limit > 0 {
		totalPages++
	}

	response := APISlashingsResponse{
		Status: "OK",
		Data: &APISlashingsData{
			Slashings:  slashings,
			Count:      uint64(len(slashings)),
			TotalCount: totalRows,
			PageIndex:  pageIdx,
			TotalPages: totalPages,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode slashings response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
