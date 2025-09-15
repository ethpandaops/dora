package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIConsolidationRequestsResponse represents the response structure for consolidation requests list
type APIConsolidationRequestsResponse struct {
	Status string                        `json:"status"`
	Data   *APIConsolidationRequestsData `json:"data"`
}

// APIConsolidationRequestsData contains the consolidation requests data
type APIConsolidationRequestsData struct {
	ConsolidationRequests []*APIConsolidationRequestInfo `json:"consolidation_requests"`
	Count                 uint64                         `json:"count"`
	TotalRequests         uint64                         `json:"total_requests"`
	TotalPending          uint64                         `json:"total_pending"`
}

// APIConsolidationRequestInfo represents information about a single consolidation request
type APIConsolidationRequestInfo struct {
	SourceAddress        string                            `json:"source_address"`
	SourcePublicKey      string                            `json:"source_public_key"`
	SourceValidatorIndex uint64                            `json:"source_validator_index,omitempty"`
	SourceValidatorName  string                            `json:"source_validator_name,omitempty"`
	SourceValidatorValid bool                              `json:"source_validator_valid"`
	TargetPublicKey      string                            `json:"target_public_key"`
	TargetValidatorIndex uint64                            `json:"target_validator_index,omitempty"`
	TargetValidatorName  string                            `json:"target_validator_name,omitempty"`
	TargetValidatorValid bool                              `json:"target_validator_valid"`
	IsIncluded           bool                              `json:"is_included"`
	SlotNumber           uint64                            `json:"slot_number,omitempty"`
	SlotRoot             string                            `json:"slot_root,omitempty"`
	Time                 int64                             `json:"time,omitempty"`
	Status               uint64                            `json:"status"` // 0=pending, 1=included, 2=orphaned
	Result               uint8                             `json:"result,omitempty"`
	ResultMessage        string                            `json:"result_message,omitempty"`
	TxStatus             uint64                            `json:"tx_status"` // 0=pending, 1=confirmed, 2=orphaned
	TransactionHash      string                            `json:"transaction_hash,omitempty"`
	TransactionDetails   *APIConsolidationRequestTxDetails `json:"transaction_details,omitempty"`
}

// APIConsolidationRequestTxDetails represents transaction details for a consolidation request
type APIConsolidationRequestTxDetails struct {
	BlockNumber uint64 `json:"block_number"`
	BlockHash   string `json:"block_hash"`
	BlockTime   int64  `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}

// APIConsolidationRequests returns a list of consolidation requests with filters
// @Summary Get consolidation requests list
// @Description Returns a list of consolidation requests (EL triggered) with detailed information and filtering options
// @Tags consolidation_requests
// @Accept json
// @Produce json
// @Param limit query int false "Number of consolidation requests to return (max 100)"
// @Param offset query int false "Offset for pagination"
// @Param min_slot query int false "Minimum slot number"
// @Param max_slot query int false "Maximum slot number"
// @Param source_address query string false "Filter by source address"
// @Param min_src_index query int false "Minimum source validator index"
// @Param max_src_index query int false "Maximum source validator index"
// @Param src_validator_name query string false "Filter by source validator name"
// @Param min_tgt_index query int false "Minimum target validator index"
// @Param max_tgt_index query int false "Maximum target validator index"
// @Param tgt_validator_name query string false "Filter by target validator name"
// @Param with_orphaned query int false "Include orphaned requests (2=orphaned only, 1=include all, 0=exclude orphaned)"
// @Param public_key query string false "Filter by public key"
// @Success 200 {object} APIConsolidationRequestsResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/consolidation_requests [get]
func APIConsolidationRequestsV1(w http.ResponseWriter, r *http.Request) {
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
	var minSlot, maxSlot, minSrcIndex, maxSrcIndex, minTgtIndex, maxTgtIndex uint64
	var sourceAddr, srcValidatorName, tgtValidatorName, pubkey string
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

	if minSrcIndexStr := query.Get("min_src_index"); minSrcIndexStr != "" {
		if parsed, err := strconv.ParseUint(minSrcIndexStr, 10, 64); err == nil {
			minSrcIndex = parsed
		}
	}

	if maxSrcIndexStr := query.Get("max_src_index"); maxSrcIndexStr != "" {
		if parsed, err := strconv.ParseUint(maxSrcIndexStr, 10, 64); err == nil {
			maxSrcIndex = parsed
		}
	}

	if minTgtIndexStr := query.Get("min_tgt_index"); minTgtIndexStr != "" {
		if parsed, err := strconv.ParseUint(minTgtIndexStr, 10, 64); err == nil {
			minTgtIndex = parsed
		}
	}

	if maxTgtIndexStr := query.Get("max_tgt_index"); maxTgtIndexStr != "" {
		if parsed, err := strconv.ParseUint(maxTgtIndexStr, 10, 64); err == nil {
			maxTgtIndex = parsed
		}
	}

	sourceAddr = query.Get("source_address")
	srcValidatorName = query.Get("src_validator_name")
	tgtValidatorName = query.Get("tgt_validator_name")
	pubkey = query.Get("public_key")

	if orphanedStr := query.Get("with_orphaned"); orphanedStr != "" {
		if parsed, err := strconv.ParseUint(orphanedStr, 10, 8); err == nil {
			withOrphaned = uint8(parsed)
		}
	}

	// Build consolidation request filter
	consolidationRequestFilter := &services.CombinedConsolidationRequestFilter{
		Filter: &dbtypes.ConsolidationRequestFilter{
			MinSlot:          minSlot,
			MaxSlot:          maxSlot,
			SourceAddress:    common.FromHex(sourceAddr),
			MinSrcIndex:      minSrcIndex,
			MaxSrcIndex:      maxSrcIndex,
			SrcValidatorName: srcValidatorName,
			MinTgtIndex:      minTgtIndex,
			MaxTgtIndex:      maxTgtIndex,
			TgtValidatorName: tgtValidatorName,
			WithOrphaned:     withOrphaned,
			PublicKey:        common.FromHex(pubkey),
		},
	}

	// Get consolidation requests from service
	dbElConsolidations, totalPendingTxRows, totalRequests := services.GlobalBeaconService.GetConsolidationRequestsByFilter(
		consolidationRequestFilter, offset, uint32(limit))

	chainState := services.GlobalBeaconService.GetChainState()
	headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
	headBlockNum := uint64(0)
	if headBlock != nil && headBlock.GetBlockIndex() != nil {
		headBlockNum = uint64(headBlock.GetBlockIndex().ExecutionNumber)
	}

	var consolidationRequests []*APIConsolidationRequestInfo
	for _, consolidation := range dbElConsolidations {
		requestInfo := &APIConsolidationRequestInfo{
			SourceAddress:   common.Address(consolidation.SourceAddress()).Hex(),
			SourcePublicKey: fmt.Sprintf("0x%x", consolidation.SourcePubkey()),
			TargetPublicKey: fmt.Sprintf("0x%x", consolidation.TargetPubkey()),
		}

		// Set source validator information
		if sourceIndex := consolidation.SourceIndex(); sourceIndex != nil {
			requestInfo.SourceValidatorIndex = *sourceIndex
			requestInfo.SourceValidatorName = services.GlobalBeaconService.GetValidatorName(*sourceIndex)
			requestInfo.SourceValidatorValid = true
		}

		// Set target validator information
		if targetIndex := consolidation.TargetIndex(); targetIndex != nil {
			requestInfo.TargetValidatorIndex = *targetIndex
			requestInfo.TargetValidatorName = services.GlobalBeaconService.GetValidatorName(*targetIndex)
			requestInfo.TargetValidatorValid = true
		}

		// Set request information if included
		if request := consolidation.Request; request != nil {
			requestInfo.IsIncluded = true
			requestInfo.SlotNumber = request.SlotNumber
			requestInfo.SlotRoot = fmt.Sprintf("0x%x", request.SlotRoot)
			requestInfo.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber)).Unix()
			requestInfo.Status = 1 // included
			requestInfo.Result = request.Result
			requestInfo.ResultMessage = getConsolidationResultMessage(request.Result)

			if consolidation.RequestOrphaned {
				requestInfo.Status = 2 // orphaned
			}
		}

		// Set transaction information
		if transaction := consolidation.Transaction; transaction != nil {
			requestInfo.TransactionHash = fmt.Sprintf("0x%x", transaction.TxHash)
			requestInfo.TransactionDetails = &APIConsolidationRequestTxDetails{
				BlockNumber: transaction.BlockNumber,
				BlockHash:   fmt.Sprintf("0x%x", transaction.BlockRoot),
				BlockTime:   int64(transaction.BlockTime),
				TxOrigin:    common.Address(transaction.TxSender).Hex(),
				TxTarget:    common.Address(transaction.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("0x%x", transaction.TxHash),
			}
			requestInfo.TxStatus = 1 // confirmed

			if consolidation.TransactionOrphaned {
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

		consolidationRequests = append(consolidationRequests, requestInfo)
	}

	response := APIConsolidationRequestsResponse{
		Status: "OK",
		Data: &APIConsolidationRequestsData{
			ConsolidationRequests: consolidationRequests,
			Count:                 uint64(len(consolidationRequests)),
			TotalRequests:         totalRequests,
			TotalPending:          totalPendingTxRows,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode consolidation requests response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

// getConsolidationResultMessage returns a human-readable message for the consolidation result
func getConsolidationResultMessage(result uint8) string {
	switch result {
	case 0:
		return "Success"
	case 1:
		return "Invalid source validator public key"
	case 2:
		return "Invalid target validator public key"
	case 3:
		return "Source validator not found"
	case 4:
		return "Target validator not found"
	case 5:
		return "Source validator already exiting"
	case 6:
		return "Source validator already exited"
	case 7:
		return "Target validator already exiting"
	case 8:
		return "Target validator already exited"
	case 9:
		return "Source and target validators must be different"
	case 10:
		return "Invalid withdrawal credentials"
	case 11:
		return "Insufficient balance"
	case 12:
		return "Consolidation already pending"
	default:
		return fmt.Sprintf("Unknown result: %d", result)
	}
}
