package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIDepositsTransactionsResponse represents the response structure for deposit transactions
type APIDepositsTransactionsResponse struct {
	Status string                       `json:"status"`
	Data   *APIDepositsTransactionsData `json:"data"`
}

// APIDepositsTransactionsData contains the deposit transactions data
type APIDepositsTransactionsData struct {
	Deposits     []*APIDepositTransactionInfo `json:"deposits"`
	Count        uint64                       `json:"count"`
	TotalCount   uint64                       `json:"total_count"`
	CurrentEpoch uint64                       `json:"current_epoch"`
}

// APIDepositTransactionInfo represents information about a single deposit transaction
type APIDepositTransactionInfo struct {
	Index                 uint64 `json:"index"`
	BlockNumber           uint64 `json:"block_number"`
	BlockHash             string `json:"block_hash,omitempty"`
	Time                  int64  `json:"time"`
	PublicKey             string `json:"public_key"`
	ValidatorIndex        int64  `json:"validator_index"`
	ValidatorName         string `json:"validator_name,omitempty"`
	ValidatorStatus       string `json:"validator_status"`
	Amount                uint64 `json:"amount"`
	WithdrawalCredentials string `json:"withdrawal_credentials"`
	TxHash                string `json:"tx_hash"`
	TxOrigin              string `json:"tx_origin"`
	TxTarget              string `json:"tx_target"`
	Orphaned              bool   `json:"orphaned"`
	Valid                 bool   `json:"valid"`
	ValidatorLiveness     uint8  `json:"validator_liveness,omitempty"`
	ValidatorLivenessMax  uint8  `json:"validator_liveness_max,omitempty"`
}

// APIDepositsTransactionsV1 returns a list of deposit transactions
// @Summary Get deposit transactions
// @Description Returns a list of deposit transactions that have been initiated on the execution layer with comprehensive filtering
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
// @Param address query string false "Filter by transaction sender address"
// @Param target_address query string false "Filter by transaction target address"
// @Param with_valid query int false "Filter by signature validity (0=invalid only, 1=valid only, 2=all)"
// @Success 200 {object} APIDepositsTransactionsResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/deposits/transactions [get]
// @ID getDepositsTransactions
func APIDepositsTransactionsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()
	chainState := services.GlobalBeaconService.GetChainState()
	currentEpoch := chainState.CurrentEpoch()

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

	// Build comprehensive filter
	depositFilter := &dbtypes.DepositTxFilter{
		WithOrphaned: 1, // Default to include all transactions
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

	// Address filters
	if addressStr := query.Get("address"); addressStr != "" {
		if address := common.HexToAddress(addressStr); address != (common.Address{}) {
			depositFilter.Address = address[:]
		}
	}

	if targetAddressStr := query.Get("target_address"); targetAddressStr != "" {
		if targetAddress := common.HexToAddress(targetAddressStr); targetAddress != (common.Address{}) {
			depositFilter.TargetAddress = targetAddress[:]
		}
	}

	// Valid signature filter
	if validStr := query.Get("with_valid"); validStr != "" {
		if valid, err := strconv.ParseUint(validStr, 10, 8); err == nil {
			depositFilter.WithValid = uint8(valid)
		}
	}

	// Get deposit transactions
	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()
	depositTxs, totalCountTx, err := db.GetDepositTxsFiltered(offset, uint32(limit), canonicalForkIds, depositFilter)
	if err != nil {
		logrus.WithError(err).Error("failed to get deposit transactions")
		http.Error(w, `{"status": "ERROR: failed to get deposit transactions"}`, http.StatusInternalServerError)
		return
	}
	totalCount := totalCountTx

	var deposits []*APIDepositTransactionInfo
	for _, tx := range depositTxs {
		depositInfo := &APIDepositTransactionInfo{
			Index:                 tx.Index,
			BlockNumber:           tx.BlockNumber,
			Time:                  int64(tx.BlockTime),
			PublicKey:             fmt.Sprintf("0x%x", tx.PublicKey),
			Amount:                tx.Amount,
			WithdrawalCredentials: fmt.Sprintf("0x%x", tx.WithdrawalCredentials),
			TxHash:                fmt.Sprintf("0x%x", tx.TxHash),
			TxOrigin:              common.Address(tx.TxSender).Hex(),
			TxTarget:              common.Address(tx.TxTarget).Hex(),
			Orphaned:              tx.Orphaned,
			Valid:                 tx.ValidSignature == 1 || tx.ValidSignature == 2,
		}

		// Add block hash if available
		if len(tx.BlockRoot) > 0 {
			depositInfo.BlockHash = fmt.Sprintf("0x%x", tx.BlockRoot)
		}

		// Get validator information
		validatorIndex, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(tx.PublicKey))
		if !found {
			depositInfo.ValidatorIndex = -1
			depositInfo.ValidatorStatus = "Deposited"
		} else {
			depositInfo.ValidatorIndex = int64(validatorIndex)
			depositInfo.ValidatorName = services.GlobalBeaconService.GetValidatorName(uint64(validatorIndex))

			// Get validator status and liveness
			validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(validatorIndex), false)
			if validator != nil {
				if strings.HasPrefix(validator.Status.String(), "pending") {
					depositInfo.ValidatorStatus = "Pending"
				} else if validator.Status == v1.ValidatorStateActiveOngoing {
					depositInfo.ValidatorStatus = "Active"
					depositInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
					depositInfo.ValidatorLivenessMax = 3
				} else if validator.Status == v1.ValidatorStateActiveExiting {
					depositInfo.ValidatorStatus = "Exiting"
					depositInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
					depositInfo.ValidatorLivenessMax = 3
				} else if validator.Status == v1.ValidatorStateActiveSlashed {
					depositInfo.ValidatorStatus = "Slashed"
					depositInfo.ValidatorLiveness = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
					depositInfo.ValidatorLivenessMax = 3
				} else if validator.Status == v1.ValidatorStateExitedUnslashed {
					depositInfo.ValidatorStatus = "Exited"
				} else if validator.Status == v1.ValidatorStateExitedSlashed {
					depositInfo.ValidatorStatus = "Slashed"
				} else {
					depositInfo.ValidatorStatus = validator.Status.String()
				}
			}
		}

		deposits = append(deposits, depositInfo)
	}

	response := APIDepositsTransactionsResponse{
		Status: "OK",
		Data: &APIDepositsTransactionsData{
			Deposits:     deposits,
			Count:        uint64(len(deposits)),
			TotalCount:   totalCount,
			CurrentEpoch: uint64(currentEpoch),
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode deposit transactions response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
