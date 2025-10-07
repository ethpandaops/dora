package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/ethereum/go-ethereum/p2p/enr"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/consensus/rpc"
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

	// Get sorting parameter
	urlArgs := r.URL.Query()
	var sortOrder string
	if urlArgs.Has("o") {
		sortOrder = urlArgs.Get("o")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getCLClientsPageData(sortOrder)
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

func getCLClientsPageData(sortOrder string) (*models.ClientsCLPageData, error) {
	pageData := &models.ClientsCLPageData{}
	pageCacheKey := fmt.Sprintf("clients/consensus/%s", sortOrder)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildCLClientsPageData(sortOrder)
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

		var peerId string
		if id == nil {
			peerId = fmt.Sprintf("unknown-%d", client.GetIndex())
		} else {
			peerId = id.PeerID
		}

		if _, ok := nodes[peerId]; !ok {
			node := models.ClientCLPageDataPeerMapNode{
				ID:    peerId,
				Label: client.GetName(),
				Group: "internal",
			}
			nodes[peerId] = &node
			peerMap.ClientPageDataMapNode = append(peerMap.ClientPageDataMapNode, &node)
		}
	}

	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		id := client.GetNodeIdentity()

		var peerId string
		if id == nil {
			peerId = fmt.Sprintf("unknown-%d", client.GetIndex())
		} else {
			peerId = id.PeerID
		}

		peers := client.GetNodePeers()
		for _, peer := range peers {
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

func buildCLClientsPageData(sortOrder string) (*models.ClientsCLPageData, time.Duration) {
	logrus.Debugf("clients page called")
	pageData := &models.ClientsCLPageData{
		Clients:                []*models.ClientsCLPageDataClient{},
		PeerMap:                buildCLPeerMapData(),
		ShowSensitivePeerInfos: utils.Config.Frontend.ShowSensitivePeerInfos,
		ShowPeerDASInfos:       utils.Config.Frontend.ShowPeerDASInfos,
		PeerDASInfos: &models.ClientCLPagePeerDAS{
			Warnings: models.ClientCLPageDataPeerDASWarnings{
				MissingENRsPeers:       []string{},
				MissingCGCFromENRPeers: []string{},
				EmptyColumns:           []uint64{},
			},
		},
		Nodes: make(map[string]*models.ClientCLPageDataNode),

		// DAS Guardian configuration (check enabled by default, mass scan disabled by default)
		DisableDasGuardianCheck:   utils.Config.Frontend.DisableDasGuardianCheck,
		EnableDasGuardianMassScan: utils.Config.Frontend.EnableDasGuardianMassScan,
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

	enrMap := map[string]*enr.Record{}

	parseEnrRecord := func(enrStr string) *enr.Record {
		if enr, ok := enrMap[enrStr]; ok {
			return enr
		}
		rec, err := utils.DecodeENR(enrStr)
		enrMap[enrStr] = rec
		if err != nil {
			logrus.WithFields(logrus.Fields{"enr": enrStr}).Warn("failed to decode enr. ", err)
			return nil
		}
		return rec
	}

	getEnrValues := func(values map[string]interface{}) []*models.ClientCLPageDataNodeENRValue {
		enrValues := []*models.ClientCLPageDataNodeENRValue{}
		for k, v := range values {
			enrValues = append(enrValues, &models.ClientCLPageDataNodeENRValue{
				Key:   k,
				Value: v,
			})
		}
		sort.Slice(enrValues, func(i, j int) bool {
			return enrValues[i].Key < enrValues[j].Key
		})
		return enrValues
	}

	getMetadataValuesFromIdentity := func(nodeIdentity *rpc.NodeIdentity) []*models.ClientCLPageDataNodeENRValue {
		metadataValues := []*models.ClientCLPageDataNodeENRValue{}
		if nodeIdentity != nil {
			// Add attnets if present
			if nodeIdentity.Metadata.Attnets != "" {
				metadataValues = append(metadataValues, &models.ClientCLPageDataNodeENRValue{
					Key:   "attnets",
					Value: nodeIdentity.Metadata.Attnets,
				})
			}
			// Add custody_group_count if present (MetadataV3 field for Fulu)
			if nodeIdentity.Metadata.CustodyGroupCount != nil {
				metadataValues = append(metadataValues, &models.ClientCLPageDataNodeENRValue{
					Key:   "custody_group_count",
					Value: fmt.Sprintf("%v", nodeIdentity.Metadata.CustodyGroupCount),
				})
			}
			// Add seq_number if present
			if nodeIdentity.Metadata.SeqNumber != nil {
				metadataValues = append(metadataValues, &models.ClientCLPageDataNodeENRValue{
					Key:   "seq_number",
					Value: fmt.Sprintf("%v", nodeIdentity.Metadata.SeqNumber),
				})
			}
			// Add syncnets if present
			if nodeIdentity.Metadata.Syncnets != "" {
				metadataValues = append(metadataValues, &models.ClientCLPageDataNodeENRValue{
					Key:   "syncnets",
					Value: nodeIdentity.Metadata.Syncnets,
				})
			}
		}
		sort.Slice(metadataValues, func(i, j int) bool {
			return metadataValues[i].Key < metadataValues[j].Key
		})
		return metadataValues
	}

	// Add peer node to global nodes map
	addPeerNode := func(peer *v1.Peer) {
		node, ok := pageData.Nodes[peer.PeerID]
		if !ok {
			peerAlias := peer.PeerID
			peerType := "external"
			if alias, ok := aliases[peer.PeerID]; ok {
				peerAlias = alias
				peerType = "internal"
			}

			node = &models.ClientCLPageDataNode{
				PeerID: peer.PeerID,
				Alias:  peerAlias,
				Type:   peerType,
			}
			pageData.Nodes[peer.PeerID] = node
		}

		if node.ENR == "" && peer.Enr != "" {
			node.ENR = peer.Enr
		} else if node.ENR != "" && peer.Enr != "" {
			// Need to compare `seq` field from ENRs and only store highest
			nodeENR := parseEnrRecord(node.ENR)
			peerENR := parseEnrRecord(peer.Enr)
			if nodeENR != nil && peerENR != nil && peerENR.Seq() > nodeENR.Seq() {
				node.ENR = peer.Enr // peerENR has higher sequence number, so override.
			}
		}
	}

	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		lastHeadSlot, lastHeadRoot := client.GetLastHead()

		id := client.GetNodeIdentity()
		var peerId string
		if id == nil {
			peerId = fmt.Sprintf("unknown-%d", client.GetIndex())
		} else {
			peerId = id.PeerID
		}

		// Add client to global nodes map
		node, ok := pageData.Nodes[peerId]
		if !ok {
			node = &models.ClientCLPageDataNode{
				PeerID: peerId,
				Alias:  client.GetName(),
				Type:   "internal",
			}
			pageData.Nodes[peerId] = node
		}

		if id != nil {
			if node.ENR == "" {
				node.ENR = id.Enr
			} else if node.ENR != "" {
				// Need to compare `seq` field from ENRs and only store highest
				nodeENR := parseEnrRecord(node.ENR)
				idENR := parseEnrRecord(id.Enr)
				if nodeENR != nil && idENR != nil && idENR.Seq() > nodeENR.Seq() {
					node.ENR = id.Enr // idENR has higher sequence number, so override.
				}
			}

			// Add metadata information
			if id.Metadata.Attnets != "" || id.Metadata.Syncnets != "" || id.Metadata.SeqNumber != nil || id.Metadata.CustodyGroupCount != nil {
				seqNumber := ""
				if id.Metadata.SeqNumber != nil {
					seqNumber = fmt.Sprintf("%v", id.Metadata.SeqNumber)
				}
				custodyGroupCount := ""
				if id.Metadata.CustodyGroupCount != nil {
					custodyGroupCount = fmt.Sprintf("%v", id.Metadata.CustodyGroupCount)
				}
				node.Metadata = &models.ClientCLPageDataNodeMetadata{
					Attnets:           id.Metadata.Attnets,
					Syncnets:          id.Metadata.Syncnets,
					SeqNumber:         seqNumber,
					CustodyGroupCount: custodyGroupCount,
				}
			}
		}

		peers := client.GetNodePeers()
		resPeers := []*models.ClientCLPageDataNodePeers{}

		var inPeerCount, outPeerCount uint32
		for _, peer := range peers {
			addPeerNode(peer)

			peerNode := &models.ClientCLPageDataNodePeers{
				PeerID:             peer.PeerID,
				State:              peer.State,
				Direction:          peer.Direction,
				LastSeenP2PAddress: peer.LastSeenP2PAddress,
			}

			if pageData.ShowSensitivePeerInfos {
				peerENRKeyValues := map[string]interface{}{}
				if peer.Enr != "" {
					rec := parseEnrRecord(peer.Enr)
					if rec != nil {
						peerENRKeyValues = utils.GetKeyValuesFromENR(rec)
					}
				}

				peerNode.ENR = peer.Enr
				peerNode.ENRKeyValues = getEnrValues(peerENRKeyValues)
			}

			resPeers = append(resPeers, peerNode)

			// Increase peer direction counter
			if peer.Direction == "inbound" {
				inPeerCount++
			} else {
				outPeerCount++
			}
		}

		// Sort peers by type and alias
		sort.Slice(resPeers, func(i, j int) bool {
			peerA := pageData.Nodes[resPeers[i].PeerID]
			peerB := pageData.Nodes[resPeers[j].PeerID]
			if peerA.Type == peerB.Type {
				return peerA.Alias < peerB.Alias
			}
			return peerA.Type > peerB.Type
		})

		node.Peers = resPeers

		resClient := &models.ClientsCLPageDataClient{
			Index:                int(client.GetIndex()) + 1,
			Name:                 client.GetName(),
			Version:              client.GetVersion(),
			PeerID:               peerId,
			NodeENR:              node.ENR,
			PeerCount:            inPeerCount + outPeerCount,
			PeersInboundCounter:  inPeerCount,
			PeersOutboundCounter: outPeerCount,
			HeadSlot:             uint64(lastHeadSlot),
			HeadRoot:             lastHeadRoot[:],
			Status:               client.GetStatus().String(),
			LastRefresh:          client.GetLastEventTime(),
			SpecWarnings:         client.GetSpecWarnings(),
			ClientSpecs:          client.GetSpecs(),
		}

		lastError := client.GetLastClientError()
		if lastError != nil {
			resClient.LastError = lastError.Error()
		}

		pageData.Clients = append(pageData.Clients, resClient)

	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	// Add peer in/out infos to global nodes map
	for _, edge := range pageData.PeerMap.ClientDataMapEdges {
		pageData.Nodes[edge.From].PeersOut = append(pageData.Nodes[edge.From].PeersOut, edge.To)
		pageData.Nodes[edge.To].PeersIn = append(pageData.Nodes[edge.To].PeersIn, edge.From)
	}

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
			pageData.PeerDASInfos.Warnings.HasWarnings = true
		}

		if specs.DataColumnSidecarSubnetCount != nil {
			pageData.PeerDASInfos.DataColumnSidecarSubnetCount = *specs.DataColumnSidecarSubnetCount
		} else {
			pageData.PeerDASInfos.DataColumnSidecarSubnetCount = 128
			logrus.Warnf("DATA_COLUMN_SIDECAR_SUBNET_COUNT is not defined in spec, defaulting to %d", pageData.PeerDASInfos.DataColumnSidecarSubnetCount)
			pageData.PeerDASInfos.Warnings.MissingSpecValues = true
			pageData.PeerDASInfos.Warnings.HasWarnings = true
		}

		if specs.CustodyRequirement != nil {
			pageData.PeerDASInfos.CustodyRequirement = *specs.CustodyRequirement
		} else {
			pageData.PeerDASInfos.CustodyRequirement = 4
			logrus.Warnf("CUSTODY_REQUIREMENT is not defined in spec, defaulting to %d", pageData.PeerDASInfos.CustodyRequirement)
			pageData.PeerDASInfos.Warnings.MissingSpecValues = true
			pageData.PeerDASInfos.Warnings.HasWarnings = true
		}
	}

	// Calculate additional fields for nodes: ENR key values, Node ID, Custody Columns, Custody Column Subnets
	for _, v := range pageData.Nodes {

		enrValues := map[string]interface{}{}

		// Calculate K:V pairs for ENR
		if v.ENR != "" {
			rec := parseEnrRecord(v.ENR)
			if rec != nil {
				enrValues = utils.GetKeyValuesFromENR(rec)
			}
		} else {
			pageData.PeerDASInfos.Warnings.MissingENRsPeers = append(pageData.PeerDASInfos.Warnings.MissingENRsPeers, v.PeerID)
			pageData.PeerDASInfos.Warnings.HasWarnings = true
		}

		if pageData.ShowSensitivePeerInfos {
			v.ENRKeyValues = getEnrValues(enrValues)
			// Find the client for this node to get metadata
			for _, client := range services.GlobalBeaconService.GetConsensusClients() {
				if id := client.GetNodeIdentity(); id != nil && id.PeerID == v.PeerID {
					v.MetadataKeyValues = getMetadataValuesFromIdentity(id)
					break
				}
			}
		}

		// Calculate node ID
		nodeID, err := utils.ConvertPeerIDStringToEnodeID(v.PeerID)
		if err != nil {
			logrus.WithFields(logrus.Fields{"node": v.Alias, "peer_id": v.PeerID}).Error("failed to convert peer id to enode id. ", err)
		}
		v.NodeID = nodeID.String()

		custodySubnetCount := pageData.PeerDASInfos.CustodyRequirement

		if cgcHex, ok := enrValues["cgc"]; ok {
			val, err := strconv.ParseUint(cgcHex.(string), 0, 64)
			if err != nil {
				logrus.WithFields(logrus.Fields{"node": v.Alias, "peer_id": v.PeerID, "cgc": cgcHex.(string)}).Error("failed to decode cgc. ", err)
			} else {
				custodySubnetCount = val
			}
		} else {
			pageData.PeerDASInfos.Warnings.MissingCGCFromENRPeers = append(pageData.PeerDASInfos.Warnings.MissingCGCFromENRPeers, v.PeerID)
			pageData.PeerDASInfos.Warnings.HasWarnings = true
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

		peerDASInfo := models.ClientCLPageDataNodePeerDAS{
			Columns:     resColumns,
			Subnets:     resSubnets,
			CGC:         custodySubnetCount,
			IsSuperNode: uint64(len(resColumns)) == pageData.PeerDASInfos.NumberOfColumns,
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
			nodeA := pageData.Nodes[pA]
			nodeB := pageData.Nodes[pB]

			// Compare supernodes
			if nodeA.PeerDAS.IsSuperNode != nodeB.PeerDAS.IsSuperNode {
				return nodeA.PeerDAS.IsSuperNode
			}
			// Compare node types
			if nodeA.Type != nodeB.Type {
				return nodeA.Type > nodeB.Type
			}

			// If types are the same, compare CustodyColumns length
			lenA := len(nodeA.PeerDAS.Columns)
			lenB := len(nodeB.PeerDAS.Columns)
			if lenA != lenB {
				return lenA > lenB
			}

			// If both types and CustodyColumns lengths are the same, compare aliases
			return nodeA.Alias < nodeB.Alias
		})
	}

	// Check for empty columns
	for i := uint64(0); i < pageData.PeerDASInfos.NumberOfColumns; i++ {
		if _, ok := resultColumnDistribution[i]; !ok {
			pageData.PeerDASInfos.Warnings.EmptyColumns = append(pageData.PeerDASInfos.Warnings.EmptyColumns, i)
			pageData.PeerDASInfos.Warnings.HasWarnings = true
		}
	}

	pageData.PeerDASInfos.TotalRows = int(pageData.PeerDASInfos.NumberOfColumns) / 32
	pageData.PeerDASInfos.ColumnDistribution = resultColumnDistribution

	if !pageData.ShowSensitivePeerInfos {
		for _, node := range pageData.Nodes {
			node.ENR = ""
			node.ENRKeyValues = nil
		}
	}

	// Apply sorting
	switch sortOrder {
	case "index-d":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].Index > pageData.Clients[j].Index
		})
	case "name":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].Name < pageData.Clients[j].Name
		})
	case "name-d":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].Name > pageData.Clients[j].Name
		})
	case "peers":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].PeerCount < pageData.Clients[j].PeerCount
		})
	case "peers-d":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].PeerCount > pageData.Clients[j].PeerCount
		})
	case "headslot":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].HeadSlot < pageData.Clients[j].HeadSlot
		})
	case "headslot-d":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].HeadSlot > pageData.Clients[j].HeadSlot
		})
	case "headroot":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return string(pageData.Clients[i].HeadRoot) < string(pageData.Clients[j].HeadRoot)
		})
	case "headroot-d":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return string(pageData.Clients[i].HeadRoot) > string(pageData.Clients[j].HeadRoot)
		})
	case "status":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			statusOrder := map[string]int{"online": 0, "synchronizing": 1, "optimistic": 2, "offline": 3}
			aVal, aExists := statusOrder[pageData.Clients[i].Status]
			bVal, bExists := statusOrder[pageData.Clients[j].Status]
			if !aExists {
				aVal = 4
			}
			if !bExists {
				bVal = 4
			}
			return aVal < bVal
		})
	case "status-d":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			statusOrder := map[string]int{"online": 0, "synchronizing": 1, "optimistic": 2, "offline": 3}
			aVal, aExists := statusOrder[pageData.Clients[i].Status]
			bVal, bExists := statusOrder[pageData.Clients[j].Status]
			if !aExists {
				aVal = 4
			}
			if !bExists {
				bVal = 4
			}
			return aVal > bVal
		})
	case "version":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].Version < pageData.Clients[j].Version
		})
	case "version-d":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].Version > pageData.Clients[j].Version
		})
	case "index":
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].Index < pageData.Clients[j].Index
		})
	default:
		// Default sort by name ascending
		sort.Slice(pageData.Clients, func(i, j int) bool {
			return pageData.Clients[i].Name < pageData.Clients[j].Name
		})
		pageData.IsDefaultSorting = true
		sortOrder = "name"
	}
	pageData.Sorting = sortOrder

	// Add current fork digest
	forkDigest := chainState.GetForkDigestForEpoch(chainState.CurrentEpoch())
	pageData.CurrentForkDigest = forkDigest[:]

	// Add Fulu activation epoch for DAS Guardian UI
	if specs != nil && specs.FuluForkEpoch != nil {
		pageData.FuluActivationEpoch = *specs.FuluForkEpoch
	} else {
		// If Fulu fork epoch is not set, use max uint64 (never activated)
		pageData.FuluActivationEpoch = ^uint64(0)
	}

	// Add expected chain spec for mismatch comparison (using client specs instead of chainspec)
	pageData.ExpectedChainSpec = getCanonicalClientSpecs()

	return pageData, cacheTime
}

func getCanonicalClientSpecs() map[string]interface{} {
	// Get canonical specs from the first online client with specs
	clients := services.GlobalBeaconService.GetConsensusClients()
	
	for _, client := range clients {
		if client.GetStatus() == consensus.ClientStatusOnline {
			specs := client.GetSpecs()
			if specs != nil && len(specs) > 0 {
				return specs
			}
		}
	}
	
	// If no online client has specs, try any client with specs
	for _, client := range clients {
		specs := client.GetSpecs()
		if specs != nil && len(specs) > 0 {
			return specs
		}
	}
	
	// Return empty map if no client has specs
	return make(map[string]interface{})
}
