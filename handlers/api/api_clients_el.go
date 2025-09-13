package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// APIExecutionClientNodeInfo represents the response structure for execution client node info
type APIExecutionClientNodeInfo struct {
	ClientName string    `json:"client_name"`
	NodeID     string    `json:"node_id"`
	Enode      string    `json:"enode"`
	IP         string    `json:"ip"`
	Port       int       `json:"port"`
	Version    string    `json:"version"`
	Status     string    `json:"status"`
	LastUpdate time.Time `json:"last_update"`
}

// APIExecutionClientsResponse represents the full API response
type APIExecutionClientsResponse struct {
	Clients []APIExecutionClientNodeInfo `json:"clients"`
	Count   int                          `json:"count"`
}

// APIExecutionClients returns execution client node information as JSON
// @Summary Get execution clients information
// @Description Returns a list of all connected execution clients with their node information. Sensitive information (IP addresses, ports, enode) is only included if ShowSensitivePeerInfos is enabled in the configuration.
// @Tags clients
// @Accept json
// @Produce json
// @Success 200 {object} APIExecutionClientsResponse
// @Failure 429 {object} map[string]string "Rate limit exceeded"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/clients/execution [get]
func APIExecutionClients(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	clients, err := getExecutionClientNodeInfo()
	if err != nil {
		logrus.WithError(err).Error("failed to get execution client node info")
		http.Error(w, `{"error": "failed to get client information"}`, http.StatusInternalServerError)
		return
	}

	response := APIExecutionClientsResponse{
		Clients: clients,
		Count:   len(clients),
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode response")
		http.Error(w, `{"error": "failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

// getExecutionClientNodeInfo retrieves node information for all execution clients
func getExecutionClientNodeInfo() ([]APIExecutionClientNodeInfo, error) {
	var clients []APIExecutionClientNodeInfo

	for _, client := range services.GlobalBeaconService.GetExecutionClients() {
		nodeInfo := client.GetNodeInfo()

		clientInfo := APIExecutionClientNodeInfo{
			ClientName: client.GetName(),
			NodeID:     "unknown",
			Enode:      "",
			IP:         "",
			Port:       0,
			Version:    "",
			Status:     "disconnected",
			LastUpdate: time.Now(),
		}

		if nodeInfo != nil {
			clientInfo.Version = nodeInfo.Name
			clientInfo.Status = "connected"

			// Parse enode to extract node ID and network info
			if nodeInfo.Enode != "" {
				if en, err := enode.ParseV4(nodeInfo.Enode); err == nil {
					clientInfo.NodeID = en.ID().String()
					if utils.Config.Frontend.ShowSensitivePeerInfos {
						clientInfo.IP = en.IP().String()
						clientInfo.Port = en.TCP()
						clientInfo.Enode = nodeInfo.Enode
					}
				} else {
					logrus.WithFields(logrus.Fields{
						"client": client.GetName(),
						"enode":  nodeInfo.Enode,
					}).Warn("failed to parse enode")
				}
			}
		}

		clients = append(clients, clientInfo)
	}

	return clients, nil
}
