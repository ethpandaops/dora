package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// SlotsFiltered will return the filtered "slots" page using a go template
func SlotsFiltered(w http.ResponseWriter, r *http.Request) {
	var slotsTemplateFiles = append(layoutTemplateFiles,
		"slots_filtered/slots_filtered.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(slotsTemplateFiles...)
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
	var displayColumns string = ""
	if urlArgs.Has("d") {
		displayColumns = urlArgs.Get("d")
	}

	var graffiti string
	var extradata string
	var proposer string
	var pname string
	var withOrphaned uint64
	var withMissing uint64

	if urlArgs.Has("f") {
		if urlArgs.Has("f.graffiti") {
			graffiti = urlArgs.Get("f.graffiti")
		}
		if urlArgs.Has("f.extra") {
			extradata = urlArgs.Get("f.extra")
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
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredSlotsPageData(pageIdx, pageSize, graffiti, extradata, proposer, pname, uint8(withOrphaned), uint8(withMissing), displayColumns)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "slots_filtered.go", "SlotsFiltered", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredSlotsPageData(pageIdx uint64, pageSize uint64, graffiti string, extradata string, proposer string, pname string, withOrphaned uint8, withMissing uint8, displayColumns string) (*models.SlotsFilteredPageData, error) {
	pageData := &models.SlotsFilteredPageData{}
	pageCacheKey := fmt.Sprintf("slots_filtered:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, graffiti, extradata, proposer, pname, withOrphaned, withMissing, displayColumns)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredSlotsPageData(pageIdx, pageSize, graffiti, extradata, proposer, pname, withOrphaned, withMissing, displayColumns)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.SlotsFilteredPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredSlotsPageData(pageIdx uint64, pageSize uint64, graffiti string, extradata string, proposer string, pname string, withOrphaned uint8, withMissing uint8, displayColumns string) *models.SlotsFilteredPageData {
	chainState := services.GlobalBeaconService.GetChainState()
	filterArgs := url.Values{}
	if graffiti != "" {
		filterArgs.Add("f.graffiti", graffiti)
	}
	if extradata != "" {
		filterArgs.Add("f.extra", extradata)
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

	displayMap := map[uint64]bool{}
	if displayColumns != "" {
		for _, col := range strings.Split(displayColumns, " ") {
			colNum, err := strconv.ParseUint(col, 10, 64)
			if err != nil {
				continue
			}
			displayMap[colNum] = true
		}
	}
	if len(displayMap) == 0 {
		displayMap = map[uint64]bool{
			1:  true,
			2:  true,
			3:  true,
			4:  true,
			5:  true,
			6:  true,
			7:  true,
			8:  true,
			9:  true,
			10: true,
			11: true,
		}
	} else {
		displayList := make([]uint64, len(displayMap))
		displayIdx := 0
		for col := range displayMap {
			displayList[displayIdx] = col
			displayIdx++
		}
		sort.Slice(displayList, func(a, b int) bool {
			return displayList[a] < displayList[b]
		})
		displayStr := make([]string, len(displayMap))
		for idx, col := range displayList {
			displayStr[idx] = fmt.Sprintf("%v", col)
		}
		filterArgs.Add("d", strings.Join(displayStr, " "))
	}

	pageData := &models.SlotsFilteredPageData{
		FilterGraffiti:     graffiti,
		FilterExtraData:    extradata,
		FilterProposer:     proposer,
		FilterProposerName: pname,
		FilterWithOrphaned: withOrphaned,
		FilterWithMissing:  withMissing,

		DisplayEpoch:        displayMap[1],
		DisplaySlot:         displayMap[2],
		DisplayStatus:       displayMap[3],
		DisplayTime:         displayMap[4],
		DisplayProposer:     displayMap[5],
		DisplayAttestations: displayMap[6],
		DisplayDeposits:     displayMap[7],
		DisplaySlashings:    displayMap[8],
		DisplayTxCount:      displayMap[9],
		DisplaySyncAgg:      displayMap[10],
		DisplayGraffiti:     displayMap[11],
		DisplayElExtraData:  displayMap[12],
		DisplayColCount:     uint64(len(displayMap)),
	}
	logrus.Debugf("slots_filtered page called: %v:%v [%v/%v]", pageIdx, pageSize, graffiti, extradata)
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
	currentSlot := chainState.CurrentSlot()

	// load slots
	pageData.Slots = make([]*models.SlotsFilteredPageDataSlot, 0)
	blockFilter := &dbtypes.BlockFilter{
		Graffiti:     graffiti,
		ExtraData:    extradata,
		ProposerName: pname,
		WithOrphaned: withOrphaned,
		WithMissing:  withMissing,
	}
	if proposer != "" {
		pidx, _ := strconv.ParseUint(proposer, 10, 64)
		blockFilter.ProposerIndex = &pidx
	}

	withScheduledCount := chainState.GetSpecs().SlotsPerEpoch - uint64(chainState.SlotToSlotIndex(currentSlot)) - 1
	if withScheduledCount > 16 {
		withScheduledCount = 16
	}

	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, pageIdx, uint32(pageSize), withScheduledCount)
	haveMore := false
	for idx, dbBlock := range dbBlocks {
		if idx >= int(pageSize) {
			haveMore = true
			break
		}
		slot := phase0.Slot(dbBlock.Slot)
		epoch := chainState.EpochOfSlot(slot)

		slotData := &models.SlotsFilteredPageDataSlot{
			Slot:         uint64(slot),
			Epoch:        uint64(epoch),
			Ts:           chainState.SlotToTime(slot),
			Finalized:    finalizedEpoch >= epoch,
			Synchronized: true,
			Scheduled:    slot >= currentSlot,
			Proposer:     dbBlock.Proposer,
			ProposerName: services.GlobalBeaconService.GetValidatorName(dbBlock.Proposer),
		}

		if dbBlock.Block != nil {
			if dbBlock.Block.Status != dbtypes.Missing {
				slotData.Scheduled = false
			}
			slotData.Status = uint8(dbBlock.Block.Status)
			slotData.AttestationCount = dbBlock.Block.AttestationCount
			slotData.DepositCount = dbBlock.Block.DepositCount
			slotData.ExitCount = dbBlock.Block.ExitCount
			slotData.ProposerSlashingCount = dbBlock.Block.ProposerSlashingCount
			slotData.AttesterSlashingCount = dbBlock.Block.AttesterSlashingCount
			slotData.SyncParticipation = float64(dbBlock.Block.SyncParticipation) * 100
			slotData.EthTransactionCount = dbBlock.Block.EthTransactionCount
			slotData.Graffiti = dbBlock.Block.Graffiti
			slotData.ElExtraData = dbBlock.Block.EthBlockExtra
			slotData.BlockRoot = dbBlock.Block.Root
			if dbBlock.Block.EthBlockNumber != nil {
				slotData.WithEthBlock = true
				slotData.EthBlockNumber = *dbBlock.Block.EthBlockNumber
			}

			payloadStatus := dbBlock.Block.PayloadStatus
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

	pageData.FirstPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageSlot)
	pageData.NextPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageSlot)
	pageData.LastPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageSlot)

	return pageData
}
