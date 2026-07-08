package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIFastConfirmationResponse represents the response for the fast confirmation endpoint.
type APIFastConfirmationResponse struct {
	Status string                   `json:"status"`
	Data   *APIFastConfirmationData `json:"data"`
}

// APIFastConfirmationData contains the network-wide safe block derived from the
// fast confirmation rule and the per-client fast confirmation status.
type APIFastConfirmationData struct {
	FcrEnabled           bool                         `json:"fcr_enabled"`
	SafeSlot             uint64                       `json:"safe_slot"`
	SafeRoot             string                       `json:"safe_root,omitempty"`
	SafeEpoch            uint64                       `json:"safe_epoch"`
	LastFastConfirmation *time.Time                   `json:"last_fast_confirmation,omitempty"`
	Clients              []*APIFastConfirmationClient `json:"clients"`
	ClientCount          uint64                       `json:"client_count"`
}

// APIFastConfirmationClient represents the fast confirmation status of a single consensus client.
type APIFastConfirmationClient struct {
	Index                int        `json:"index"`
	Name                 string     `json:"name"`
	Status               string     `json:"status"`
	FcrEnabled           bool       `json:"fcr_enabled"`
	SafeSlot             uint64     `json:"safe_slot,omitempty"`
	SafeRoot             string     `json:"safe_root,omitempty"`
	LastFastConfirmation *time.Time `json:"last_fast_confirmation,omitempty"`
}

// APINetworkFastConfirmationV1 returns the safe block from the fast confirmation rule.
// @Summary Get fast confirmation (safe block) status
// @Description Returns the network-wide safe block derived from the fast confirmation rule (fast_confirmation event stream), plus the per-client fast confirmation status. Clients that don't support the fast confirmation rule are listed with fcr_enabled=false.
// @Tags network
// @Produce json
// @Success 200 {object} APIFastConfirmationResponse
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/network/fast_confirmation [get]
// @ID getNetworkFastConfirmation
func APINetworkFastConfirmationV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	chainState := services.GlobalBeaconService.GetChainState()
	if chainState == nil {
		http.Error(w, `{"status": "ERROR: chain state not available"}`, http.StatusInternalServerError)
		return
	}

	safeSlot, safeRoot, lastFastConfirmation := chainState.GetFastConfirmedBlock()

	data := &APIFastConfirmationData{
		FcrEnabled: !lastFastConfirmation.IsZero(),
		Clients:    make([]*APIFastConfirmationClient, 0),
	}

	if data.FcrEnabled {
		data.SafeSlot = uint64(safeSlot)
		data.SafeRoot = fmt.Sprintf("0x%x", safeRoot)
		data.SafeEpoch = uint64(chainState.EpochOfSlot(safeSlot))
		data.LastFastConfirmation = &lastFastConfirmation
	}

	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		clientData := &APIFastConfirmationClient{
			Index:  int(client.GetIndex()) + 1,
			Name:   client.GetName(),
			Status: client.GetStatus().String(),
		}

		if clientSafeSlot, clientSafeRoot, clientLastConfirmation := client.GetLastFastConfirmation(); !clientLastConfirmation.IsZero() {
			clientData.FcrEnabled = true
			clientData.SafeSlot = uint64(clientSafeSlot)
			clientData.SafeRoot = fmt.Sprintf("0x%x", clientSafeRoot)
			clientData.LastFastConfirmation = &clientLastConfirmation
		}

		data.Clients = append(data.Clients, clientData)
	}

	sort.Slice(data.Clients, func(a, b int) bool {
		return data.Clients[a].Index < data.Clients[b].Index
	})

	data.ClientCount = uint64(len(data.Clients))

	response := APIFastConfirmationResponse{
		Status: "OK",
		Data:   data,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode fast confirmation response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
