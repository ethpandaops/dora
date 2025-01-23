package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// ElConsolidations will return the filtered "el_consolidations" page using a go template
func ElConsolidations(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"el_consolidations/el_consolidations.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/el_consolidations", "Consolidation Requests", templateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var pageIdx uint64 = 1
	if urlArgs.Has("p") {
		pageIdx, _ = strconv.ParseUint(urlArgs.Get("p"), 10, 64)
		if pageIdx < 1 {
			pageIdx = 1
		}
	}

	var minSlot uint64
	var maxSlot uint64
	var sourceAddr string
	var minSrcIndex uint64
	var maxSrcIndex uint64
	var srcVName string
	var minTgtIndex uint64
	var maxTgtIndex uint64
	var tgtVName string
	var withOrphaned uint64 = 1
	var pubkey string

	if urlArgs.Has("f") {
		if urlArgs.Has("f.mins") {
			minSlot, _ = strconv.ParseUint(urlArgs.Get("f.mins"), 10, 64)
		}
		if urlArgs.Has("f.maxs") {
			maxSlot, _ = strconv.ParseUint(urlArgs.Get("f.maxs"), 10, 64)
		}
		if urlArgs.Has("f.address") {
			sourceAddr = urlArgs.Get("f.address")
		}
		if urlArgs.Has("f.minsi") {
			minSrcIndex, _ = strconv.ParseUint(urlArgs.Get("f.minsi"), 10, 64)
		}
		if urlArgs.Has("f.maxsi") {
			maxSrcIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxsi"), 10, 64)
		}
		if urlArgs.Has("f.svname") {
			srcVName = urlArgs.Get("f.svname")
		}
		if urlArgs.Has("f.minti") {
			minTgtIndex, _ = strconv.ParseUint(urlArgs.Get("f.minti"), 10, 64)
		}
		if urlArgs.Has("f.maxti") {
			maxTgtIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxti"), 10, 64)
		}
		if urlArgs.Has("f.tvname") {
			tgtVName = urlArgs.Get("f.tvname")
		}
		if urlArgs.Has("f.orphaned") {
			withOrphaned, _ = strconv.ParseUint(urlArgs.Get("f.orphaned"), 10, 64)
		}
		if urlArgs.Has("f.pubkey") {
			pubkey = urlArgs.Get("f.pubkey")
		}
	}
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredElConsolidationsPageData(pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minSrcIndex, maxSrcIndex, srcVName, minTgtIndex, maxTgtIndex, tgtVName, uint8(withOrphaned), pubkey)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "el_consolidations.go", "Consolidation Requests", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredElConsolidationsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, sourceAddr string, minSrcIndex uint64, maxSrcIndex uint64, srcVName string, minTgtIndex uint64, maxTgtIndex uint64, tgtVName string, withOrphaned uint8, pubkey string) (*models.ElConsolidationsPageData, error) {
	pageData := &models.ElConsolidationsPageData{}
	pageCacheKey := fmt.Sprintf("el_consolidations:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minSrcIndex, maxSrcIndex, srcVName, minTgtIndex, maxTgtIndex, tgtVName, withOrphaned, pubkey)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredElConsolidationsPageData(pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minSrcIndex, maxSrcIndex, srcVName, minTgtIndex, maxTgtIndex, tgtVName, withOrphaned, pubkey)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ElConsolidationsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredElConsolidationsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, sourceAddr string, minSrcIndex uint64, maxSrcIndex uint64, srcVName string, minTgtIndex uint64, maxTgtIndex uint64, tgtVName string, withOrphaned uint8, pubkey string) *models.ElConsolidationsPageData {
	filterArgs := url.Values{}
	if minSlot != 0 {
		filterArgs.Add("f.mins", fmt.Sprintf("%v", minSlot))
	}
	if maxSlot != 0 {
		filterArgs.Add("f.maxs", fmt.Sprintf("%v", maxSlot))
	}
	if sourceAddr != "" {
		filterArgs.Add("f.address", sourceAddr)
	}
	if minSrcIndex != 0 {
		filterArgs.Add("f.minsi", fmt.Sprintf("%v", minSrcIndex))
	}
	if maxSrcIndex != 0 {
		filterArgs.Add("f.maxsi", fmt.Sprintf("%v", maxSrcIndex))
	}
	if srcVName != "" {
		filterArgs.Add("f.svname", srcVName)
	}
	if minTgtIndex != 0 {
		filterArgs.Add("f.minti", fmt.Sprintf("%v", minTgtIndex))
	}
	if maxTgtIndex != 0 {
		filterArgs.Add("f.maxti", fmt.Sprintf("%v", maxTgtIndex))
	}
	if tgtVName != "" {
		filterArgs.Add("f.tvname", tgtVName)
	}
	if withOrphaned != 1 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}
	if pubkey != "" {
		filterArgs.Add("f.pubkey", pubkey)
	}

	pageData := &models.ElConsolidationsPageData{
		FilterAddress:          sourceAddr,
		FilterMinSlot:          minSlot,
		FilterMaxSlot:          maxSlot,
		FilterMinSrcIndex:      minSrcIndex,
		FilterMaxSrcIndex:      maxSrcIndex,
		FilterSrcValidatorName: srcVName,
		FilterMinTgtIndex:      minTgtIndex,
		FilterMaxTgtIndex:      maxTgtIndex,
		FilterTgtValidatorName: tgtVName,
		FilterWithOrphaned:     withOrphaned,
		FilterPublicKey:        pubkey,
	}
	logrus.Debugf("el_consolidations page called: %v:%v [%v,%v,%v,%v,%v,%v,%v,%v]", pageIdx, pageSize, minSlot, maxSlot, minSrcIndex, maxSrcIndex, srcVName, minTgtIndex, maxTgtIndex, tgtVName)
	if pageIdx == 1 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pageIdx
	pageData.CurrentPageIndex = pageIdx
	if pageIdx > 1 {
		pageData.PrevPageIndex = pageIdx - 1
	}

	// Update the filter to use CombinedConsolidationRequestFilter
	consolidationRequestFilter := &services.CombinedConsolidationRequestFilter{
		Filter: &dbtypes.ConsolidationRequestFilter{
			MinSlot:          minSlot,
			MaxSlot:          maxSlot,
			SourceAddress:    common.FromHex(sourceAddr),
			MinSrcIndex:      minSrcIndex,
			MaxSrcIndex:      maxSrcIndex,
			SrcValidatorName: srcVName,
			MinTgtIndex:      minTgtIndex,
			MaxTgtIndex:      maxTgtIndex,
			TgtValidatorName: tgtVName,
			WithOrphaned:     withOrphaned,
			PublicKey:        common.FromHex(pubkey),
		},
	}

	dbElConsolidations, totalPendingTxRows, totalRequests := services.GlobalBeaconService.GetConsolidationRequestsByFilter(consolidationRequestFilter, (pageIdx-1)*pageSize, uint32(pageSize))
	chainState := services.GlobalBeaconService.GetChainState()
	headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
	headBlockNum := uint64(0)
	if headBlock != nil && headBlock.GetBlockIndex() != nil {
		headBlockNum = uint64(headBlock.GetBlockIndex().ExecutionNumber)
	}

	for _, consolidation := range dbElConsolidations {
		elConsolidationData := &models.ElConsolidationsPageDataConsolidation{
			SourceAddr:      consolidation.SourceAddress(),
			SourcePublicKey: consolidation.SourcePubkey(),
			TargetPublicKey: consolidation.TargetPubkey(),
		}

		if sourceIndex := consolidation.SourceIndex(); sourceIndex != nil {
			elConsolidationData.SourceValidatorIndex = *sourceIndex
			elConsolidationData.SourceValidatorName = services.GlobalBeaconService.GetValidatorName(*sourceIndex)
			elConsolidationData.SourceValidatorValid = true
		}

		if targetIndex := consolidation.TargetIndex(); targetIndex != nil {
			elConsolidationData.TargetValidatorIndex = *targetIndex
			elConsolidationData.TargetValidatorName = services.GlobalBeaconService.GetValidatorName(*targetIndex)
			elConsolidationData.TargetValidatorValid = true
		}

		if request := consolidation.Request; request != nil {
			elConsolidationData.IsIncluded = true
			elConsolidationData.SlotNumber = request.SlotNumber
			elConsolidationData.SlotRoot = request.SlotRoot
			elConsolidationData.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber))
			elConsolidationData.Status = uint64(1)
			if consolidation.RequestOrphaned {
				elConsolidationData.Status = uint64(2)
			}
		}

		if transaction := consolidation.Transaction; transaction != nil {
			elConsolidationData.TransactionHash = transaction.TxHash
			elConsolidationData.LinkedTransaction = true
			elConsolidationData.TransactionDetails = &models.ElConsolidationsPageDataConsolidationTxDetails{
				BlockNumber: transaction.BlockNumber,
				BlockHash:   fmt.Sprintf("%#x", transaction.BlockRoot),
				BlockTime:   transaction.BlockTime,
				TxOrigin:    common.Address(transaction.TxSender).Hex(),
				TxTarget:    common.Address(transaction.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("%#x", transaction.TxHash),
			}
			elConsolidationData.TxStatus = uint64(1)
			if consolidation.TransactionOrphaned {
				elConsolidationData.TxStatus = uint64(2)
			}

			if !elConsolidationData.IsIncluded {
				queuePos := int64(transaction.DequeueBlock) - int64(headBlockNum)
				targetSlot := int64(chainState.CurrentSlot()) + queuePos
				if targetSlot > 0 {
					elConsolidationData.SlotNumber = uint64(targetSlot)
					elConsolidationData.Time = chainState.SlotToTime(phase0.Slot(targetSlot))
				}
			}
		}

		pageData.ElRequests = append(pageData.ElRequests, elConsolidationData)
	}

	pageData.RequestCount = uint64(len(pageData.ElRequests))

	if pageData.RequestCount > 0 {
		pageData.FirstIndex = pageData.ElRequests[0].SlotNumber
		pageData.LastIndex = pageData.ElRequests[pageData.RequestCount-1].SlotNumber
	}

	totalRows := totalPendingTxRows + totalRequests
	pageData.TotalPages = totalRows / pageSize
	if totalRows%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	pageData.FirstPageLink = fmt.Sprintf("/validators/el_consolidations?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/el_consolidations?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/el_consolidations?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/el_consolidations?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
