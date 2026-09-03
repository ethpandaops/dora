package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// ClientsCLNodes returns every node known to the explorer (consensus clients and their
// peers) as JSON, without the per-client peer lists. The consensus clients page loads
// this lazily instead of embedding it into the HTML document.
func ClientsCLNodes(w http.ResponseWriter, r *http.Request) {
	pageData, ok := getCLClientsDataForRequest(w, r)
	if !ok {
		return
	}

	nodes := make([]*models.ClientCLPageDataNode, 0, len(pageData.Nodes))
	for _, node := range pageData.Nodes {
		nodeCopy := *node
		nodeCopy.Peers = nil
		nodes = append(nodes, &nodeCopy)
	}

	writeCLClientsJSON(w, "nodes", &models.ClientsCLNodesData{Nodes: nodes})
}

// ClientsCLPeerMap returns the peer graph (nodes and deduplicated edges) as JSON.
func ClientsCLPeerMap(w http.ResponseWriter, r *http.Request) {
	pageData, ok := getCLClientsDataForRequest(w, r)
	if !ok {
		return
	}

	writeCLClientsJSON(w, "peer map", pageData.PeerMap)
}

// ClientsCLNodePeers returns the peer connections reported by a single consensus
// client, identified by its peer ID.
func ClientsCLNodePeers(w http.ResponseWriter, r *http.Request) {
	peerID := mux.Vars(r)["peerId"]
	if peerID == "" {
		writeJSONError(w, "Peer ID is required", http.StatusBadRequest)
		return
	}

	pageData, ok := getCLClientsDataForRequest(w, r)
	if !ok {
		return
	}

	for _, node := range pageData.Nodes {
		if node.PeerID != peerID {
			continue
		}
		peers := node.Peers
		if peers == nil {
			peers = []*models.ClientCLPageDataNodePeers{}
		}
		writeCLClientsJSON(w, "node peers", &models.ClientsCLNodePeersData{PeerID: peerID, Peers: peers})
		return
	}

	writeJSONError(w, "Node not found", http.StatusNotFound)
}

// getCLClientsDataForRequest applies the call rate limit and returns the cached page
// model. On failure the error response is already written and false is returned.
func getCLClientsDataForRequest(w http.ResponseWriter, r *http.Request) (*models.ClientsCLPageData, bool) {
	pageError := services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	var pageData *models.ClientsCLPageData
	if pageError == nil {
		pageData, pageError = getCLClientsPageData()
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return nil, false
	}
	return pageData, true
}

// writeCLClientsJSON encodes value as the JSON response body.
func writeCLClientsJSON(w http.ResponseWriter, name string, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		logrus.WithError(err).Errorf("error encoding consensus clients %v data", name)
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}
