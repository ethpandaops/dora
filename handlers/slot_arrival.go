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

// buildSlotArrivalData queries Xatu for the slot's block events and aggregates
// them per observing client. It returns the response and the cache timeout:
// no caching while the ingest pipeline may still receive events for the slot,
// a short timeout for recent slots and a long one for historic slots.
func buildSlotArrivalData(ctx context.Context, slot phase0.Slot) (*models.SlotArrivalResponse, time.Duration, error) {
	client := xatu.GlobalClient
	chainState := services.GlobalBeaconService.GetChainState()
	slotTime := chainState.SlotToTime(slot)

	settled := time.Now().After(slotTime.Add(client.SettleDelay()))

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
		return nil, -1, fmt.Errorf("build query: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rows, err := client.Query(queryCtx, settled, query)
	if err != nil {
		return nil, -1, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	type clientAgg struct {
		row   xch.BeaconApiEthV1EventsBlockRow
		count int
	}

	perClient := map[string]*clientAgg{}
	observations := 0

	for rows.Next() {
		var row xch.BeaconApiEthV1EventsBlockRow
		if err := rows.ScanStruct(&row); err != nil {
			return nil, -1, fmt.Errorf("scan: %w", err)
		}

		observations++

		// Rows are ordered by propagation time, so the first row per client is
		// its earliest observation; later rows only bump the duplicate count.
		if agg := perClient[row.MetaClientName]; agg != nil {
			agg.count++
		} else {
			perClient[row.MetaClientName] = &clientAgg{row: row, count: 1}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, -1, fmt.Errorf("rows: %w", err)
	}

	response := &models.SlotArrivalResponse{
		Slot:         uint64(slot),
		Settled:      settled,
		Observations: observations,
	}

	if len(perClient) > 0 {
		clients := make([]*models.SlotArrivalClient, 0, len(perClient))
		continents := map[string]*models.SlotArrivalContinent{}

		for _, agg := range perClient {
			clients = append(clients, &models.SlotArrivalClient{
				Name:           agg.row.MetaClientName,
				Implementation: agg.row.MetaConsensusImplementation,
				Continent:      agg.row.MetaClientGeoContinentCode,
				MinMs:          agg.row.PropagationSlotStartDiff,
				Observations:   agg.count,
			})

			continent := continents[agg.row.MetaClientGeoContinentCode]
			if continent == nil {
				continent = &models.SlotArrivalContinent{
					Code:  agg.row.MetaClientGeoContinentCode,
					MinMs: agg.row.PropagationSlotStartDiff,
				}
				continents[agg.row.MetaClientGeoContinentCode] = continent
			}

			continent.Clients++
			if agg.row.PropagationSlotStartDiff < continent.MinMs {
				continent.MinMs = agg.row.PropagationSlotStartDiff
			}
		}

		sort.Slice(clients, func(a, b int) bool {
			return clients[a].MinMs < clients[b].MinMs
		})

		response.Clients = clients
		response.Stats = &models.SlotArrivalStats{
			UniqueClients: len(clients),
			MinMs:         clients[0].MinMs,
			P50Ms:         clients[len(clients)/2].MinMs,
			P90Ms:         clients[len(clients)*9/10].MinMs,
			MaxMs:         clients[len(clients)-1].MinMs,
		}

		continentList := make([]*models.SlotArrivalContinent, 0, len(continents))
		for _, continent := range continents {
			continentList = append(continentList, continent)
		}

		sort.Slice(continentList, func(a, b int) bool {
			return continentList[a].MinMs < continentList[b].MinMs
		})

		response.Continents = continentList
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
