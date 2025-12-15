package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIEpochsResponse represents the response structure for epochs list
type APIEpochsResponse struct {
	Status string         `json:"status"`
	Data   *APIEpochsData `json:"data"`
}

// APIEpochsData contains the epochs data
type APIEpochsData struct {
	Epochs         []*APIEpochInfo `json:"epochs"`
	EpochCount     uint64          `json:"epoch_count"`
	FirstEpoch     uint64          `json:"first_epoch"`
	LastEpoch      uint64          `json:"last_epoch"`
	TotalEpochs    uint64          `json:"total_epochs"`
	CurrentEpoch   uint64          `json:"current_epoch"`
	FinalizedEpoch uint64          `json:"finalized_epoch"`
}

// APIEpochInfo represents information about a single epoch
type APIEpochInfo struct {
	Epoch                 uint64  `json:"epoch"`
	Finalized             bool    `json:"finalized"`
	VotingFinalized       bool    `json:"voting_finalized"`
	VotingJustified       bool    `json:"voting_justified"`
	Validators            uint64  `json:"validators"`
	ValidatorBalance      uint64  `json:"validator_balance"`
	EligibleEther         uint64  `json:"eligible_ether"`
	TargetVoted           uint64  `json:"target_voted"`
	HeadVoted             uint64  `json:"head_voted"`
	TotalVoted            uint64  `json:"total_voted"`
	VoteParticipation     float64 `json:"vote_participation"`
	Attestations          uint64  `json:"attestations"`
	Deposits              uint64  `json:"deposits"`
	DepositsAmount        uint64  `json:"deposits_amount"`
	ProposerSlashings     uint64  `json:"proposer_slashings"`
	AttesterSlashings     uint64  `json:"attester_slashings"`
	Exits                 uint64  `json:"exits"`
	WithdrawalsCount      uint64  `json:"withdrawals_count"`
	WithdrawalsAmount     uint64  `json:"withdrawals_amount"`
	BLSChanges            uint64  `json:"bls_changes"`
	SyncParticipation     float64 `json:"sync_participation"`
	ProposedBlocks        uint64  `json:"proposed_blocks"`
	MissedBlocks          uint64  `json:"missed_blocks"`
	OrphanedBlocks        uint64  `json:"orphaned_blocks"`
	MinSyncCommitteeSize  uint64  `json:"min_sync_committee_size,omitempty"`
	MaxSyncCommitteeSize  uint64  `json:"max_sync_committee_size,omitempty"`
	WithdrawalRequests    uint64  `json:"withdrawal_requests,omitempty"`
	ConsolidationRequests uint64  `json:"consolidation_requests,omitempty"`
}

// APIEpochs returns a list of epochs with filters
// @Summary Get epochs list
// @Description Returns a list of epochs with detailed information and statistics
// @Tags epochs
// @Accept json
// @Produce json
// @Param epoch query int false "First epoch to display"
// @Param limit query int false "Number of epochs to return (max 100)"
// @Success 200 {object} APIEpochsResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/epochs [get]
// @ID getEpochs
func APIEpochsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()

	// Parse epoch parameter
	var firstEpoch uint64
	epochStr := query.Get("epoch")
	if epochStr != "" {
		epoch, err := strconv.ParseUint(epochStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid epoch parameter"}`, http.StatusBadRequest)
			return
		}
		firstEpoch = epoch
	} else {
		// Default to latest epoch
		chainState := services.GlobalBeaconService.GetChainState()
		currentSlot := chainState.CurrentSlot()
		firstEpoch = uint64(chainState.EpochOfSlot(currentSlot))
	}

	// Parse limit parameter
	limit := uint64(10)
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

	// Get epoch stats
	chainState := services.GlobalBeaconService.GetChainState()
	currentSlot := chainState.CurrentSlot()
	currentEpoch := chainState.EpochOfSlot(currentSlot)
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()

	// Determine epoch range
	lastEpoch := firstEpoch
	if firstEpoch >= limit {
		lastEpoch = firstEpoch - limit + 1
	} else {
		lastEpoch = 0
	}

	epochs := []*APIEpochInfo{}

	// Fetch epochs data
	for epoch := firstEpoch; epoch >= lastEpoch && epoch <= firstEpoch; epoch-- {
		epochInfo := &APIEpochInfo{
			Epoch: epoch,
		}

		// Check if finalized
		epochInfo.Finalized = int64(finalizedEpoch) >= int64(epoch)

		// Get epoch stats from beacon indexer
		epochStats := services.GlobalBeaconService.GetBeaconIndexer().GetEpochStats(phase0.Epoch(epoch), nil)
		if epochStats != nil {
			epochStatsValues := epochStats.GetValues(false)
			if epochStatsValues != nil {
				epochInfo.Validators = epochStatsValues.ActiveValidators
				epochInfo.ValidatorBalance = uint64(epochStatsValues.ActiveBalance)
				epochInfo.EligibleEther = uint64(epochStatsValues.EffectiveBalance)
			}
		}

		// Get deposits, slashings, exits, etc. from blocks in epoch
		specs := chainState.GetSpecs()
		firstSlot := epoch * specs.SlotsPerEpoch

		// Get blocks for this epoch
		blocks := services.GlobalBeaconService.GetDbBlocksForSlots(firstSlot, uint32(specs.SlotsPerEpoch), true, true)

		for _, slot := range blocks {
			if slot.Status == dbtypes.Missing {
				epochInfo.MissedBlocks++
				continue
			} else if slot.Status == dbtypes.Orphaned {
				epochInfo.OrphanedBlocks++
				continue
			}

			// Canonical block
			epochInfo.ProposedBlocks++
			epochInfo.Attestations += slot.AttestationCount
			epochInfo.Deposits += slot.DepositCount
			epochInfo.ProposerSlashings += slot.ProposerSlashingCount
			epochInfo.AttesterSlashings += slot.AttesterSlashingCount
			epochInfo.Exits += slot.ExitCount
			epochInfo.BLSChanges += slot.BLSChangeCount
			epochInfo.WithdrawalsCount += slot.WithdrawCount
			epochInfo.WithdrawalsAmount += slot.WithdrawAmount
			epochInfo.SyncParticipation += float64(slot.SyncParticipation)
		}

		epochs = append(epochs, epochInfo)

		if epoch == 0 {
			break
		}
	}

	response := APIEpochsResponse{
		Status: "OK",
		Data: &APIEpochsData{
			Epochs:         epochs,
			EpochCount:     uint64(len(epochs)),
			FirstEpoch:     firstEpoch,
			LastEpoch:      lastEpoch,
			TotalEpochs:    uint64(currentEpoch) + 1,
			CurrentEpoch:   uint64(currentEpoch),
			FinalizedEpoch: uint64(finalizedEpoch),
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode epochs response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
