package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// SlotBlockAccessList returns the decoded EIP-7928 block access list of the block
// identified by the path slot number or block root as JSON. The slot page loads it
// lazily when the access list tab is opened and renders the entries client-side,
// so a block that touches tens of thousands of addresses does not bloat the page.
//
// The path value is resolved against the indexed blocks first (a slot number yields
// its canonical block), so only a block the explorer knows reaches the block loader
// and an unknown root cannot drive requests to the beacon nodes.
func SlotBlockAccessList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	_, blockRoot, found := resolveSlotPathBlock(r.Context(), mux.Vars(r)["slotOrHash"])
	if !found {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	cacheKey := fmt.Sprintf("slotbal:%x", blockRoot[:])
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotBlockAccessListResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout := buildSlotBlockAccessListData(pageCall.CallCtx, blockRoot)
		pageCall.CacheTimeout = cacheTimeout
		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building slot block access list data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.SlotBlockAccessListResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding slot block access list data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

// buildSlotBlockAccessListData loads the block and decodes its access list. The list
// of a stored block never changes, so a decoded list is cached for long; a block that
// could not be loaded or decoded yields an empty list that is only cached briefly so
// a transient node failure does not stick.
func buildSlotBlockAccessListData(ctx context.Context, blockRoot phase0.Root) (*models.SlotBlockAccessListResponse, time.Duration) {
	result := &models.SlotBlockAccessListResponse{
		Entries: []*models.SlotPageBlockAccessListEntry{},
	}

	blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(ctx, blockRoot)
	if err != nil || blockData == nil {
		if err != nil {
			logrus.WithError(err).Debugf("could not load block 0x%x for its access list", blockRoot[:])
		}
		return result, 10 * time.Second
	}

	if len(blockData.BlockAccessList) == 0 {
		return result, 30 * time.Minute
	}

	accesses, err := utils.DecodeBlockAccessList(blockData.BlockAccessList)
	if err != nil {
		logrus.WithError(err).Errorf("error decoding block access list for block 0x%x", blockRoot[:])
		return result, 10 * time.Second
	}

	result.Entries = convertBALToModel(accesses)

	return result, 30 * time.Minute
}
