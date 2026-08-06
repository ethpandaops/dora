package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
)

// SlotBidSeen returns which clients observed each execution payload bid of the
// path slot on gossip, as JSON for the lazy seen-by callout on the slot page.
func SlotBidSeen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	slot, err := strconv.ParseUint(vars["slotOrHash"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid slot", http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("slotbidseen:%d", slot)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotBidSeenResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout := buildSlotBidSeenData(pageCall.CallCtx, phase0.Slot(slot))
		pageCall.CacheTimeout = cacheTimeout
		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building slot bid seen data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.SlotBidSeenResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding slot bid seen data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

// buildSlotBidSeenData resolves the bid observations for the slot and returns
// the response and the cache timeout (long once the slot is out of the live
// bid cache window, short while observations can still arrive).
func buildSlotBidSeenData(ctx context.Context, slot phase0.Slot) (*models.SlotBidSeenResponse, time.Duration) {
	cs := services.GlobalBeaconService
	chainState := cs.GetChainState()

	result := &models.SlotBidSeenResponse{
		Clients: []string{},
		Bids:    []*models.SlotBidSeenBid{},
	}

	bidSeen := cs.GetSlotBidSeen(ctx, slot)
	if bidSeen != nil {
		result.Clients = bidSeen.Clients
		for _, entry := range bidSeen.Bids {
			bid := &models.SlotBidSeenBid{
				ParentRoot:   fmt.Sprintf("0x%x", entry.Bid.ParentRoot),
				ParentHash:   fmt.Sprintf("0x%x", entry.Bid.ParentHash),
				BlockHash:    fmt.Sprintf("0x%x", entry.Bid.BlockHash),
				BuilderIndex: entry.Bid.BuilderIndex,
				Seen:         make([]*models.SlotBidSeenObservation, 0, len(entry.SeenTimes)),
			}
			for clientIdx, seenTime := range entry.SeenByClientIndex() {
				bid.Seen = append(bid.Seen, &models.SlotBidSeenObservation{
					Client: clientIdx,
					Time:   seenTime,
				})
			}
			result.Bids = append(result.Bids, bid)
		}
	}

	// Observations can still arrive while the slot is within the live bid
	// cache window; once past it the stored object no longer changes.
	cacheTimeout := 12 * time.Second
	if chainState.CurrentSlot() > slot+32 {
		cacheTimeout = 30 * time.Minute
	}

	return result, cacheTimeout
}
