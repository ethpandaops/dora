package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// ClientsElStatus returns the status message for an execution client by connecting via P2P
func ClientsElStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientName := vars["clientName"]

	if clientName == "" {
		writeJSONError(w, "Client name is required", http.StatusBadRequest)
		return
	}

	// Find the client by name
	var enodeURL string
	for _, c := range services.GlobalBeaconService.GetExecutionClients() {
		if c.GetName() == clientName {
			nodeInfo := c.GetNodeInfo()
			if nodeInfo != nil {
				enodeURL = nodeInfo.Enode
			}
			break
		}
	}

	if enodeURL == "" {
		writeJSONError(w, fmt.Sprintf("Client '%s' not found or enode not available", clientName), http.StatusNotFound)
		return
	}

	// Get status message via P2P using mimicry
	status, err := getP2PStatusMessage(r.Context(), enodeURL)
	if err != nil {
		logrus.WithError(err).WithField("client", clientName).Warn("failed to get P2P status")
		writeJSONError(w, fmt.Sprintf("Failed to get status: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

type statusMessageResponse struct {
	ProtocolVersion string  `json:"protocolVersion"`
	NetworkId       uint64  `json:"networkId"`
	GenesisHash     string  `json:"genesisHash"`
	ForkIdHash      string  `json:"forkIdHash"`
	ForkIdNext      uint64  `json:"forkIdNext"`
	HeadHash        string  `json:"headHash"`
	EarliestBlock   *uint64 `json:"earliestBlock,omitempty"`
	LatestBlock     *uint64 `json:"latestBlock,omitempty"`
	LatestBlockHash string  `json:"latestBlockHash,omitempty"`
	TotalDifficulty *string `json:"totalDifficulty,omitempty"`
	Error           string  `json:"error,omitempty"`
}

func getP2PStatusMessage(ctx context.Context, enodeURL string) (*statusMessageResponse, error) {
	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)

	client, err := mimicry.New(connCtx, log, enodeURL, "dora-status-checker/1.0")
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	statusChan := make(chan mimicry.Status, 1)
	errChan := make(chan error, 1)
	disconnectChan := make(chan *mimicry.Disconnect, 1)

	client.OnStatus(connCtx, func(ctx context.Context, status mimicry.Status) error {
		statusChan <- status
		return nil
	})

	client.OnDisconnect(connCtx, func(ctx context.Context, reason *mimicry.Disconnect) error {
		disconnectChan <- reason
		return nil
	})

	go func() {
		if err := client.Start(connCtx); err != nil {
			errChan <- err
		}
	}()

	select {
	case status := <-statusChan:
		defer client.Stop(connCtx)
		return buildStatusResponse(status)

	case reason := <-disconnectChan:
		client.Stop(connCtx)
		if reason != nil {
			return nil, fmt.Errorf("peer disconnected: %v", reason)
		}
		return nil, fmt.Errorf("peer disconnected")

	case err := <-errChan:
		client.Stop(connCtx)
		return nil, err

	case <-connCtx.Done():
		client.Stop(connCtx)
		return nil, fmt.Errorf("timeout waiting for status")
	}
}

func buildStatusResponse(status mimicry.Status) (*statusMessageResponse, error) {
	response := &statusMessageResponse{
		NetworkId:   status.GetNetworkID(),
		GenesisHash: "0x" + hex.EncodeToString(status.GetGenesis()),
		ForkIdHash:  "0x" + hex.EncodeToString(status.GetForkIDHash()),
		ForkIdNext:  status.GetForkIDNext(),
		HeadHash:    "0x" + hex.EncodeToString(status.GetHead()),
	}

	if s69, ok := status.(*mimicry.Status69); ok {
		response.ProtocolVersion = "eth69"
		response.EarliestBlock = &s69.EarliestBlock
		response.LatestBlock = &s69.LatestBlock
		response.LatestBlockHash = s69.LatestBlockHash.Hex()

		if s69.EarliestBlock > s69.LatestBlock {
			response.Error = fmt.Sprintf("INVALID: earliestBlock (%d) > latestBlock (%d)",
				s69.EarliestBlock, s69.LatestBlock)
		}
	} else if s68, ok := status.(*mimicry.Status68); ok {
		response.ProtocolVersion = "eth68"
		td := fmt.Sprintf("0x%x", s68.TD)
		response.TotalDifficulty = &td
	} else {
		response.ProtocolVersion = "eth"
	}

	return response, nil
}

func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}
