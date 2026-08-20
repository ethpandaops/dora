package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/clients/xatu"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	xch "github.com/ethpandaops/xatu/pkg/proto/clickhouse"
)

// SlotArrival returns block propagation observations for the path slot from
// Xatu, as JSON for the lazy propagation tab on the slot page.
func SlotArrival(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if xatu.GlobalClient == nil {
		http.Error(w, "Xatu is not configured", http.StatusNotFound)
		return
	}

	vars := mux.Vars(r)
	slot, err := strconv.ParseUint(vars["slotOrHash"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid slot", http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("slotarrival:%d", slot)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotArrivalResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout, buildErr := buildSlotArrivalData(pageCall.CallCtx, phase0.Slot(slot))
		if buildErr != nil {
			logrus.WithError(buildErr).Error("error loading slot arrival data from xatu")
			pageCall.CacheTimeout = -1

			return &models.SlotArrivalResponse{Slot: slot}
		}

		pageCall.CacheTimeout = cacheTimeout

		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building slot arrival data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.SlotArrivalResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding slot arrival data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

// arrivalNode accumulates per-node observations across both series.
type arrivalNode struct {
	fullName       string
	implementation string
	continent      string
	country        string
	countryCode    string
	apiMs          *uint32
	p2pMs          *uint32
	headMs         *uint32
	npMs           *uint32
	npDurMs        *uint32
	npStatus       string
	observations   int
}

// buildSlotArrivalData queries Xatu for the slot's block observations on the
// beacon API event stream and the libp2p gossip layer, and aggregates them
// per observing node. It returns the response and the cache timeout: no
// caching while the ingest pipeline may still receive events for the slot, a
// short timeout for recent slots and a long one for historic slots.
func buildSlotArrivalData(ctx context.Context, slot phase0.Slot) (*models.SlotArrivalResponse, time.Duration, error) {
	client := xatu.GlobalClient
	chainState := services.GlobalBeaconService.GetChainState()
	slotTime := chainState.SlotToTime(slot)

	settled := time.Now().After(slotTime.Add(client.SettleDelay()))

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	nodes := map[string]*arrivalNode{}

	apiObservations, err := loadAPIArrivals(queryCtx, client, slot, slotTime, settled, nodes)
	if err != nil {
		return nil, -1, err
	}

	p2pObservations, err := loadP2PArrivals(queryCtx, client, slot, slotTime, settled, nodes)
	if err != nil {
		return nil, -1, err
	}

	headObservations, err := loadHeadArrivals(queryCtx, client, slot, slotTime, settled, nodes)
	if err != nil {
		return nil, -1, err
	}

	npObservations, err := loadEngineTimings(queryCtx, client, slot, slotTime, settled, nodes)
	if err != nil {
		return nil, -1, err
	}

	response := &models.SlotArrivalResponse{
		Slot:             uint64(slot),
		Settled:          settled,
		Observations:     apiObservations,
		P2PObservations:  p2pObservations,
		HeadObservations: headObservations,
		NPObservations:   npObservations,
	}

	if len(nodes) > 0 {
		buildArrivalAggregates(response, nodes)
	}

	cacheTimeout := time.Duration(-1)

	switch {
	case !settled:
		// The pipeline may still be receiving events for this slot; never cache.
		cacheTimeout = -1
	case time.Since(slotTime) < 30*time.Minute:
		cacheTimeout = 30 * time.Second
	default:
		cacheTimeout = time.Hour
	}

	return response, cacheTimeout, nil
}

// loadAPIArrivals loads the beacon API block event series into nodes.
func loadAPIArrivals(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, nodes map[string]*arrivalNode) (int, error) {
	req := &xch.ListBeaconApiEthV1EventsBlockRequest{
		MetaNetworkName: &xch.StringFilter{Filter: &xch.StringFilter_Eq{Eq: client.Network()}},
		Slot:            &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{Eq: uint32(slot)}},
		SlotStartDateTime: &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{
			Eq: uint32(slotTime.Unix()),
		}},
		OrderBy:  "propagation_slot_start_diff",
		PageSize: 10000,
	}

	query, err := xch.BuildListBeaconApiEthV1EventsBlockQuery(req)
	if err != nil {
		return 0, fmt.Errorf("build api query: %w", err)
	}

	rows, err := client.Query(ctx, settled, query)
	if err != nil {
		return 0, fmt.Errorf("api query: %w", err)
	}
	defer rows.Close()

	observations := 0

	for rows.Next() {
		var row xch.BeaconApiEthV1EventsBlockRow
		if err := rows.ScanStruct(&row); err != nil {
			return 0, fmt.Errorf("api scan: %w", err)
		}

		observations++

		node := nodes[row.MetaClientName]
		if node == nil {
			node = &arrivalNode{fullName: row.MetaClientName}
			nodes[row.MetaClientName] = node
		}

		node.observations++
		node.implementation = row.MetaConsensusImplementation
		node.continent = row.MetaClientGeoContinentCode
		node.country = row.MetaClientGeoCountry
		node.countryCode = row.MetaClientGeoCountryCode

		// Rows are ordered by propagation time, so the first row per node is
		// its earliest observation.
		if node.apiMs == nil {
			ms := row.PropagationSlotStartDiff
			node.apiMs = &ms
		}
	}

	return observations, rows.Err()
}

// loadP2PArrivals loads the libp2p gossipsub block series into nodes.
func loadP2PArrivals(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, nodes map[string]*arrivalNode) (int, error) {
	req := &xch.ListLibp2PGossipsubBeaconBlockRequest{
		MetaNetworkName: &xch.StringFilter{Filter: &xch.StringFilter_Eq{Eq: client.Network()}},
		Slot:            &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{Eq: uint32(slot)}},
		SlotStartDateTime: &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{
			Eq: uint32(slotTime.Unix()),
		}},
		OrderBy:  "propagation_slot_start_diff",
		PageSize: 10000,
	}

	query, err := xch.BuildListLibp2PGossipsubBeaconBlockQuery(req)
	if err != nil {
		return 0, fmt.Errorf("build p2p query: %w", err)
	}

	rows, err := client.Query(ctx, settled, query)
	if err != nil {
		return 0, fmt.Errorf("p2p query: %w", err)
	}
	defer rows.Close()

	observations := 0

	for rows.Next() {
		var row xch.Libp2PGossipsubBeaconBlockRow
		if err := rows.ScanStruct(&row); err != nil {
			return 0, fmt.Errorf("p2p scan: %w", err)
		}

		observations++

		node := nodes[row.MetaClientName]
		if node == nil {
			node = &arrivalNode{fullName: row.MetaClientName}
			nodes[row.MetaClientName] = node
		}

		node.observations++

		if node.continent == "" {
			node.continent = row.MetaClientGeoContinentCode
			node.country = row.MetaClientGeoCountry
			node.countryCode = row.MetaClientGeoCountryCode
		}

		if node.p2pMs == nil {
			ms := row.PropagationSlotStartDiff
			node.p2pMs = &ms
		}
	}

	return observations, rows.Err()
}

// loadHeadArrivals loads the beacon API head event series into nodes. A head
// event marks the node adopting the block as its head via fork choice, a
// stronger signal than merely having seen the block.
func loadHeadArrivals(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, nodes map[string]*arrivalNode) (int, error) {
	req := &xch.ListBeaconApiEthV1EventsHeadRequest{
		MetaNetworkName: &xch.StringFilter{Filter: &xch.StringFilter_Eq{Eq: client.Network()}},
		Slot:            &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{Eq: uint32(slot)}},
		SlotStartDateTime: &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{
			Eq: uint32(slotTime.Unix()),
		}},
		OrderBy:  "propagation_slot_start_diff",
		PageSize: 10000,
	}

	query, err := xch.BuildListBeaconApiEthV1EventsHeadQuery(req)
	if err != nil {
		return 0, fmt.Errorf("build head query: %w", err)
	}

	rows, err := client.Query(ctx, settled, query)
	if err != nil {
		return 0, fmt.Errorf("head query: %w", err)
	}
	defer rows.Close()

	observations := 0

	for rows.Next() {
		var row xch.BeaconApiEthV1EventsHeadRow
		if err := rows.ScanStruct(&row); err != nil {
			return 0, fmt.Errorf("head scan: %w", err)
		}

		observations++

		node := nodes[row.MetaClientName]
		if node == nil {
			node = &arrivalNode{fullName: row.MetaClientName}
			nodes[row.MetaClientName] = node
		}

		node.observations++

		if node.continent == "" {
			node.continent = row.MetaClientGeoContinentCode
			node.country = row.MetaClientGeoCountry
			node.countryCode = row.MetaClientGeoCountryCode
		}

		if node.headMs == nil {
			ms := row.PropagationSlotStartDiff
			node.headMs = &ms
		}
	}

	return observations, rows.Err()
}

// lateThresholdMs marks observations arriving more than a full slot after
// slot start as late: typically syncing or stalled nodes whose event times
// say nothing about propagation.
const lateThresholdMs = 12000

// loadEngineTimings loads engine API newPayload calls observed by the
// snooper into nodes: when the consensus client handed the payload to its
// execution client (derived from the observed completion minus the call
// duration), and how long the execution client took to import it.
func loadEngineTimings(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, nodes map[string]*arrivalNode) (int, error) {
	req := &xch.ListConsensusEngineApiNewPayloadRequest{
		MetaNetworkName: &xch.StringFilter{Filter: &xch.StringFilter_Eq{Eq: client.Network()}},
		Slot:            &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{Eq: uint32(slot)}},
		SlotStartDateTime: &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{
			Eq: uint32(slotTime.Unix()),
		}},
		OrderBy:  "event_date_time",
		PageSize: 10000,
	}

	query, err := xch.BuildListConsensusEngineApiNewPayloadQuery(req)
	if err != nil {
		return 0, fmt.Errorf("build newpayload query: %w", err)
	}

	rows, err := client.Query(ctx, settled, query)
	if err != nil {
		return 0, fmt.Errorf("newpayload query: %w", err)
	}
	defer rows.Close()

	slotStartMs := slotTime.UnixMilli()
	observations := 0

	for rows.Next() {
		var row xch.ConsensusEngineApiNewPayloadRow
		if err := rows.ScanStruct(&row); err != nil {
			return 0, fmt.Errorf("newpayload scan: %w", err)
		}

		observations++

		node := nodes[row.MetaClientName]
		if node == nil {
			node = &arrivalNode{fullName: row.MetaClientName}
			nodes[row.MetaClientName] = node
		}

		node.observations++

		if node.npMs == nil {
			// event_date_time is stamped when the sentry receives the snooper's
			// event, which is emitted after the call completes - so the call
			// START is the observed completion minus the call duration.
			offset := row.EventDateTime/1000 - slotStartMs - int64(row.DurationMs) //nolint:gosec // duration is bounded
			if offset < 0 {
				offset = 0
			}

			ms := uint32(min(offset, int64(^uint32(0))))           //nolint:gosec // clamped above
			dur := uint32(min(row.DurationMs, uint64(^uint32(0)))) //nolint:gosec // clamped

			node.npMs = &ms
			node.npDurMs = &dur
			node.npStatus = row.Status
		}
	}

	return observations, rows.Err()
}

// buildArrivalAggregates fills the response's nodes, stats, histogram and
// per-continent/group summaries from the accumulated per-node observations.
// Late nodes are listed after on-time nodes and excluded from all summaries.
func buildArrivalAggregates(response *models.SlotArrivalResponse, nodes map[string]*arrivalNode) {
	nodeList := make([]*models.SlotArrivalNode, 0, len(nodes))
	lateList := make([]*models.SlotArrivalNode, 0)
	continents := map[string]*models.SlotArrivalContinent{}
	continentValues := map[string][]uint32{}
	groups := map[string][]uint32{}

	for _, node := range nodes {
		// Earliest block arrival: beacon API or gossip layer. Head adoption is
		// tracked separately and only used as a fallback for head-only nodes.
		var minMs uint32

		switch {
		case node.apiMs != nil && node.p2pMs != nil:
			minMs = min(*node.apiMs, *node.p2pMs)
		case node.apiMs != nil:
			minMs = *node.apiMs
		case node.p2pMs != nil:
			minMs = *node.p2pMs
		case node.headMs != nil:
			minMs = *node.headMs
		case node.npMs != nil:
			minMs = *node.npMs
		}

		group, operator, display := parseSentryName(node.fullName, xatu.GlobalClient.Network())

		entry := &models.SlotArrivalNode{
			Name:           display,
			FullName:       node.fullName,
			Group:          group,
			Operator:       operator,
			Implementation: node.implementation,
			Continent:      node.continent,
			Country:        node.country,
			CountryCode:    node.countryCode,
			MinMs:          minMs,
			APIMs:          node.apiMs,
			P2PMs:          node.p2pMs,
			HeadMs:         node.headMs,
			NPMs:           node.npMs,
			NPDurMs:        node.npDurMs,
			NPStatus:       node.npStatus,
			Observations:   node.observations,
		}

		if minMs > lateThresholdMs {
			entry.Late = true

			lateList = append(lateList, entry)

			continue
		}

		nodeList = append(nodeList, entry)

		continent := continents[node.continent]
		if continent == nil {
			continent = &models.SlotArrivalContinent{Code: node.continent, MinMs: minMs}
			continents[node.continent] = continent
		}

		continent.Nodes++
		continentValues[node.continent] = append(continentValues[node.continent], minMs)

		if minMs < continent.MinMs {
			continent.MinMs = minMs
		}

		groups[group] = append(groups[group], minMs)
	}

	sort.Slice(nodeList, func(a, b int) bool {
		return nodeList[a].MinMs < nodeList[b].MinMs
	})
	sort.Slice(lateList, func(a, b int) bool {
		return lateList[a].MinMs < lateList[b].MinMs
	})

	if len(nodeList) == 0 {
		response.Nodes = lateList
		response.Stats = &models.SlotArrivalStats{LateNodes: len(lateList)}

		return
	}

	response.Stats = &models.SlotArrivalStats{
		UniqueNodes: len(nodeList),
		MinMs:       nodeList[0].MinMs,
		P50Ms:       nodeList[len(nodeList)/2].MinMs,
		P90Ms:       nodeList[len(nodeList)*9/10].MinMs,
		MaxMs:       nodeList[len(nodeList)-1].MinMs,
		LateNodes:   len(lateList),
	}
	response.Histogram = buildArrivalHistogram(nodeList)
	response.Nodes = append(nodeList, lateList...)

	continentList := make([]*models.SlotArrivalContinent, 0, len(continents))

	for code, continent := range continents {
		values := continentValues[code]
		sort.Slice(values, func(a, b int) bool { return values[a] < values[b] })
		continent.P50Ms = values[len(values)/2]

		continentList = append(continentList, continent)
	}

	sort.Slice(continentList, func(a, b int) bool {
		return continentList[a].MinMs < continentList[b].MinMs
	})

	response.Continents = continentList

	groupList := make([]*models.SlotArrivalGroup, 0, len(groups))

	for name, values := range groups {
		sort.Slice(values, func(a, b int) bool { return values[a] < values[b] })
		groupList = append(groupList, &models.SlotArrivalGroup{
			Name:  name,
			Nodes: len(values),
			P50Ms: values[len(values)/2],
		})
	}

	sort.Slice(groupList, func(a, b int) bool {
		return groupList[a].Nodes > groupList[b].Nodes
	})

	response.Groups = groupList
}

// buildArrivalHistogram buckets each node's earliest observation into a
// small fixed number of bars with a friendly bucket width.
func buildArrivalHistogram(nodes []*models.SlotArrivalNode) []*models.SlotArrivalBucket {
	if len(nodes) == 0 {
		return nil
	}

	maxMs := nodes[len(nodes)-1].MinMs
	if maxMs == 0 {
		maxMs = 1
	}

	// Pick the smallest friendly bucket width that keeps the bar count <= 24.
	widths := []uint32{50, 100, 200, 250, 500, 1000, 2000, 5000, 10000}
	width := widths[len(widths)-1]

	for _, w := range widths {
		if maxMs/w < 24 {
			width = w
			break
		}
	}

	buckets := make([]*models.SlotArrivalBucket, maxMs/width+1)

	for i := range buckets {
		buckets[i] = &models.SlotArrivalBucket{
			FromMs: uint32(i) * width,     //nolint:gosec // bounded by bucket count
			ToMs:   uint32(i+1)*width - 1, //nolint:gosec // bounded by bucket count
		}
	}

	for _, node := range nodes {
		buckets[node.MinMs/width].Count++
	}

	return buckets
}

// parseSentryName splits a xatu sentry name of the form
// <classifier>/<operator>/<node> into a display group, operator and short
// display name. Names without that shape (e.g. locally run xatu instances)
// pass through unchanged. Community and corp contributoor nodes carry a
// hashed suffix that is shortened for display.
func parseSentryName(name, network string) (group, operator, display string) {
	parts := strings.Split(name, "/")
	if len(parts) != 3 {
		return "other", "", name
	}

	classifier, mid, node := parts[0], parts[1], parts[2]

	shortHash := func(s string) string {
		h := strings.TrimPrefix(s, "hashed-")
		if len(h) > 8 {
			h = h[:8]
		}

		return h
	}

	switch {
	case classifier == "ethpandaops":
		display = node
		for _, prefix := range []string{"utility-" + mid + "-", mid + "-", "utility-" + network + "-", network + "-"} {
			display = strings.Replace(display, prefix, "", 1)
		}

		return "ethpandaops", "ethpandaops", display
	case strings.HasPrefix(classifier, "pub-"):
		return "community", mid, mid + " #" + shortHash(node)
	case strings.HasPrefix(classifier, "corp-"):
		return "corp", mid, mid + " #" + shortHash(node)
	default:
		return "other", mid, node
	}
}
