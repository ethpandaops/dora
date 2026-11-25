package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

func EpochAttesters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if xatu.GlobalClient == nil && !utils.Config.Indexer.AttestationArrivals.Enabled {
		http.Error(w, "Attester arrivals are not available", http.StatusNotFound)
		return
	}

	epoch, err := strconv.ParseUint(mux.Vars(r)["epoch"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid epoch", http.StatusBadRequest)
		return
	}

	if phase0.Epoch(epoch) > services.GlobalBeaconService.GetChainState().CurrentEpoch() {
		http.Error(w, "Epoch is in the future", http.StatusNotFound)
		return
	}

	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(fmt.Sprintf("epochattesters:%d", epoch), true, &models.EpochAttestersResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout, buildErr := buildEpochAttestersData(pageCall.CallCtx, phase0.Epoch(epoch))
		if buildErr != nil {
			logrus.WithError(buildErr).Error("error loading epoch attesters data from xatu")
			pageCall.CacheTimeout = -1

			return &models.EpochAttestersResponse{Epoch: epoch}
		}

		pageCall.CacheTimeout = cacheTimeout

		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building epoch attesters data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.EpochAttestersResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding epoch attesters data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

func loadEpochAttesterArrivals(ctx context.Context, client *xatu.Client, firstSlot, endSlot phase0.Slot, firstTime, endTime time.Time, settled bool, validators []phase0.ValidatorIndex, add func(phase0.ValidatorIndex, phase0.Slot, uint32)) error {
	rows, err := client.Query(ctx, settled, `SELECT slot, assumeNotNull(attesting_validator_index) AS validator, toUInt32(min(propagation_slot_start_diff)) AS first_ms
FROM beacon_api_eth_v1_events_attestation
WHERE meta_network_name = ? AND slot_start_date_time >= toDateTime(?) AND slot_start_date_time < toDateTime(?) AND slot >= ? AND slot < ? AND attesting_validator_index IS NOT NULL`+attesterValidatorFilter(validators)+`
GROUP BY slot, validator`, client.Network(), firstTime.Unix(), endTime.Unix(), uint32(firstSlot), uint32(endSlot))
	if err != nil {
		if isMissingTableError(err) {
			return nil
		}

		return fmt.Errorf("epoch attester arrival query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var slot, validator, firstMs uint32

		if err := rows.Scan(&slot, &validator, &firstMs); err != nil {
			return fmt.Errorf("epoch attester arrival scan: %w", err)
		}

		add(phase0.ValidatorIndex(validator), phase0.Slot(slot), firstMs)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("epoch attester arrival query: %w", err)
	}

	return nil
}

func epochAttesterArrivals(ctx context.Context, firstSlot, endSlot phase0.Slot, settled bool, validators []phase0.ValidatorIndex, add func(phase0.ValidatorIndex, phase0.Slot, uint32)) error {
	chainState := services.GlobalBeaconService.GetChainState()
	seen := 0
	count := func(index phase0.ValidatorIndex, slot phase0.Slot, seenMs uint32) {
		add(index, slot, seenMs)
		seen++
	}

	if client := xatu.GlobalClient; client != nil {
		if err := loadEpochAttesterArrivals(ctx, client, firstSlot, endSlot, chainState.SlotToTime(firstSlot), chainState.SlotToTime(endSlot), settled, validators, count); err != nil {
			return err
		}
	}

	if seen == 0 && utils.Config.Indexer.AttestationArrivals.Enabled {
		for slot := firstSlot; slot < endSlot; slot++ {
			arrivals, _ := services.GlobalBeaconService.GetBeaconIndexer().GetAttestationArrivals(slot, nil)
			for index, seenMs := range arrivals {
				add(index, slot, seenMs)
			}
		}
	}

	return nil
}

func epochAttesterSlots(chainState *consensus.ChainState, epoch phase0.Epoch) (phase0.Slot, phase0.Slot, time.Time) {
	firstSlot := chainState.EpochToSlot(epoch)
	endSlot := chainState.EpochToSlot(epoch + 1)
	endTime := chainState.SlotToTime(endSlot)

	if currentSlot := chainState.CurrentSlot(); endSlot > currentSlot+1 {
		endSlot = currentSlot + 1
	}

	return firstSlot, endSlot, endTime
}

func buildEpochAttestersData(ctx context.Context, epoch phase0.Epoch) (*models.EpochAttestersResponse, time.Duration, error) {
	chainState := services.GlobalBeaconService.GetChainState()
	firstSlot, endSlot, endTime := epochAttesterSlots(chainState, epoch)
	settled := attesterSettled(endTime)

	queryCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	response := &models.EpochAttestersResponse{
		Epoch:            uint64(epoch),
		Settled:          settled,
		SlotMs:           lateThreshold(chainState),
		BucketMs:         attesterBucketMs,
		AttestationDueMs: attestationDeadlineMs(chainState, firstSlot),
		AggregateDueMs:   aggregateDeadlineMs(chainState, firstSlot),
	}

	groups := &attesterGroups{
		groups:           map[string]*attesterGroup{},
		slotMs:           response.SlotMs,
		attestationDueMs: response.AttestationDueMs,
		aggregateDueMs:   response.AggregateDueMs,
	}

	if err := epochAttesterArrivals(queryCtx, firstSlot, endSlot, settled, nil, func(index phase0.ValidatorIndex, slot phase0.Slot, seenMs uint32) {
		groups.addArrival(index, slot, seenMs)
		response.Seen++
	}); err != nil {
		return nil, -1, err
	}

	cacheTimeout := attesterCacheTimeout(chainState, settled, endTime)

	if response.Seen == 0 {
		return response, cacheTimeout, nil
	}

	response.Expected = groups.addExpected(queryCtx, firstSlot, endSlot)

	response.Clients = groups.clients()

	return response, cacheTimeout, nil
}
