package api

import (
	"bytes"
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

// APIDepositsQueueResponse represents the response structure for deposit queue
type APIDepositsQueueResponse struct {
	Status string                `json:"status"`
	Data   *APIDepositsQueueData `json:"data"`
}

// APIDepositsQueueData contains the deposit queue data
type APIDepositsQueueData struct {
	Deposits             []*APIDepositQueueInfo `json:"deposits"`
	Count                uint64                 `json:"count"`
	TotalCount           uint64                 `json:"total_count"`
	CurrentEpoch         uint64                 `json:"current_epoch"`
	IsElectra            bool                   `json:"is_electra"`
	TotalNewValidators   uint64                 `json:"total_new_validators,omitempty"`
	TotalAmount          uint64                 `json:"total_amount,omitempty"`
	EstimatedProcessTime *int64                 `json:"estimated_process_time,omitempty"`
}

// APIDepositQueueInfo represents information about a single queued deposit
type APIDepositQueueInfo struct {
	Index                 uint64 `json:"index,omitempty"`
	QueuePosition         uint64 `json:"queue_position"`
	EstimatedTime         int64  `json:"estimated_time"`
	PublicKey             string `json:"public_key"`
	ValidatorIndex        int64  `json:"validator_index"`
	ValidatorName         string `json:"validator_name,omitempty"`
	ValidatorStatus       string `json:"validator_status"`
	Amount                uint64 `json:"amount"`
	WithdrawalCredentials string `json:"withdrawal_credentials"`
	TxHash                string `json:"tx_hash,omitempty"`
	TxOrigin              string `json:"tx_origin,omitempty"`
	TxTarget              string `json:"tx_target,omitempty"`
	BlockNumber           uint64 `json:"block_number,omitempty"`
	BlockHash             string `json:"block_hash,omitempty"`
	Time                  int64  `json:"time,omitempty"`
	ValidatorLiveness     uint8  `json:"validator_liveness,omitempty"`
	ValidatorLivenessMax  uint8  `json:"validator_liveness_max,omitempty"`
}

// APIDepositsQueueV1 returns a list of queued deposits
// @Summary Get deposit queue
// @Description Returns a list of deposits in the queue waiting to be processed (Electra only) with comprehensive filtering
// @Tags deposits
// @Accept json
// @Produce json
// @Param limit query int false "Number of deposits to return (max 100)"
// @Param offset query int false "Offset for pagination"
// @Param min_index query int false "Minimum deposit index"
// @Param max_index query int false "Maximum deposit index"
// @Param public_key query string false "Filter by validator public key (with or without 0x prefix)"
// @Param min_amount query int false "Minimum deposit amount in gwei"
// @Param max_amount query int false "Maximum deposit amount in gwei"
// @Success 200 {object} APIDepositsQueueResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/deposits/queue [get]
// @ID getDepositsQueue
func APIDepositsQueueV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	currentEpoch := chainState.CurrentEpoch()
	isElectra := specs.ElectraForkEpoch != nil && *specs.ElectraForkEpoch <= uint64(currentEpoch)

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
	var minIndex, maxIndex uint64
	var minAmount, maxAmount uint64
	var pubkeyBytes []byte

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

	if pubkeyStr := query.Get("public_key"); pubkeyStr != "" {
		pubkeyStr = strings.TrimPrefix(pubkeyStr, "0x")
		if bytes := common.FromHex(pubkeyStr); len(bytes) == 48 {
			pubkeyBytes = bytes
		}
	}

	if minAmountStr := query.Get("min_amount"); minAmountStr != "" {
		if parsed, err := strconv.ParseUint(minAmountStr, 10, 64); err == nil {
			minAmount = parsed
		}
	}

	if maxAmountStr := query.Get("max_amount"); maxAmountStr != "" {
		if parsed, err := strconv.ParseUint(maxAmountStr, 10, 64); err == nil {
			maxAmount = parsed
		}
	}

	var deposits []*APIDepositQueueInfo
	var totalCount uint64
	var totalNewValidators, totalAmount uint64
	var estimatedProcessTime *int64

	if isElectra {
		// Get queued deposits for Electra
		headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
		queuedDeposits := services.GlobalBeaconService.GetIndexedDepositQueue(r.Context(), headBlock)

		if queuedDeposits != nil && len(queuedDeposits.Queue) > 0 {
			totalNewValidators = queuedDeposits.TotalNew
			totalAmount = uint64(queuedDeposits.TotalGwei)

			// Overall estimated process time for the queue
			if queuedDeposits.QueueEstimation > 0 {
				estimateTime := chainState.EpochToTime(queuedDeposits.QueueEstimation).Unix()
				estimatedProcessTime = &estimateTime
			}

			// Apply filters to get filtered queue entries
			var filteredEntries []struct {
				entry *services.IndexedDepositQueueEntry
				index int
			}

			for i, queueEntry := range queuedDeposits.Queue {
				// Apply filters
				if minIndex > 0 && queueEntry.DepositIndex != nil && *queueEntry.DepositIndex < minIndex {
					continue
				}
				if maxIndex > 0 && queueEntry.DepositIndex != nil && *queueEntry.DepositIndex > maxIndex {
					continue
				}
				if minAmount > 0 && uint64(queueEntry.PendingDeposit.Amount) < minAmount {
					continue
				}
				if maxAmount > 0 && uint64(queueEntry.PendingDeposit.Amount) > maxAmount {
					continue
				}
				if pubkeyBytes != nil && !bytes.Equal(queueEntry.PendingDeposit.Pubkey[:], pubkeyBytes) {
					continue
				}

				filteredEntries = append(filteredEntries, struct {
					entry *services.IndexedDepositQueueEntry
					index int
				}{queueEntry, i})
			}

			totalCount = uint64(len(filteredEntries))

			// Apply pagination to filtered results
			endIdx := int(offset + limit)
			if endIdx > len(filteredEntries) {
				endIdx = len(filteredEntries)
			}

			if int(offset) < len(filteredEntries) {
				selectedEntries := filteredEntries[offset:endIdx]

				// Get deposit indexes for transaction details
				depositIndexes := make([]uint64, 0)
				for _, item := range selectedEntries {
					if item.entry.DepositIndex != nil {
						depositIndexes = append(depositIndexes, *item.entry.DepositIndex)
					}
				}

				// Get transaction details for these deposits
				txDetailsMap := map[uint64]*dbtypes.DepositTx{}
				for _, txDetail := range db.GetDepositTxsByIndexes(r.Context(), depositIndexes) {
					txDetailsMap[txDetail.Index] = txDetail
				}

				for _, item := range selectedEntries {
					queueEntry := item.entry
					estimatedTime := chainState.EpochToTime(queueEntry.EpochEstimate).Unix()

					depositInfo := &APIDepositQueueInfo{
						QueuePosition:         queueEntry.QueuePos,
						EstimatedTime:         estimatedTime,
						PublicKey:             fmt.Sprintf("0x%x", queueEntry.PendingDeposit.Pubkey[:]),
						WithdrawalCredentials: fmt.Sprintf("0x%x", queueEntry.PendingDeposit.WithdrawalCredentials[:]),
						Amount:                uint64(queueEntry.PendingDeposit.Amount),
					}

					// Set transaction details if available
					if queueEntry.DepositIndex != nil {
						depositInfo.Index = *queueEntry.DepositIndex

						if tx, txFound := txDetailsMap[depositInfo.Index]; txFound {
							depositInfo.TxHash = fmt.Sprintf("0x%x", tx.TxHash)
							depositInfo.TxOrigin = common.Address(tx.TxSender).Hex()
							depositInfo.TxTarget = common.Address(tx.TxTarget).Hex()
							depositInfo.BlockNumber = tx.BlockNumber
							depositInfo.Time = int64(tx.BlockTime)

							if len(tx.BlockRoot) > 0 {
								depositInfo.BlockHash = fmt.Sprintf("0x%x", tx.BlockRoot)
							}
						}
					}

					// Get validator information
					validatorIndex, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(queueEntry.PendingDeposit.Pubkey[:]))
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
			}
		}
	}

	response := APIDepositsQueueResponse{
		Status: "OK",
		Data: &APIDepositsQueueData{
			Deposits:             deposits,
			Count:                uint64(len(deposits)),
			TotalCount:           totalCount,
			CurrentEpoch:         uint64(currentEpoch),
			IsElectra:            isElectra,
			TotalNewValidators:   totalNewValidators,
			TotalAmount:          totalAmount,
			EstimatedProcessTime: estimatedProcessTime,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode deposit queue response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
