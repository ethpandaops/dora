package handlers

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/ethpandaops/dora/clients/xatu"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	xch "github.com/ethpandaops/xatu/pkg/proto/clickhouse"
)

// errorCacheTimeout is how long a failed lookup is remembered, so an outage
// costs one query per epoch per interval instead of one per page build.
const errorCacheTimeout = 30 * time.Second

// getEpochArrivalData returns the epoch's per-slot arrival summaries through
// the frontend cache, so the epoch page renders them inline without paying a
// ClickHouse round trip on every build. Returns nil when xatu is not
// configured or the data cannot be loaded.
func getEpochArrivalData(epoch phase0.Epoch) *models.EpochArrivalResponse {
	if xatu.GlobalClient == nil {
		return nil
	}

	cacheKey := fmt.Sprintf("epocharrival:%d", epoch)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.EpochArrivalResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout, buildErr := buildEpochArrivalData(pageCall.CallCtx, epoch)
		if buildErr != nil {
			logrus.WithError(buildErr).Error("error loading epoch arrival data from xatu")
			// brief negative cache: with ClickHouse down the epoch page rebuilds
			// every slot, and each rebuild would otherwise spend the full query
			// budget failing
			pageCall.CacheTimeout = errorCacheTimeout

			// an empty response, not nil: the cache marshals whatever the build
			// returns, and a nil value panics there, which would leave the
			// negative cache above doing nothing at all
			return &models.EpochArrivalResponse{Epoch: uint64(epoch)}
		}

		pageCall.CacheTimeout = cacheTimeout

		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building epoch arrival data")
		return nil
	}

	result, _ := pageRes.(*models.EpochArrivalResponse)
	if result == nil || len(result.Slots) == 0 {
		return nil
	}

	return result
}

// buildEpochArrivalData queries Xatu for all block observations in the epoch
// and aggregates each slot's per-node earliest arrivals into min/p90. The
// engine API series is skipped: a newPayload call never precedes the node's
// own gossip observation, so it cannot change a node's earliest arrival.
func buildEpochArrivalData(ctx context.Context, epoch phase0.Epoch) (*models.EpochArrivalResponse, time.Duration, error) {
	client := xatu.GlobalClient
	chainState := services.GlobalBeaconService.GetChainState()

	firstSlot := chainState.EpochToSlot(epoch)
	lastSlot := chainState.EpochToSlot(epoch+1) - 1
	firstTime := chainState.SlotToTime(firstSlot)
	lastTime := chainState.SlotToTime(lastSlot)

	settled := time.Now().After(lastTime.Add(client.SettleDelay()))

	// this runs on the epoch page build path, so keep the budget tight
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
	// late observations are dropped below, so leave them in ClickHouse rather
	// than paging them across the wire only to discard them
	lateMs := lateThreshold(chainState)
	freshFilter := &xch.UInt32Filter{Filter: &xch.UInt32Filter_Lte{Lte: lateMs}}

	pageSize := xatu.MaxQueryPageSize()

	// Every observation in the epoch has to be reduced here: the generated
	// builders cannot aggregate, so min/p90 cannot be pushed into ClickHouse.
	err := client.QueryPaged(queryCtx, settled, func(pageOffset uint32) (string, []any, error) {
		query, err := xch.BuildListBeaconApiEthV1EventsBlockQuery(&xch.ListBeaconApiEthV1EventsBlockRequest{
			MetaNetworkName: networkFilter, Slot: slotFilter, SlotStartDateTime: timeFilter,
			PropagationSlotStartDiff: freshFilter,
			PageSize:                 pageSize, PageToken: xatuPageToken(pageOffset),
		})

		return query.Query, query.Args, err
	}, func(rows driver.Rows) error {
		var row xch.BeaconApiEthV1EventsBlockRow
		if err := rows.ScanStruct(&row); err != nil {
			return err
		}

		record(row.Slot, row.MetaClientName, row.PropagationSlotStartDiff)

		return nil
	})
	if err != nil {
		return nil, -1, fmt.Errorf("api query: %w", err)
	}

	err = client.QueryPaged(queryCtx, settled, func(pageOffset uint32) (string, []any, error) {
		query, err := xch.BuildListLibp2PGossipsubBeaconBlockQuery(&xch.ListLibp2PGossipsubBeaconBlockRequest{
			MetaNetworkName: networkFilter, Slot: slotFilter, SlotStartDateTime: timeFilter,
			PropagationSlotStartDiff: freshFilter,
			PageSize:                 pageSize, PageToken: xatuPageToken(pageOffset),
		})

		return query.Query, query.Args, err
	}, func(rows driver.Rows) error {
		var row xch.Libp2PGossipsubBeaconBlockRow
		if err := rows.ScanStruct(&row); err != nil {
			return err
		}

		record(row.Slot, row.MetaClientName, row.PropagationSlotStartDiff)

		return nil
	})
	if err != nil {
		return nil, -1, fmt.Errorf("p2p query: %w", err)
	}

	err = client.QueryPaged(queryCtx, settled, func(pageOffset uint32) (string, []any, error) {
		query, err := xch.BuildListBeaconApiEthV1EventsHeadQuery(&xch.ListBeaconApiEthV1EventsHeadRequest{
			MetaNetworkName: networkFilter, Slot: slotFilter, SlotStartDateTime: timeFilter,
			PropagationSlotStartDiff: freshFilter,
			PageSize:                 pageSize, PageToken: xatuPageToken(pageOffset),
		})

		return query.Query, query.Args, err
	}, func(rows driver.Rows) error {
		var row xch.BeaconApiEthV1EventsHeadRow
		if err := rows.ScanStruct(&row); err != nil {
			return err
		}

		record(row.Slot, row.MetaClientName, row.PropagationSlotStartDiff)

		return nil
	})
	if err != nil {
		return nil, -1, fmt.Errorf("head query: %w", err)
	}

	// per-slot values, late observations excluded as on the slot page
	slotValues := map[uint32][]uint32{}

	for key, ms := range earliest {
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
			Nodes: uint32(len(values)), //nolint:gosec // node counts are small
			MinMs: values[0],
			P90Ms: values[len(values)*9/10],
		}
	}

	cacheTimeout := time.Duration(-1)

	switch {
	case !settled:
		cacheTimeout = unsettledCacheTimeout(chainState)
	case time.Since(lastTime) < 30*time.Minute:
		cacheTimeout = 30 * time.Second
	default:
		cacheTimeout = time.Hour
	}

	return response, cacheTimeout, nil
}
