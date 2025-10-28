package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
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
	var invertgraffiti bool
	var invertextradata bool
	var invertproposer bool
	var withOrphaned uint64
	var withMissing uint64
	var minSyncAgg string
	var maxSyncAgg string
	var minExecTime string
	var maxExecTime string
	var minTxCount string
	var maxTxCount string
	var minBlobCount string
	var maxBlobCount string
	var forkIds string

	if urlArgs.Has("f") {
		if urlArgs.Has("f.graffiti") {
			graffiti = urlArgs.Get("f.graffiti")
		}
		if urlArgs.Has("f.ginvert") {
			invertgraffiti = urlArgs.Get("f.ginvert") == "on"
		}
		if urlArgs.Has("f.extra") {
			extradata = urlArgs.Get("f.extra")
		}
		if urlArgs.Has("f.einvert") {
			invertextradata = urlArgs.Get("f.einvert") == "on"
		}
		if urlArgs.Has("f.proposer") {
			proposer = urlArgs.Get("f.proposer")
		}
		if urlArgs.Has("f.pname") {
			pname = urlArgs.Get("f.pname")
		}
		if urlArgs.Has("f.pinvert") {
			invertproposer = urlArgs.Get("f.pinvert") == "on"
		}
		if urlArgs.Has("f.orphaned") {
			withOrphaned, _ = strconv.ParseUint(urlArgs.Get("f.orphaned"), 10, 64)
		}
		if urlArgs.Has("f.missing") {
			withMissing, _ = strconv.ParseUint(urlArgs.Get("f.missing"), 10, 64)
		}
		if urlArgs.Has("f.minsync") {
			minSyncAgg = urlArgs.Get("f.minsync")
		}
		if urlArgs.Has("f.maxsync") {
			maxSyncAgg = urlArgs.Get("f.maxsync")
		}
		if urlArgs.Has("f.minexec") {
			minExecTime = urlArgs.Get("f.minexec")
		}
		if urlArgs.Has("f.maxexec") {
			maxExecTime = urlArgs.Get("f.maxexec")
		}
		if urlArgs.Has("f.mintx") {
			minTxCount = urlArgs.Get("f.mintx")
		}
		if urlArgs.Has("f.maxtx") {
			maxTxCount = urlArgs.Get("f.maxtx")
		}
		if urlArgs.Has("f.minblob") {
			minBlobCount = urlArgs.Get("f.minblob")
		}
		if urlArgs.Has("f.maxblob") {
			maxBlobCount = urlArgs.Get("f.maxblob")
		}
		if urlArgs.Has("f.fork") {
			forkIds = urlArgs.Get("f.fork")
		}
	} else {
		withOrphaned = 1
		withMissing = 1
	}
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredSlotsPageData(pageIdx, pageSize, graffiti, invertgraffiti, extradata, invertextradata, proposer, pname, invertproposer, uint8(withOrphaned), uint8(withMissing), minSyncAgg, maxSyncAgg, minExecTime, maxExecTime, minTxCount, maxTxCount, minBlobCount, maxBlobCount, forkIds, displayColumns)
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

func getFilteredSlotsPageData(pageIdx uint64, pageSize uint64, graffiti string, invertgraffiti bool, extradata string, invertextradata bool, proposer string, pname string, invertproposer bool, withOrphaned uint8, withMissing uint8, minSyncAgg string, maxSyncAgg string, minExecTime string, maxExecTime string, minTxCount string, maxTxCount string, minBlobCount string, maxBlobCount string, forkIds string, displayColumns string) (*models.SlotsFilteredPageData, error) {
	pageData := &models.SlotsFilteredPageData{}
	pageCacheKey := fmt.Sprintf("slots_filtered:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, graffiti, invertgraffiti, extradata, invertextradata, proposer, pname, invertproposer, withOrphaned, withMissing, minSyncAgg, maxSyncAgg, minExecTime, maxExecTime, minTxCount, maxTxCount, minBlobCount, maxBlobCount, forkIds, displayColumns)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredSlotsPageData(pageIdx, pageSize, graffiti, invertgraffiti, extradata, invertextradata, proposer, pname, invertproposer, withOrphaned, withMissing, minSyncAgg, maxSyncAgg, minExecTime, maxExecTime, minTxCount, maxTxCount, minBlobCount, maxBlobCount, forkIds, displayColumns)
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

func buildFilteredSlotsPageData(pageIdx uint64, pageSize uint64, graffiti string, invertgraffiti bool, extradata string, invertextradata bool, proposer string, pname string, invertproposer bool, withOrphaned uint8, withMissing uint8, minSyncAgg string, maxSyncAgg string, minExecTime string, maxExecTime string, minTxCount string, maxTxCount string, minBlobCount string, maxBlobCount string, forkIds string, displayColumns string) *models.SlotsFilteredPageData {
	chainState := services.GlobalBeaconService.GetChainState()
	filterArgs := url.Values{}
	if graffiti != "" {
		filterArgs.Add("f.graffiti", graffiti)
	}
	if invertgraffiti {
		filterArgs.Add("f.ginvert", "on")
	}
	if extradata != "" {
		filterArgs.Add("f.extra", extradata)
	}
	if invertextradata {
		filterArgs.Add("f.einvert", "on")
	}
	if proposer != "" {
		filterArgs.Add("f.proposer", proposer)
	}
	if pname != "" {
		filterArgs.Add("f.pname", pname)
	}
	if invertproposer {
		filterArgs.Add("f.pinvert", "on")
	}
	if withOrphaned != 0 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}
	if withMissing != 0 {
		filterArgs.Add("f.missing", fmt.Sprintf("%v", withMissing))
	}
	if minSyncAgg != "" {
		filterArgs.Add("f.minsync", minSyncAgg)
	}
	if maxSyncAgg != "" {
		filterArgs.Add("f.maxsync", maxSyncAgg)
	}
	if minExecTime != "" {
		filterArgs.Add("f.minexec", minExecTime)
	}
	if maxExecTime != "" {
		filterArgs.Add("f.maxexec", maxExecTime)
	}
	if minTxCount != "" {
		filterArgs.Add("f.mintx", minTxCount)
	}
	if maxTxCount != "" {
		filterArgs.Add("f.maxtx", maxTxCount)
	}
	if minBlobCount != "" {
		filterArgs.Add("f.minblob", minBlobCount)
	}
	if maxBlobCount != "" {
		filterArgs.Add("f.maxblob", maxBlobCount)
	}
	if forkIds != "" {
		filterArgs.Add("f.fork", forkIds)
	}

	// Check if snooper clients are configured
	hasSnooperClients := false
	if snooperManager := services.GlobalBeaconService.GetSnooperManager(); snooperManager != nil {
		hasSnooperClients = snooperManager.HasClients()
	}

	displayMap := map[uint64]bool{}
	if displayColumns != "" {
		for col := range strings.SplitSeq(displayColumns, " ") {
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
			12: false,
			13: false,
			14: false,
			15: false,
			16: false,
			17: !hasSnooperClients, // Disable receive delay if snooper clients exist
			18: hasSnooperClients,  // Enable exec time if snooper clients exist
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
		FilterGraffiti:        graffiti,
		FilterExtraData:       extradata,
		FilterProposer:        proposer,
		FilterProposerName:    pname,
		FilterInvertGraffiti:  invertgraffiti,
		FilterInvertExtraData: invertextradata,
		FilterInvertProposer:  invertproposer,
		FilterWithOrphaned:    withOrphaned,
		FilterWithMissing:     withMissing,
		FilterMinSyncAgg:      minSyncAgg,
		FilterMaxSyncAgg:      maxSyncAgg,
		FilterMinExecTime:     minExecTime,
		FilterMaxExecTime:     maxExecTime,
		FilterMinTxCount:      minTxCount,
		FilterMaxTxCount:      maxTxCount,
		FilterMinBlobCount:    minBlobCount,
		FilterMaxBlobCount:    maxBlobCount,
		FilterForkIds:         forkIds,

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
		DisplayGasUsage:     displayMap[13],
		DisplayGasLimit:     displayMap[14],
		DisplayMevBlock:     displayMap[15],
		DisplayBlockSize:    displayMap[16],
		DisplayRecvDelay:    displayMap[17],
		DisplayExecTime:     displayMap[18],
		DisplayColCount:     uint64(len(displayMap)),

		HasSnooperClients: hasSnooperClients,
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
		Graffiti:        graffiti,
		ExtraData:       extradata,
		ProposerName:    pname,
		InvertGraffiti:  invertgraffiti,
		InvertExtraData: invertextradata,
		InvertProposer:  invertproposer,
		WithOrphaned:    withOrphaned,
		WithMissing:     withMissing,
	}
	if proposer != "" {
		pidx, _ := strconv.ParseUint(proposer, 10, 64)
		blockFilter.ProposerIndex = &pidx
	}
	if minSyncAgg != "" {
		minSync, err := strconv.ParseFloat(minSyncAgg, 32)
		if err == nil {
			minSyncFloat32 := float32(minSync)
			// Convert percentage (0-100) to ratio (0-1)
			minSyncFloat32 = minSyncFloat32 / 100.0
			blockFilter.MinSyncParticipation = &minSyncFloat32
		}
	}
	if maxSyncAgg != "" {
		maxSync, err := strconv.ParseFloat(maxSyncAgg, 32)
		if err == nil {
			maxSyncFloat32 := float32(maxSync)
			// Convert percentage (0-100) to ratio (0-1)
			maxSyncFloat32 = maxSyncFloat32 / 100.0
			blockFilter.MaxSyncParticipation = &maxSyncFloat32
		}
	}
	if minExecTime != "" {
		minExec, err := strconv.ParseUint(minExecTime, 10, 32)
		if err == nil {
			minExecUint32 := uint32(minExec)
			blockFilter.MinExecTime = &minExecUint32
		}
	}
	if maxExecTime != "" {
		maxExec, err := strconv.ParseUint(maxExecTime, 10, 32)
		if err == nil {
			maxExecUint32 := uint32(maxExec)
			blockFilter.MaxExecTime = &maxExecUint32
		}
	}
	if minTxCount != "" {
		minTx, err := strconv.ParseUint(minTxCount, 10, 64)
		if err == nil {
			blockFilter.MinTxCount = &minTx
		}
	}
	if maxTxCount != "" {
		maxTx, err := strconv.ParseUint(maxTxCount, 10, 64)
		if err == nil {
			blockFilter.MaxTxCount = &maxTx
		}
	}
	if minBlobCount != "" {
		minBlob, err := strconv.ParseUint(minBlobCount, 10, 64)
		if err == nil {
			blockFilter.MinBlobCount = &minBlob
		}
	}
	if maxBlobCount != "" {
		maxBlob, err := strconv.ParseUint(maxBlobCount, 10, 64)
		if err == nil {
			blockFilter.MaxBlobCount = &maxBlob
		}
	}
	if forkIds != "" {
		forkIdList := strings.Split(forkIds, ",")
		parsedForkIds := make([]uint64, 0, len(forkIdList))
		for _, forkIdStr := range forkIdList {
			forkIdStr = strings.TrimSpace(forkIdStr)
			if forkIdStr != "" {
				forkId, err := strconv.ParseUint(forkIdStr, 10, 64)
				if err == nil {
					parsedForkIds = append(parsedForkIds, forkId)
				}
			}
		}
		if len(parsedForkIds) > 0 {
			blockFilter.ForkIds = parsedForkIds
		}
	}

	withScheduledCount := chainState.GetSpecs().SlotsPerEpoch - uint64(chainState.SlotToSlotIndex(currentSlot)) - 1
	if withScheduledCount > 16 {
		withScheduledCount = 16
	}

	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, pageIdx, uint32(pageSize), withScheduledCount)
	mevBlocksMap := make(map[string]*dbtypes.MevBlock)

	if pageData.DisplayMevBlock {
		var execBlockHashes [][]byte

		for _, dbBlock := range dbBlocks {
			if dbBlock.Block != nil && dbBlock.Block.Status > 0 && dbBlock.Block.EthBlockHash != nil {
				execBlockHashes = append(execBlockHashes, dbBlock.Block.EthBlockHash)
			}
		}

		if len(execBlockHashes) > 0 {
			mevBlocksMap = db.GetMevBlocksByBlockHashes(execBlockHashes)
		}
	}

	haveMore := false
	for idx, dbBlock := range dbBlocks {
		if idx >= int(pageSize) {
			haveMore = true
			break
		}
		slot := phase0.Slot(dbBlock.Slot)

		slotData := &models.SlotsFilteredPageDataSlot{
			Slot:         uint64(slot),
			Epoch:        uint64(chainState.EpochOfSlot(slot)),
			Ts:           chainState.SlotToTime(slot),
			Finalized:    finalizedEpoch >= chainState.EpochOfSlot(slot),
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
			slotData.BlobCount = dbBlock.Block.BlobCount
			slotData.Graffiti = dbBlock.Block.Graffiti
			slotData.ElExtraData = dbBlock.Block.EthBlockExtra
			slotData.GasUsed = dbBlock.Block.EthGasUsed
			slotData.GasLimit = dbBlock.Block.EthGasLimit
			slotData.BlockSize = dbBlock.Block.BlockSize
			slotData.BlockRoot = dbBlock.Block.Root
			slotData.RecvDelay = dbBlock.Block.RecvDelay
			if dbBlock.Block.EthBlockNumber != nil {
				slotData.WithEthBlock = true
				slotData.EthBlockNumber = *dbBlock.Block.EthBlockNumber
			}

			if pageData.DisplayMevBlock && dbBlock.Block.EthBlockHash != nil {
				if mevBlock, exists := mevBlocksMap[fmt.Sprintf("%x", dbBlock.Block.EthBlockHash)]; exists {
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

			// Add execution times if available
			if pageData.DisplayExecTime && dbBlock.Block.MinExecTime > 0 && dbBlock.Block.MaxExecTime > 0 {
				slotData.MinExecTime = dbBlock.Block.MinExecTime
				slotData.MaxExecTime = dbBlock.Block.MaxExecTime

				// Deserialize execution times if available
				if len(dbBlock.Block.ExecTimes) > 0 {
					execTimes := []beacon.ExecutionTime{}
					if err := services.GlobalBeaconService.GetBeaconIndexer().GetDynSSZ().UnmarshalSSZ(&execTimes, dbBlock.Block.ExecTimes); err == nil {
						slotData.ExecutionTimes = make([]models.ExecutionTimeDetail, 0, len(execTimes))
						totalAvg := uint64(0)
						totalCount := uint64(0)

						for _, et := range execTimes {
							detail := models.ExecutionTimeDetail{
								ClientType: getClientTypeName(et.ClientType),
								MinTime:    et.MinTime,
								MaxTime:    et.MaxTime,
								AvgTime:    et.AvgTime,
								Count:      et.Count,
							}
							slotData.ExecutionTimes = append(slotData.ExecutionTimes, detail)
							totalAvg += uint64(et.AvgTime) * uint64(et.Count)
							totalCount += uint64(et.Count)
						}

						if totalCount > 0 {
							slotData.AvgExecTime = uint32(totalAvg / totalCount)
						}
					}
				}

				// If we don't have detailed times, calculate average from min/max
				if slotData.AvgExecTime == 0 {
					slotData.AvgExecTime = (slotData.MinExecTime + slotData.MaxExecTime) / 2
				}
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

	// Populate UrlParams for page jump functionality
	pageData.UrlParams = make(map[string]string)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams[key] = values[0]
		}
	}
	pageData.UrlParams["c"] = fmt.Sprintf("%v", pageData.PageSize)

	pageData.FirstPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageSlot)
	pageData.NextPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageSlot)
	pageData.LastPageLink = fmt.Sprintf("/slots/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageSlot)

	return pageData
}
