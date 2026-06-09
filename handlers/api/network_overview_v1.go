package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

const (
	networkOverviewFinalizingThreshold   = uint64(2)
	networkOverviewParticipationQuorum   = 67.0
	networkOverviewParticipationSource   = "attestation_index"
	networkOverviewRawAggregateSource    = "epoch_vote_index"
	networkOverviewParticipationWarnText = "attestation index incomplete; participation omitted"
)

// APINetworkOverviewResponse represents the response structure for network overview.
type APINetworkOverviewResponse struct {
	Status string                  `json:"status"`
	Data   *APINetworkOverviewData `json:"data,omitempty"`
}

// APINetworkOverviewData contains one coherent network health snapshot.
type APINetworkOverviewData struct {
	NetworkInfo           *APINetworkInfo            `json:"network_info"`
	CurrentState          *APICurrentState           `json:"current_state"`
	Checkpoints           *APICheckpoints            `json:"checkpoints"`
	ValidatorStats        *APIValidatorStats         `json:"validator_stats"`
	QueueStats            *APIQueueStats             `json:"queue_stats,omitempty"`
	Forks                 []*APINetworkForkInfo      `json:"forks"`
	IsSynced              bool                       `json:"is_synced"`
	CurrentSlot           uint64                     `json:"current_slot"`
	CurrentEpoch          uint64                     `json:"current_epoch"`
	FinalizedEpoch        uint64                     `json:"finalized_epoch"`
	EpochsSinceFinality   uint64                     `json:"epochs_since_finality"`
	Finalizing            bool                       `json:"finalizing"`
	ActiveValidatorCount  uint64                     `json:"active_validator_count"`
	TotalValidatorCount   uint64                     `json:"total_validator_count"`
	PendingValidatorCount uint64                     `json:"pending_validator_count"`
	ExitedValidatorCount  uint64                     `json:"exited_validator_count"`
	DataQualityWarnings   []string                   `json:"data_quality_warnings"`
	Participation         *APINetworkParticipation   `json:"participation,omitempty"`
	RawAggregates         *APINetworkRawAggregates   `json:"raw_aggregates,omitempty"`
	Metadata              APINetworkOverviewMetadata `json:"metadata"`
}

// APINetworkInfo contains basic network information.
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

// APICurrentState contains current network state.
type APICurrentState struct {
	CurrentSlot          uint64  `json:"current_slot"`
	CurrentEpoch         uint64  `json:"current_epoch"`
	CurrentEpochProgress float64 `json:"current_epoch_progress"`
	SlotsPerEpoch        uint64  `json:"slots_per_epoch"`
	SlotDurationMs       uint64  `json:"slot_duration_ms"`
	EpochDurationMs      uint64  `json:"epoch_duration_ms"`
}

// APICheckpoints contains finalization and justification information.
type APICheckpoints struct {
	FinalizedEpoch int64  `json:"finalized_epoch"`
	FinalizedRoot  string `json:"finalized_root,omitempty"`
	JustifiedEpoch int64  `json:"justified_epoch"`
	JustifiedRoot  string `json:"justified_root,omitempty"`
}

// APIValidatorStats contains validator statistics.
type APIValidatorStats struct {
	TotalBalance         uint64 `json:"total_balance"`
	TotalActiveBalance   uint64 `json:"total_active_balance"`
	ActiveValidatorCount uint64 `json:"active_validator_count"`
	AverageBalance       uint64 `json:"average_balance"`
	TotalEligibleEther   uint64 `json:"total_eligible_ether"`
}

// APIQueueStats contains queue statistics (Electra era).
type APIQueueStats struct {
	EnteringValidatorCount        uint64 `json:"entering_validator_count"`
	ExitingValidatorCount         uint64 `json:"exiting_validator_count"`
	EnteringEtherAmount           uint64 `json:"entering_ether_amount"`
	EtherChurnPerDay              uint64 `json:"ether_churn_per_day"`
	DepositEstimatedTimeToProcess uint64 `json:"deposit_estimated_time"`
	ExitEstimatedTimeToProcess    uint64 `json:"exit_estimated_time"`
}

type APINetworkOverviewMetadata struct {
	SlotsPerEpoch uint64 `json:"slots_per_epoch"`
}

type APINetworkParticipation struct {
	Rate          *float64 `json:"rate"`
	Source        string   `json:"source"`
	Complete      bool     `json:"complete"`
	IndexedSlots  uint64   `json:"indexed_slots"`
	ExpectedSlots uint64   `json:"expected_slots"`
	Warning       string   `json:"warning,omitempty"`
}

type APINetworkRawAggregates struct {
	GlobalParticipationRate float64 `json:"globalparticipationrate"`
	VoteParticipation       float64 `json:"vote_participation"`
	AttestationsIndexed     uint64  `json:"attestations_indexed"`
	IndexedSlots            uint64  `json:"indexed_slots"`
	ExpectedSlots           uint64  `json:"expected_slots"`
	Source                  string  `json:"source"`
	Complete                bool    `json:"complete"`
}

type networkOverviewInput struct {
	CurrentSlot       uint64
	SlotsPerEpoch     uint64
	FinalizedEpoch    uint64
	ValidatorStatuses map[v1.ValidatorState]uint64
	RawAggregate      *networkOverviewRawAggregate
}

type networkOverviewRawAggregate struct {
	GlobalParticipationRate float64
	VoteParticipation       float64
	AttestationsIndexed     uint64
	IndexedSlots            uint64
	ExpectedSlots           uint64
	Complete                bool
}

// APINetworkOverviewV1 returns a safe network health overview.
// @Summary Get network overview
// @Description Returns the existing network overview data plus a safe health snapshot with current head slot, finality, validator counts, metadata, and provenance-aware aggregate data when available.
// @Tags network
// @Accept json
// @Produce json
// @Success 200 {object} APINetworkOverviewResponse
// @Failure 500 {object} ApiResponse "Server Error"
// @Router /v1/network/overview [get]
// @ID getNetworkOverview
func APINetworkOverviewV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var pageData *APINetworkOverviewData
	if services.GlobalFrontendCache == nil {
		pageData, _ = buildNetworkOverviewData(r.Context())
	} else {
		pageCacheKey := "api_network_overview"
		pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
			data, cacheTimeout := buildNetworkOverviewData(pageCall.CallCtx)
			pageCall.CacheTimeout = cacheTimeout
			return data
		})

		if pageErr != nil {
			logrus.WithError(pageErr).Error("failed to process network overview page")
			sendServerErrorResponse(w, r.URL.String(), "failed to process network overview")
			return
		}

		if pageRes != nil {
			if resData, ok := pageRes.(*APINetworkOverviewData); ok {
				pageData = resData
			}
		}
	}

	if pageData == nil {
		sendServerErrorResponse(w, r.URL.String(), "failed to build network overview")
		return
	}

	response := APINetworkOverviewResponse{
		Status: "OK",
		Data:   pageData,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode network overview response")
		sendServerErrorResponse(w, r.URL.String(), "failed to encode response")
		return
	}
}

func buildNetworkOverviewData(ctx context.Context) (*APINetworkOverviewData, time.Duration) {
	if services.GlobalBeaconService == nil {
		return nil, time.Minute
	}

	chainState := services.GlobalBeaconService.GetChainState()
	if chainState == nil {
		return nil, time.Minute
	}

	specs := chainState.GetSpecs()
	if specs == nil || specs.SlotsPerEpoch == 0 {
		return nil, time.Minute
	}

	currentEpoch := chainState.CurrentEpoch()
	currentSlot := chainState.CurrentSlot()
	currentSlotIndex := chainState.SlotToSlotIndex(currentSlot) + 1

	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	if beaconIndexer == nil {
		return nil, time.Duration(specs.SlotDurationMs) * time.Millisecond
	}

	canonicalHead := beaconIndexer.GetCanonicalHead(nil)
	if canonicalHead == nil {
		return nil, time.Duration(specs.SlotDurationMs) * time.Millisecond
	}

	headEpoch := chainState.EpochOfSlot(canonicalHead.Slot)
	finalizedEpoch, finalizedRoot := chainState.GetFinalizedCheckpoint()
	justifiedEpoch, justifiedRoot := chainState.GetJustifiedCheckpoint()
	rawAggregate := getNetworkOverviewRawAggregate(ctx, uint64(headEpoch), specs.SlotsPerEpoch)

	data := buildSafeNetworkOverview(networkOverviewInput{
		CurrentSlot:       uint64(canonicalHead.Slot),
		SlotsPerEpoch:     specs.SlotsPerEpoch,
		FinalizedEpoch:    uint64(finalizedEpoch),
		ValidatorStatuses: beaconIndexer.GetValidatorStatusMap(headEpoch, canonicalHead.Root),
		RawAggregate:      rawAggregate,
	})

	syncState := dbtypes.IndexerSyncState{}
	db.GetExplorerState(ctx, "indexer.syncstate", &syncState)
	if finalizedEpoch >= 1 {
		data.IsSynced = syncState.Epoch >= uint64(finalizedEpoch-1)
	} else {
		data.IsSynced = true
	}

	networkName := specs.ConfigName
	if utils.Config.Chain.DisplayName != "" {
		networkName = utils.Config.Chain.DisplayName
	}

	data.NetworkInfo = &APINetworkInfo{
		NetworkName:           networkName,
		ConfigName:            specs.ConfigName,
		DepositContract:       common.Address(specs.DepositContractAddress).String(),
		WithdrawalContract:    services.GlobalBeaconService.GetSystemContractAddress(rpc.WithdrawalRequestContract).String(),
		ConsolidationContract: services.GlobalBeaconService.GetSystemContractAddress(rpc.ConsolidationRequestContract).String(),
	}

	if networkGenesis := chainState.GetGenesis(); networkGenesis != nil {
		data.NetworkInfo.GenesisTime = networkGenesis.GenesisTime.Unix()
		data.NetworkInfo.GenesisRoot = fmt.Sprintf("0x%x", networkGenesis.GenesisValidatorsRoot)
		data.NetworkInfo.GenesisValidatorsRoot = fmt.Sprintf("0x%x", networkGenesis.GenesisValidatorsRoot)
	}

	data.CurrentState = &APICurrentState{
		CurrentSlot:          uint64(currentSlot),
		CurrentEpoch:         uint64(currentEpoch),
		CurrentEpochProgress: float64(100) * float64(currentSlotIndex) / float64(specs.SlotsPerEpoch),
		SlotsPerEpoch:        specs.SlotsPerEpoch,
		SlotDurationMs:       specs.SlotDurationMs,
		EpochDurationMs:      specs.SlotDurationMs * specs.SlotsPerEpoch,
	}

	data.Checkpoints = &APICheckpoints{
		FinalizedEpoch: int64(finalizedEpoch),
		JustifiedEpoch: int64(justifiedEpoch),
	}
	if finalizedRoot != consensus.NullRoot {
		data.Checkpoints.FinalizedRoot = fmt.Sprintf("0x%x", finalizedRoot)
	}
	if justifiedRoot != consensus.NullRoot {
		data.Checkpoints.JustifiedRoot = fmt.Sprintf("0x%x", justifiedRoot)
	}

	data.ValidatorStats = &APIValidatorStats{}
	recentEpochStatsValues, _ := services.GlobalBeaconService.GetRecentEpochStats(nil)
	if recentEpochStatsValues != nil {
		data.ValidatorStats.ActiveValidatorCount = recentEpochStatsValues.ActiveValidators
		data.ValidatorStats.TotalActiveBalance = uint64(recentEpochStatsValues.ActiveBalance)
		data.ValidatorStats.TotalEligibleEther = uint64(recentEpochStatsValues.EffectiveBalance)
		if recentEpochStatsValues.ActiveValidators > 0 {
			data.ValidatorStats.AverageBalance = uint64(recentEpochStatsValues.ActiveBalance) / recentEpochStatsValues.ActiveValidators
		}
		data.ValidatorStats.TotalBalance = uint64(recentEpochStatsValues.TotalBalance)
	}

	if specs.ElectraForkEpoch != nil && *specs.ElectraForkEpoch <= uint64(currentEpoch) {
		activationQueueLength, exitQueueLength := beaconIndexer.GetActivationExitQueueLengths(currentEpoch, nil)

		data.QueueStats = &APIQueueStats{
			EnteringValidatorCount: activationQueueLength,
			ExitingValidatorCount:  exitQueueLength,
		}

		depositQueue := beaconIndexer.GetLatestDepositQueue(nil)
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

			data.QueueStats.EnteringValidatorCount += validatorCount
			data.QueueStats.EnteringEtherAmount = uint64(depositAmount)

			if data.ValidatorStats.TotalEligibleEther > 0 {
				etherChurnPerEpoch := chainState.GetActivationExitChurnLimit(data.ValidatorStats.TotalEligibleEther)
				data.QueueStats.EtherChurnPerDay = etherChurnPerEpoch * 225 // ~225 epochs per day

				if data.QueueStats.EtherChurnPerDay > 0 {
					depositDays := float64(depositAmount) / float64(data.QueueStats.EtherChurnPerDay)
					data.QueueStats.DepositEstimatedTimeToProcess = uint64(depositDays * 24 * 60 * 60)

					exitEtherAmount := data.QueueStats.ExitingValidatorCount * data.ValidatorStats.AverageBalance
					exitDays := float64(exitEtherAmount) / float64(data.QueueStats.EtherChurnPerDay)
					data.QueueStats.ExitEstimatedTimeToProcess = uint64(exitDays * 24 * 60 * 60)
				}
			}
		}
	}

	data.Forks = buildNetworkForks(chainState)

	cacheTimeout := time.Duration(specs.SlotDurationMs) * time.Millisecond
	return data, cacheTimeout
}

func buildSafeNetworkOverview(input networkOverviewInput) *APINetworkOverviewData {
	currentEpoch := uint64(0)
	if input.SlotsPerEpoch > 0 {
		currentEpoch = input.CurrentSlot / input.SlotsPerEpoch
	}

	warnings := []string{}
	epochsSinceFinality := uint64(0)
	if currentEpoch >= input.FinalizedEpoch {
		epochsSinceFinality = currentEpoch - input.FinalizedEpoch
	} else {
		warnings = append(warnings, fmt.Sprintf("finalized_epoch %d is ahead of current_epoch %d", input.FinalizedEpoch, currentEpoch))
	}

	activeValidatorCount, pendingValidatorCount, exitedValidatorCount, totalValidatorCount := summarizeValidatorStatuses(input.ValidatorStatuses)
	data := &APINetworkOverviewData{
		CurrentSlot:           input.CurrentSlot,
		CurrentEpoch:          currentEpoch,
		FinalizedEpoch:        input.FinalizedEpoch,
		EpochsSinceFinality:   epochsSinceFinality,
		Finalizing:            epochsSinceFinality <= networkOverviewFinalizingThreshold,
		ActiveValidatorCount:  activeValidatorCount,
		TotalValidatorCount:   totalValidatorCount,
		PendingValidatorCount: pendingValidatorCount,
		ExitedValidatorCount:  exitedValidatorCount,
		DataQualityWarnings:   warnings,
		Metadata: APINetworkOverviewMetadata{
			SlotsPerEpoch: input.SlotsPerEpoch,
		},
	}

	if input.RawAggregate != nil {
		data.RawAggregates = &APINetworkRawAggregates{
			GlobalParticipationRate: input.RawAggregate.GlobalParticipationRate,
			VoteParticipation:       input.RawAggregate.VoteParticipation,
			AttestationsIndexed:     input.RawAggregate.AttestationsIndexed,
			IndexedSlots:            input.RawAggregate.IndexedSlots,
			ExpectedSlots:           input.RawAggregate.ExpectedSlots,
			Source:                  networkOverviewRawAggregateSource,
			Complete:                input.RawAggregate.Complete,
		}

		var participationWarning string
		if data.Finalizing && input.RawAggregate.GlobalParticipationRate < networkOverviewParticipationQuorum {
			participationWarning = fmt.Sprintf(
				"vote participation aggregate reports %.1f%% while finalized_epoch is only %d epochs behind current_epoch; aggregate is likely incomplete",
				input.RawAggregate.GlobalParticipationRate,
				data.EpochsSinceFinality,
			)
			data.DataQualityWarnings = append(data.DataQualityWarnings, participationWarning)
		}
		data.Participation = buildNetworkOverviewParticipation(input.RawAggregate, participationWarning)
	}

	return data
}

func summarizeValidatorStatuses(statuses map[v1.ValidatorState]uint64) (active uint64, pending uint64, exited uint64, total uint64) {
	for status, count := range statuses {
		total += count

		switch status {
		case v1.ValidatorStatePendingInitialized, v1.ValidatorStatePendingQueued:
			pending += count
		case v1.ValidatorStateActiveOngoing, v1.ValidatorStateActiveExiting, v1.ValidatorStateActiveSlashed:
			active += count
		case v1.ValidatorStateExitedUnslashed, v1.ValidatorStateExitedSlashed, v1.ValidatorStateWithdrawalPossible, v1.ValidatorStateWithdrawalDone:
			exited += count
		}
	}

	return active, pending, exited, total
}

func buildNetworkOverviewParticipation(raw *networkOverviewRawAggregate, warning string) *APINetworkParticipation {
	if raw == nil {
		return nil
	}

	participation := &APINetworkParticipation{
		Rate:          nil,
		Source:        networkOverviewParticipationSource,
		Complete:      raw.Complete && warning == "",
		IndexedSlots:  raw.IndexedSlots,
		ExpectedSlots: raw.ExpectedSlots,
	}

	if raw.Complete && warning == "" {
		rate := raw.GlobalParticipationRate
		participation.Rate = &rate
	} else {
		participation.Warning = networkOverviewParticipationWarnText
		if warning != "" {
			participation.Warning = warning
		}
	}

	return participation
}

func getNetworkOverviewRawAggregate(ctx context.Context, epoch uint64, slotsPerEpoch uint64) *networkOverviewRawAggregate {
	if slotsPerEpoch == 0 {
		return nil
	}

	dbEpochs := services.GlobalBeaconService.GetDbEpochs(ctx, epoch, 1)
	if len(dbEpochs) == 0 || dbEpochs[0] == nil {
		return nil
	}

	return buildNetworkOverviewRawAggregate(dbEpochs[0], slotsPerEpoch)
}

func buildNetworkOverviewRawAggregate(dbEpoch *dbtypes.Epoch, slotsPerEpoch uint64) *networkOverviewRawAggregate {
	if dbEpoch == nil || dbEpoch.Eligible == 0 || slotsPerEpoch == 0 {
		return nil
	}

	globalParticipationRate := float64(dbEpoch.VotedTarget) * 100 / float64(dbEpoch.Eligible)
	voteParticipation := float64(dbEpoch.VotedTotal) * 100 / float64(dbEpoch.Eligible)
	indexedSlots := uint64(dbEpoch.BlockCount)

	return &networkOverviewRawAggregate{
		GlobalParticipationRate: globalParticipationRate,
		VoteParticipation:       voteParticipation,
		AttestationsIndexed:     dbEpoch.AttestationCount,
		IndexedSlots:            indexedSlots,
		ExpectedSlots:           slotsPerEpoch,
		Complete:                indexedSlots >= slotsPerEpoch,
	}
}
