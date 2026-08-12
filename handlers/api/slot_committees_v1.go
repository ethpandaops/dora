package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// APISlotCommitteesResponse represents the response structure for slot committees
type APISlotCommitteesResponse struct {
	Status string                 `json:"status"`
	Data   *APISlotCommitteesData `json:"data"`
}

// APISlotCommitteesData contains the attester committees of a single slot
type APISlotCommitteesData struct {
	Slot       uint64                  `json:"slot"`
	Epoch      uint64                  `json:"epoch"`
	Committees []*APISlotCommitteeInfo `json:"committees"`
}

// APISlotCommitteeInfo represents a single attester committee of a slot.
// Validators are global validator indices in committee order.
type APISlotCommitteeInfo struct {
	Index      uint64   `json:"index"`
	Validators []uint64 `json:"validators"`
}

// APISlotCommitteesV1 returns the attester committees for a slot
// @Summary Get slot attester committees
// @Description Returns the attester committees for the specified slot. Committee members are global validator indices in committee order.
// @Tags Slot
// @Produce json
// @Param slot path int true "Slot number"
// @Param committee query string false "Comma-separated list of committee indices to filter by"
// @Param dependent_root query string false "Resolve committees on the fork with this committee-shuffling dependent root (0x-prefixed hex)"
// @Param block_root query string false "Resolve committees on the fork that this block root sits on (0x-prefixed hex)"
// @Success 200 {object} APISlotCommitteesResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 404 {object} map[string]string "Committees not available"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slot/{slot}/committees [get]
// @ID getSlotCommittees
// @Security BearerAuth
func APISlotCommitteesV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)

	slot, err := strconv.ParseUint(vars["slot"], 10, 64)
	if err != nil {
		http.Error(w, `{"status": "ERROR: invalid slot parameter"}`, http.StatusBadRequest)
		return
	}

	chainState := services.GlobalBeaconService.GetChainState()
	epoch := chainState.EpochOfSlot(phase0.Slot(slot))
	currentEpoch := chainState.CurrentEpoch()

	// Attester duties are known one epoch in advance, everything beyond that is unknowable.
	if epoch > currentEpoch+1 {
		http.Error(
			w,
			fmt.Sprintf(`{"status": "ERROR: slot is too far in the future. The current epoch is %v"}`, currentEpoch),
			http.StatusBadRequest,
		)
		return
	}

	// Parse optional committee filter
	var committeeFilter map[uint64]bool
	if committeeParam := r.URL.Query().Get("committee"); committeeParam != "" {
		committeeFilter = make(map[uint64]bool, 8)
		for _, committeeStr := range strings.Split(committeeParam, ",") {
			committeeStr = strings.TrimSpace(committeeStr)
			if committeeStr == "" {
				continue
			}
			committeeIdx, perr := strconv.ParseUint(committeeStr, 10, 64)
			if perr != nil {
				http.Error(w, `{"status": "ERROR: invalid committee parameter"}`, http.StatusBadRequest)
				return
			}
			committeeFilter[committeeIdx] = true
		}
	}

	depRoot, hasFork, ok := parseDutiesFork(r.Context(), w, r, epoch)
	if !ok {
		return
	}

	var slotCommittees [][]phase0.ValidatorIndex
	if hasFork {
		slotCommittees = services.GlobalBeaconService.GetSlotCommitteesForRoot(r.Context(), phase0.Slot(slot), depRoot)
	} else {
		slotCommittees = services.GlobalBeaconService.GetSlotCommittees(r.Context(), phase0.Slot(slot))
	}
	if slotCommittees == nil {
		http.Error(
			w,
			fmt.Sprintf(`{"status": "ERROR: committees not available for slot %v"}`, slot),
			http.StatusNotFound,
		)
		return
	}

	committees := make([]*APISlotCommitteeInfo, 0, len(slotCommittees))
	for committeeIdx, committee := range slotCommittees {
		if committeeFilter != nil && !committeeFilter[uint64(committeeIdx)] {
			continue
		}

		members := make([]uint64, len(committee))
		for i, validatorIndex := range committee {
			members[i] = uint64(validatorIndex)
		}

		committees = append(committees, &APISlotCommitteeInfo{
			Index:      uint64(committeeIdx),
			Validators: members,
		})
	}

	response := APISlotCommitteesResponse{
		Status: "OK",
		Data: &APISlotCommitteesData{
			Slot:       slot,
			Epoch:      uint64(epoch),
			Committees: committees,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode slot committees response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
