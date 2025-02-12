package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
)

// Slots will return the main "slots" page using a go template
func ValidatorSlots(w http.ResponseWriter, r *http.Request) {
	var slotsTemplateFiles = append(layoutTemplateFiles,
		"validator_slots/slots.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(slotsTemplateFiles...)
	vars := mux.Vars(r)
	validator, _ := strconv.ParseUint(vars["index"], 10, 64)

	data := InitPageData(w, r, "blockchain", fmt.Sprintf("/validators/%v/slots", validator), "Validator Slots", slotsTemplateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var pageIdx uint64 = 0
	if urlArgs.Has("s") {
		pageIdx, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getValidatorSlotsPageData(validator, pageIdx, pageSize)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "validator_slots.go", "ValidatorSlots", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getValidatorSlotsPageData(validator uint64, pageIdx uint64, pageSize uint64) (*models.ValidatorSlotsPageData, error) {
	pageData := &models.ValidatorSlotsPageData{}
	pageCacheKey := fmt.Sprintf("valslots:%v:%v:%v", validator, pageIdx, pageSize)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildValidatorSlotsPageData(validator, pageIdx, pageSize)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ValidatorSlotsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildValidatorSlotsPageData(validator uint64, pageIdx uint64, pageSize uint64) (*models.ValidatorSlotsPageData, time.Duration) {
	pageData := &models.ValidatorSlotsPageData{
		Index: validator,
		Name:  services.GlobalBeaconService.GetValidatorName(validator),
	}
	logrus.Debugf("validator slots page called (%v): %v:%v", validator, pageIdx, pageSize)
	if pageIdx == 0 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pageIdx + 1
	pageData.CurrentPageIndex = pageIdx + 1
	pageData.CurrentPageSlot = pageIdx
	if pageIdx >= 1 {
		pageData.PrevPageIndex = pageIdx
		pageData.PrevPageSlot = pageIdx - 1
	}
	pageData.LastPageSlot = 0

	chainState := services.GlobalBeaconService.GetChainState()
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()

	// load slots
	pageData.Slots = make([]*models.ValidatorSlotsPageDataSlot, 0)
	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(&dbtypes.BlockFilter{
		ProposerIndex: &validator,
		WithOrphaned:  1,
		WithMissing:   1,
	}, pageIdx, uint32(pageSize), chainState.GetSpecs().SlotsPerEpoch)
	haveMore := false
	for idx, blockAssignment := range dbBlocks {
		if idx >= int(pageSize) {
			haveMore = true
			break
		}
		slot := blockAssignment.Slot
		epoch := chainState.EpochOfSlot(phase0.Slot(slot))

		slotData := &models.ValidatorSlotsPageDataSlot{
			Slot:         slot,
			Epoch:        uint64(epoch),
			Ts:           chainState.SlotToTime(phase0.Slot(slot)),
			Finalized:    finalizedEpoch >= epoch,
			Status:       uint8(0),
			Proposer:     validator,
			ProposerName: pageData.Name,
		}

		if blockAssignment.Block != nil {
			dbBlock := blockAssignment.Block
			slotData.Status = uint8(dbBlock.Status)
			slotData.AttestationCount = dbBlock.AttestationCount
			slotData.DepositCount = dbBlock.DepositCount
			slotData.ExitCount = dbBlock.ExitCount
			slotData.ProposerSlashingCount = dbBlock.ProposerSlashingCount
			slotData.AttesterSlashingCount = dbBlock.AttesterSlashingCount
			slotData.SyncParticipation = float64(dbBlock.SyncParticipation) * 100
			slotData.EthTransactionCount = dbBlock.EthTransactionCount
			slotData.Graffiti = dbBlock.Graffiti
			slotData.BlockRoot = dbBlock.Root
			if dbBlock.EthBlockNumber != nil {
				slotData.WithEthBlock = true
				slotData.EthBlockNumber = *dbBlock.EthBlockNumber
			}

			payloadStatus := dbBlock.PayloadStatus
			if !chainState.IsEip7732Enabled(epoch) {
				payloadStatus = dbtypes.PayloadStatusCanonical
			}
			slotData.PayloadStatus = uint8(payloadStatus)
		}
		pageData.Slots = append(pageData.Slots, slotData)
	}
	pageData.SlotCount = uint64(len(pageData.Slots))
	if pageData.SlotCount > 0 {
		pageData.FirstSlot = pageData.Slots[0].Slot
		pageData.LastSlot = pageData.Slots[pageData.SlotCount-1].Slot
	}
	if haveMore {
		pageData.NextPageIndex = pageIdx + 1
		pageData.NextPageSlot = pageIdx + 1
		pageData.TotalPages++
	}

	return pageData, 5 * time.Minute
}
