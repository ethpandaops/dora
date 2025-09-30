package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIValidatorsResponse represents the response structure for validators list
type APIValidatorsResponse struct {
	Status string             `json:"status"`
	Data   *APIValidatorsData `json:"data"`
}

// APIValidatorsData contains the validators data
type APIValidatorsData struct {
	Validators     []*APIValidatorInfo `json:"validators"`
	Count          uint64              `json:"count"`
	TotalCount     uint64              `json:"total_count"`
	PageIndex      uint64              `json:"page_index"`
	TotalPages     uint64              `json:"total_pages"`
	FirstValidator uint64              `json:"first_validator"`
	LastValidator  uint64              `json:"last_validator"`
	CurrentEpoch   uint64              `json:"current_epoch"`
	FinalizedEpoch uint64              `json:"finalized_epoch"`
}

// APIValidatorInfo represents information about a single validator
type APIValidatorInfo struct {
	Index                uint64 `json:"index"`
	Name                 string `json:"name,omitempty"`
	PublicKey            string `json:"public_key"`
	Balance              uint64 `json:"balance"`
	EffectiveBalance     uint64 `json:"effective_balance"`
	Status               string `json:"status"`
	ActivationEpoch      uint64 `json:"activation_epoch,omitempty"`
	ActivationTime       int64  `json:"activation_time,omitempty"`
	ExitEpoch            uint64 `json:"exit_epoch,omitempty"`
	ExitTime             int64  `json:"exit_time,omitempty"`
	WithdrawalAddress    string `json:"withdrawal_address,omitempty"`
	WithdrawalCreds      string `json:"withdrawal_credentials"`
	ValidatorLiveness    uint8  `json:"validator_liveness,omitempty"`
	ValidatorLivenessMax uint8  `json:"validator_liveness_max,omitempty"`
}

// APIValidators returns a list of validators with filters
// @Summary Get validators list
// @Description Returns a list of validators with detailed information and filtering options
// @Tags validators
// @Accept json
// @Produce json
// @Param limit query int false "Number of validators to return (max 1000)"
// @Param page query int false "Page number (starts at 1)"
// @Param pubkey query string false "Filter by public key"
// @Param index query string false "Filter by validator index"
// @Param name query string false "Filter by validator name"
// @Param status query string false "Filter by validator status (comma-separated)"
// @Param order query string false "Sort order: index, index-d, pubkey, pubkey-d, balance, balance-d, activation, activation-d, exit, exit-d"
// @Success 200 {object} APIValidatorsResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/validators [get]
// @ID getValidators
func APIValidatorsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()
	chainState := services.GlobalBeaconService.GetChainState()
	currentEpoch := chainState.CurrentEpoch()
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()

	// Parse limit parameter
	limit := uint64(50)
	limitStr := query.Get("limit")
	if limitStr != "" {
		parsedLimit, err := strconv.ParseUint(limitStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid limit parameter"}`, http.StatusBadRequest)
			return
		}
		if parsedLimit > 1000 {
			parsedLimit = 1000
		}
		if parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Parse page parameter
	pageNumber := uint64(1)
	pageStr := query.Get("page")
	if pageStr != "" {
		parsedPage, err := strconv.ParseUint(pageStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid page parameter"}`, http.StatusBadRequest)
			return
		}
		if parsedPage > 0 {
			pageNumber = parsedPage
		}
	}

	// Parse filter parameters
	filterPubKey := query.Get("pubkey")
	filterIndex := query.Get("index")
	filterName := query.Get("name")
	filterStatus := query.Get("status")
	sortOrder := query.Get("order")

	// Build validator filter
	validatorFilter := dbtypes.ValidatorFilter{
		Limit:  limit,
		Offset: (pageNumber - 1) * limit,
	}

	// Apply filters
	if filterPubKey != "" {
		filterPubKeyVal, err := hex.DecodeString(strings.Replace(filterPubKey, "0x", "", -1))
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid public key format"}`, http.StatusBadRequest)
			return
		}
		validatorFilter.PubKey = filterPubKeyVal
	}

	if filterIndex != "" {
		filterIndexVal, err := strconv.ParseUint(filterIndex, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid index parameter"}`, http.StatusBadRequest)
			return
		}
		validatorFilter.MinIndex = &filterIndexVal
		validatorFilter.MaxIndex = &filterIndexVal
	}

	if filterName != "" {
		validatorFilter.ValidatorName = filterName
	}

	if filterStatus != "" {
		filterStatusVal := strings.Split(filterStatus, ",")
		validatorFilter.Status = make([]v1.ValidatorState, 0)
		for _, status := range filterStatusVal {
			statusVal := v1.ValidatorState(0)
			err := statusVal.UnmarshalJSON([]byte(fmt.Sprintf("\"%v\"", status)))
			if err == nil {
				validatorFilter.Status = append(validatorFilter.Status, statusVal)
			} else {
				http.Error(w, fmt.Sprintf(`{"status": "ERROR: invalid status value: %s"}`, status), http.StatusBadRequest)
				return
			}
		}
	}

	// Apply sort order
	switch sortOrder {
	case "index-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderIndexDesc
	case "pubkey":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderPubKeyAsc
	case "pubkey-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderPubKeyDesc
	case "balance":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderBalanceAsc
	case "balance-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderBalanceDesc
	case "activation":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderActivationEpochAsc
	case "activation-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderActivationEpochDesc
	case "exit":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderExitEpochAsc
	case "exit-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderExitEpochDesc
	default:
		validatorFilter.OrderBy = dbtypes.ValidatorOrderIndexAsc
	}

	// Get validators from service
	validatorSet, validatorSetLen := services.GlobalBeaconService.GetFilteredValidatorSet(&validatorFilter, true)

	var validators []*APIValidatorInfo
	for _, validator := range validatorSet {
		if validator.Validator == nil {
			continue
		}

		validatorInfo := &APIValidatorInfo{
			Index:            uint64(validator.Index),
			Name:             services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
			PublicKey:        fmt.Sprintf("0x%x", validator.Validator.PublicKey[:]),
			Balance:          uint64(validator.Balance),
			EffectiveBalance: uint64(validator.Validator.EffectiveBalance),
			WithdrawalCreds:  fmt.Sprintf("0x%x", validator.Validator.WithdrawalCredentials),
		}

		// Set validator status and liveness
		if strings.HasPrefix(validator.Status.String(), "pending") {
			validatorInfo.Status = "Pending"
		} else if validator.Status == v1.ValidatorStateActiveOngoing {
			validatorInfo.Status = "Active"
			validatorInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
			validatorInfo.ValidatorLivenessMax = 3
		} else if validator.Status == v1.ValidatorStateActiveExiting {
			validatorInfo.Status = "Exiting"
			validatorInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
			validatorInfo.ValidatorLivenessMax = 3
		} else if validator.Status == v1.ValidatorStateActiveSlashed {
			validatorInfo.Status = "Slashed"
			validatorInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
			validatorInfo.ValidatorLivenessMax = 3
		} else if validator.Status == v1.ValidatorStateExitedUnslashed {
			validatorInfo.Status = "Exited"
		} else if validator.Status == v1.ValidatorStateExitedSlashed {
			validatorInfo.Status = "Slashed"
		} else {
			validatorInfo.Status = validator.Status.String()
		}

		// Set activation information
		if validator.Validator.ActivationEpoch < 18446744073709551615 {
			validatorInfo.ActivationEpoch = uint64(validator.Validator.ActivationEpoch)
			validatorInfo.ActivationTime = chainState.EpochToTime(validator.Validator.ActivationEpoch).Unix()
		}

		// Set exit information
		if validator.Validator.ExitEpoch < 18446744073709551615 {
			validatorInfo.ExitEpoch = uint64(validator.Validator.ExitEpoch)
			validatorInfo.ExitTime = chainState.EpochToTime(validator.Validator.ExitEpoch).Unix()
		}

		// Set withdrawal address if available
		if validator.Validator.WithdrawalCredentials[0] == 0x01 || validator.Validator.WithdrawalCredentials[0] == 0x02 {
			validatorInfo.WithdrawalAddress = fmt.Sprintf("0x%x", validator.Validator.WithdrawalCredentials[12:])
		}

		validators = append(validators, validatorInfo)
	}

	// Calculate pagination info
	totalPages := validatorSetLen / limit
	if (validatorSetLen % limit) > 0 {
		totalPages++
	}

	firstValidator := (pageNumber - 1) * limit
	lastValidator := firstValidator + uint64(len(validators))

	response := APIValidatorsResponse{
		Status: "OK",
		Data: &APIValidatorsData{
			Validators:     validators,
			Count:          uint64(len(validators)),
			TotalCount:     validatorSetLen,
			PageIndex:      pageNumber,
			TotalPages:     totalPages,
			FirstValidator: firstValidator,
			LastValidator:  lastValidator,
			CurrentEpoch:   uint64(currentEpoch),
			FinalizedEpoch: uint64(finalizedEpoch),
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode validators response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}