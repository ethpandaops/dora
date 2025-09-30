package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIWithdrawalRequestsResponse represents the response structure for withdrawal requests list
type APIWithdrawalRequestsResponse struct {
	Status string                     `json:"status"`
	Data   *APIWithdrawalRequestsData `json:"data"`
}

// APIWithdrawalRequestsData contains the withdrawal requests data
type APIWithdrawalRequestsData struct {
	WithdrawalRequests []*APIWithdrawalRequestInfo `json:"withdrawal_requests"`
	Count              uint64                      `json:"count"`
	TotalRequests      uint64                      `json:"total_requests"`
	TotalPending       uint64                      `json:"total_pending"`
}

// APIWithdrawalRequestInfo represents information about a single withdrawal request
type APIWithdrawalRequestInfo struct {
	SourceAddress      string                         `json:"source_address"`
	Amount             uint64                         `json:"amount"`
	PublicKey          string                         `json:"public_key"`
	ValidatorIndex     uint64                         `json:"validator_index,omitempty"`
	ValidatorName      string                         `json:"validator_name,omitempty"`
	ValidatorValid     bool                           `json:"validator_valid"`
	IsIncluded         bool                           `json:"is_included"`
	SlotNumber         uint64                         `json:"slot_number,omitempty"`
	SlotRoot           string                         `json:"slot_root,omitempty"`
	Time               int64                          `json:"time,omitempty"`
	Status             uint64                         `json:"status"` // 0=pending, 1=included, 2=orphaned
	Result             uint8                          `json:"result,omitempty"`
	ResultMessage      string                         `json:"result_message,omitempty"`
	TxStatus           uint64                         `json:"tx_status"` // 0=pending, 1=confirmed, 2=orphaned
	TransactionHash    string                         `json:"transaction_hash,omitempty"`
	TransactionDetails *APIWithdrawalRequestTxDetails `json:"transaction_details,omitempty"`
}

// APIWithdrawalRequestTxDetails represents transaction details for a withdrawal request
type APIWithdrawalRequestTxDetails struct {
	BlockNumber uint64 `json:"block_number"`
	BlockHash   string `json:"block_hash"`
	BlockTime   int64  `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}

// APIWithdrawalRequests returns a list of withdrawal requests with filters
// @Summary Get withdrawal requests list
// @Description Returns a list of withdrawal requests (EL triggered) with detailed information and filtering options
// @Tags withdrawal_requests
// @Accept json
// @Produce json
// @Param limit query int false "Number of withdrawal requests to return (max 100)"
// @Param offset query int false "Offset for pagination"
// @Param min_slot query int false "Minimum slot number"
// @Param max_slot query int false "Maximum slot number"
// @Param source_address query string false "Filter by source address"
// @Param min_index query int false "Minimum validator index"
// @Param max_index query int false "Maximum validator index"
// @Param validator_name query string false "Filter by validator name"
// @Param with_orphaned query int false "Include orphaned requests (1=include, 0=exclude)"
// @Param type query int false "Filter by type (1=withdrawals, 2=exits)"
// @Param public_key query string false "Filter by public key"
// @Success 200 {object} APIWithdrawalRequestsResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/withdrawal_requests [get]
// @ID getWithdrawalRequests
func APIWithdrawalRequestsV1(w http.ResponseWriter, r *http.Request) {
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

	// Parse offset parameter
	offset := uint64(0)
	offsetStr := query.Get("offset")
	if offsetStr != "" {
		parsedOffset, err := strconv.ParseUint(offsetStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid offset parameter"}`, http.StatusBadRequest)
			return
		}
		offset = parsedOffset
	}

	// Parse filter parameters
	var minSlot, maxSlot, minIndex, maxIndex uint64
	var sourceAddr, validatorName, pubkey string
	var withOrphaned uint8 = 1 // Default to include orphaned
	var withType uint8

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

	sourceAddr = query.Get("source_address")
	validatorName = query.Get("validator_name")
	pubkey = query.Get("public_key")

	if orphanedStr := query.Get("with_orphaned"); orphanedStr != "" {
		if parsed, err := strconv.ParseUint(orphanedStr, 10, 8); err == nil {
			withOrphaned = uint8(parsed)
		}
	}

	if typeStr := query.Get("type"); typeStr != "" {
		if parsed, err := strconv.ParseUint(typeStr, 10, 8); err == nil {
			withType = uint8(parsed)
		}
	}

	// Build withdrawal request filter
	withdrawalRequestFilter := &services.CombinedWithdrawalRequestFilter{
		Filter: &dbtypes.WithdrawalRequestFilter{
			MinSlot:       minSlot,
			MaxSlot:       maxSlot,
			SourceAddress: common.FromHex(sourceAddr),
			MinIndex:      minIndex,
			MaxIndex:      maxIndex,
			ValidatorName: validatorName,
			WithOrphaned:  withOrphaned,
			PublicKey:     common.FromHex(pubkey),
		},
	}

	// Handle type filter
	switch withType {
	case 1: // withdrawals
		minAmount := uint64(1)
		withdrawalRequestFilter.Filter.MinAmount = &minAmount
	case 2: // exits
		maxAmount := uint64(0)
		withdrawalRequestFilter.Filter.MaxAmount = &maxAmount
	}

	// Get withdrawal requests from service
	dbElWithdrawals, totalPendingTxRows, totalRequests := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(
		withdrawalRequestFilter, offset, uint32(limit))

	chainState := services.GlobalBeaconService.GetChainState()
	headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
	headBlockNum := uint64(0)
	if headBlock != nil && headBlock.GetBlockIndex() != nil {
		headBlockNum = uint64(headBlock.GetBlockIndex().ExecutionNumber)
	}

	var withdrawalRequests []*APIWithdrawalRequestInfo
	for _, elWithdrawal := range dbElWithdrawals {
		requestInfo := &APIWithdrawalRequestInfo{
			SourceAddress: common.Address(elWithdrawal.SourceAddress()).Hex(),
			Amount:        elWithdrawal.Amount(),
			PublicKey:     fmt.Sprintf("0x%x", elWithdrawal.ValidatorPubkey()),
		}

		// Set validator information
		if validatorIndex := elWithdrawal.ValidatorIndex(); validatorIndex != nil {
			requestInfo.ValidatorIndex = *validatorIndex
			requestInfo.ValidatorName = services.GlobalBeaconService.GetValidatorName(*validatorIndex)
			requestInfo.ValidatorValid = true
		}

		// Set request information if included
		if request := elWithdrawal.Request; request != nil {
			requestInfo.IsIncluded = true
			requestInfo.SlotNumber = request.SlotNumber
			requestInfo.SlotRoot = fmt.Sprintf("0x%x", request.SlotRoot)
			requestInfo.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber)).Unix()
			requestInfo.Status = 1 // included
			requestInfo.Result = request.Result
			requestInfo.ResultMessage = getWithdrawalResultMessage(request.Result, chainState.GetSpecs())

			if elWithdrawal.RequestOrphaned {
				requestInfo.Status = 2 // orphaned
			}
		}

		// Set transaction information
		if transaction := elWithdrawal.Transaction; transaction != nil {
			requestInfo.TransactionHash = fmt.Sprintf("0x%x", transaction.TxHash)
			requestInfo.TransactionDetails = &APIWithdrawalRequestTxDetails{
				BlockNumber: transaction.BlockNumber,
				BlockHash:   fmt.Sprintf("0x%x", transaction.BlockRoot),
				BlockTime:   int64(transaction.BlockTime),
				TxOrigin:    common.Address(transaction.TxSender).Hex(),
				TxTarget:    common.Address(transaction.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("0x%x", transaction.TxHash),
			}
			requestInfo.TxStatus = 1 // confirmed

			if elWithdrawal.TransactionOrphaned {
				requestInfo.TxStatus = 2 // orphaned
			}

			// If not included yet, estimate the slot number based on queue position
			if !requestInfo.IsIncluded {
				queuePos := int64(transaction.DequeueBlock) - int64(headBlockNum)
				targetSlot := int64(chainState.CurrentSlot()) + queuePos
				if targetSlot > 0 {
					requestInfo.SlotNumber = uint64(targetSlot)
					requestInfo.Time = chainState.SlotToTime(phase0.Slot(targetSlot)).Unix()
				}
			}
		}

		withdrawalRequests = append(withdrawalRequests, requestInfo)
	}

	response := APIWithdrawalRequestsResponse{
		Status: "OK",
		Data: &APIWithdrawalRequestsData{
			WithdrawalRequests: withdrawalRequests,
			Count:              uint64(len(withdrawalRequests)),
			TotalRequests:      totalRequests,
			TotalPending:       totalPendingTxRows,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode withdrawal requests response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

// getWithdrawalResultMessage returns a human-readable message for the withdrawal result
func getWithdrawalResultMessage(result uint8, specs *consensus.ChainSpec) string {
	switch result {
	case 0:
		return "Success"
	case 1:
		return "Partial withdrawal"
	case 2:
		return "Invalid validator public key"
	case 3:
		return "Invalid withdrawal amount"
	case 4:
		return "Validator not found"
	case 5:
		return "Invalid withdrawal credentials"
	case 6:
		return "Validator already exiting"
	case 7:
		return "Validator already exited"
	case 8:
		return "Validator balance too low"
	case 9:
		return "Excess balance withdrawal"
	default:
		return fmt.Sprintf("Unknown result: %d", result)
	}
}