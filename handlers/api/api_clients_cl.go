package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// APIConsensusClientNodeInfo represents the response structure for consensus client node info
type APIConsensusClientNodeInfo struct {
	ClientName         string                      `json:"client_name"`
	ClientType         string                      `json:"client_type"`
	Version            string                      `json:"version"`
	PeerID             string                      `json:"peer_id"`
	NodeID             string                      `json:"node_id"`
	ENR                string                      `json:"enr"`
	ENRDecoded         map[string]interface{}      `json:"enr_decoded,omitempty"`
	HeadSlot           uint64                      `json:"head_slot"`
	HeadRoot           string                      `json:"head_root"`
	Status             string                      `json:"status"`
	PeerCount          uint32                      `json:"peer_count"`
	PeersInbound       uint32                      `json:"peers_inbound"`
	PeersOutbound      uint32                      `json:"peers_outbound"`
	LastRefresh        time.Time                   `json:"last_refresh"`
	LastError          string                      `json:"last_error,omitempty"`
	SupportsDataColumn bool                        `json:"supports_data_column"`
	ColumnIndexes      []uint64                    `json:"column_indexes,omitempty"`
	Metadata           *APIConsensusClientMetadata `json:"metadata,omitempty"`
}

// APIConsensusClientMetadata represents the metadata from the node identity
type APIConsensusClientMetadata struct {
	Attnets           string `json:"attnets,omitempty"`
	Syncnets          string `json:"syncnets,omitempty"`
	SeqNumber         string `json:"seq_number,omitempty"`
	CustodyGroupCount string `json:"custody_group_count,omitempty"` // MetadataV3 field for Fulu
}

// APIConsensusClientsResponse represents the full API response
type APIConsensusClientsResponse struct {
	Clients []APIConsensusClientNodeInfo `json:"clients"`
	Count   int                          `json:"count"`
}

// APIConsensusClients returns consensus client node information as JSON
// @Summary Get consensus clients information
// @Description Returns a list of all connected consensus clients with their node information, including PeerDAS support. Sensitive information (PeerID, NodeID, ENR) is only included if ShowSensitivePeerInfos is enabled in the configuration.
// @Tags clients
// @Accept json
// @Produce json
// @Success 200 {object} APIConsensusClientsResponse
// @Failure 429 {object} map[string]string "Rate limit exceeded"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/clients/consensus [get]
func APIConsensusClients(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check rate limit
	if err := services.GlobalCallRateLimiter.CheckCallLimit(r, 1); err != nil {
		http.Error(w, `{"error": "rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	clients, err := getConsensusClientNodeInfo()
	if err != nil {
		logrus.WithError(err).Error("failed to get consensus client node info")
		http.Error(w, `{"error": "failed to get client information"}`, http.StatusInternalServerError)
		return
	}

	response := APIConsensusClientsResponse{
		Clients: clients,
		Count:   len(clients),
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode response")
		http.Error(w, `{"error": "failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

// getConsensusClientNodeInfo retrieves node information for all consensus clients
func getConsensusClientNodeInfo() ([]APIConsensusClientNodeInfo, error) {
	var clients []APIConsensusClientNodeInfo

	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		clientInfo := APIConsensusClientNodeInfo{
			ClientName:         client.GetName(),
			ClientType:         client.GetClientType().String(),
			Version:            client.GetVersion(),
			PeerID:             "unknown",
			NodeID:             "unknown",
			ENR:                "",
			HeadSlot:           0,
			HeadRoot:           "",
			Status:             client.GetStatus().String(),
			PeerCount:          0,
			PeersInbound:       0,
			PeersOutbound:      0,
			LastRefresh:        time.Now(),
			LastError:          "",
			SupportsDataColumn: false,
			ColumnIndexes:      []uint64{},
		}

		// Get error information
		if err := client.GetLastError(); err != nil {
			clientInfo.LastError = err.Error()
		}

		// Get head information
		if headSlot, headRoot := client.GetLastHead(); headSlot > 0 {
			clientInfo.HeadSlot = uint64(headSlot)
			clientInfo.HeadRoot = fmt.Sprintf("%x", headRoot)
		}

		// Get peer information
		if peers := client.GetNodePeers(); peers != nil {
			clientInfo.PeerCount = uint32(len(peers))

			// Count inbound/outbound peers
			for _, peer := range peers {
				if peer.Direction == "inbound" {
					clientInfo.PeersInbound++
				} else if peer.Direction == "outbound" {
					clientInfo.PeersOutbound++
				}
			}
		}

		// Get node identity information
		if nodeIdentity := client.GetNodeIdentity(); nodeIdentity != nil {
			if utils.Config.Frontend.ShowSensitivePeerInfos {
				clientInfo.PeerID = nodeIdentity.PeerID
				clientInfo.ENR = nodeIdentity.Enr
				// Note: NodeID not available in the interface, using PeerID as fallback
				clientInfo.NodeID = nodeIdentity.PeerID

				// Decode ENR if available
				if nodeIdentity.Enr != "" {
					if enrRecord, err := utils.DecodeENR(nodeIdentity.Enr); err == nil {
						clientInfo.ENRDecoded = utils.GetKeyValuesFromENR(enrRecord)
					}
				}
			}

			// Add metadata information (available regardless of sensitive info setting)
			if nodeIdentity.Metadata.Attnets != "" || nodeIdentity.Metadata.Syncnets != "" || nodeIdentity.Metadata.SeqNumber != nil || nodeIdentity.Metadata.CustodyGroupCount != nil {
				seqNumber := ""
				if nodeIdentity.Metadata.SeqNumber != nil {
					seqNumber = fmt.Sprintf("%v", nodeIdentity.Metadata.SeqNumber)
				}
				custodyGroupCount := ""
				if nodeIdentity.Metadata.CustodyGroupCount != nil {
					custodyGroupCount = fmt.Sprintf("%v", nodeIdentity.Metadata.CustodyGroupCount)
				}
				clientInfo.Metadata = &APIConsensusClientMetadata{
					Attnets:           nodeIdentity.Metadata.Attnets,
					Syncnets:          nodeIdentity.Metadata.Syncnets,
					SeqNumber:         seqNumber,
					CustodyGroupCount: custodyGroupCount,
				}
			}
		}

		// Note: PeerDAS information not available in the current interface
		// This would need to be implemented separately if needed

		clients = append(clients, clientInfo)
	}

	return clients, nil
}
