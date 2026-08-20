package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/ethpandaops/dora/clients/xatu"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	xch "github.com/ethpandaops/xatu/pkg/proto/clickhouse"
)

// EpochArrival returns per-slot block arrival summaries for the path epoch
// from Xatu, as JSON for the arrival columns on the epoch page.
func EpochArrival(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if xatu.GlobalClient == nil {
		http.Error(w, "Xatu is not configured", http.StatusNotFound)
		return
	}

	vars := mux.Vars(r)
	epoch, err := strconv.ParseUint(vars["epoch"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid epoch", http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("epocharrival:%d", epoch)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.EpochArrivalResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout, buildErr := buildEpochArrivalData(pageCall.CallCtx, phase0.Epoch(epoch))
		if buildErr != nil {
			logrus.WithError(buildErr).Error("error loading epoch arrival data from xatu")
			pageCall.CacheTimeout = -1

			return &models.EpochArrivalResponse{Epoch: epoch, Slots: map[uint64]*models.EpochArrivalSlot{}}
		}

		pageCall.CacheTimeout = cacheTimeout

		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building epoch arrival data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.EpochArrivalResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding epoch arrival data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

// buildEpochArrivalData queries Xatu for all block observations in the epoch
// and aggregates each slot's per-node earliest arrivals into min/p90. The
// engine API series is skipped: a newPayload call never precedes the node's
// own gossip observation, so it cannot change a node's earliest arrival.
func buildEpochArrivalData(ctx context.Context, epoch phase0.Epoch) (*models.EpochArrivalResponse, time.Duration, error) {
	client := xatu.GlobalClient
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	firstSlot := phase0.Slot(uint64(epoch) * specs.SlotsPerEpoch)
	lastSlot := firstSlot + phase0.Slot(specs.SlotsPerEpoch) - 1
	firstTime := chainState.SlotToTime(firstSlot)
	lastTime := chainState.SlotToTime(lastSlot)

	settled := time.Now().After(lastTime.Add(client.SettleDelay()))

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// earliest observation per slot per node across the three arrival series
	type slotNode struct {
		slot uint32
		node string
	}

	earliest := map[slotNode]uint32{}

	record := func(slot uint32, node string, ms uint32) {
		key := slotNode{slot: slot, node: node}
		if current, ok := earliest[key]; !ok || ms < current {
			earliest[key] = ms
		}
	}

	slotFilter := &xch.UInt32Filter{Filter: &xch.UInt32Filter_Between{Between: &xch.UInt32Range{
		Min: uint32(firstSlot),
		Max: wrapperspb.UInt32(uint32(lastSlot)), //nolint:gosec // slot numbers fit
	}}}
	timeFilter := &xch.UInt32Filter{Filter: &xch.UInt32Filter_Between{Between: &xch.UInt32Range{
		Min: uint32(firstTime.Unix()),                   //nolint:gosec // unix timestamps fit
		Max: wrapperspb.UInt32(uint32(lastTime.Unix())), //nolint:gosec // unix timestamps fit
	}}}
	networkFilter := &xch.StringFilter{Filter: &xch.StringFilter_Eq{Eq: client.Network()}}

	apiQuery, err := xch.BuildListBeaconApiEthV1EventsBlockQuery(&xch.ListBeaconApiEthV1EventsBlockRequest{
		MetaNetworkName: networkFilter, Slot: slotFilter, SlotStartDateTime: timeFilter, PageSize: 10000,
	})
	if err != nil {
		return nil, -1, fmt.Errorf("build api query: %w", err)
	}

	rows, err := client.Query(queryCtx, settled, apiQuery)
	if err != nil {
		return nil, -1, fmt.Errorf("api query: %w", err)
	}

	for rows.Next() {
		var row xch.BeaconApiEthV1EventsBlockRow
		if err := rows.ScanStruct(&row); err != nil {
			rows.Close()
			return nil, -1, fmt.Errorf("api scan: %w", err)
		}

		record(row.Slot, row.MetaClientName, row.PropagationSlotStartDiff)
	}

	rows.Close()

	p2pQuery, err := xch.BuildListLibp2PGossipsubBeaconBlockQuery(&xch.ListLibp2PGossipsubBeaconBlockRequest{
		MetaNetworkName: networkFilter, Slot: slotFilter, SlotStartDateTime: timeFilter, PageSize: 10000,
	})
	if err != nil {
		return nil, -1, fmt.Errorf("build p2p query: %w", err)
	}

	rows, err = client.Query(queryCtx, settled, p2pQuery)
	if err != nil {
		return nil, -1, fmt.Errorf("p2p query: %w", err)
	}

	for rows.Next() {
		var row xch.Libp2PGossipsubBeaconBlockRow
		if err := rows.ScanStruct(&row); err != nil {
			rows.Close()
			return nil, -1, fmt.Errorf("p2p scan: %w", err)
		}

		record(row.Slot, row.MetaClientName, row.PropagationSlotStartDiff)
	}

	rows.Close()

	headQuery, err := xch.BuildListBeaconApiEthV1EventsHeadQuery(&xch.ListBeaconApiEthV1EventsHeadRequest{
		MetaNetworkName: networkFilter, Slot: slotFilter, SlotStartDateTime: timeFilter, PageSize: 10000,
	})
	if err != nil {
		return nil, -1, fmt.Errorf("build head query: %w", err)
	}

	rows, err = client.Query(queryCtx, settled, headQuery)
	if err != nil {
		return nil, -1, fmt.Errorf("head query: %w", err)
	}

	for rows.Next() {
		var row xch.BeaconApiEthV1EventsHeadRow
		if err := rows.ScanStruct(&row); err != nil {
			rows.Close()
			return nil, -1, fmt.Errorf("head scan: %w", err)
		}

		record(row.Slot, row.MetaClientName, row.PropagationSlotStartDiff)
	}

	rows.Close()

	// per-slot values, late observations excluded as on the slot page
	slotValues := map[uint32][]uint32{}

	for key, ms := range earliest {
		if ms > lateThresholdMs {
			continue
		}

		slotValues[key.slot] = append(slotValues[key.slot], ms)
	}

	response := &models.EpochArrivalResponse{
		Epoch:   uint64(epoch),
		Settled: settled,
		Slots:   make(map[uint64]*models.EpochArrivalSlot, len(slotValues)),
	}

	for slot, values := range slotValues {
		sort.Slice(values, func(a, b int) bool { return values[a] < values[b] })
		response.Slots[uint64(slot)] = &models.EpochArrivalSlot{
			Nodes: len(values),
			MinMs: values[0],
			P90Ms: values[len(values)*9/10],
		}
	}

	cacheTimeout := time.Duration(-1)

	switch {
	case !settled:
		cacheTimeout = -1
	case time.Since(lastTime) < 30*time.Minute:
		cacheTimeout = 30 * time.Second
	default:
		cacheTimeout = time.Hour
	}

	return response, cacheTimeout, nil
}
