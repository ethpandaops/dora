package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/services"
)

// SlotInclusionLists returns the inclusion lists (EIP-7805) published in the
// path slot with their committee, gossip observations and the outcome of their
// transactions in the following slot, as JSON for the lazily loaded inclusion
// lists tab of the slot page. With brief=1 the transaction details and the bid
// cross-check are left out, which is all the transaction highlighting of the
// following slot's page needs.
func SlotInclusionLists(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	slot, err := strconv.ParseUint(vars["slotOrHash"], 10, 64)
	if err != nil || slot >= 2147483648 {
		http.Error(w, "Invalid slot", http.StatusBadRequest)
		return
	}

	brief := r.URL.Query().Get("brief") == "1"

	cacheKey := fmt.Sprintf("slotinclusionlists:%d:%v", slot, brief)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &services.SlotInclusionListsView{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		chainState := services.GlobalBeaconService.GetChainState()

		// The lists, their observations and their evaluation can still change
		// while the slot is within the live cache window; once past it the
		// stored object no longer changes.
		pageCall.CacheTimeout = 12 * time.Second
		if chainState.CurrentSlot() > phase0.Slot(slot)+32 {
			pageCall.CacheTimeout = 30 * time.Minute
		}

		return services.GlobalBeaconService.GetSlotInclusionListsView(pageCall.CallCtx, phase0.Slot(slot), !brief)
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building slot inclusion lists data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*services.SlotInclusionListsView)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding slot inclusion lists data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}
