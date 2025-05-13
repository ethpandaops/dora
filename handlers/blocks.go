package handlers

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// Blocks will return the main "blocks" page using a go template
func Blocks(w http.ResponseWriter, r *http.Request) {
	var blocksTemplateFiles = append(layoutTemplateFiles,
		"blocks/blocks.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(blocksTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/blocks", "Blocks", blocksTemplateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var firstSlot uint64 = math.MaxUint64
	if urlArgs.Has("s") {
		firstSlot, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
	}
	var displayColumns string = ""
	if urlArgs.Has("d") {
		displayColumns = urlArgs.Get("d")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getBlocksPageData(firstSlot, pageSize, displayColumns)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "blocks.go", "Blocks", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getBlocksPageData(firstSlot uint64, pageSize uint64, displayColumns string) (*models.BlocksPageData, error) {
	pageData := &models.BlocksPageData{}
	pageCacheKey := fmt.Sprintf("blocks:%v:%v:%v", firstSlot, pageSize, displayColumns)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildBlocksPageData(firstSlot, pageSize, displayColumns)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.BlocksPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildBlocksPageData(firstSlot uint64, pageSize uint64, displayColumns string) (*models.BlocksPageData, time.Duration) {
	logrus.Debugf("blocks page called: %v:%v", firstSlot, pageSize)
	pageData := &models.BlocksPageData{}

	// Set display columns based on the parameter
	displayMap := map[uint64]bool{}
	displayList := []string{}
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
			7:  false,
			8:  false,
			9:  false,
			10: true,
			11: false,
			12: false,
			13: true,
			14: true,
			15: true,
			16: true,
			17: true,
		}
	} else {
		for col := range displayMap {
			displayList = append(displayList, fmt.Sprintf("%v", col))
		}
	}

	pageData.DisplayChain = displayMap[1]
	pageData.DisplayNumber = displayMap[2]
	pageData.DisplaySlot = displayMap[3]
	pageData.DisplayStatus = displayMap[4]
	pageData.DisplayTime = displayMap[5]
	pageData.DisplayProposer = displayMap[6]
	pageData.DisplayAttestations = displayMap[7]
	pageData.DisplayDeposits = displayMap[8]
	pageData.DisplaySlashings = displayMap[9]
	pageData.DisplayTxCount = displayMap[10]
	pageData.DisplaySyncAgg = displayMap[11]
	pageData.DisplayGraffiti = displayMap[12]
	pageData.DisplayElExtraData = displayMap[13]
	pageData.DisplayGasUsage = displayMap[14]
	pageData.DisplayGasLimit = displayMap[15]
	pageData.DisplayMevBlock = displayMap[16]
	pageData.DisplayBlockSize = displayMap[17]
	pageData.DisplayColCount = uint64(len(displayMap))

	// Build column selection URL parameter if not default
	displayColumnsParam := ""
	if len(displayList) > 0 {
		sort.Slice(displayList, func(a, b int) bool {
			colA, _ := strconv.ParseUint(displayList[a], 10, 64)
			colB, _ := strconv.ParseUint(displayList[b], 10, 64)
			return colA < colB
		})
		displayColumnsParam = "&d=" + strings.Join(displayList, "+")
	}

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

	// Add pagination links with column selection preserved
	pageData.FirstPageLink = fmt.Sprintf("/blocks?c=%v%v", pageData.PageSize, displayColumnsParam)
	pageData.PrevPageLink = fmt.Sprintf("/blocks?s=%v&c=%v%v", pageData.PrevPageSlot, pageData.PageSize, displayColumnsParam)
	pageData.NextPageLink = fmt.Sprintf("/blocks?s=%v&c=%v%v", pageData.NextPageSlot, pageData.PageSize, displayColumnsParam)
	pageData.LastPageLink = fmt.Sprintf("/blocks?s=%v&c=%v%v", pageData.LastPageSlot, pageData.PageSize, displayColumnsParam)

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	slotLimit := pageSize - 1
	var lastSlot uint64
	if firstSlot > uint64(slotLimit) {
		lastSlot = firstSlot - uint64(slotLimit)
	} else {
		lastSlot = 0
	}

	// Get slot assignments
	firstEpoch := chainState.EpochOfSlot(phase0.Slot(firstSlot))

	// load blocks
	pageData.Blocks = make([]*models.BlocksPageDataSlot, 0)
	dbBlocks := services.GlobalBeaconService.GetDbBlocksForSlots(firstSlot, uint32(pageSize), false, true)
	dbIdx := 0
	dbCnt := len(dbBlocks)
	blockCount := uint64(0)
	allFinalized := true
	allSynchronized := true
	isFirstPage := firstSlot >= uint64(currentSlot)
	openForks := map[int][]byte{}
	maxOpenFork := 0

	mevBlocksMap := make(map[string]*dbtypes.MevBlock)

	if pageData.DisplayMevBlock {
		var execBlockHashes [][]byte

		for _, dbSlot := range dbBlocks {
			if dbSlot != nil && dbSlot.Status > 0 && dbSlot.EthBlockHash != nil {
				execBlockHashes = append(execBlockHashes, dbSlot.EthBlockHash)
			}
		}

		if len(execBlockHashes) > 0 {
			mevBlocksMap = db.GetMevBlocksByBlockHashes(execBlockHashes)
		}
	}

	for slotIdx := int64(firstSlot); slotIdx >= int64(lastSlot); slotIdx-- {
		slot := uint64(slotIdx)
		finalized := finalizedEpoch > 0 && finalizedEpoch >= chainState.EpochOfSlot(phase0.Slot(slot))
		if !finalized {
			allFinalized = false
		}

		for dbIdx < dbCnt && dbBlocks[dbIdx] != nil && dbBlocks[dbIdx].Slot == slot {
			dbSlot := dbBlocks[dbIdx]
			dbIdx++

			slotData := &models.BlocksPageDataSlot{
				Slot:                  slot,
				Epoch:                 uint64(chainState.EpochOfSlot(phase0.Slot(slot))),
				Ts:                    chainState.SlotToTime(phase0.Slot(slot)),
				Finalized:             finalized,
				Status:                uint8(dbSlot.Status),
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
				BlobCount:             dbSlot.BlobCount,
				Graffiti:              dbSlot.Graffiti,
				ElExtraData:           dbSlot.EthBlockExtra,
				GasUsed:               dbSlot.EthGasUsed,
				GasLimit:              dbSlot.EthGasLimit,
				BlockSize:             dbSlot.BlockSize,
				BlockRoot:             dbSlot.Root,
				ParentRoot:            dbSlot.ParentRoot,
				ForkGraph:             make([]*models.BlocksPageDataForkGraph, 0),
			}
			if dbSlot.EthBlockNumber != nil {
				slotData.WithEthBlock = true
				slotData.EthBlockNumber = *dbSlot.EthBlockNumber
			}

			if pageData.DisplayMevBlock && dbSlot.EthBlockHash != nil {
				if mevBlock, exists := mevBlocksMap[fmt.Sprintf("%x", dbSlot.EthBlockHash)]; exists {
					slotData.IsMevBlock = true

					var relays []string
					for _, relay := range utils.Config.MevIndexer.Relays {
						relayFlag := uint64(1) << uint64(relay.Index)
						if mevBlock.SeenbyRelays&relayFlag > 0 {
							relays = append(relays, relay.Name)
						}
					}
					slotData.MevBlockRelays = strings.Join(relays, ", ")
				}
			}

			pageData.Blocks = append(pageData.Blocks, slotData)
			blockCount++
			buildBlocksPageSlotGraph(pageData, slotData, &maxOpenFork, openForks, isFirstPage)
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

func buildBlocksPageSlotGraph(pageData *models.BlocksPageData, slotData *models.BlocksPageDataSlot, maxOpenFork *int, openForks map[int][]byte, isFirstPage bool) {
	// fork tree
	var forkGraphIdx int = -1
	var freeForkIdx int = -1
	getForkGraph := func(slotData *models.BlocksPageDataSlot, forkIdx int) *models.BlocksPageDataForkGraph {
		forkGraph := &models.BlocksPageDataForkGraph{}
		graphCount := len(slotData.ForkGraph)
		if graphCount > forkIdx {
			forkGraph = slotData.ForkGraph[forkIdx]
		} else {
			for graphCount <= forkIdx {
				forkGraph = &models.BlocksPageDataForkGraph{
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
				for _, slot := range pageData.Blocks {
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
