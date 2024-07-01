package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// ElRequests will return the filtered "el_requests" page using a go template
func ElRequests(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"el_requests/el_requests.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/requests", "EL Requests", templateFiles)

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
	var address string
	var minSrcIndex uint64
	var maxSrcIndex uint64
	var sourceName string
	var withRequestType uint64
	var withOrphaned uint64

	if urlArgs.Has("f") {
		if urlArgs.Has("f.mins") {
			minSlot, _ = strconv.ParseUint(urlArgs.Get("f.mins"), 10, 64)
		}
		if urlArgs.Has("f.maxs") {
			maxSlot, _ = strconv.ParseUint(urlArgs.Get("f.maxs"), 10, 64)
		}
		if urlArgs.Has("f.address") {
			address = urlArgs.Get("f.address")
		}
		if urlArgs.Has("f.srcmin") {
			minSrcIndex, _ = strconv.ParseUint(urlArgs.Get("f.srcmin"), 10, 64)
		}
		if urlArgs.Has("f.srcmax") {
			maxSrcIndex, _ = strconv.ParseUint(urlArgs.Get("f.srcmax"), 10, 64)
		}
		if urlArgs.Has("f.srcname") {
			sourceName = urlArgs.Get("f.srcname")
		}
		if urlArgs.Has("f.type") {
			withRequestType, _ = strconv.ParseUint(urlArgs.Get("f.type"), 10, 64)
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
		data.Data, pageError = getFilteredElRequestsPageData(pageIdx, pageSize, minSlot, maxSlot, address, minSrcIndex, maxSrcIndex, sourceName, uint8(withRequestType), uint8(withOrphaned))
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "el_requests.go", "ElRequests", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredElRequestsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, address string, minSrcIndex uint64, maxSrcIndex uint64, srcName string, withReqType uint8, withOrphaned uint8) (*models.ElRequestsPageData, error) {
	pageData := &models.ElRequestsPageData{}
	pageCacheKey := fmt.Sprintf("el_requests:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSlot, maxSlot, address, minSrcIndex, maxSrcIndex, srcName, withReqType, withOrphaned)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredElRequestsPageData(pageIdx, pageSize, minSlot, maxSlot, address, minSrcIndex, maxSrcIndex, srcName, withReqType, withOrphaned)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ElRequestsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredElRequestsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, address string, minSrcIndex uint64, maxSrcIndex uint64, srcName string, withReqType uint8, withOrphaned uint8) *models.ElRequestsPageData {
	filterArgs := url.Values{}
	if minSlot != 0 {
		filterArgs.Add("f.mins", fmt.Sprintf("%v", minSlot))
	}
	if maxSlot != 0 {
		filterArgs.Add("f.maxs", fmt.Sprintf("%v", maxSlot))
	}
	if address != "" {
		filterArgs.Add("f.address", address)
	}
	if minSrcIndex != 0 {
		filterArgs.Add("f.srcmin", fmt.Sprintf("%v", minSrcIndex))
	}
	if maxSrcIndex != 0 {
		filterArgs.Add("f.srcmax", fmt.Sprintf("%v", maxSrcIndex))
	}
	if srcName != "" {
		filterArgs.Add("f.srcname", srcName)
	}
	if withReqType != 0 {
		filterArgs.Add("f.type", fmt.Sprintf("%v", withReqType))
	}
	if withOrphaned != 0 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}

	pageData := &models.ElRequestsPageData{
		FilterMinSlot:        minSlot,
		FilterMaxSlot:        maxSlot,
		FilterSourceAddress:  address,
		FilterMinSourceIndex: minSrcIndex,
		FilterMaxSourceIndex: maxSrcIndex,
		FilterSourceName:     srcName,
		FilterWithRequest:    withReqType,
		FilterWithOrphaned:   withOrphaned,
	}
	logrus.Debugf("el_requests page called: %v:%v [%v,%v,%v,%v,%v]", pageIdx, pageSize, minSlot, maxSlot, minSrcIndex, maxSrcIndex, srcName)
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
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	if finalizedEpoch < 0 {
		finalizedEpoch = 0
	}

	voluntaryExitFilter := &dbtypes.ElRequestFilter{
		MinSlot:             minSlot,
		MaxSlot:             maxSlot,
		SourceAddress:       common.FromHex(address),
		MinSourceIndex:      minSrcIndex,
		MaxSourceIndex:      maxSrcIndex,
		SourceValidatorName: srcName,
		RequestType:         withReqType,
		WithOrphaned:        withOrphaned,
	}

	dbElRequests, totalRows := services.GlobalBeaconService.GetElRequestsByFilter(voluntaryExitFilter, pageIdx-1, uint32(pageSize))

	for _, elRequest := range dbElRequests {
		elRequestData := &models.ElRequestsPageDataRequest{
			SlotNumber:    elRequest.SlotNumber,
			SlotRoot:      elRequest.SlotRoot,
			Time:          utils.SlotToTime(elRequest.SlotNumber),
			Orphaned:      elRequest.Orphaned,
			RequestType:   elRequest.RequestType,
			SourceAddress: elRequest.SourceAddress,
			SourcePubkey:  elRequest.SourcePubkey,
			TargetPubkey:  elRequest.TargetPubkey,
		}

		if elRequest.SourceIndex != nil {
			elRequestData.SourceIndex = *elRequest.SourceIndex
			elRequestData.SourceName = services.GlobalBeaconService.GetValidatorName(*elRequest.SourceIndex)
			elRequestData.SourceIndexValid = true
		}

		if elRequest.TargetIndex != nil {
			elRequestData.TargetIndex = *elRequest.TargetIndex
			elRequestData.TargetName = services.GlobalBeaconService.GetValidatorName(*elRequest.TargetIndex)
			elRequestData.SourceIndexValid = true
		}

		if elRequest.Amount != nil {
			elRequestData.Amount = *elRequest.Amount
		}

		pageData.ElRequests = append(pageData.ElRequests, elRequestData)
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

	pageData.FirstPageLink = fmt.Sprintf("/validators/el_requests?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/el_requests?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/el_requests?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/el_requests?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
