package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethpandaops/dora/clients/xatu"
	"github.com/ethpandaops/dora/handlers"
	"github.com/ethpandaops/dora/types/models"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// APISlotArrivalResponse represents the response for the slot arrival endpoint.
type APISlotArrivalResponse struct {
	Status string                      `json:"status"`
	Data   *models.SlotArrivalResponse `json:"data"`
}

// APISlotArrivalV1 returns the block propagation observations for a slot.
// @Summary Get block propagation for a slot
// @Description Returns when each observing node first saw the block of a slot, as milliseconds after slot start, from Xatu.
// @Description Covers beacon API block and head events, libp2p block gossip and engine API newPayload calls, with per-continent and per-group summaries, mirroring the propagation tab on the slot page.
// @Tags Slot
// @Produce json
// @Param slotOrHash path string true "Slot number or block root (0x-prefixed hex)"
// @Success 200 {object} APISlotArrivalResponse
// @Failure 400 {object} map[string]string "Invalid slot number or root format"
// @Failure 404 {object} map[string]string "Slot not found or Xatu not configured"
// @Failure 503 {object} map[string]string "Propagation data unavailable"
// @Router /v1/slot/{slotOrHash}/arrival [get]
// @ID getSlotArrival
func APISlotArrivalV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if xatu.GlobalClient == nil {
		http.Error(w, `{"status": "ERROR: xatu is not configured"}`, http.StatusNotFound)
		return
	}

	dbSlot := resolveSlotOrHash(r.Context(), w, mux.Vars(r)["slotOrHash"])
	if dbSlot == nil {
		return
	}

	execHash := ""
	if len(dbSlot.EthBlockHash) > 0 {
		execHash = fmt.Sprintf("0x%x", dbSlot.EthBlockHash)
	}

	execNumber := uint64(0)
	if dbSlot.EthBlockNumber != nil {
		execNumber = *dbSlot.EthBlockNumber
	}

	data, err := handlers.GetSlotArrivalData(dbSlot.Slot, fmt.Sprintf("0x%x", dbSlot.Root), execHash, execNumber)
	if err != nil {
		logrus.WithError(err).Error("failed to load slot arrival data")
		http.Error(w, `{"status": "ERROR: propagation data unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	resp := APISlotArrivalResponse{Status: "OK", Data: data}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logrus.WithError(err).Error("failed to encode slot arrival response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
	}
}
