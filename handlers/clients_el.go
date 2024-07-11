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
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// ClientsEl will return the main "clients" page using a go template
func ClientsEl(w http.ResponseWriter, r *http.Request) {
	var clientsTemplateFiles = append(layoutTemplateFiles,
		"clients/clients_el.html",
	)

	var pageTemplate = templates.GetTemplate(clientsTemplateFiles...)
	data := InitPageData(w, r, "clients/execution", "/clients/execution", "Execution clients", clientsTemplateFiles)

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getELClientsPageData()
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "clients_el.go", "Execution clients", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getELClientsPageData() (*models.ClientsELPageData, error) {
	pageData := &models.ClientsELPageData{}
	pageCacheKey := "clients/execution"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildELClientsPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ClientsELPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildELPeerMapData() *models.ClientELPageDataPeerMap {
	peerMap := &models.ClientELPageDataPeerMap{
		ClientPageDataMapNode: []*models.ClientELPageDataPeerMapNode{},
		ClientDataMapEdges:    []*models.ClientELDataMapPeerMapEdge{},
	}

	nodes := make(map[string]*models.ClientELPageDataPeerMapNode)
	edges := make(map[string]*models.ClientELDataMapPeerMapEdge)

	for _, client := range services.GlobalBeaconService.GetExecutionClients() {
		nodeInfo := client.GetNodeInfo()
		peerID := nodeInfo.ID
		if _, ok := nodes[peerID]; !ok {
			node := models.ClientELPageDataPeerMapNode{
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

	for _, client := range services.GlobalBeaconService.GetExecutionClients() {
		nodeInfo := client.GetNodeInfo()
		peerId := nodeInfo.ID
		peers := client.GetNodePeers()
		for _, peer := range peers {
			peerId := peerId
			// Check if the PeerId is already in the nodes map, if not add it as an "external" node
			if _, ok := nodes[peer.ID]; !ok {
				node := models.ClientELPageDataPeerMapNode{
					ID:    peer.ID,
					Label: fmt.Sprintf("%s...%s", peer.ID[0:5], peer.ID[len(peer.ID)-5:]),
					Group: "external",
					Image: fmt.Sprintf("/identicon?key=%s", peer.ID),
					Shape: "circularImage",
				}
				nodes[peer.ID] = &node
				peerMap.ClientPageDataMapNode = append(peerMap.ClientPageDataMapNode, &node)
			}

			// Deduplicate edges. When adding an edge, we index by sorted peer IDs.
			sortedPeerIds := []string{peerId, peer.ID}
			sort.Strings(sortedPeerIds)
			idx := strings.Join(sortedPeerIds, "-")

			// Increase value based on peer count
			p1 := nodes[peer.ID]
			p1.Value++
			nodes[peer.ID] = p1
			p2 := nodes[peerId]
			p2.Value++

			if _, ok := edges[idx]; !ok {
				edge := models.ClientELDataMapPeerMapEdge{}
				if nodes[peer.ID].Group == "external" {
					edge.Dashes = true
				}
				if peer.Network.Inbound {
					edge.From = peer.ID
					edge.To = peerId
				} else {
					edge.From = peerId
					edge.To = peer.ID
				}
				edges[idx] = &edge
				peerMap.ClientDataMapEdges = append(peerMap.ClientDataMapEdges, &edge)
			}
		}
	}

	return peerMap
}

func buildELClientsPageData() (*models.ClientsELPageData, time.Duration) {
	logrus.Debugf("clients page called")
	pageData := &models.ClientsELPageData{
		Clients: []*models.ClientsELPageDataClient{},
		PeerMap: buildELPeerMapData(),
	}
	cacheTime := time.Duration(utils.Config.Chain.Config.SecondsPerSlot) * time.Second

	aliases := map[string]string{}
	for _, client := range services.GlobalBeaconService.GetExecutionClients() {

		aliases[client.GetNodeInfo().ID] = client.GetName()
	}

	for _, client := range services.GlobalBeaconService.GetExecutionClients() {
		lastHeadSlot, lastHeadRoot, clientRefresh := client.GetLastHead()
		if lastHeadSlot < 0 {
			lastHeadSlot = 0
		}

		peers := client.GetNodePeers()
		resPeers := []*models.ClientELPageDataClientPeers{}

		var inPeerCount, outPeerCount uint32
		for _, peer := range peers {
			peerAlias := peer.ID
			peerType := "external"
			if alias, ok := aliases[peer.ID]; ok {
				peerAlias = alias
				peerType = "internal"
			}
			direction := "outbound"
			if peer.Network.Inbound {
				direction = "inbound"
			}
			resPeers = append(resPeers, &models.ClientELPageDataClientPeers{
				ID:        peer.ID,
				State:     peer.Name,
				Direction: direction,
				Alias:     peerAlias,
				Type:      peerType,
			})

			if direction == "inbound" {
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

		nodeInfo := client.GetNodeInfo()

		resClient := &models.ClientsELPageDataClient{
			Index:                int(client.GetIndex()) + 1,
			Name:                 client.GetName(),
			Version:              client.GetVersion(),
			Peers:                resPeers,
			PeerID:               nodeInfo.ID,
			PeersInboundCounter:  inPeerCount,
			PeersOutboundCounter: outPeerCount,
			HeadSlot:             uint64(lastHeadSlot),
			HeadRoot:             lastHeadRoot,
			Status:               client.GetStatus(),
			LastRefresh:          clientRefresh,
			LastError:            client.GetLastClientError(),
		}
		pageData.Clients = append(pageData.Clients, resClient)

	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	return pageData, cacheTime
}
