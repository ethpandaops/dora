package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
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
	var withOrphaned uint64

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
	} else {
		withOrphaned = 1
	}
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredElConsolidationsPageData(pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minSrcIndex, maxSrcIndex, srcVName, minTgtIndex, maxTgtIndex, tgtVName, uint8(withOrphaned))
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

func getFilteredElConsolidationsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, sourceAddr string, minSrcIndex uint64, maxSrcIndex uint64, srcVName string, minTgtIndex uint64, maxTgtIndex uint64, tgtVName string, withOrphaned uint8) (*models.ElConsolidationsPageData, error) {
	pageData := &models.ElConsolidationsPageData{}
	pageCacheKey := fmt.Sprintf("el_consolidations:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minSrcIndex, maxSrcIndex, srcVName, minTgtIndex, maxTgtIndex, tgtVName, withOrphaned)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredElConsolidationsPageData(pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minSrcIndex, maxSrcIndex, srcVName, minTgtIndex, maxTgtIndex, tgtVName, withOrphaned)
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

func buildFilteredElConsolidationsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, sourceAddr string, minSrcIndex uint64, maxSrcIndex uint64, srcVName string, minTgtIndex uint64, maxTgtIndex uint64, tgtVName string, withOrphaned uint8) *models.ElConsolidationsPageData {
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
	if withOrphaned != 0 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
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

	// load voluntary exits
	consolidationRequestFilter := &dbtypes.ConsolidationRequestFilter{
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
	}

	dbElConsolidations, totalRows := services.GlobalBeaconService.GetConsolidationRequestsByFilter(consolidationRequestFilter, pageIdx-1, uint32(pageSize))

	chainState := services.GlobalBeaconService.GetChainState()
	validatorSetRsp := services.GlobalBeaconService.GetCachedValidatorSet()
	matcherHeight := services.GlobalBeaconService.GetConsolidationIndexer().GetMatcherHeight()

	for _, elConsolidation := range dbElConsolidations {
		elConsolidationData := &models.ElConsolidationsPageDataConsolidation{
			SlotNumber:      elConsolidation.SlotNumber,
			SlotRoot:        elConsolidation.SlotRoot,
			Time:            chainState.SlotToTime(phase0.Slot(elConsolidation.SlotNumber)),
			Orphaned:        elConsolidation.Orphaned,
			SourceAddr:      elConsolidation.SourceAddress,
			SourcePublicKey: elConsolidation.SourcePubkey,
			TargetPublicKey: elConsolidation.TargetPubkey,
			TransactionHash: elConsolidation.TxHash,
		}

		if elConsolidation.SourceIndex != nil {
			elConsolidationData.SourceValidatorIndex = *elConsolidation.SourceIndex
			elConsolidationData.SourceValidatorName = services.GlobalBeaconService.GetValidatorName(*elConsolidation.SourceIndex)

			if uint64(len(validatorSetRsp)) > elConsolidationData.SourceValidatorIndex && validatorSetRsp[elConsolidationData.SourceValidatorIndex] != nil {
				elConsolidationData.SourceValidatorValid = true
			}
		}

		if elConsolidation.TargetIndex != nil {
			elConsolidationData.TargetValidatorIndex = *elConsolidation.TargetIndex
			elConsolidationData.TargetValidatorName = services.GlobalBeaconService.GetValidatorName(*elConsolidation.TargetIndex)

			if uint64(len(validatorSetRsp)) > elConsolidationData.TargetValidatorIndex && validatorSetRsp[elConsolidationData.TargetValidatorIndex] != nil {
				elConsolidationData.TargetValidatorValid = true
			}
		}

		if elConsolidation.BlockNumber > matcherHeight && len(elConsolidationData.TransactionHash) == 0 {
			// consolidation request has been matched with a tx yet, try to find the tx on the fly
			consolidationRequestTx := db.GetConsolidationRequestTxsByDequeueRange(elConsolidation.BlockNumber, elConsolidation.BlockNumber)
			if len(consolidationRequestTx) > 1 {
				forkIds := services.GlobalBeaconService.GetParentForkIds(beacon.ForkKey(elConsolidation.ForkId))
				isParentFork := func(forkId uint64) bool {
					for _, parentForkId := range forkIds {
						if uint64(parentForkId) == forkId {
							return true
						}
					}
					return false
				}

				matchingTxs := []*dbtypes.ConsolidationRequestTx{}
				for _, tx := range consolidationRequestTx {
					if isParentFork(tx.ForkId) {
						matchingTxs = append(matchingTxs, tx)
					}
				}

				if len(matchingTxs) >= int(elConsolidation.SlotIndex)+1 {
					elConsolidationData.TransactionHash = matchingTxs[elConsolidation.SlotIndex].TxHash
				}

			} else if len(consolidationRequestTx) == 1 {
				elConsolidationData.TransactionHash = consolidationRequestTx[0].TxHash
			}
		}

		if len(elConsolidationData.TransactionHash) > 0 {
			elConsolidationData.LinkedTransaction = true
		}

		pageData.ElRequests = append(pageData.ElRequests, elConsolidationData)
	}
	pageData.RequestCount = uint64(len(pageData.ElRequests))

	if pageData.RequestCount > 0 {
		pageData.FirstIndex = pageData.ElRequests[0].SlotNumber
		pageData.LastIndex = pageData.ElRequests[pageData.RequestCount-1].SlotNumber
	}

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
