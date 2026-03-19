package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIClientHeadForksResponse represents the response for the client head forks endpoint.
type APIClientHeadForksResponse struct {
	Status string                  `json:"status"`
	Data   *APIClientHeadForksData `json:"data"`
}

// APIClientHeadForksData contains information about consensus client head forks.
type APIClientHeadForksData struct {
	Forks     []*APIClientHeadFork `json:"forks"`
	ForkCount uint64               `json:"fork_count"`
}

// APIClientHeadFork represents a single head fork with its associated clients.
type APIClientHeadFork struct {
	HeadSlot    uint64                   `json:"head_slot"`
	HeadRoot    string                   `json:"head_root"`
	Clients     []*APIClientHeadForkPeer `json:"clients"`
	ClientCount uint64                   `json:"client_count"`
}

// APIClientHeadForkPeer represents a consensus client on a particular fork head.
type APIClientHeadForkPeer struct {
	Index       int       `json:"index"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Status      string    `json:"status"`
	HeadSlot    uint64    `json:"head_slot"`
	Distance    uint64    `json:"distance"`
	LastRefresh time.Time `json:"last_refresh"`
	LastError   string    `json:"last_error,omitempty"`
}

// APINetworkClientHeadForksV1 returns consensus client head fork information.
// @Summary Get consensus client head forks
// @Description Returns information about consensus client head forks, showing which clients are on which chain head. Useful for detecting chain splits from the client perspective.
// @Tags network
// @Produce json
// @Success 200 {object} APIClientHeadForksResponse
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/network/client_head_forks [get]
// @ID getNetworkClientHeadForks
func APINetworkClientHeadForksV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	data, err := buildClientHeadForksData(r.Context())
	if err != nil {
		logrus.WithError(err).Error("failed to build client head forks data")
		http.Error(w, `{"status": "ERROR: failed to build client head forks data"}`, http.StatusInternalServerError)
		return
	}

	response := APIClientHeadForksResponse{
		Status: "OK",
		Data:   data,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode client head forks response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

func buildClientHeadForksData(ctx context.Context) (*APIClientHeadForksData, error) {
	chainState := services.GlobalBeaconService.GetChainState()
	if chainState == nil {
		return nil, fmt.Errorf("chain state not available")
	}

	headForks := services.GlobalBeaconService.GetConsensusClientForks()

	finalizedEpoch, _ := services.GlobalBeaconService.GetBeaconIndexer().GetBlockCacheState()

	// Filter out clients on finalized canonical blocks — they aren't truly forked
	for idx, fork := range headForks {
		if idx == 0 {
			continue
		}

		if fork.Slot < chainState.EpochToSlot(finalizedEpoch) {
			dbBlock := db.GetSlotByRoot(ctx, fork.Root[:])
			if dbBlock != nil && dbBlock.Status == dbtypes.Canonical {
				headForks[0].AllClients = append(headForks[0].AllClients, fork.AllClients...)
				headForks[idx] = nil
			}
		}
	}

	forks := make([]*APIClientHeadFork, 0, len(headForks))

	for _, fork := range headForks {
		if fork == nil {
			continue
		}

		forkData := &APIClientHeadFork{
			HeadSlot: uint64(fork.Slot),
			HeadRoot: fmt.Sprintf("0x%x", fork.Root),
			Clients:  make([]*APIClientHeadForkPeer, 0, len(fork.AllClients)),
		}

		for _, client := range fork.AllClients {
			consensusClient := client.GetClient()
			clientHeadSlot, _ := consensusClient.GetLastHead()

			peer := &APIClientHeadForkPeer{
				Index:       int(client.GetIndex()) + 1,
				Name:        consensusClient.GetName(),
				Version:     consensusClient.GetVersion(),
				Status:      consensusClient.GetStatus().String(),
				LastRefresh: consensusClient.GetLastEventTime(),
				HeadSlot:    uint64(clientHeadSlot),
				Distance:    uint64(fork.Slot - clientHeadSlot),
			}

			if lastErr := consensusClient.GetLastClientError(); lastErr != nil {
				peer.LastError = lastErr.Error()
			}

			forkData.Clients = append(forkData.Clients, peer)
		}

		sort.Slice(forkData.Clients, func(a, b int) bool {
			return forkData.Clients[a].Index < forkData.Clients[b].Index
		})

		forkData.ClientCount = uint64(len(forkData.Clients))
		forks = append(forks, forkData)
	}

	return &APIClientHeadForksData{
		Forks:     forks,
		ForkCount: uint64(len(forks)),
	}, nil
}
