package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// ClientsCL will return the main "clients" page using a go template
func ClientsCL(w http.ResponseWriter, r *http.Request) {
	var clientsTemplateFiles = append(layoutTemplateFiles,
		"clients/clients_cl.html",
	)

	var pageTemplate = templates.GetTemplate(clientsTemplateFiles...)
	data := InitPageData(w, r, "clients/consensus", "/clients/consensus", "Consensus clients", clientsTemplateFiles)

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getCLClientsPageData()
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "clients_cl.go", "Consensus clients", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getCLClientsPageData() (*models.ClientsCLPageData, error) {
	pageData := &models.ClientsCLPageData{}
	pageCacheKey := "clients/consensus"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildCLClientsPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ClientsCLPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildCLPeerMapData() *models.ClientCLPageDataPeerMap {
	peerMap := &models.ClientCLPageDataPeerMap{
		ClientPageDataMapNode: []*models.ClientCLPageDataPeerMapNode{},
		ClientDataMapEdges:    []*models.ClientCLDataMapPeerMapEdge{},
	}

	nodes := make(map[string]*models.ClientCLPageDataPeerMapNode)
	edges := make(map[string]*models.ClientCLDataMapPeerMapEdge)

	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		peerID := client.GetPeerID()
		if _, ok := nodes[peerID]; !ok {
			node := models.ClientCLPageDataPeerMapNode{
				ID:    peerID,
				Label: client.GetName(),
				Group: "internal",
				Image: fmt.Sprintf("/identicon?key=%s", peerID),
				Shape: "circularImage",
			}
			nodes[peerID] = &node
			peerMap.ClientPageDataMapNode = append(peerMap.ClientPageDataMapNode, &node)
		}
	}

	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		peerId := client.GetPeerID()
		peers := client.GetNodePeers()
		for _, peer := range peers {
			peerId := peerId
			// Check if the PeerId is already in the nodes map, if not add it as an "external" node
			if _, ok := nodes[peer.PeerID]; !ok {
				node := models.ClientCLPageDataPeerMapNode{
					ID:    peer.PeerID,
					Label: fmt.Sprintf("%s...%s", peer.PeerID[0:5], peer.PeerID[len(peer.PeerID)-5:]),
					Group: "external",
					Image: fmt.Sprintf("/identicon?key=%s", peer.PeerID),
					Shape: "circularImage",
				}
				nodes[peer.PeerID] = &node
				peerMap.ClientPageDataMapNode = append(peerMap.ClientPageDataMapNode, &node)
			}

			// Deduplicate edges. When adding an edge, we index by sorted peer IDs.
			sortedPeerIds := []string{peerId, peer.PeerID}
			sort.Strings(sortedPeerIds)
			idx := strings.Join(sortedPeerIds, "-")

			// Increase value based on peer count
			p1 := nodes[peer.PeerID]
			p1.Value++
			nodes[peer.PeerID] = p1
			p2 := nodes[peerId]
			p2.Value++

			if _, ok := edges[idx]; !ok {
				edge := models.ClientCLDataMapPeerMapEdge{}
				if nodes[peer.PeerID].Group == "external" {
					edge.Dashes = true
				}
				if peer.Direction == "inbound" {
					edge.From = peer.PeerID
					edge.To = peerId
				} else {
					edge.From = peerId
					edge.To = peer.PeerID
				}
				edges[idx] = &edge
				peerMap.ClientDataMapEdges = append(peerMap.ClientDataMapEdges, &edge)
			}
		}
	}

	return peerMap
}

func buildCLClientsPageData() (*models.ClientsCLPageData, time.Duration) {
	logrus.Debugf("clients page called")
	pageData := &models.ClientsCLPageData{
		Clients: []*models.ClientsCLPageDataClient{},
		PeerMap: buildCLPeerMapData(),
	}
	chainState := services.GlobalBeaconService.GetChainState()

	var cacheTime time.Duration
	if specs := chainState.GetSpecs(); specs != nil {
		cacheTime = specs.SecondsPerSlot
	} else {
		cacheTime = 1 * time.Second
	}

	aliases := map[string]string{}
	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		aliases[client.GetPeerID()] = client.GetName()
	}

	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		lastHeadSlot, lastHeadRoot := client.GetLastHead()

		peers := client.GetNodePeers()
		resPeers := []*models.ClientCLPageDataClientPeers{}

		var inPeerCount, outPeerCount uint32
		for _, peer := range peers {
			peerAlias := peer.PeerID
			peerType := "external"
			if alias, ok := aliases[peer.PeerID]; ok {
				peerAlias = alias
				peerType = "internal"
			}
			resPeers = append(resPeers, &models.ClientCLPageDataClientPeers{
				ID:        peer.PeerID,
				State:     peer.State,
				Direction: peer.Direction,
				Alias:     peerAlias,
				Type:      peerType,
			})

			if peer.Direction == "inbound" {
				inPeerCount++
			} else {
				outPeerCount++
			}
		}
		sort.Slice(resPeers, func(i, j int) bool {
			if resPeers[i].Type == resPeers[j].Type {
				return resPeers[i].Alias < resPeers[j].Alias
			}
			return resPeers[i].Type > resPeers[j].Type
		})

		resClient := &models.ClientsCLPageDataClient{
			Index:                int(client.GetIndex()) + 1,
			Name:                 client.GetName(),
			Version:              client.GetVersion(),
			Peers:                resPeers,
			PeerID:               client.GetPeerID(),
			PeersInboundCounter:  inPeerCount,
			PeersOutboundCounter: outPeerCount,
			HeadSlot:             uint64(lastHeadSlot),
			HeadRoot:             lastHeadRoot[:],
			Status:               client.GetStatus().String(),
			LastRefresh:          client.GetLastEventTime(),
		}

		lastError := client.GetLastClientError()
		if lastError != nil {
			resClient.LastError = lastError.Error()
		}

		pageData.Clients = append(pageData.Clients, resClient)

	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	return pageData, cacheTime
}
