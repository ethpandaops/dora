package handlers

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

// Slots will return the main "slots" page using a go template
func Slots(w http.ResponseWriter, r *http.Request) {
	var slotsTemplateFiles = append(layoutTemplateFiles,
		"slots/slots.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(slotsTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "blockchain", "/slots", "Slots", slotsTemplateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var graffiti string
	if urlArgs.Has("q") {
		graffiti = urlArgs.Get("q")
	}
	var pageData *models.SlotsPageData
	if graffiti == "" {
		var firstSlot uint64 = math.MaxUint64
		if urlArgs.Has("s") {
			firstSlot, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
		}
		pageData = getSlotsPageData(firstSlot, pageSize)
	} else {
		var pageIdx uint64 = 0
		if urlArgs.Has("s") {
			pageIdx, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
		}
		pageData = getSlotsPageDataWithGraffitiFilter(graffiti, pageIdx, pageSize)
	}

	logrus.Printf("slots page called")
	data.Data = pageData

	if handleTemplateError(w, r, "slots.go", "Slots", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getSlotsPageData(firstSlot uint64, pageSize uint64) *models.SlotsPageData {
	pageData := &models.SlotsPageData{}

	now := time.Now()
	currentSlot := utils.TimeToSlot(uint64(now.Unix()))
	currentEpoch := utils.EpochOfSlot(currentSlot)
	maxSlot := currentSlot + 8
	if maxSlot >= (currentEpoch+1)*utils.Config.Chain.Config.SlotsPerEpoch {
		maxSlot = ((currentEpoch + 1) * utils.Config.Chain.Config.SlotsPerEpoch) - 1
	}
	if firstSlot > uint64(maxSlot) {
		pageData.IsDefaultPage = true
		firstSlot = uint64(maxSlot)
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pagesBefore := (firstSlot + 1) / pageSize
	if ((firstSlot + 1) % pageSize) > 0 {
		pagesBefore++
	}
	pagesAfter := (maxSlot - firstSlot) / pageSize
	if ((maxSlot - firstSlot) % pageSize) > 0 {
		pagesAfter++
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pagesBefore + pagesAfter
	pageData.CurrentPageIndex = pagesAfter + 1
	pageData.CurrentPageSlot = firstSlot
	pageData.PrevPageIndex = pageData.CurrentPageIndex - 1
	pageData.PrevPageSlot = pageData.CurrentPageSlot + pageSize
	if pageData.CurrentPageSlot >= pageSize {
		pageData.NextPageIndex = pageData.CurrentPageIndex + 1
		pageData.NextPageSlot = pageData.CurrentPageSlot - pageSize
	}
	pageData.LastPageSlot = pageSize - 1

	finalizedHead, _ := services.GlobalBeaconService.GetFinalizedBlockHead()
	slotLimit := pageSize - 1
	var lastSlot uint64
	if firstSlot > uint64(slotLimit) {
		lastSlot = firstSlot - uint64(slotLimit)
	} else {
		lastSlot = 0
	}

	// get slot assignments
	firstEpoch := utils.EpochOfSlot(firstSlot)
	lastEpoch := utils.EpochOfSlot(lastSlot)
	slotAssignments, syncedEpochs := services.GlobalBeaconService.GetProposerAssignments(firstEpoch, lastEpoch)

	// load slots
	pageData.Slots = make([]*models.SlotsPageDataSlot, 0)
	dbSlots := services.GlobalBeaconService.GetDbBlocksForSlots(uint64(firstSlot), uint32(pageSize), true)
	dbIdx := 0
	dbCnt := len(dbSlots)
	blockCount := uint64(0)
	for slotIdx := int64(firstSlot); slotIdx >= int64(lastSlot); slotIdx-- {
		slot := uint64(slotIdx)
		finalized := false
		if finalizedHead != nil && uint64(finalizedHead.Data.Header.Message.Slot) >= slot {
			finalized = true
		}
		haveBlock := false
		for dbIdx < dbCnt && dbSlots[dbIdx] != nil && dbSlots[dbIdx].Slot == slot {
			dbSlot := dbSlots[dbIdx]
			dbIdx++
			blockStatus := uint8(1)
			if dbSlot.Orphaned {
				blockStatus = 2
			}

			slotData := &models.SlotsPageDataSlot{
				Slot:                  slot,
				Epoch:                 utils.EpochOfSlot(slot),
				Ts:                    utils.SlotToTime(slot),
				Finalized:             finalized,
				Status:                blockStatus,
				Synchronized:          true,
				Proposer:              dbSlot.Proposer,
				ProposerName:          "", // TODO
				AttestationCount:      dbSlot.AttestationCount,
				DepositCount:          dbSlot.DepositCount,
				ExitCount:             dbSlot.ExitCount,
				ProposerSlashingCount: dbSlot.ProposerSlashingCount,
				AttesterSlashingCount: dbSlot.AttesterSlashingCount,
				SyncParticipation:     float64(dbSlot.SyncParticipation) * 100,
				EthTransactionCount:   dbSlot.EthTransactionCount,
				EthBlockNumber:        dbSlot.EthBlockNumber,
				Graffiti:              dbSlot.Graffiti,
				BlockRoot:             dbSlot.Root,
			}
			pageData.Slots = append(pageData.Slots, slotData)
			blockCount++
			haveBlock = true
		}

		if !haveBlock {
			epoch := utils.EpochOfSlot(slot)
			slotData := &models.SlotsPageDataSlot{
				Slot:         slot,
				Epoch:        epoch,
				Ts:           utils.SlotToTime(slot),
				Finalized:    finalized,
				Scheduled:    epoch >= currentEpoch,
				Status:       0,
				Synchronized: syncedEpochs[epoch],
				Proposer:     slotAssignments[slot],
				ProposerName: "", // TODO
			}
			pageData.Slots = append(pageData.Slots, slotData)
			blockCount++
		}
	}
	pageData.SlotCount = uint64(blockCount)
	pageData.FirstSlot = firstSlot
	pageData.LastSlot = lastSlot

	return pageData
}

func getSlotsPageDataWithGraffitiFilter(graffiti string, pageIdx uint64, pageSize uint64) *models.SlotsPageData {
	pageData := &models.SlotsPageData{
		GraffitiFilter: graffiti,
	}
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

	finalizedHead, _ := services.GlobalBeaconService.GetFinalizedBlockHead()

	// load slots
	pageData.Slots = make([]*models.SlotsPageDataSlot, 0)
	dbBlocks := services.GlobalBeaconService.GetDbBlocksByGraffiti(graffiti, pageIdx, uint32(pageSize), true)
	haveMore := false
	for idx, dbBlock := range dbBlocks {
		if idx >= int(pageSize) {
			haveMore = true
			break
		}
		slot := dbBlock.Slot
		finalized := false
		if finalizedHead != nil && uint64(finalizedHead.Data.Header.Message.Slot) >= slot {
			finalized = true
		}
		blockStatus := uint8(1)
		if dbBlock.Orphaned {
			blockStatus = 2
		}

		slotData := &models.SlotsPageDataSlot{
			Slot:                  slot,
			Epoch:                 utils.EpochOfSlot(slot),
			Ts:                    utils.SlotToTime(slot),
			Finalized:             finalized,
			Status:                blockStatus,
			Synchronized:          true,
			Proposer:              dbBlock.Proposer,
			ProposerName:          "", // TODO
			AttestationCount:      dbBlock.AttestationCount,
			DepositCount:          dbBlock.DepositCount,
			ExitCount:             dbBlock.ExitCount,
			ProposerSlashingCount: dbBlock.ProposerSlashingCount,
			AttesterSlashingCount: dbBlock.AttesterSlashingCount,
			SyncParticipation:     float64(dbBlock.SyncParticipation) * 100,
			EthTransactionCount:   dbBlock.EthTransactionCount,
			EthBlockNumber:        dbBlock.EthBlockNumber,
			Graffiti:              dbBlock.Graffiti,
			BlockRoot:             dbBlock.Root,
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

	return pageData
}
