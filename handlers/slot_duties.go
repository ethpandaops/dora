package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
)

// SlotDuties resolves attester committee or PTC duty mappings for the path slot.
// Query parameters:
//
//	committees=M1,M2  -> {"validators":[idx,...],"names":{...}}
//	                     validators are concatenated in the requested committee order
//	ptc=1             -> {"validators":[idx,...],"names":{...}}
func SlotDuties(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	slot, err := strconv.ParseUint(vars["slotOrHash"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid slot", http.StatusBadRequest)
		return
	}

	isPtc := r.URL.Query().Get("ptc") == "1"
	committeesParam := r.URL.Query().Get("committees")

	cacheKey := fmt.Sprintf("slotduties-%d-ptc", slot)
	if !isPtc {
		cacheKey = fmt.Sprintf("slotduties-%d-c-%s", slot, committeesParam)
	}

	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotDutiesResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout := buildSlotDutiesData(pageCall.CallCtx, phase0.Slot(slot), isPtc, committeesParam)
		pageCall.CacheTimeout = cacheTimeout
		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building slot duties")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.SlotDutiesResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding slot duties")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

// buildSlotDutiesData resolves the requested duties and returns the response and
// the cache timeout (long for finalized/immutable slots, short otherwise).
func buildSlotDutiesData(ctx context.Context, slot phase0.Slot, isPtc bool, committeesParam string) (*models.SlotDutiesResponse, time.Duration) {
	cs := services.GlobalBeaconService
	chainState := cs.GetChainState()

	result := &models.SlotDutiesResponse{Validators: []uint64{}, Names: make(map[uint64]string)}
	addName := func(idx uint64) {
		if name := cs.GetValidatorName(idx); name != "" {
			result.Names[idx] = name
		}
	}

	if isPtc {
		for _, v := range cs.GetSlotPtc(ctx, slot) {
			result.Validators = append(result.Validators, uint64(v))
			addName(uint64(v))
		}
	} else {
		committees := cs.GetSlotCommittees(ctx, slot)
		for _, committeeStr := range strings.Split(committeesParam, ",") {
			committeeStr = strings.TrimSpace(committeeStr)
			if committeeStr == "" {
				continue
			}
			committee, perr := strconv.Atoi(committeeStr)
			if perr != nil || committee < 0 || committee >= len(committees) {
				continue
			}
			for _, v := range committees[committee] {
				result.Validators = append(result.Validators, uint64(v))
				addName(uint64(v))
			}
		}
	}

	// Finalized duties are immutable; cache them for longer. Unfinalized duties
	// can change on a reorg, so keep their cache short.
	finalizedEpoch, _ := cs.GetFinalizedEpoch()
	cacheTimeout := 12 * time.Second
	if chainState.EpochOfSlot(slot) <= finalizedEpoch {
		cacheTimeout = 30 * time.Minute
	}

	return result, cacheTimeout
}
