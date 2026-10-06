package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/services"
)

// APISlotInclusionListsResponse represents the response for the slot inclusion lists endpoint.
type APISlotInclusionListsResponse struct {
	Status string                           `json:"status"`
	Data   *services.SlotInclusionListsView `json:"data"`
}

// APISlotInclusionListsV1 returns the EIP-7805 inclusion lists published in a slot.
// @Summary Get inclusion lists for a slot
// @Description Returns the EIP-7805 inclusion lists published in a slot: the inclusion list committee
// @Description with each member's submission status, the lists with their gossip observations, and for
// @Description every block of the following slot how its execution payload treated each list transaction
// @Description (included, validly omitted with the reason, not enforced, or unsatisfied).
// @Tags Slot
// @Produce json
// @Param slotOrHash path string true "Slot number or block root (0x-prefixed hex)"
// @Success 200 {object} APISlotInclusionListsResponse
// @Failure 400 {object} map[string]string "Invalid slot number or root format"
// @Failure 404 {object} map[string]string "Slot not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slot/{slotOrHash}/inclusion_lists [get]
// @ID getSlotInclusionLists
func APISlotInclusionListsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// A slot number is used as is, so that the lists of a slot without a block
	// can be queried too; a block root resolves to the slot of its block.
	slotOrHash := mux.Vars(r)["slotOrHash"]
	slot, err := strconv.ParseUint(slotOrHash, 10, 64)
	if err != nil || slot >= 2147483648 {
		dbSlot := resolveSlotOrHash(r.Context(), w, slotOrHash)
		if dbSlot == nil {
			return
		}
		slot = dbSlot.Slot
	}

	resp := APISlotInclusionListsResponse{
		Status: "OK",
		Data:   services.GlobalBeaconService.GetSlotInclusionListsView(r.Context(), phase0.Slot(slot), true),
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logrus.WithError(err).Error("failed to encode inclusion lists response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
	}
}
