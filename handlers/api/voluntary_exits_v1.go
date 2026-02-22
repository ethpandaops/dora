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

// APIVoluntaryExitsResponse represents the response structure for voluntary exits list
type APIVoluntaryExitsResponse struct {
	Status string                 `json:"status"`
	Data   *APIVoluntaryExitsData `json:"data"`
}

// APIVoluntaryExitsData contains the voluntary exits data
type APIVoluntaryExitsData struct {
	VoluntaryExits []*APIVoluntaryExitInfo `json:"voluntary_exits"`
	Count          uint64                  `json:"count"`
	TotalCount     uint64                  `json:"total_count"`
	PageIndex      uint64                  `json:"page_index"`
	TotalPages     uint64                  `json:"total_pages"`
}

// APIVoluntaryExitInfo represents information about a single voluntary exit
type APIVoluntaryExitInfo struct {
	SlotNumber             uint64 `json:"slot_number"`
	SlotRoot               string `json:"slot_root"`
	Time                   int64  `json:"time"`
	Orphaned               bool   `json:"orphaned"`
	ValidatorIndex         uint64 `json:"validator_index"`
	ValidatorName          string `json:"validator_name,omitempty"`
	ValidatorStatus        string `json:"validator_status"`
	ValidatorBalance       uint64 `json:"validator_balance,omitempty"`
	PublicKey              string `json:"public_key,omitempty"`
	WithdrawalCredentials  string `json:"withdrawal_credentials,omitempty"`
	ValidatorLiveness      uint8  `json:"validator_liveness,omitempty"`
	ValidatorLivenessTotal uint8  `json:"validator_liveness_total,omitempty"`
}

// APIVoluntaryExits returns a list of voluntary exits with filters
// @Summary Get voluntary exits list
// @Description Returns a list of voluntary exits with detailed information and filtering options
// @Tags voluntary_exits
// @Accept json
// @Produce json
// @Param limit query int false "Number of voluntary exits to return (max 100)"
// @Param page query int false "Page number (starts at 1)"
// @Param min_slot query int false "Minimum slot number"
// @Param max_slot query int false "Maximum slot number"
// @Param min_index query int false "Minimum validator index"
// @Param max_index query int false "Maximum validator index"
// @Param validator_name query string false "Filter by validator name"
// @Param with_orphaned query int false "Include orphaned exits (1=include, 0=exclude)"
// @Success 200 {object} APIVoluntaryExitsResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/voluntary_exits [get]
// @ID getVoluntaryExits
func APIVoluntaryExitsV1(w http.ResponseWriter, r *http.Request) {
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
	var validatorName string
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

	if orphanedStr := query.Get("with_orphaned"); orphanedStr != "" {
		if parsed, err := strconv.ParseUint(orphanedStr, 10, 8); err == nil {
			withOrphaned = uint8(parsed)
		}
	}

	// Build voluntary exit filter
	voluntaryExitFilter := &dbtypes.VoluntaryExitFilter{
		MinSlot:       minSlot,
		MaxSlot:       maxSlot,
		MinIndex:      minIndex,
		MaxIndex:      maxIndex,
		ValidatorName: validatorName,
		WithOrphaned:  withOrphaned,
	}

	// Get voluntary exits from service
	dbVoluntaryExits, totalRows := services.GlobalBeaconService.GetVoluntaryExitsByFilter(r.Context(), voluntaryExitFilter, pageIdx-1, uint32(limit))
	chainState := services.GlobalBeaconService.GetChainState()

	var voluntaryExits []*APIVoluntaryExitInfo
	for _, voluntaryExit := range dbVoluntaryExits {
		exitInfo := &APIVoluntaryExitInfo{
			SlotNumber:     voluntaryExit.SlotNumber,
			SlotRoot:       fmt.Sprintf("0x%x", voluntaryExit.SlotRoot),
			Time:           chainState.SlotToTime(phase0.Slot(voluntaryExit.SlotNumber)).Unix(),
			Orphaned:       voluntaryExit.Orphaned,
			ValidatorIndex: voluntaryExit.ValidatorIndex,
			ValidatorName:  services.GlobalBeaconService.GetValidatorName(voluntaryExit.ValidatorIndex),
		}

		// Get validator information
		validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(voluntaryExit.ValidatorIndex), false)
		if validator == nil {
			exitInfo.ValidatorStatus = "Unknown"
		} else {
			exitInfo.ValidatorBalance = uint64(validator.Balance)
			exitInfo.PublicKey = fmt.Sprintf("0x%x", validator.Validator.PublicKey[:])
			exitInfo.WithdrawalCredentials = fmt.Sprintf("0x%x", validator.Validator.WithdrawalCredentials)

			if strings.HasPrefix(validator.Status.String(), "pending") {
				exitInfo.ValidatorStatus = "Pending"
			} else if validator.Status == v1.ValidatorStateActiveOngoing {
				exitInfo.ValidatorStatus = "Active"
				exitInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				exitInfo.ValidatorLivenessTotal = 3
			} else if validator.Status == v1.ValidatorStateActiveExiting {
				exitInfo.ValidatorStatus = "Exiting"
				exitInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				exitInfo.ValidatorLivenessTotal = 3
			} else if validator.Status == v1.ValidatorStateActiveSlashed {
				exitInfo.ValidatorStatus = "Slashed"
				exitInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				exitInfo.ValidatorLivenessTotal = 3
			} else if validator.Status == v1.ValidatorStateExitedUnslashed {
				exitInfo.ValidatorStatus = "Exited"
			} else if validator.Status == v1.ValidatorStateExitedSlashed {
				exitInfo.ValidatorStatus = "Slashed"
			} else {
				exitInfo.ValidatorStatus = validator.Status.String()
			}
		}

		voluntaryExits = append(voluntaryExits, exitInfo)
	}

	// Calculate pagination info
	totalPages := totalRows / limit
	if totalRows%limit > 0 {
		totalPages++
	}

	response := APIVoluntaryExitsResponse{
		Status: "OK",
		Data: &APIVoluntaryExitsData{
			VoluntaryExits: voluntaryExits,
			Count:          uint64(len(voluntaryExits)),
			TotalCount:     totalRows,
			PageIndex:      pageIdx,
			TotalPages:     totalPages,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode voluntary exits response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
