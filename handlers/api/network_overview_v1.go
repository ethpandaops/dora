package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// APINetworkOverviewResponse represents the response structure for network overview
type APINetworkOverviewResponse struct {
	Status string                  `json:"status"`
	Data   *APINetworkOverviewData `json:"data"`
}

// APINetworkOverviewData contains comprehensive network state information
type APINetworkOverviewData struct {
	NetworkInfo    *APINetworkInfo       `json:"network_info"`
	CurrentState   *APICurrentState      `json:"current_state"`
	Checkpoints    *APICheckpoints       `json:"checkpoints"`
	ValidatorStats *APIValidatorStats    `json:"validator_stats"`
	QueueStats     *APIQueueStats        `json:"queue_stats,omitempty"`
	Forks          []*APINetworkForkInfo `json:"forks"`
	IsSynced       bool                  `json:"is_synced"`
}

// APINetworkInfo contains basic network information
type APINetworkInfo struct {
	NetworkName           string `json:"network_name"`
	ConfigName            string `json:"config_name"`
	DepositContract       string `json:"deposit_contract"`
	WithdrawalContract    string `json:"withdrawal_contract"`
	ConsolidationContract string `json:"consolidation_contract"`
	GenesisTime           int64  `json:"genesis_time"`
	GenesisRoot           string `json:"genesis_root"`
	GenesisValidatorsRoot string `json:"genesis_validators_root"`
}

// APICurrentState contains current network state
type APICurrentState struct {
	CurrentSlot          uint64  `json:"current_slot"`
	CurrentEpoch         uint64  `json:"current_epoch"`
	CurrentEpochProgress float64 `json:"current_epoch_progress"`
	SlotsPerEpoch        uint64  `json:"slots_per_epoch"`
	SecondsPerSlot       uint64  `json:"seconds_per_slot"`
	SecondsPerEpoch      uint64  `json:"seconds_per_epoch"`
}

// APICheckpoints contains finalization and justification information
type APICheckpoints struct {
	FinalizedEpoch int64  `json:"finalized_epoch"`
	FinalizedRoot  string `json:"finalized_root,omitempty"`
	JustifiedEpoch int64  `json:"justified_epoch"`
	JustifiedRoot  string `json:"justified_root,omitempty"`
}

// APIValidatorStats contains validator statistics
type APIValidatorStats struct {
	TotalBalance         uint64 `json:"total_balance"`
	TotalActiveBalance   uint64 `json:"total_active_balance"`
	ActiveValidatorCount uint64 `json:"active_validator_count"`
	AverageBalance       uint64 `json:"average_balance"`
	TotalEligibleEther   uint64 `json:"total_eligible_ether"`
}

// APIQueueStats contains queue statistics (Electra era)
type APIQueueStats struct {
	EnteringValidatorCount        uint64 `json:"entering_validator_count"`
	ExitingValidatorCount         uint64 `json:"exiting_validator_count"`
	EnteringEtherAmount           uint64 `json:"entering_ether_amount"`
	EtherChurnPerDay              uint64 `json:"ether_churn_per_day"`
	DepositEstimatedTimeToProcess uint64 `json:"deposit_estimated_time"`
	ExitEstimatedTimeToProcess    uint64 `json:"exit_estimated_time"`
}

// APINetworkOverviewV1 returns comprehensive network state overview
// @Summary Get network overview
// @Description Returns comprehensive network state information including network info, current state, checkpoints, validator stats, queue stats, and fork information
// @Tags network
// @Accept json
// @Produce json
// @Success 200 {object} APINetworkOverviewResponse
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/network/overview [get]
// @ID getNetworkOverview
func APINetworkOverviewV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Use caching layer for this endpoint since it has no filters
	var pageData *APINetworkOverviewData
	pageCacheKey := "api_network_overview"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		data, cacheTimeout := buildNetworkOverviewData()
		pageCall.CacheTimeout = cacheTimeout
		return data
	})

	if pageErr != nil {
		logrus.WithError(pageErr).Error("failed to process network overview page")
		http.Error(w, `{"status": "ERROR: failed to process network overview"}`, http.StatusInternalServerError)
		return
	}

	if pageRes != nil {
		if resData, ok := pageRes.(*APINetworkOverviewData); ok {
			pageData = resData
		}
	}

	if pageData == nil {
		http.Error(w, `{"status": "ERROR: failed to build network overview"}`, http.StatusInternalServerError)
		return
	}

	response := APINetworkOverviewResponse{
		Status: "OK",
		Data:   pageData,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode network overview response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

func buildNetworkOverviewData() (*APINetworkOverviewData, time.Duration) {
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	currentEpoch := chainState.CurrentEpoch()
	currentSlot := chainState.CurrentSlot()
	currentSlotIndex := chainState.SlotToSlotIndex(currentSlot) + 1
	networkGenesis := chainState.GetGenesis()

	if specs == nil {
		return nil, time.Minute
	}

	finalizedEpoch, finalizedRoot := chainState.GetFinalizedCheckpoint()
	justifiedEpoch, justifiedRoot := chainState.GetJustifiedCheckpoint()

	// Check sync state
	syncState := dbtypes.IndexerSyncState{}
	db.GetExplorerState("indexer.syncstate", &syncState)
	var isSynced bool
	if finalizedEpoch >= 1 {
		isSynced = syncState.Epoch >= uint64(finalizedEpoch-1)
	} else {
		isSynced = true
	}

	// Network info
	networkName := specs.ConfigName
	if utils.Config.Chain.DisplayName != "" {
		networkName = utils.Config.Chain.DisplayName
	}

	networkInfo := &APINetworkInfo{
		NetworkName:           networkName,
		ConfigName:            specs.ConfigName,
		DepositContract:       common.Address(specs.DepositContractAddress).String(),
		WithdrawalContract:    services.GlobalBeaconService.GetSystemContractAddress(rpc.WithdrawalRequestContract).String(),
		ConsolidationContract: services.GlobalBeaconService.GetSystemContractAddress(rpc.ConsolidationRequestContract).String(),
	}

	if networkGenesis != nil {
		networkInfo.GenesisTime = networkGenesis.GenesisTime.Unix()
		networkInfo.GenesisRoot = fmt.Sprintf("0x%x", networkGenesis.GenesisValidatorsRoot)
		networkInfo.GenesisValidatorsRoot = fmt.Sprintf("0x%x", networkGenesis.GenesisValidatorsRoot)
	}

	// Current state
	currentState := &APICurrentState{
		CurrentSlot:          uint64(currentSlot),
		CurrentEpoch:         uint64(currentEpoch),
		CurrentEpochProgress: float64(100) * float64(currentSlotIndex) / float64(specs.SlotsPerEpoch),
		SlotsPerEpoch:        specs.SlotsPerEpoch,
		SecondsPerSlot:       uint64(specs.SecondsPerSlot.Seconds()),
		SecondsPerEpoch:      uint64(specs.SecondsPerSlot.Seconds() * float64(specs.SlotsPerEpoch)),
	}

	// Checkpoints
	checkpoints := &APICheckpoints{
		FinalizedEpoch: int64(finalizedEpoch),
		JustifiedEpoch: int64(justifiedEpoch),
	}
	if finalizedRoot != consensus.NullRoot {
		checkpoints.FinalizedRoot = fmt.Sprintf("0x%x", finalizedRoot)
	}
	if justifiedRoot != consensus.NullRoot {
		checkpoints.JustifiedRoot = fmt.Sprintf("0x%x", justifiedRoot)
	}

	// Validator stats
	validatorStats := &APIValidatorStats{}
	recentEpochStatsValues, _ := services.GlobalBeaconService.GetRecentEpochStats(nil)
	if recentEpochStatsValues != nil {
		validatorStats.ActiveValidatorCount = recentEpochStatsValues.ActiveValidators
		validatorStats.TotalActiveBalance = uint64(recentEpochStatsValues.ActiveBalance)
		validatorStats.TotalEligibleEther = uint64(recentEpochStatsValues.EffectiveBalance)
		if recentEpochStatsValues.ActiveValidators > 0 {
			validatorStats.AverageBalance = uint64(recentEpochStatsValues.ActiveBalance) / recentEpochStatsValues.ActiveValidators
		}
		validatorStats.TotalBalance = uint64(recentEpochStatsValues.TotalBalance)
	}

	// Queue stats (only for Electra era)
	var queueStats *APIQueueStats
	if specs.ElectraForkEpoch != nil && *specs.ElectraForkEpoch <= uint64(currentEpoch) {
		activationQueueLength, exitQueueLength := services.GlobalBeaconService.GetBeaconIndexer().GetActivationExitQueueLengths(currentEpoch, nil)

		queueStats = &APIQueueStats{
			EnteringValidatorCount: activationQueueLength,
			ExitingValidatorCount:  exitQueueLength,
		}

		// Electra deposit queue
		depositQueue := services.GlobalBeaconService.GetBeaconIndexer().GetLatestDepositQueue(nil)
		if depositQueue != nil {
			depositAmount := phase0.Gwei(0)
			validatorCount := uint64(0)

			newValidators := map[phase0.BLSPubKey]interface{}{}
			for _, deposit := range depositQueue {
				depositAmount += deposit.Amount
				_, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(deposit.Pubkey)
				if !found {
					_, isNew := newValidators[deposit.Pubkey]
					if !isNew {
						newValidators[deposit.Pubkey] = nil
						validatorCount++
					}
				}
			}

			queueStats.EnteringValidatorCount += validatorCount
			queueStats.EnteringEtherAmount = uint64(depositAmount)

			if validatorStats.TotalEligibleEther > 0 {
				etherChurnPerEpoch := chainState.GetActivationExitChurnLimit(validatorStats.TotalEligibleEther)
				queueStats.EtherChurnPerDay = etherChurnPerEpoch * 225 // ~225 epochs per day

				if queueStats.EtherChurnPerDay > 0 {
					// Calculate deposit time in seconds
					depositDays := float64(depositAmount) / float64(queueStats.EtherChurnPerDay)
					queueStats.DepositEstimatedTimeToProcess = uint64(depositDays * 24 * 60 * 60)

					// Calculate exit time in seconds
					// Assuming each validator has the average balance
					exitEtherAmount := queueStats.ExitingValidatorCount * validatorStats.AverageBalance
					exitDays := float64(exitEtherAmount) / float64(queueStats.EtherChurnPerDay)
					queueStats.ExitEstimatedTimeToProcess = uint64(exitDays * 24 * 60 * 60)
				}
			}
		}
	}

	// Fork information (reuse from network forks API)
	forks := buildNetworkForks(chainState)

	data := &APINetworkOverviewData{
		NetworkInfo:    networkInfo,
		CurrentState:   currentState,
		Checkpoints:    checkpoints,
		ValidatorStats: validatorStats,
		QueueStats:     queueStats,
		Forks:          forks,
		IsSynced:       isSynced,
	}

	// Cache for SecondsPerSlot duration
	cacheTimeout := time.Duration(specs.SecondsPerSlot.Seconds()) * time.Second
	return data, cacheTimeout
}
