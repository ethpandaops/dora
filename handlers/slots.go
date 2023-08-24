package handlers

import (
	"bytes"
	"fmt"
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
	data.Data = pageData

	if handleTemplateError(w, r, "slots.go", "Slots", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getSlotsPageData(firstSlot uint64, pageSize uint64) *models.SlotsPageData {
	pageData := &models.SlotsPageData{}
	pageCacheKey := fmt.Sprintf("slots:%v:%v", firstSlot, pageSize)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildSlotsPageData(firstSlot, pageSize)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.SlotsPageData)
	return pageData
}

func buildSlotsPageData(firstSlot uint64, pageSize uint64) (*models.SlotsPageData, time.Duration) {
	logrus.Printf("slots page called: %v:%v", firstSlot, pageSize)
	pageData := &models.SlotsPageData{
		ShowForkTree: true,
	}

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

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
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
	allFinalized := true
	allSynchronized := true
	isFirstPage := firstSlot >= currentSlot
	openForks := map[int][]byte{}
	maxOpenFork := 0
	for slotIdx := int64(firstSlot); slotIdx >= int64(lastSlot); slotIdx-- {
		slot := uint64(slotIdx)
		finalized := finalizedEpoch >= int64(utils.EpochOfSlot(slot))
		if !finalized {
			allFinalized = false
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
				ProposerName:          services.GlobalBeaconService.GetValidatorName(dbSlot.Proposer),
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
				ParentRoot:            dbSlot.ParentRoot,
				ForkGraph:             make([]*models.SlotsPageDataForkGraph, 0),
			}
			pageData.Slots = append(pageData.Slots, slotData)
			blockCount++
			haveBlock = true
			buildSlotsPageSlotGraph(pageData, slotData, &maxOpenFork, openForks, isFirstPage)
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
				ProposerName: services.GlobalBeaconService.GetValidatorName(slotAssignments[slot]),
			}
			if !slotData.Synchronized {
				allSynchronized = false
			}
			pageData.Slots = append(pageData.Slots, slotData)
			blockCount++
			buildSlotsPageSlotGraph(pageData, slotData, &maxOpenFork, openForks, isFirstPage)
		}
	}
	pageData.SlotCount = uint64(blockCount)
	pageData.FirstSlot = firstSlot
	pageData.LastSlot = lastSlot
	pageData.ForkTreeWidth = (maxOpenFork * 20) + 20

	var cacheTimeout time.Duration

	if !allSynchronized {
		cacheTimeout = 30 * time.Second
	} else if allFinalized {
		cacheTimeout = 30 * time.Minute
	} else if firstEpoch < uint64(currentEpoch) {
		cacheTimeout = 10 * time.Minute
	} else {
		cacheTimeout = 12 * time.Second
	}
	return pageData, cacheTimeout
}

func buildSlotsPageSlotGraph(pageData *models.SlotsPageData, slotData *models.SlotsPageDataSlot, maxOpenFork *int, openForks map[int][]byte, isFirstPage bool) {
	// fork tree
	var forkGraphIdx int = -1
	var freeForkIdx int = -1
	getForkGraph := func(slotData *models.SlotsPageDataSlot, forkIdx int) *models.SlotsPageDataForkGraph {
		forkGraph := &models.SlotsPageDataForkGraph{}
		graphCount := len(slotData.ForkGraph)
		if graphCount > forkIdx {
			forkGraph = slotData.ForkGraph[forkIdx]
		} else {
			for graphCount <= forkIdx {
				forkGraph = &models.SlotsPageDataForkGraph{
					Index: graphCount,
					Left:  10 + (graphCount * 20),
					Tiles: map[string]bool{},
				}
				slotData.ForkGraph = append(slotData.ForkGraph, forkGraph)
				graphCount++
			}
		}
		return forkGraph
	}

	for forkIdx := 0; forkIdx < *maxOpenFork; forkIdx++ {
		forkGraph := getForkGraph(slotData, forkIdx)
		if openForks[forkIdx] == nil {
			if freeForkIdx == -1 {
				freeForkIdx = forkIdx
			}
			continue
		} else {
			forkGraph.Tiles["vline"] = true
			if bytes.Equal(openForks[forkIdx], slotData.BlockRoot) {
				if forkGraphIdx != -1 {
					continue
				}
				forkGraphIdx = forkIdx
				openForks[forkIdx] = slotData.ParentRoot
				forkGraph.Block = true
				for targetIdx := forkIdx + 1; targetIdx < *maxOpenFork; targetIdx++ {
					if openForks[targetIdx] == nil || !bytes.Equal(openForks[targetIdx], slotData.BlockRoot) {
						continue
					}
					for idx := forkIdx + 1; idx <= targetIdx; idx++ {
						splitGraph := getForkGraph(slotData, idx)
						if idx == targetIdx {
							splitGraph.Tiles["tline"] = true
							splitGraph.Tiles["lline"] = true
							splitGraph.Tiles["fork"] = true
						} else {
							splitGraph.Tiles["hline"] = true
						}
					}
					forkGraph.Tiles["rline"] = true
					openForks[targetIdx] = nil
				}
			}
		}
	}
	if forkGraphIdx == -1 && slotData.Status > 0 {
		// fork head
		hasHead := false
		hasForks := false
		if !isFirstPage {
			// get blocks that build on top of this
			refBlocks := services.GlobalBeaconService.GetDbBlocksByParentRoot(slotData.BlockRoot)
			refBlockCount := len(refBlocks)
			if refBlockCount > 0 {
				freeForkIdx = *maxOpenFork
				*maxOpenFork++
				hasHead = true

				// add additional forks
				if refBlockCount > 1 {
					for idx := 1; idx < refBlockCount; idx++ {
						graphIdx := *maxOpenFork
						*maxOpenFork++
						splitGraph := getForkGraph(slotData, graphIdx)
						splitGraph.Tiles["tline"] = true
						splitGraph.Tiles["lline"] = true
						splitGraph.Tiles["fork"] = true
						if idx < refBlockCount-1 {
							splitGraph.Tiles["hline"] = true
						}
					}
				}

				// add line up to the top for each fork
				for _, slot := range pageData.Slots {
					if bytes.Equal(slot.BlockRoot, slotData.BlockRoot) {
						continue
					}
					for idx := 0; idx < refBlockCount; idx++ {
						splitGraph := getForkGraph(slot, freeForkIdx+idx)
						splitGraph.Tiles["vline"] = true
					}
				}
			}
		}

		if freeForkIdx == -1 {
			freeForkIdx = *maxOpenFork
			*maxOpenFork++
		}
		openForks[freeForkIdx] = slotData.ParentRoot
		forkGraph := getForkGraph(slotData, freeForkIdx)
		forkGraph.Block = true
		if hasHead {
			forkGraph.Tiles["vline"] = true
			if hasForks {
				forkGraph.Tiles["rline"] = true
			}
		} else {
			forkGraph.Tiles["bline"] = true
		}
	}
}

func getSlotsPageDataWithGraffitiFilter(graffiti string, pageIdx uint64, pageSize uint64) *models.SlotsPageData {
	pageData := &models.SlotsPageData{}
	pageCacheKey := fmt.Sprintf("slots:%v:%v:g-%v", pageIdx, pageSize, graffiti)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildSlotsPageDataWithGraffitiFilter(graffiti, pageIdx, pageSize)
	}).(*models.SlotsPageData)
	return pageData
}

func buildSlotsPageDataWithGraffitiFilter(graffiti string, pageIdx uint64, pageSize uint64) *models.SlotsPageData {
	pageData := &models.SlotsPageData{
		GraffitiFilter: graffiti,
	}
	logrus.Printf("slots page called (filtered): %v:%v [%v]", pageIdx, pageSize, graffiti)
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
		blockStatus := uint8(1)
		if dbBlock.Orphaned {
			blockStatus = 2
		}

		slotData := &models.SlotsPageDataSlot{
			Slot:                  slot,
			Epoch:                 utils.EpochOfSlot(slot),
			Ts:                    utils.SlotToTime(slot),
			Finalized:             finalizedEpoch >= int64(utils.EpochOfSlot(slot)),
			Status:                blockStatus,
			Synchronized:          true,
			Proposer:              dbBlock.Proposer,
			ProposerName:          services.GlobalBeaconService.GetValidatorName(dbBlock.Proposer),
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
