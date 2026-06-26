package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// APIEpochHealthResponseV1 reports the three participation rates that together
// describe the health of an epoch. Post-ePBS (EIP-7732) a beacon block can be
// proposed while its execution payload is missing, so payload participation is
// tracked separately from proposal participation. The chain is only fully
// healthy for an epoch when all three rates reach 100%.
type APIEpochHealthResponseV1 struct {
	Epoch                 uint64  `json:"epoch"`
	Ts                    uint64  `json:"ts"`
	Finalized             bool    `json:"finalized"`
	EligibleEther         uint64  `json:"eligible_ether"`
	VotedEther            uint64  `json:"voted_ether"`
	VoteParticipation     float64 `json:"vote_participation"`
	ProposedBlocks        uint64  `json:"proposed_blocks"`
	ProposalParticipation float64 `json:"proposal_participation"`
	ProposedPayloads      uint64  `json:"proposed_payloads"`
	PayloadParticipation  float64 `json:"payload_participation"`
	Slots                 uint64  `json:"slots"`
	Healthy               bool    `json:"healthy"`
}

// ApiEpochHealthV1 godoc
// @Summary Get epoch health by number, latest, finalized
// @Tags Epoch
// @Description Returns the vote, proposal and payload participation rates for an epoch. The chain is only fully healthy when all three reach 100%. Post-ePBS (EIP-7732) payloads are revealed separately from beacon blocks and may be missing.
// @Produce  json
// @Param  epoch path string true "Epoch number, the string latest or the string finalized"
// @Success 200 {object} ApiResponse{data=APIEpochHealthResponseV1} "Success"
// @Failure 400 {object} ApiResponse "Failure"
// @Failure 500 {object} ApiResponse "Server Error"
// @Router /v1/epoch/{epoch}/health [get]
// @ID getEpochHealth
func ApiEpochHealthV1(w http.ResponseWriter, r *http.Request) {
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

	data := &APIEpochHealthResponseV1{
		Epoch:     uint64(epoch),
		Ts:        uint64(chainState.EpochToTime(phase0.Epoch(epoch)).Unix()),
		Finalized: finalizedEpoch >= phase0.Epoch(epoch),
	}

	dbEpochs := services.GlobalBeaconService.GetDbEpochs(r.Context(), uint64(epoch), 1)
	dbEpoch := dbEpochs[0]
	if dbEpoch != nil {
		specs := chainState.GetSpecs()

		// Count only the slots that have already passed so the current in-progress epoch
		// is not penalised for slots that are still scheduled in the future.
		currentSlot := chainState.CurrentSlot()
		lastSlot := chainState.EpochToSlot(phase0.Epoch(epoch+1)) - 1
		passedSlotCount := uint64(specs.SlotsPerEpoch)
		if currentSlot < lastSlot {
			passedSlotCount -= uint64(lastSlot - currentSlot)
		}

		// Pre-ePBS the execution payload is bundled inside the beacon block, so every
		// canonical block carries a payload. Post-ePBS (EIP-7732) payloads are revealed
		// separately and may be missing, so use the dedicated payload count.
		proposedPayloads := uint64(dbEpoch.BlockCount)
		if chainState.IsEip7732Enabled(phase0.Epoch(epoch)) {
			proposedPayloads = dbEpoch.PayloadCount
		}

		data.EligibleEther = dbEpoch.Eligible
		data.VotedEther = dbEpoch.VotedTarget
		data.ProposedBlocks = uint64(dbEpoch.BlockCount)
		data.ProposedPayloads = proposedPayloads
		data.Slots = passedSlotCount

		if dbEpoch.Eligible > 0 {
			data.VoteParticipation = float64(dbEpoch.VotedTarget) * 100.0 / float64(dbEpoch.Eligible)
		}
		if passedSlotCount > 0 {
			data.ProposalParticipation = float64(dbEpoch.BlockCount) * 100.0 / float64(passedSlotCount)
			data.PayloadParticipation = float64(proposedPayloads) * 100.0 / float64(passedSlotCount)
		}

		// The chain is fully healthy for the epoch only when every eligible validator
		// voted, every passed slot produced a block, and every block revealed its payload.
		const healthyThreshold = 100.0 - 1e-9
		data.Healthy = passedSlotCount > 0 &&
			data.VoteParticipation >= healthyThreshold &&
			data.ProposalParticipation >= healthyThreshold &&
			data.PayloadParticipation >= healthyThreshold
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
