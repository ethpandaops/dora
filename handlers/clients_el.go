package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/p2p/enode"
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

func buildELPeerMapData(parseEnodeRecord func(enrStr string) *enode.Node) *models.ClientELPageDataPeerMap {
	peerMap := &models.ClientELPageDataPeerMap{
		ClientPageDataMapNode: []*models.ClientELPageDataPeerMapNode{},
		ClientDataMapEdges:    []*models.ClientELDataMapPeerMapEdge{},
	}

	nodes := make(map[string]*models.ClientELPageDataPeerMapNode)
	edges := make(map[string]*models.ClientELDataMapPeerMapEdge)

	for _, client := range services.GlobalBeaconService.GetExecutionClients() {
		nodeInfo := client.GetNodeInfo()
		peerID := fmt.Sprintf("unknown-%v", client.GetIndex())
		var en *enode.Node
		if nodeInfo != nil && nodeInfo.Enode != "" {
			en = parseEnodeRecord(nodeInfo.Enode)
			if en != nil {
				peerID = en.ID().String()
			}

		}

		if _, ok := nodes[peerID]; !ok {
			node := models.ClientELPageDataPeerMapNode{
				ID:    peerID,
				Label: client.GetName(),
				Group: "internal",
			}
			nodes[peerID] = &node
			peerMap.ClientPageDataMapNode = append(peerMap.ClientPageDataMapNode, &node)
		}
	}

	for _, client := range services.GlobalBeaconService.GetExecutionClients() {
		nodeInfo := client.GetNodeInfo()
		nodeID := "unknown"
		if nodeInfo != nil {
			en := parseEnodeRecord(nodeInfo.Enode)
			if en != nil {
				nodeID = en.ID().String()
			}
		}
		peers := client.GetNodePeers()
		for idx, peer := range peers {
			en := parseEnodeRecord(peer.Enode)
			peerID := fmt.Sprintf("unknown-peer-%v-%v", client.GetIndex(), idx)
			if en != nil {
				peerID = en.ID().String()
			}

			// Check if the PeerId is already in the nodes map, if not add it as an "external" node
			if _, ok := nodes[peerID]; !ok {
				node := models.ClientELPageDataPeerMapNode{
					ID:    peerID,
					Label: fmt.Sprintf("%s...%s", peerID[0:5], peerID[len(peerID)-5:]),
					Group: "external",
				}
				nodes[peerID] = &node
				peerMap.ClientPageDataMapNode = append(peerMap.ClientPageDataMapNode, &node)
			}

			// Deduplicate edges. When adding an edge, we index by sorted peer IDs.
			sortedPeerIds := []string{nodeID, peerID}
			sort.Strings(sortedPeerIds)
			idx := strings.Join(sortedPeerIds, "-")

			// Increase value based on peer count
			p1 := nodes[peerID]
			p1.Value++
			nodes[peerID] = p1
			p2 := nodes[nodeID]
			p2.Value++

			if _, ok := edges[idx]; !ok {
				edge := models.ClientELDataMapPeerMapEdge{
					Interaction: nodes[peerID].Group,
				}
				if peer.Network.Inbound {
					edge.From = peerID
					edge.To = nodeID
				} else {
					edge.From = nodeID
					edge.To = peerID
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

	enodeMap := map[string]*enode.Node{}

	parseEnodeRecord := func(enodeStr string) *enode.Node {
		if enr, ok := enodeMap[enodeStr]; ok {
			return enr
		}
		rec, err := enode.ParseV4(enodeStr)
		enodeMap[enodeStr] = rec
		if err != nil {
			logrus.WithFields(logrus.Fields{"enr": enodeStr}).Warn("failed to decode enode. ", err)
			return nil
		}
		return rec
	}

	pageData := &models.ClientsELPageData{
		Clients:                []*models.ClientsELPageDataClient{},
		PeerMap:                buildELPeerMapData(parseEnodeRecord),
		ShowSensitivePeerInfos: utils.Config.Frontend.ShowSensitivePeerInfos,
		Nodes:                  map[string]*models.ClientsELPageDataNode{},
	}
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	cacheTime := specs.SecondsPerSlot

	aliases := map[string]string{}
	for _, client := range services.GlobalBeaconService.GetExecutionClients() {

		nodeInfo := client.GetNodeInfo()
		if nodeInfo != nil && nodeInfo.Enode != "" {
			en := parseEnodeRecord(nodeInfo.Enode)
			nodeID := "unknown"
			if en != nil {
				nodeID = en.ID().String()
			}

			aliases[nodeID] = client.GetName()
		}
	}

	for _, client := range services.GlobalBeaconService.GetExecutionClients() {
		lastHeadSlot, lastHeadRoot := client.GetLastHead()

		peers := client.GetNodePeers()
		resPeers := []*models.ClientELPageDataNodePeers{}

		var inPeerCount, outPeerCount uint32
		for _, peer := range peers {
			en := parseEnodeRecord(peer.Enode)
			peerID := "unknown"
			enoderaw := "unknown"
			if en != nil {
				peerID = en.ID().String()
				enoderaw = en.String()
			}
			peerAlias := peerID
			peerType := "external"
			if alias, ok := aliases[peerID]; ok {
				peerAlias = alias
				peerType = "internal"
			}
			direction := "outbound"
			if peer.Network.Inbound {
				direction = "inbound"
			}

			resPeers = append(resPeers, &models.ClientELPageDataNodePeers{
				ID:        peerID,
				State:     peer.Name,
				Direction: direction,
				Alias:     peerAlias,
				Name:      peer.Name,
				Enode:     enoderaw,
				Caps:      peer.Caps,
				Protocols: peer.Protocols,
				Type:      peerType,
			})

			if direction == "inbound" {
				inPeerCount++
			} else if direction == "outbound" {
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

		peerID := "unknown"
		peerName := "unknown"
		enoderaw := "unknown"
		ipAddr := "unknown"
		listenAddr := "unknown"
		if nodeInfo != nil {
			en := parseEnodeRecord(nodeInfo.Enode)
			if en != nil {
				peerID = en.ID().String()
				enoderaw = en.String()
			}
			peerName = nodeInfo.Name
			ipAddr = nodeInfo.IP
			listenAddr = nodeInfo.ListenAddr
		}

		resClient := &models.ClientsELPageDataClient{
			Index:                int(client.GetIndex()) + 1,
			Name:                 client.GetName(),
			Version:              client.GetVersion(),
			DidFetchPeers:        client.DidFetchPeers(),
			PeerCount:            uint32(len(peers)),
			PeersInboundCounter:  inPeerCount,
			PeersOutboundCounter: outPeerCount,
			HeadSlot:             uint64(lastHeadSlot),
			HeadRoot:             lastHeadRoot[:],
			Status:               client.GetStatus().String(),
			LastRefresh:          client.GetLastEventTime(),
			PeerID:               peerID,
		}

		resNode := &models.ClientsELPageDataNode{
			Name:          client.GetName(),
			Version:       client.GetVersion(),
			Status:        client.GetStatus().String(),
			Peers:         resPeers,
			PeerID:        peerID,
			PeerName:      peerName,
			DidFetchPeers: client.DidFetchPeers(),
		}

		if pageData.ShowSensitivePeerInfos {
			resNode.Enode = enoderaw
			resNode.IPAddr = ipAddr
			resNode.ListenAddr = listenAddr
		}

		lastError := client.GetLastClientError()
		if lastError != nil {
			resClient.LastError = lastError.Error()
		}

		pageData.Clients = append(pageData.Clients, resClient)
		pageData.Nodes[peerID] = resNode
	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	return pageData, cacheTime
}
