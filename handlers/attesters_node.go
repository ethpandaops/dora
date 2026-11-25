package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/xatu"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

const attesterNodeLimit = 2000

func SlotAttestersNode(w http.ResponseWriter, r *http.Request) {
	slot, err := strconv.ParseUint(mux.Vars(r)["slotOrHash"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid slot", http.StatusBadRequest)
		return
	}

	chainState := services.GlobalBeaconService.GetChainState()
	blockRoot := normalizeBlockRoot(r.URL.Query().Get("root"))

	serveAttestersNode(w, r, fmt.Sprintf("slotattestersnode:%d:%s", slot, blockRoot), phase0.Slot(slot), phase0.Slot(slot)+1, chainState.SlotToTime(phase0.Slot(slot)), blockRoot)
}

func EpochAttestersNode(w http.ResponseWriter, r *http.Request) {
	epoch, err := strconv.ParseUint(mux.Vars(r)["epoch"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid epoch", http.StatusBadRequest)
		return
	}

	chainState := services.GlobalBeaconService.GetChainState()
	if phase0.Epoch(epoch) > chainState.CurrentEpoch() {
		http.Error(w, "Epoch is in the future", http.StatusNotFound)
		return
	}

	firstSlot, endSlot, endTime := epochAttesterSlots(chainState, phase0.Epoch(epoch))

	serveAttestersNode(w, r, fmt.Sprintf("epochattestersnode:%d", epoch), firstSlot, endSlot, endTime, "")
}

func serveAttestersNode(w http.ResponseWriter, r *http.Request, cacheKey string, firstSlot, endSlot phase0.Slot, endTime time.Time, blockRoot string) {
	w.Header().Set("Content-Type", "application/json")

	if xatu.GlobalClient == nil && !utils.Config.Indexer.AttestationArrivals.Enabled {
		http.Error(w, "Attester arrivals are not available", http.StatusNotFound)
		return
	}

	node := r.URL.Query().Get("name")
	if node == "" {
		http.Error(w, "Missing node name", http.StatusBadRequest)
		return
	}

	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey+":"+node, true, &models.AttestersNodeResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout, buildErr := buildAttestersNodeData(pageCall.CallCtx, node, firstSlot, endSlot, endTime, blockRoot)
		if buildErr != nil {
			logrus.WithError(buildErr).Error("error loading node attesters data from xatu")
			pageCall.CacheTimeout = -1

			return &models.AttestersNodeResponse{Node: node}
		}

		pageCall.CacheTimeout = cacheTimeout

		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building node attesters data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.AttestersNodeResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding node attesters data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

type attesterDuty struct {
	slot  phase0.Slot
	index phase0.ValidatorIndex
}

func buildAttestersNodeData(ctx context.Context, node string, firstSlot, endSlot phase0.Slot, endTime time.Time, blockRoot string) (*models.AttestersNodeResponse, time.Duration, error) {
	chainState := services.GlobalBeaconService.GetChainState()
	settled := attesterSettled(endTime)

	queryCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	validators := map[attesterDuty]*models.AttestersNodeValidator{}

	for slot := firstSlot; slot < endSlot; slot++ {
		for _, committee := range services.GlobalBeaconService.GetSlotCommittees(queryCtx, slot) {
			for _, index := range committee {
				if services.GlobalBeaconService.GetValidatorNameAt(uint64(index), slot) == node {
					validators[attesterDuty{slot, index}] = &models.AttestersNodeValidator{Index: uint64(index), Slot: uint64(slot)}
				}
			}
		}
	}

	add := func(index phase0.ValidatorIndex, slot phase0.Slot, seenMs uint32) {
		if services.GlobalBeaconService.GetValidatorNameAt(uint64(index), slot) != node {
			return
		}

		validator := validators[attesterDuty{slot, index}]
		if validator == nil {
			validator = &models.AttestersNodeValidator{Index: uint64(index), Slot: uint64(slot)}
			validators[attesterDuty{slot, index}] = validator
		}

		validator.Seen = true
		validator.SeenMs = seenMs
	}

	if endSlot == firstSlot+1 {
		arrivals, _, err := slotAttesterArrivals(queryCtx, firstSlot, chainState.SlotToTime(firstSlot), settled, blockRoot)
		if err != nil {
			return nil, -1, err
		}

		for index, seenMs := range arrivals {
			add(index, firstSlot, seenMs)
		}
	} else if err := epochAttesterArrivals(queryCtx, firstSlot, endSlot, settled, add); err != nil {
		return nil, -1, err
	}

	response := &models.AttestersNodeResponse{
		Node:             node,
		AttestationDueMs: attestationDeadlineMs(chainState, firstSlot),
		AggregateDueMs:   aggregateDeadlineMs(chainState, firstSlot),
		Total:            uint64(len(validators)),
		Validators:       make([]*models.AttestersNodeValidator, 0, len(validators)),
	}

	for _, validator := range validators {
		response.Validators = append(response.Validators, validator)
	}

	sort.Slice(response.Validators, func(a, b int) bool {
		va, vb := response.Validators[a], response.Validators[b]

		switch {
		case va.Seen != vb.Seen:
			return !va.Seen
		case va.SeenMs != vb.SeenMs:
			return va.SeenMs > vb.SeenMs
		case va.Slot != vb.Slot:
			return va.Slot < vb.Slot
		}

		return va.Index < vb.Index
	})

	if len(response.Validators) > attesterNodeLimit {
		response.Validators = response.Validators[:attesterNodeLimit]
	}

	return response, attesterCacheTimeout(chainState, settled, endTime), nil
}
