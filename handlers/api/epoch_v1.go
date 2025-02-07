package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/services"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type APIEpochResponseV1 struct {
	Epoch                   uint64 `json:"epoch"`
	Ts                      uint64 `json:"ts"`
	AttestationsCount       uint64 `json:"attestationscount"`
	AttesterSlashingsCount  uint64 `json:"attesterslashingscount"`
	AverageValidatorBalance uint64 `json:"averagevalidatorbalance"`
	BlocksCount             uint64 `json:"blockscount"`
	DepositsCount           uint64 `json:"depositscount"`
	EligibleEther           uint64 `json:"eligibleether"`
	Finalized               bool   `json:"finalized"`
	GlobalParticipationRate uint64 `json:"globalparticipationrate"`
	MissedBlocks            uint64 `json:"missedblocks"`
	OrphanedBlocks          uint64 `json:"orphanedblocks"`
	ProposedBlocks          uint64 `json:"proposedblocks"`
	ProposerSlashingsCount  uint64 `json:"proposerslashingscount"`
	ScheduledBlocks         uint64 `json:"scheduledblocks"`
	TotalValidatorBalance   uint64 `json:"totalvalidatorbalance"`
	ValidatorsCount         uint64 `json:"validatorscount"`
	VoluntaryExitsCount     uint64 `json:"voluntaryexitscount"`
	VotedEther              uint64 `json:"votedether"`
	RewardsExported         uint64 `json:"rewards_exported"`
	WithdrawalCount         uint64 `json:"withdrawalcount"`
}

// ApiEpoch godoc
// @Summary Get epoch by number, latest, finalized
// @Tags Epoch
// @Description Returns information for a specified epoch by the epoch number or an epoch tag (can be latest or finalized)
// @Produce  json
// @Param  epoch path string true "Epoch number, the string latest or the string finalized"
// @Success 200 {object} ApiResponse{data=APIEpochResponseV1} "Success"
// @Failure 400 {object} ApiResponse "Failure"
// @Failure 500 {object} ApiResponse "Server Error"
// @Router /api/v1/epoch/{epoch} [get]
func ApiEpochV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)

	epoch, err := strconv.ParseInt(vars["epoch"], 10, 64)
	if err != nil && vars["epoch"] != "latest" && vars["epoch"] != "finalized" {
		sendBadRequestResponse(w, r.URL.String(), "invalid epoch provided")
		return
	}

	chainState := services.GlobalBeaconService.GetChainState()
	if vars["epoch"] == "latest" {
		epoch = int64(chainState.CurrentEpoch())
	}

	finalizedEpoch, _ := chainState.GetFinalizedCheckpoint()
	if vars["epoch"] == "finalized" {
		epoch = int64(finalizedEpoch)
	}

	if epoch > int64(chainState.CurrentEpoch()) {
		sendBadRequestResponse(w, r.URL.String(), fmt.Sprintf("epoch is in the future. The latest epoch is %v", chainState.CurrentEpoch()))
		return
	}

	if epoch < 0 {
		sendBadRequestResponse(w, r.URL.String(), "epoch must be a positive number")
		return
	}

	data := &APIEpochResponseV1{
		Epoch:     uint64(epoch),
		Ts:        uint64(chainState.EpochToTime(phase0.Epoch(epoch)).Unix()),
		Finalized: finalizedEpoch >= phase0.Epoch(epoch),
	}

	dbEpochs := services.GlobalBeaconService.GetDbEpochs(uint64(epoch), 1)
	dbEpoch := dbEpochs[0]
	if dbEpoch != nil {
		data.AttestationsCount = dbEpoch.AttestationCount
		data.DepositsCount = dbEpoch.DepositCount
		data.VoluntaryExitsCount = dbEpoch.ExitCount
		data.WithdrawalCount = dbEpoch.WithdrawCount
		data.RewardsExported = dbEpoch.WithdrawAmount
		data.ProposerSlashingsCount = dbEpoch.ProposerSlashingCount
		data.AttesterSlashingsCount = dbEpoch.AttesterSlashingCount
		data.EligibleEther = dbEpoch.Eligible
		data.VotedEther = dbEpoch.VotedTotal
		data.GlobalParticipationRate = uint64(dbEpoch.VotedTarget * 100 / dbEpoch.Eligible)
		data.ValidatorsCount = dbEpoch.ValidatorCount
		data.TotalValidatorBalance = dbEpoch.ValidatorBalance
		if dbEpoch.ValidatorCount > 0 {
			data.AverageValidatorBalance = dbEpoch.ValidatorBalance / dbEpoch.ValidatorCount
		}

		specs := chainState.GetSpecs()
		currentSlot := chainState.CurrentSlot()
		lastSlot := chainState.EpochToSlot(phase0.Epoch(epoch+1)) - 1
		passedSlotCount := uint64(specs.SlotsPerEpoch)
		if currentSlot < lastSlot {
			data.ScheduledBlocks = passedSlotCount - uint64(lastSlot-currentSlot)
			passedSlotCount -= data.ScheduledBlocks
		}

		data.MissedBlocks = passedSlotCount - uint64(dbEpoch.BlockCount)
		data.BlocksCount = uint64(dbEpoch.BlockCount + dbEpoch.OrphanedCount)
		data.OrphanedBlocks = uint64(dbEpoch.OrphanedCount)
		data.ProposedBlocks = uint64(dbEpoch.BlockCount)
	}

	j := json.NewEncoder(w)
	response := &ApiResponse{}
	response.Status = "OK"
	response.Data = data

	err = j.Encode(response)
	if err != nil {
		sendServerErrorResponse(w, r.URL.String(), "could not serialize data results")
		logrus.Errorf("error serializing json data for API %v route: %v", r.URL, err)
	}
}
