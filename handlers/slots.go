package handlers

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// Slots will return the main "slots" page using a go template
func Slots(w http.ResponseWriter, r *http.Request) {
	var slotsTemplateFiles = append(layoutTemplateFiles,
		"slots/slots.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(slotsTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/slots", "Slots", slotsTemplateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var firstSlot uint64 = math.MaxUint64
	if urlArgs.Has("s") {
		firstSlot, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getSlotsPageData(firstSlot, pageSize)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "slots.go", "Slots", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getSlotsPageData(firstSlot uint64, pageSize uint64) (*models.SlotsPageData, error) {
	pageData := &models.SlotsPageData{}
	pageCacheKey := fmt.Sprintf("slots:%v:%v", firstSlot, pageSize)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildSlotsPageData(firstSlot, pageSize)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.SlotsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildSlotsPageData(firstSlot uint64, pageSize uint64) (*models.SlotsPageData, time.Duration) {
	logrus.Debugf("slots page called: %v:%v", firstSlot, pageSize)
	pageData := &models.SlotsPageData{}

	chainState := services.GlobalBeaconService.GetChainState()
	currentSlot := chainState.CurrentSlot()
	currentEpoch := chainState.EpochOfSlot(currentSlot)
	maxSlot := currentSlot + 8
	if maxSlot >= chainState.EpochToSlot(currentEpoch+1) {
		maxSlot = chainState.EpochToSlot(currentEpoch+1) - 1
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
	pagesAfter := (uint64(maxSlot) - firstSlot) / pageSize
	if ((uint64(maxSlot) - firstSlot) % pageSize) > 0 {
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
	firstEpoch := chainState.EpochOfSlot(phase0.Slot(firstSlot))

	// load slots
	pageData.Slots = make([]*models.SlotsPageDataSlot, 0)
	dbSlots := services.GlobalBeaconService.GetDbBlocksForSlots(firstSlot, uint32(pageSize), true, true)
	dbIdx := 0
	dbCnt := len(dbSlots)
	blockCount := uint64(0)
	allFinalized := true
	allSynchronized := true
	isFirstPage := firstSlot >= uint64(currentSlot)
	openForks := map[int][]byte{}
	maxOpenFork := 0
	for slotIdx := int64(firstSlot); slotIdx >= int64(lastSlot); slotIdx-- {
		slot := uint64(slotIdx)
		finalized := finalizedEpoch > 0 && finalizedEpoch >= chainState.EpochOfSlot(phase0.Slot(slot))
		if !finalized {
			allFinalized = false
		}

		for dbIdx < dbCnt && dbSlots[dbIdx] != nil && dbSlots[dbIdx].Slot == slot {
			dbSlot := dbSlots[dbIdx]
			dbIdx++

			epoch := chainState.EpochOfSlot(phase0.Slot(slot))
			payloadStatus := dbSlot.PayloadStatus
			if !chainState.IsEip7732Enabled(phase0.Epoch(epoch)) {
				payloadStatus = dbtypes.PayloadStatusCanonical
			}

			slotData := &models.SlotsPageDataSlot{
				Slot:                  slot,
				Epoch:                 uint64(epoch),
				Ts:                    chainState.SlotToTime(phase0.Slot(slot)),
				Finalized:             finalized,
				Status:                uint8(dbSlot.Status),
				PayloadStatus:         uint8(payloadStatus),
				Scheduled:             slot >= uint64(currentSlot) && dbSlot.Status == dbtypes.Missing,
				Synchronized:          dbSlot.SyncParticipation != -1,
				Proposer:              dbSlot.Proposer,
				ProposerName:          services.GlobalBeaconService.GetValidatorName(dbSlot.Proposer),
				AttestationCount:      dbSlot.AttestationCount,
				DepositCount:          dbSlot.DepositCount,
				ExitCount:             dbSlot.ExitCount,
				ProposerSlashingCount: dbSlot.ProposerSlashingCount,
				AttesterSlashingCount: dbSlot.AttesterSlashingCount,
				SyncParticipation:     float64(dbSlot.SyncParticipation) * 100,
				EthTransactionCount:   dbSlot.EthTransactionCount,
				Graffiti:              dbSlot.Graffiti,
				BlockRoot:             dbSlot.Root,
				ParentRoot:            dbSlot.ParentRoot,
				ForkGraph:             make([]*models.SlotsPageDataForkGraph, 0),
			}
			if dbSlot.EthBlockNumber != nil {
				slotData.WithEthBlock = true
				slotData.EthBlockNumber = *dbSlot.EthBlockNumber
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
	} else if firstEpoch < currentEpoch {
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
			refBlocks := services.GlobalBeaconService.GetDbBlocksByParentRoot(phase0.Root(slotData.BlockRoot))
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
