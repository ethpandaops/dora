package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIDepositsIncludedResponse represents the response structure for deposits included in blocks
type APIDepositsIncludedResponse struct {
	Status string                   `json:"status"`
	Data   *APIDepositsIncludedData `json:"data"`
}

// APIDepositsIncludedData contains the deposits included data
type APIDepositsIncludedData struct {
	Deposits     []*APIDepositIncludedInfo `json:"deposits"`
	Count        uint64                    `json:"count"`
	TotalCount   uint64                    `json:"total_count"`
	CurrentEpoch uint64                    `json:"current_epoch"`
}

// APIDepositIncludedInfo represents information about a single deposit included in blocks
type APIDepositIncludedInfo struct {
	Index                 uint64 `json:"index,omitempty"`
	SlotNumber            uint64 `json:"slot_number,omitempty"`
	SlotRoot              string `json:"slot_root,omitempty"`
	Time                  int64  `json:"time"`
	BlockNumber           uint64 `json:"block_number,omitempty"`
	PublicKey             string `json:"public_key"`
	ValidatorIndex        int64  `json:"validator_index"`
	ValidatorName         string `json:"validator_name,omitempty"`
	Amount                uint64 `json:"amount"`
	WithdrawalCredentials string `json:"withdrawal_credentials"`
	TxHash                string `json:"tx_hash,omitempty"`
	TxOrigin              string `json:"tx_origin,omitempty"`
	TxTarget              string `json:"tx_target,omitempty"`
	Orphaned              bool   `json:"orphaned"`
	Valid                 bool   `json:"valid,omitempty"`
}

// APIDepositsIncludedV1 returns a list of deposits that have been included in blocks
// @Summary Get deposits included in blocks
// @Description Returns a list of deposits that have been successfully included in beacon chain blocks with comprehensive filtering
// @Tags deposits
// @Accept json
// @Produce json
// @Param limit query int false "Number of deposits to return (max 100)"
// @Param offset query int false "Offset for pagination"
// @Param min_index query int false "Minimum deposit index"
// @Param max_index query int false "Maximum deposit index"
// @Param public_key query string false "Filter by validator public key (with or without 0x prefix)"
// @Param validator_name query string false "Filter by validator name"
// @Param min_amount query int false "Minimum deposit amount in gwei"
// @Param max_amount query int false "Maximum deposit amount in gwei"
// @Param with_orphaned query int false "Include orphaned deposits (0=canonical only, 1=include all, 2=orphaned only)"
// @Param address query string false "Filter by depositor address"
// @Param with_valid query int false "Filter by signature validity (0=invalid only, 1=valid only, 2=all)"
// @Success 200 {object} APIDepositsIncludedResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/deposits/included [get]
// @ID getDepositsIncluded
func APIDepositsIncludedV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()
	chainState := services.GlobalBeaconService.GetChainState()
	currentEpoch := chainState.CurrentEpoch()

	// Parse limit parameter
	limit := uint32(50)
	limitStr := query.Get("limit")
	if limitStr != "" {
		parsedLimit, err := strconv.ParseUint(limitStr, 10, 32)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid limit parameter"}`, http.StatusBadRequest)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		if parsedLimit > 0 {
			limit = uint32(parsedLimit)
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
	depositFilter := &dbtypes.DepositTxFilter{
		WithOrphaned: 1, // Default to include all (canonical + orphaned)
	}

	// Index range filters
	if minIndexStr := query.Get("min_index"); minIndexStr != "" {
		if minIndex, err := strconv.ParseUint(minIndexStr, 10, 64); err == nil {
			depositFilter.MinIndex = minIndex
		}
	}

	if maxIndexStr := query.Get("max_index"); maxIndexStr != "" {
		if maxIndex, err := strconv.ParseUint(maxIndexStr, 10, 64); err == nil {
			depositFilter.MaxIndex = maxIndex
		}
	}

	// Public key filter
	if pubkeyStr := query.Get("public_key"); pubkeyStr != "" {
		pubkeyStr = strings.TrimPrefix(pubkeyStr, "0x")
		if pubkeyBytes := common.FromHex(pubkeyStr); len(pubkeyBytes) == 48 {
			depositFilter.PublicKey = pubkeyBytes
		}
	}

	// Validator name filter
	if validatorName := query.Get("validator_name"); validatorName != "" {
		depositFilter.ValidatorName = validatorName
	}

	// Amount range filters
	if minAmountStr := query.Get("min_amount"); minAmountStr != "" {
		if minAmount, err := strconv.ParseUint(minAmountStr, 10, 64); err == nil {
			depositFilter.MinAmount = minAmount
		}
	}

	if maxAmountStr := query.Get("max_amount"); maxAmountStr != "" {
		if maxAmount, err := strconv.ParseUint(maxAmountStr, 10, 64); err == nil {
			depositFilter.MaxAmount = maxAmount
		}
	}

	// Orphaned status filter
	if orphanedStr := query.Get("with_orphaned"); orphanedStr != "" {
		if orphaned, err := strconv.ParseUint(orphanedStr, 10, 8); err == nil {
			depositFilter.WithOrphaned = uint8(orphaned)
		}
	}

	// Address filter (depositor address)
	if addressStr := query.Get("address"); addressStr != "" {
		if address := common.HexToAddress(addressStr); address != (common.Address{}) {
			depositFilter.Address = address[:]
		}
	}

	// Valid signature filter
	if validStr := query.Get("with_valid"); validStr != "" {
		if valid, err := strconv.ParseUint(validStr, 10, 8); err == nil {
			depositFilter.WithValid = uint8(valid)
		}
	}

	// Get deposits included in blocks using the proper service method
	combinedFilter := &services.CombinedDepositRequestFilter{
		Filter: depositFilter,
	}

	dbDeposits, totalCount := services.GlobalBeaconService.GetDepositRequestsByFilter(combinedFilter, offset, limit)

	var deposits []*APIDepositIncludedInfo
	for _, deposit := range dbDeposits {
		depositInfo := &APIDepositIncludedInfo{
			PublicKey:             fmt.Sprintf("0x%x", deposit.PublicKey()),
			WithdrawalCredentials: fmt.Sprintf("0x%x", deposit.WithdrawalCredentials()),
			Amount:                deposit.Amount(),
			Time:                  chainState.SlotToTime(phase0.Slot(deposit.Request.SlotNumber)).Unix(),
			SlotNumber:            deposit.Request.SlotNumber,
			SlotRoot:              fmt.Sprintf("0x%x", deposit.Request.SlotRoot),
			Orphaned:              deposit.RequestOrphaned,
		}

		if deposit.SourceAddress() != nil {
			depositInfo.TxOrigin = common.Address(deposit.SourceAddress()).Hex()
		}

		if deposit.Request.Index != nil {
			depositInfo.Index = *deposit.Request.Index
		}

		if deposit.Transaction != nil {
			depositInfo.BlockNumber = deposit.Transaction.BlockNumber
			depositInfo.TxHash = fmt.Sprintf("0x%x", deposit.Transaction.TxHash)
			depositInfo.TxOrigin = common.Address(deposit.Transaction.TxSender).Hex()
			depositInfo.TxTarget = common.Address(deposit.Transaction.TxTarget).Hex()
			depositInfo.Valid = deposit.Transaction.ValidSignature != 0
		}

		validatorIndex, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(deposit.PublicKey()))
		if found {
			depositInfo.ValidatorIndex = int64(validatorIndex)
			depositInfo.ValidatorName = services.GlobalBeaconService.GetValidatorName(uint64(validatorIndex))
		} else {
			depositInfo.ValidatorIndex = -1
		}

		deposits = append(deposits, depositInfo)
	}

	response := APIDepositsIncludedResponse{
		Status: "OK",
		Data: &APIDepositsIncludedData{
			Deposits:     deposits,
			Count:        uint64(len(deposits)),
			TotalCount:   totalCount,
			CurrentEpoch: uint64(currentEpoch),
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode deposits included response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
