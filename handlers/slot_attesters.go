package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/xatu"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

const (
	attesterBucketMs = 100
	attesterUnknown  = "unknown"
)

func SlotAttesters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if xatu.GlobalClient == nil && !utils.Config.Indexer.AttestationArrivals.Enabled {
		http.Error(w, "Attester arrivals are not available", http.StatusNotFound)
		return
	}

	slot, err := strconv.ParseUint(mux.Vars(r)["slotOrHash"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid slot", http.StatusBadRequest)
		return
	}

	blockRoot := normalizeBlockRoot(r.URL.Query().Get("root"))

	cacheKey := fmt.Sprintf("slotattesters:%d:%s", slot, blockRoot)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotAttestersResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout, buildErr := buildSlotAttestersData(pageCall.CallCtx, phase0.Slot(slot), blockRoot)
		if buildErr != nil {
			logrus.WithError(buildErr).Error("error loading slot attesters data from xatu")
			pageCall.CacheTimeout = -1

			return &models.SlotAttestersResponse{Slot: slot}
		}

		pageCall.CacheTimeout = cacheTimeout

		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building slot attesters data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.SlotAttestersResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding slot attesters data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

func attesterClientName(validatorName string) string {
	_, clientType := parseClientTypesFromName(validatorName)
	if clientType == consensus.UnknownClient {
		return attesterUnknown
	}

	return clientType.String()
}

type attesterNode struct {
	node    *models.SlotAttestersNode
	seenMs  []uint32
	buckets map[uint32]uint64
}

type attesterGroup struct {
	client  *models.SlotAttestersClient
	seenMs  []uint32
	buckets map[uint32]uint64
	nodes   map[string]*attesterNode
}

type attesterGroups struct {
	groups           map[string]*attesterGroup
	slotMs           uint32
	attestationDueMs uint32
	aggregateDueMs   uint32
}

func (g *attesterGroups) group(index phase0.ValidatorIndex, slot phase0.Slot) (*attesterGroup, *attesterNode) {
	validatorName := services.GlobalBeaconService.GetValidatorNameAt(uint64(index), slot)
	name := attesterClientName(validatorName)

	group := g.groups[name]
	if group == nil {
		group = &attesterGroup{
			client:  &models.SlotAttestersClient{Name: name},
			buckets: map[uint32]uint64{},
			nodes:   map[string]*attesterNode{},
		}
		g.groups[name] = group
	}

	if validatorName == "" {
		return group, nil
	}

	node := group.nodes[validatorName]
	if node == nil {
		node = &attesterNode{node: &models.SlotAttestersNode{Name: validatorName}, buckets: map[uint32]uint64{}}
		group.nodes[validatorName] = node
	}

	return group, node
}

func forEachAttesterDuty(ctx context.Context, firstSlot, endSlot phase0.Slot, duty func(phase0.Slot, phase0.ValidatorIndex)) {
	if endSlot == firstSlot+1 {
		for _, committee := range services.GlobalBeaconService.GetSlotCommittees(ctx, firstSlot) {
			for _, index := range committee {
				duty(firstSlot, index)
			}
		}

		return
	}

	epoch := services.GlobalBeaconService.GetChainState().EpochOfSlot(firstSlot)

	for slotIndex, committees := range services.GlobalBeaconService.GetEpochCommittees(ctx, epoch) {
		slot := firstSlot + phase0.Slot(slotIndex)
		if slot >= endSlot {
			break
		}

		for _, committee := range committees {
			for _, index := range committee {
				duty(slot, index)
			}
		}
	}
}

func (g *attesterGroups) addExpected(ctx context.Context, firstSlot, endSlot phase0.Slot) uint64 {
	expected := uint64(0)

	forEachAttesterDuty(ctx, firstSlot, endSlot, func(slot phase0.Slot, index phase0.ValidatorIndex) {
		group, node := g.group(index, slot)
		group.client.Expected++

		if node != nil {
			node.node.Expected++
		}

		expected++
	})

	return expected
}

func (g *attesterGroups) addArrival(index phase0.ValidatorIndex, slot phase0.Slot, seenMs uint32) {
	group, node := g.group(index, slot)
	group.client.Seen++
	group.seenMs = append(group.seenMs, seenMs)

	if node != nil {
		node.node.Seen++
		node.seenMs = append(node.seenMs, seenMs)
	}

	if seenMs >= g.attestationDueMs {
		group.client.AfterAttestationDue++

		if node != nil {
			node.node.AfterAttestationDue++
		}
	}

	if seenMs >= g.aggregateDueMs {
		group.client.AfterDue++

		if node != nil {
			node.node.AfterDue++
		}
	}

	if seenMs >= g.slotMs {
		group.client.Late++

		if node != nil {
			node.node.Late++
		}

		return
	}

	bucket := seenMs / attesterBucketMs * attesterBucketMs
	group.buckets[bucket]++

	if node != nil {
		node.buckets[bucket]++
	}
}

func attesterPercentiles(seenMs []uint32) (uint32, uint32) {
	if len(seenMs) == 0 {
		return 0, 0
	}

	sort.Slice(seenMs, func(a, b int) bool { return seenMs[a] < seenMs[b] })

	return seenMs[len(seenMs)/2], seenMs[len(seenMs)*9/10]
}

func attesterBuckets(counts map[uint32]uint64) []*models.SlotAttestationBucket {
	buckets := make([]*models.SlotAttestationBucket, 0, len(counts))

	for ms, count := range counts {
		buckets = append(buckets, &models.SlotAttestationBucket{Ms: ms, Count: count})
	}

	sort.Slice(buckets, func(a, b int) bool { return buckets[a].Ms < buckets[b].Ms })

	return buckets
}

func (g *attesterGroups) clients() []*models.SlotAttestersClient {
	clients := make([]*models.SlotAttestersClient, 0, len(g.groups))

	for _, group := range g.groups {
		group.client.P50Ms, group.client.P90Ms = attesterPercentiles(group.seenMs)

		group.client.Buckets = attesterBuckets(group.buckets)

		for _, node := range group.nodes {
			node.node.P50Ms, node.node.P90Ms = attesterPercentiles(node.seenMs)
			node.node.Buckets = attesterBuckets(node.buckets)
			group.client.Nodes = append(group.client.Nodes, node.node)
		}

		sort.Slice(group.client.Nodes, func(a, b int) bool {
			return group.client.Nodes[a].Name < group.client.Nodes[b].Name
		})

		clients = append(clients, group.client)
	}

	sort.Slice(clients, func(a, b int) bool {
		if (clients[a].Name == attesterUnknown) != (clients[b].Name == attesterUnknown) {
			return clients[b].Name == attesterUnknown
		}

		return clients[a].Name < clients[b].Name
	})

	return clients
}

func loadAttesterNodeGeo(ctx context.Context, firstTime, endTime time.Time, settled bool, clients []*models.SlotAttestersClient) {
	client := xatu.GlobalClient
	if client == nil || len(clients) == 0 {
		return
	}

	rows, err := client.Query(ctx, settled, `SELECT meta_client_name, any(meta_client_geo_continent_code), any(meta_client_geo_country), any(meta_client_geo_country_code)
FROM beacon_api_eth_v1_events_head
WHERE meta_network_name = ? AND slot_start_date_time >= toDateTime(?) AND slot_start_date_time < toDateTime(?)
GROUP BY meta_client_name`, client.Network(), firstTime.Unix(), endTime.Unix())
	if err != nil {
		if !isMissingTableError(err) {
			logrus.WithError(err).Warn("error loading attester node locations from xatu")
		}

		return
	}
	defer func() { _ = rows.Close() }()

	nodes := map[string]*models.SlotAttestersNode{}

	for _, group := range clients {
		for _, node := range group.Nodes {
			nodes[node.Name] = node
		}
	}

	for rows.Next() {
		var name, continent, country, countryCode string

		if err := rows.Scan(&name, &continent, &country, &countryCode); err != nil {
			logrus.WithError(err).Warn("error scanning attester node locations from xatu")
			return
		}

		_, _, display := parseSentryName(name, client.Network())

		if node := nodes[display]; node != nil {
			node.Continent = continent
			node.Country = country
			node.CountryCode = countryCode
		}
	}
}

func attesterValidatorFilter(validators []phase0.ValidatorIndex) string {
	if len(validators) == 0 || len(validators) > 10000 {
		return ""
	}

	filter := strings.Builder{}
	filter.WriteString(" AND attesting_validator_index IN (")

	for i, index := range validators {
		if i > 0 {
			filter.WriteByte(',')
		}

		filter.WriteString(strconv.FormatUint(uint64(index), 10))
	}

	filter.WriteByte(')')

	return filter.String()
}

func loadAttesterArrivals(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, blockRoot string, validators []phase0.ValidatorIndex) (map[phase0.ValidatorIndex]uint32, uint64, error) {
	rows, err := client.Query(ctx, settled, `SELECT assumeNotNull(attesting_validator_index) AS validator, beacon_block_root AS root, toUInt32(min(propagation_slot_start_diff)) AS first_ms
FROM beacon_api_eth_v1_events_attestation
WHERE meta_network_name = ? AND slot_start_date_time = toDateTime(?) AND slot = ? AND attesting_validator_index IS NOT NULL`+attesterValidatorFilter(validators)+`
GROUP BY validator, root`, client.Network(), slotTime.Unix(), uint32(slot))
	if err != nil {
		if isMissingTableError(err) {
			return nil, 0, nil
		}

		return nil, 0, fmt.Errorf("attester arrival query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	arrivals := map[phase0.ValidatorIndex]uint32{}
	otherRoots := uint64(0)

	for rows.Next() {
		var validator, firstMs uint32

		var root string

		if err := rows.Scan(&validator, &root, &firstMs); err != nil {
			return nil, 0, fmt.Errorf("attester arrival scan: %w", err)
		}

		if blockRoot != "" && root != blockRoot {
			otherRoots++

			continue
		}

		index := phase0.ValidatorIndex(validator)
		if seenMs, seen := arrivals[index]; !seen || firstMs < seenMs {
			arrivals[index] = firstMs
		}
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("attester arrival query: %w", err)
	}

	return arrivals, otherRoots, nil
}

func slotAttesterArrivals(ctx context.Context, slot phase0.Slot, slotTime time.Time, settled bool, blockRoot string, validators []phase0.ValidatorIndex) (map[phase0.ValidatorIndex]uint32, uint64, error) {
	var arrivals map[phase0.ValidatorIndex]uint32

	var otherRoots uint64

	if client := xatu.GlobalClient; client != nil {
		var err error

		arrivals, otherRoots, err = loadAttesterArrivals(ctx, client, slot, slotTime, settled, blockRoot, validators)
		if err != nil {
			return nil, 0, err
		}
	}

	if len(arrivals) == 0 && otherRoots == 0 && utils.Config.Indexer.AttestationArrivals.Enabled {
		var root *phase0.Root

		if raw, err := hex.DecodeString(strings.TrimPrefix(blockRoot, "0x")); err == nil && len(raw) == len(phase0.Root{}) {
			root = (*phase0.Root)(raw)
		}

		arrivals, otherRoots = services.GlobalBeaconService.GetBeaconIndexer().GetAttestationArrivals(slot, root)
	}

	return arrivals, otherRoots, nil
}

func attesterSettled(slotTime time.Time) bool {
	settleDelay := 30 * time.Second
	if client := xatu.GlobalClient; client != nil {
		settleDelay = client.SettleDelay()
	}

	return time.Now().After(slotTime.Add(settleDelay))
}

func attesterCacheTimeout(chainState *consensus.ChainState, settled bool, endTime time.Time) time.Duration {
	switch {
	case !settled:
		return unsettledCacheTimeout(chainState)
	case time.Since(endTime) < 30*time.Minute:
		return 30 * time.Second
	}

	return time.Hour
}

func buildSlotAttestersData(ctx context.Context, slot phase0.Slot, blockRoot string) (*models.SlotAttestersResponse, time.Duration, error) {
	chainState := services.GlobalBeaconService.GetChainState()
	slotTime := chainState.SlotToTime(slot)
	slotMs := lateThreshold(chainState)
	settled := attesterSettled(slotTime)

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	arrivals, otherRoots, err := slotAttesterArrivals(queryCtx, slot, slotTime, settled, blockRoot, nil)
	if err != nil {
		return nil, -1, err
	}

	response := &models.SlotAttestersResponse{
		Slot:             uint64(slot),
		Settled:          settled,
		SlotMs:           slotMs,
		BucketMs:         attesterBucketMs,
		AttestationDueMs: attestationDeadlineMs(chainState, slot),
		AggregateDueMs:   aggregateDeadlineMs(chainState, slot),
		Seen:             uint64(len(arrivals)),
		OtherRoots:       otherRoots,
	}

	cacheTimeout := attesterCacheTimeout(chainState, settled, slotTime)

	if len(arrivals) == 0 && otherRoots == 0 {
		return response, cacheTimeout, nil
	}

	groups := &attesterGroups{
		groups:           map[string]*attesterGroup{},
		slotMs:           slotMs,
		attestationDueMs: response.AttestationDueMs,
		aggregateDueMs:   response.AggregateDueMs,
	}

	response.Expected = groups.addExpected(queryCtx, slot, slot+1)

	for index, seenMs := range arrivals {
		groups.addArrival(index, slot, seenMs)
	}

	response.Clients = groups.clients()

	epoch := chainState.EpochOfSlot(slot)
	loadAttesterNodeGeo(queryCtx, chainState.SlotToTime(chainState.EpochToSlot(epoch)), chainState.SlotToTime(chainState.EpochToSlot(epoch+1)), settled, response.Clients)

	return response, cacheTimeout, nil
}
