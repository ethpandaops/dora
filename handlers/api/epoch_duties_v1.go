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

// APIEpochDutiesResponse represents the response structure for epoch attester duties
type APIEpochDutiesResponse struct {
	Status string              `json:"status"`
	Data   *APIEpochDutiesData `json:"data"`
}

// APIEpochDutiesData contains the attester committees for all slots of an epoch
type APIEpochDutiesData struct {
	Epoch             uint64                    `json:"epoch"`
	DependentRoot     string                    `json:"dependent_root,omitempty"`
	CommitteesPerSlot uint64                    `json:"committees_per_slot"`
	Slots             []*APIEpochDutiesSlotInfo `json:"slots"`
}

// APIEpochDutiesSlotInfo contains the attester committees of a single slot.
// The position in the committees array is the committee index, the values are
// global validator indices in committee order.
type APIEpochDutiesSlotInfo struct {
	Slot       uint64     `json:"slot"`
	Committees [][]uint64 `json:"committees"`
}

// APIEpochDutiesV1 returns the attester committees for every slot in an epoch
// @Summary Get epoch attester duties
// @Description Returns the attester committees for every slot in the specified epoch. Committee members are global validator indices in committee order.
// @Tags Epoch
// @Produce json
// @Param epoch path int true "Epoch number"
// @Param dependent_root query string false "Resolve duties on the fork with this committee-shuffling dependent root (0x-prefixed hex)"
// @Param block_root query string false "Resolve duties on the fork that this block root sits on (0x-prefixed hex)"
// @Success 200 {object} APIEpochDutiesResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 404 {object} map[string]string "Duties not available"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/epoch/{epoch}/duties [get]
// @ID getEpochDuties
// @Security BearerAuth
func APIEpochDutiesV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)

	epoch, err := strconv.ParseUint(vars["epoch"], 10, 64)
	if err != nil {
		http.Error(w, `{"status": "ERROR: invalid epoch parameter"}`, http.StatusBadRequest)
		return
	}

	chainState := services.GlobalBeaconService.GetChainState()
	currentEpoch := uint64(chainState.CurrentEpoch())

	// Attester duties are known one epoch in advance, everything beyond that is unknowable.
	if epoch > currentEpoch+1 {
		http.Error(
			w,
			fmt.Sprintf(`{"status": "ERROR: epoch is too far in the future. The latest epoch is %v"}`, currentEpoch),
			http.StatusBadRequest,
		)
		return
	}

	firstSlot := chainState.EpochStartSlot(phase0.Epoch(epoch))

	depRoot, hasFork, ok := parseDutiesFork(r.Context(), w, r, phase0.Epoch(epoch))
	if !ok {
		return
	}

	// Load the whole epoch's committees in a single read (one blockdb/S3 fetch),
	// rather than a per-slot lookup that would reload the same duties object for
	// every slot in the epoch.
	var epochCommittees [][][]phase0.ValidatorIndex
	if hasFork {
		epochCommittees = services.GlobalBeaconService.GetEpochCommitteesForRoot(r.Context(), phase0.Epoch(epoch), depRoot)
	} else {
		epochCommittees = services.GlobalBeaconService.GetEpochCommittees(r.Context(), phase0.Epoch(epoch))
	}
	if epochCommittees == nil {
		http.Error(
			w,
			fmt.Sprintf(`{"status": "ERROR: duties not available for epoch %v"}`, epoch),
			http.StatusNotFound,
		)
		return
	}

	slots := make([]*APIEpochDutiesSlotInfo, 0, len(epochCommittees))
	committeesPerSlot := uint64(0)

	for slotIdx, committees := range epochCommittees {
		slot := firstSlot + phase0.Slot(slotIdx)

		slotCommittees := make([][]uint64, len(committees))
		for committeeIdx, committee := range committees {
			members := make([]uint64, len(committee))
			for i, validatorIndex := range committee {
				members[i] = uint64(validatorIndex)
			}
			slotCommittees[committeeIdx] = members
		}

		if uint64(len(slotCommittees)) > committeesPerSlot {
			committeesPerSlot = uint64(len(slotCommittees))
		}

		slots = append(slots, &APIEpochDutiesSlotInfo{
			Slot:       uint64(slot),
			Committees: slotCommittees,
		})
	}

	data := &APIEpochDutiesData{
		Epoch:             epoch,
		CommitteesPerSlot: committeesPerSlot,
		Slots:             slots,
	}

	// Report the fork's dependent root when one was requested, otherwise fall
	// back to the canonical dependent root (only known while the epoch stats are
	// held in memory).
	if hasFork && depRoot != (phase0.Root{}) {
		data.DependentRoot = fmt.Sprintf("0x%x", depRoot[:])
	} else if epochStats := services.GlobalBeaconService.GetBeaconIndexer().GetEpochStats(phase0.Epoch(epoch), nil); epochStats != nil {
		dependentRoot := epochStats.GetDependentRoot()
		data.DependentRoot = fmt.Sprintf("0x%x", dependentRoot[:])
	}

	response := APIEpochDutiesResponse{
		Status: "OK",
		Data:   data,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode epoch duties response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
