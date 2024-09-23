package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/p2p/enr"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
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
		id := client.GetNodeIdentity()
		if id == nil {
			continue
		}
		peerID := id.PeerID
		if _, ok := nodes[peerID]; !ok {
			node := models.ClientCLPageDataPeerMapNode{
				ID:    peerID,
				Label: client.GetName(),
				Group: "internal",
			}
			nodes[peerID] = &node
			peerMap.ClientPageDataMapNode = append(peerMap.ClientPageDataMapNode, &node)
		}
	}

	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		id := client.GetNodeIdentity()
		if id == nil {
			continue
		}
		peerId := id.PeerID
		peers := client.GetNodePeers()
		for _, peer := range peers {
			peerId := peerId
			// Check if the PeerId is already in the nodes map, if not add it as an "external" node
			if _, ok := nodes[peer.PeerID]; !ok {
				node := models.ClientCLPageDataPeerMapNode{
					ID:    peer.PeerID,
					Label: fmt.Sprintf("%s...%s", peer.PeerID[0:5], peer.PeerID[len(peer.PeerID)-5:]),
					Group: "external",
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
				edge := models.ClientCLDataMapPeerMapEdge{
					Interaction: nodes[peer.PeerID].Group,
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
		Clients:                []*models.ClientsCLPageDataClient{},
		PeerMap:                buildCLPeerMapData(),
		ShowSensitivePeerInfos: utils.Config.Frontend.ShowSensitivePeerInfos,
		ShowPeerDASInfos:       utils.Config.Frontend.ShowPeerDASInfos,
		PeerDASInfos: &models.ClientCLPagePeerDAS{
			Warnings: models.ClientCLPageDataPeerDASWarnings{
				MissingENRsPeers:       []string{},
				MissingCSCFromENRPeers: []string{},
				EmptyColumns:           []uint64{},
			},
		},
		Nodes: make(map[string]*models.ClientCLNode),
	}
	chainState := services.GlobalBeaconService.GetChainState()

	var cacheTime time.Duration
	specs := chainState.GetSpecs()
	if specs != nil {
		cacheTime = specs.SecondsPerSlot
	} else {
		cacheTime = 1 * time.Second
	}

	aliases := map[string]string{}
	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		id := client.GetNodeIdentity()
		if id == nil {
			continue
		}
		aliases[id.PeerID] = client.GetName()
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
				ID:                 peer.PeerID,
				State:              peer.State,
				Direction:          peer.Direction,
				Alias:              peerAlias,
				Type:               peerType,
				ENR:                peer.Enr,
				LastSeenP2PAddress: peer.LastSeenP2PAddress,
			})

			// Add peer to global nodes map
			node, ok := pageData.Nodes[peer.PeerID]
			if !ok {
				pageData.Nodes[peer.PeerID] = &models.ClientCLNode{
					PeerID: peer.PeerID,
					Alias:  peerAlias,
					Type:   peerType,
					ENR:    peer.Enr,
				}
			} else {
				if node.ENR == "" && peer.Enr != "" {
					node.ENR = peer.Enr
				}
			}

			// Increase peer direction counter
			if peer.Direction == "inbound" {
				inPeerCount++
			} else {
				outPeerCount++
			}
		}

		// Sort peers by type and alias
		sort.Slice(resPeers, func(i, j int) bool {
			if resPeers[i].Type == resPeers[j].Type {
				return resPeers[i].Alias < resPeers[j].Alias
			}
			return resPeers[i].Type > resPeers[j].Type
		})

		id := client.GetNodeIdentity()
		if id == nil {
			continue
		}

		resClient := &models.ClientsCLPageDataClient{
			Index:                 int(client.GetIndex()) + 1,
			Name:                  client.GetName(),
			Version:               client.GetVersion(),
			Peers:                 resPeers,
			PeerID:                id.PeerID,
			ENR:                   id.Enr,
			P2PAddresses:          id.P2PAddresses,
			DisoveryAddresses:     id.DiscoveryAddresses,
			AttestationSubnetSubs: id.Metadata.Attnets,
			PeersInboundCounter:   inPeerCount,
			PeersOutboundCounter:  outPeerCount,
			HeadSlot:              uint64(lastHeadSlot),
			HeadRoot:              lastHeadRoot[:],
			Status:                client.GetStatus().String(),
			LastRefresh:           client.GetLastEventTime(),
		}

		// Add client to global nodes map
		node, ok := pageData.Nodes[id.PeerID]
		if !ok {
			pageData.Nodes[id.PeerID] = &models.ClientCLNode{
				PeerID: id.PeerID,
				Alias:  client.GetName(),
				Type:   "internal",
				ENR:    id.Enr,
			}
		} else {
			if node.ENR == "" && id.Enr != "" {
				node.ENR = id.Enr
			}
		}

		lastError := client.GetLastClientError()
		if lastError != nil {
			resClient.LastError = lastError.Error()
		}

		pageData.Clients = append(pageData.Clients, resClient)

	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	columnDistribution := make(map[uint64]map[string]bool)
	resultColumnDistribution := make(map[uint64][]string)

	// Verify and parse PeerDAS spec config
	if specs != nil {
		if specs.NumberOfColumns != nil {
			pageData.PeerDASInfos.NumberOfColumns = *specs.NumberOfColumns
		} else {
			pageData.PeerDASInfos.NumberOfColumns = 128
			logrus.Warnf("NUMBER_OF_COLUMNS is not defined in spec, defaulting to %d", pageData.PeerDASInfos.NumberOfColumns)
			pageData.PeerDASInfos.Warnings.MissingSpecValues = true
		}

		if specs.DataColumnSidecarSubnetCount != nil {
			pageData.PeerDASInfos.DataColumnSidecarSubnetCount = *specs.DataColumnSidecarSubnetCount
		} else {
			pageData.PeerDASInfos.DataColumnSidecarSubnetCount = 128
			logrus.Warnf("DATA_COLUMN_SIDECAR_SUBNET_COUNT is not defined in spec, defaulting to %d", pageData.PeerDASInfos.DataColumnSidecarSubnetCount)
			pageData.PeerDASInfos.Warnings.MissingSpecValues = true
		}

		if specs.CustodyRequirement != nil {
			pageData.PeerDASInfos.CustodyRequirement = *specs.CustodyRequirement
		} else {
			pageData.PeerDASInfos.CustodyRequirement = 4
			logrus.Warnf("CUSTODY_REQUIREMENT is not defined in spec, defaulting to %d", pageData.PeerDASInfos.CustodyRequirement)
			pageData.PeerDASInfos.Warnings.MissingSpecValues = true
		}
	}

	// Calculate additional fields for nodes: ENR key values, Node ID, Custody Columns, Custody Column Subnets
	for _, v := range pageData.Nodes {

		// Calculate K:V pairs for ENR
		if v.ENR != "" {
			rec, err := utils.DecodeENR(v.ENR)
			if err != nil {
				logrus.WithFields(logrus.Fields{"node": v.Alias, "enr": v.ENR}).Error("failed to decode enr. ", err)
				rec = &enr.Record{}
			}
			v.ENRKeyValues = utils.GetKeyValuesFromENR(rec)
		} else {
			pageData.PeerDASInfos.Warnings.MissingENRsPeers = append(pageData.PeerDASInfos.Warnings.MissingENRsPeers, v.PeerID)
		}

		// Calculate node ID
		nodeID, err := utils.ConvertPeerIDStringToEnodeID(v.PeerID)
		if err != nil {
			logrus.WithFields(logrus.Fields{"node": v.Alias, "peer_id": v.PeerID}).Error("failed to convert peer id to enode id. ", err)
		}
		v.NodeID = nodeID.String()

		custodySubnetCount := pageData.PeerDASInfos.CustodyRequirement

		if cscHex, ok := v.ENRKeyValues["csc"]; ok {
			val, err := strconv.ParseUint(cscHex.(string), 0, 64)
			if err != nil {
				logrus.WithFields(logrus.Fields{"node": v.Alias, "peer_id": v.PeerID, "csc": cscHex.(string)}).Error("failed to decode csc. ", err)
			} else {
				custodySubnetCount = val
			}
		} else {
			pageData.PeerDASInfos.Warnings.MissingCSCFromENRPeers = append(pageData.PeerDASInfos.Warnings.MissingCSCFromENRPeers, v.PeerID)
		}

		// Calculate custody columns and subnets for peer DAS
		resColumns, err := utils.CustodyColumnsSlice(nodeID, custodySubnetCount, pageData.PeerDASInfos.NumberOfColumns, pageData.PeerDASInfos.DataColumnSidecarSubnetCount)
		if err != nil {
			logrus.WithFields(logrus.Fields{"node": v.Alias, "node_id": nodeID}).Error("failed to get custody columns. ", err)
		}

		resSubnets, err := utils.CustodyColumnSubnetsSlice(nodeID, custodySubnetCount, pageData.PeerDASInfos.DataColumnSidecarSubnetCount)
		if err != nil {
			logrus.WithFields(logrus.Fields{"client": v.Alias, "node_id": nodeID}).Error("failed to get custody column subnets. ", err)
		}

		// Transform the custody columns to a map for easier access
		for _, idx := range resColumns {
			if _, ok := columnDistribution[idx]; !ok {
				columnDistribution[idx] = make(map[string]bool)
			}
			columnDistribution[idx][v.PeerID] = true
		}

		peerDASInfo := models.ClientCLPageDataPeerDAS{
			CustodyColumns:       resColumns,
			CustodyColumnSubnets: resSubnets,
			CustodySubnetCount:   custodySubnetCount,
		}
		v.PeerDAS = &peerDASInfo
	}

	// Transform the column distribution to a slice
	for k, v := range columnDistribution {
		for key := range v {
			if _, ok := resultColumnDistribution[k]; !ok {
				resultColumnDistribution[k] = []string{}
			}
			resultColumnDistribution[k] = append(resultColumnDistribution[k], key)
		}

		// Sort the peer IDs by type and alias
		sort.Slice(resultColumnDistribution[k], func(i, j int) bool {
			pA := resultColumnDistribution[k][i]
			pB := resultColumnDistribution[k][j]
			if pageData.Nodes[pA].Type == pageData.Nodes[pB].Type {
				return pageData.Nodes[pA].Alias < pageData.Nodes[pB].Alias
			}
			return pageData.Nodes[pA].Type > pageData.Nodes[pB].Type
		})
	}

	// Check for empty columns
	for i := uint64(0); i < pageData.PeerDASInfos.NumberOfColumns; i++ {
		if _, ok := resultColumnDistribution[i]; !ok {
			pageData.PeerDASInfos.Warnings.EmptyColumns = append(pageData.PeerDASInfos.Warnings.EmptyColumns, i)
		}
	}

	pageData.PeerDASInfos.TotalRows = int(pageData.PeerDASInfos.NumberOfColumns) / 32
	pageData.PeerDASInfos.ColumnDistribution = resultColumnDistribution

	return pageData, cacheTime
}
