package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

// SlotsFiltered will return the filtered "slots" page using a go template
func SlotsFiltered(w http.ResponseWriter, r *http.Request) {
	var slotsTemplateFiles = append(layoutTemplateFiles,
		"slots_filtered/slots_filtered.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(slotsTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "blockchain", "/slots/filtered", "Filtered Slots", slotsTemplateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var pageIdx uint64 = 0
	if urlArgs.Has("s") {
		pageIdx, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
	}

	var graffiti string
	var proposer string
	var pname string
	var withOrphaned uint64
	var withMissing uint64

	if urlArgs.Has("f") {
		if urlArgs.Has("f.graffiti") {
			graffiti = urlArgs.Get("f.graffiti")
		}
		if urlArgs.Has("f.proposer") {
			proposer = urlArgs.Get("f.proposer")
		}
		if urlArgs.Has("f.pname") {
			pname = urlArgs.Get("f.pname")
		}
		if urlArgs.Has("f.orphaned") {
			withOrphaned, _ = strconv.ParseUint(urlArgs.Get("f.orphaned"), 10, 64)
		}
		if urlArgs.Has("f.missing") {
			withMissing, _ = strconv.ParseUint(urlArgs.Get("f.missing"), 10, 64)
		}
	} else {
		withOrphaned = 1
		withMissing = 1
	}
	data.Data = getFilteredSlotsPageData(pageIdx, pageSize, graffiti, proposer, pname, uint8(withOrphaned), uint8(withMissing))

	if handleTemplateError(w, r, "slots_filtered.go", "SlotsFiltered", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredSlotsPageData(pageIdx uint64, pageSize uint64, graffiti string, proposer string, pname string, withOrphaned uint8, withMissing uint8) *models.SlotsFilteredPageData {
	pageData := &models.SlotsFilteredPageData{}
	pageCacheKey := fmt.Sprintf("slots_filtered:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, graffiti, proposer, pname, withOrphaned, withMissing)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredSlotsPageData(pageIdx, pageSize, graffiti, proposer, pname, withOrphaned, withMissing)
	}).(*models.SlotsFilteredPageData)
	return pageData
}

func buildFilteredSlotsPageData(pageIdx uint64, pageSize uint64, graffiti string, proposer string, pname string, withOrphaned uint8, withMissing uint8) *models.SlotsFilteredPageData {
	filterArgs := url.Values{}
	if graffiti != "" {
		filterArgs.Add("f.graffiti", graffiti)
	}
	if proposer != "" {
		filterArgs.Add("f.proposer", proposer)
	}
	if pname != "" {
		filterArgs.Add("f.pname", pname)
	}
	if withOrphaned != 0 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}
	if withMissing != 0 {
		filterArgs.Add("f.missing", fmt.Sprintf("%v", withMissing))
	}

	pageData := &models.SlotsFilteredPageData{
		FilterGraffiti:     graffiti,
		FilterProposer:     proposer,
		FilterProposerName: pname,
		FilterWithOrphaned: withOrphaned,
		FilterWithMissing:  withMissing,
	}
	logrus.Debugf("slots_filtered page called: %v:%v [%v]", pageIdx, pageSize, graffiti)
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

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	currentSlot := utils.TimeToSlot(uint64(time.Now().Unix()))

	// load slots
	pageData.Slots = make([]*models.SlotsFilteredPageDataSlot, 0)
	blockFilter := &dbtypes.BlockFilter{
		Graffiti:     graffiti,
		ProposerName: pname,
		WithOrphaned: withOrphaned,
		WithMissing:  withMissing,
	}
	if proposer != "" {
		pidx, _ := strconv.ParseUint(proposer, 10, 64)
		blockFilter.ProposerIndex = &pidx
	}

	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, pageIdx, uint32(pageSize))
	haveMore := false
	for idx, dbBlock := range dbBlocks {
		if idx >= int(pageSize) {
			haveMore = true
			break
		}
		slot := dbBlock.Slot

		slotData := &models.SlotsFilteredPageDataSlot{
			Slot:         slot,
			Epoch:        utils.EpochOfSlot(slot),
			Ts:           utils.SlotToTime(slot),
			Finalized:    finalizedEpoch >= int64(utils.EpochOfSlot(slot)),
			Synchronized: true,
			Scheduled:    slot >= currentSlot,
			Proposer:     dbBlock.Proposer,
			ProposerName: services.GlobalBeaconService.GetValidatorName(dbBlock.Proposer),
		}

		if dbBlock.Block != nil {
			slotData.Status = 1
			if dbBlock.Block.Orphaned == 1 {
				slotData.Status = 2
			}
			slotData.AttestationCount = dbBlock.Block.AttestationCount
			slotData.DepositCount = dbBlock.Block.DepositCount
			slotData.ExitCount = dbBlock.Block.ExitCount
			slotData.ProposerSlashingCount = dbBlock.Block.ProposerSlashingCount
			slotData.AttesterSlashingCount = dbBlock.Block.AttesterSlashingCount
			slotData.SyncParticipation = float64(dbBlock.Block.SyncParticipation) * 100
			slotData.EthTransactionCount = dbBlock.Block.EthTransactionCount
			slotData.Graffiti = dbBlock.Block.Graffiti
			slotData.BlockRoot = dbBlock.Block.Root
			if dbBlock.Block.EthBlockNumber != nil {
				slotData.WithEthBlock = true
				slotData.EthBlockNumber = *dbBlock.Block.EthBlockNumber
			}
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

	pageData.FirstPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageSlot)
	pageData.NextPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageSlot)
	pageData.LastPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageSlot)

	return pageData
}
