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

type attesterGroup struct {
	client  *models.SlotAttestersClient
	seenMs  []uint32
	buckets map[uint32]uint64
}

func loadAttesterArrivals(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, blockRoot string) (map[phase0.ValidatorIndex]uint32, uint64, error) {
	rows, err := client.Query(ctx, settled, `SELECT assumeNotNull(attesting_validator_index) AS validator, beacon_block_root AS root, toUInt32(min(propagation_slot_start_diff)) AS first_ms
FROM beacon_api_eth_v1_events_attestation
WHERE meta_network_name = ? AND slot_start_date_time = toDateTime(?) AND slot = ? AND attesting_validator_index IS NOT NULL
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

func buildSlotAttestersData(ctx context.Context, slot phase0.Slot, blockRoot string) (*models.SlotAttestersResponse, time.Duration, error) {
	client := xatu.GlobalClient
	chainState := services.GlobalBeaconService.GetChainState()
	slotTime := chainState.SlotToTime(slot)
	slotMs := lateThreshold(chainState)

	settleDelay := 30 * time.Second
	if client != nil {
		settleDelay = client.SettleDelay()
	}

	settled := time.Now().After(slotTime.Add(settleDelay))

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var arrivals map[phase0.ValidatorIndex]uint32

	var otherRoots uint64

	if client != nil {
		var err error

		arrivals, otherRoots, err = loadAttesterArrivals(queryCtx, client, slot, slotTime, settled, blockRoot)
		if err != nil {
			return nil, -1, err
		}
	}

	if len(arrivals) == 0 && utils.Config.Indexer.AttestationArrivals.Enabled {
		var root *phase0.Root

		if raw, err := hex.DecodeString(strings.TrimPrefix(blockRoot, "0x")); err == nil && len(raw) == len(phase0.Root{}) {
			root = (*phase0.Root)(raw)
		}

		arrivals, otherRoots = services.GlobalBeaconService.GetBeaconIndexer().GetAttestationArrivals(slot, root)
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

	cacheTimeout := time.Hour

	switch {
	case !settled:
		cacheTimeout = unsettledCacheTimeout(chainState)
	case time.Since(slotTime) < 30*time.Minute:
		cacheTimeout = 30 * time.Second
	}

	if len(arrivals) == 0 && otherRoots == 0 {
		return response, cacheTimeout, nil
	}

	groups := map[string]*attesterGroup{}
	groupOf := func(index phase0.ValidatorIndex) *attesterGroup {
		name := attesterClientName(services.GlobalBeaconService.GetValidatorNameAt(uint64(index), slot))

		group := groups[name]
		if group == nil {
			group = &attesterGroup{
				client:  &models.SlotAttestersClient{Name: name},
				buckets: map[uint32]uint64{},
			}
			groups[name] = group
		}

		return group
	}

	for _, committee := range services.GlobalBeaconService.GetSlotCommittees(queryCtx, slot) {
		for _, index := range committee {
			groupOf(index).client.Expected++
			response.Expected++
		}
	}

	for index, seenMs := range arrivals {
		group := groupOf(index)
		group.client.Seen++
		group.seenMs = append(group.seenMs, seenMs)

		if seenMs >= response.AggregateDueMs {
			group.client.AfterDue++
		}

		if seenMs >= slotMs {
			group.client.Late++

			continue
		}

		group.buckets[seenMs/attesterBucketMs*attesterBucketMs]++
	}

	for _, group := range groups {
		if len(group.seenMs) > 0 {
			sort.Slice(group.seenMs, func(a, b int) bool { return group.seenMs[a] < group.seenMs[b] })
			group.client.P50Ms = group.seenMs[len(group.seenMs)/2]
			group.client.P90Ms = group.seenMs[len(group.seenMs)*9/10]
		}

		for ms, count := range group.buckets {
			group.client.Buckets = append(group.client.Buckets, &models.SlotAttestationBucket{Ms: ms, Count: count})
		}

		sort.Slice(group.client.Buckets, func(a, b int) bool {
			return group.client.Buckets[a].Ms < group.client.Buckets[b].Ms
		})

		response.Clients = append(response.Clients, group.client)
	}

	sort.Slice(response.Clients, func(a, b int) bool {
		if (response.Clients[a].Name == attesterUnknown) != (response.Clients[b].Name == attesterUnknown) {
			return response.Clients[b].Name == attesterUnknown
		}

		return response.Clients[a].Name < response.Clients[b].Name
	})

	return response, cacheTimeout, nil
}
