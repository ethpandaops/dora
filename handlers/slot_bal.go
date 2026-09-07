package handlers

import (
	"context"
	"encoding/hex"
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
	"github.com/ethpandaops/dora/utils"
)

// SlotBlockAccessList returns the decoded EIP-7928 block access list of the block
// identified by the path slot number or block root as JSON. The slot page loads it
// lazily when the access list tab is opened and renders the entries client-side,
// so a block that touches tens of thousands of addresses does not bloat the page.
func SlotBlockAccessList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	slotOrHash := strings.ReplaceAll(vars["slotOrHash"], "0x", "")
	blockSlot := int64(-1)
	blockRoot, err := hex.DecodeString(slotOrHash)
	if err != nil || len(slotOrHash) != 64 {
		blockRoot = []byte{}
		blockSlot, err = strconv.ParseInt(vars["slotOrHash"], 10, 64)
		if err != nil || blockSlot >= 2147483648 { // block slot must be lower then max int4
			http.Error(w, "Invalid slot", http.StatusBadRequest)
			return
		}
	}

	cacheKey := fmt.Sprintf("slotbal:%v:%x", blockSlot, blockRoot)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotBlockAccessListResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout := buildSlotBlockAccessListData(pageCall.CallCtx, blockSlot, blockRoot)
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

// buildSlotBlockAccessListData loads the block (the canonical one when addressed by
// slot number) and decodes its access list. The list of a stored block never
// changes, so a decoded list is cached for long; a block that could not be loaded
// or decoded yields an empty list that is only cached briefly so a transient node
// failure does not stick.
func buildSlotBlockAccessListData(ctx context.Context, blockSlot int64, blockRoot []byte) (*models.SlotBlockAccessListResponse, time.Duration) {
	result := &models.SlotBlockAccessListResponse{
		Entries: []*models.SlotPageBlockAccessListEntry{},
	}

	var blockData *services.CombinedBlockResponse
	var err error
	if blockSlot > -1 {
		blockData, err = services.GlobalBeaconService.GetSlotDetailsBySlot(ctx, phase0.Slot(blockSlot))
	} else {
		blockData, err = services.GlobalBeaconService.GetSlotDetailsByBlockroot(ctx, phase0.Root(blockRoot))
	}
	if err != nil || blockData == nil {
		if err != nil {
			logrus.WithError(err).Debugf("could not load block %v/0x%x for its access list", blockSlot, blockRoot)
		}
		return result, 10 * time.Second
	}

	if len(blockData.BlockAccessList) == 0 {
		return result, 30 * time.Minute
	}

	accesses, err := utils.DecodeBlockAccessList(blockData.BlockAccessList)
	if err != nil {
		logrus.WithError(err).Errorf("error decoding block access list for block 0x%x", blockData.Root[:])
		return result, 10 * time.Second
	}

	result.Entries = convertBALToModel(accesses)

	return result, 30 * time.Minute
}
