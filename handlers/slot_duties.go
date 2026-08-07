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

	"github.com/ethpandaops/dora/dbtypes"
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
	slotOrHash := vars["slotOrHash"]

	isPtc := r.URL.Query().Get("ptc") == "1"
	committeesParam := r.URL.Query().Get("committees")

	// The path may be a plain slot number (canonical duties) or a block root
	// (fork-aware duties for an orphaned block). When it is a root, resolve the
	// block's slot and the fork's committee-shuffling dependent root so the
	// duties are resolved on the correct fork.
	var slot phase0.Slot
	var depRoot phase0.Root
	var hasFork bool

	if strings.HasPrefix(slotOrHash, "0x") && len(slotOrHash) == 66 {
		blockRoot, err := parseSlotDutiesRoot(slotOrHash)
		if err != nil {
			http.Error(w, "Invalid block root", http.StatusBadRequest)
			return
		}

		dbBlock := services.GlobalBeaconService.GetDbBlocksByFilter(r.Context(), &dbtypes.BlockFilter{BlockRoot: blockRoot[:], WithOrphaned: 1}, 0, 1, 0)
		if len(dbBlock) == 0 || dbBlock[0].Block == nil {
			http.Error(w, "Block not found", http.StatusNotFound)
			return
		}

		slot = phase0.Slot(dbBlock[0].Block.Slot)
		epoch := services.GlobalBeaconService.GetChainState().EpochOfSlot(slot)
		depRoot, _ = services.GlobalBeaconService.ResolveDependentRoot(r.Context(), epoch, blockRoot)
		hasFork = true
	} else {
		slotNum, err := strconv.ParseUint(slotOrHash, 10, 64)
		if err != nil {
			http.Error(w, "Invalid slot", http.StatusBadRequest)
			return
		}
		slot = phase0.Slot(slotNum)
	}

	cacheKey := fmt.Sprintf("slotduties:%d:ptc", slot)
	if !isPtc {
		cacheKey = fmt.Sprintf("slotduties:%d:c:%s", slot, committeesParam)
	}
	if hasFork {
		cacheKey = fmt.Sprintf("%s:dr:%x", cacheKey, depRoot[:])
	}

	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotDutiesResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout := buildSlotDutiesData(pageCall.CallCtx, slot, isPtc, committeesParam, depRoot, hasFork)
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

// parseSlotDutiesRoot parses a 0x-prefixed 32-byte hex block root.
func parseSlotDutiesRoot(s string) (phase0.Root, error) {
	var root phase0.Root
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return root, err
	}
	if len(b) != 32 {
		return root, fmt.Errorf("invalid root length")
	}
	copy(root[:], b)
	return root, nil
}

// buildSlotDutiesData resolves the requested duties and returns the response and
// the cache timeout (long for finalized/immutable slots, short otherwise). When
// hasFork is set, duties are resolved on the fork identified by depRoot.
func buildSlotDutiesData(ctx context.Context, slot phase0.Slot, isPtc bool, committeesParam string, depRoot phase0.Root, hasFork bool) (*models.SlotDutiesResponse, time.Duration) {
	cs := services.GlobalBeaconService
	chainState := cs.GetChainState()

	result := &models.SlotDutiesResponse{Validators: []uint64{}, Names: make(map[uint64]string)}
	addName := func(idx uint64) {
		if name := cs.GetValidatorNameAt(idx, slot); name != "" {
			result.Names[idx] = name
		}
	}

	if isPtc {
		var ptc []phase0.ValidatorIndex
		if hasFork {
			ptc = cs.GetSlotPtcForRoot(ctx, slot, depRoot)
		} else {
			ptc = cs.GetSlotPtc(ctx, slot)
		}
		for _, v := range ptc {
			result.Validators = append(result.Validators, uint64(v))
			addName(uint64(v))
		}
	} else {
		var committees [][]phase0.ValidatorIndex
		if hasFork {
			committees = cs.GetSlotCommitteesForRoot(ctx, slot, depRoot)
		} else {
			committees = cs.GetSlotCommittees(ctx, slot)
		}
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
