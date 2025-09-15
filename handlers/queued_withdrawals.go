package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// QueuedWithdrawals will return the filtered "queued_withdrawals" page using a go template
func QueuedWithdrawals(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"queued_withdrawals/queued_withdrawals.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/queued_withdrawals", "Queued Withdrawals", templateFiles)

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

	var minIndex uint64
	var maxIndex uint64
	var validatorName string
	var pubkey string

	if urlArgs.Has("f") {
		if urlArgs.Has("f.mini") {
			minIndex, _ = strconv.ParseUint(urlArgs.Get("f.mini"), 10, 64)
		}
		if urlArgs.Has("f.maxi") {
			maxIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxi"), 10, 64)
		}
		if urlArgs.Has("f.vname") {
			validatorName = urlArgs.Get("f.vname")
		}
		if urlArgs.Has("f.pubkey") {
			pubkey = urlArgs.Get("f.pubkey")
		}
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredQueuedWithdrawalsPageData(pageIdx, pageSize, minIndex, maxIndex, validatorName, pubkey)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "queued_withdrawals.go", "Queued Withdrawals", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredQueuedWithdrawalsPageData(pageIdx uint64, pageSize uint64, minIndex uint64, maxIndex uint64, validatorName string, pubkey string) (*models.QueuedWithdrawalsPageData, error) {
	pageData := &models.QueuedWithdrawalsPageData{}
	pageCacheKey := fmt.Sprintf("queued_withdrawals:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minIndex, maxIndex, validatorName, pubkey)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredQueuedWithdrawalsPageData(pageIdx, pageSize, minIndex, maxIndex, validatorName, pubkey)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.QueuedWithdrawalsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredQueuedWithdrawalsPageData(pageIdx uint64, pageSize uint64, minIndex uint64, maxIndex uint64, validatorName string, pubkey string) *models.QueuedWithdrawalsPageData {
	filterArgs := url.Values{}
	if minIndex != 0 {
		filterArgs.Add("f.mini", fmt.Sprintf("%v", minIndex))
	}
	if maxIndex != 0 {
		filterArgs.Add("f.maxi", fmt.Sprintf("%v", maxIndex))
	}
	if validatorName != "" {
		filterArgs.Add("f.vname", validatorName)
	}
	if pubkey != "" {
		filterArgs.Add("f.pubkey", pubkey)
	}

	pageData := &models.QueuedWithdrawalsPageData{
		FilterMinIndex:      minIndex,
		FilterMaxIndex:      maxIndex,
		FilterValidatorName: validatorName,
		FilterPublicKey:     pubkey,
	}

	logrus.Debugf("queued_withdrawals page called: %v:%v [%v,%v,%v,%v]", pageIdx, pageSize, minIndex, maxIndex, validatorName, pubkey)
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

	// Build queue filter
	queueFilter := &services.WithdrawalQueueFilter{
		ValidatorName: validatorName,
		PublicKey:     common.FromHex(pubkey),
	}
	if minIndex > 0 {
		queueFilter.MinValidatorIndex = &minIndex
	}
	if maxIndex > 0 {
		queueFilter.MaxValidatorIndex = &maxIndex
	}

	dbQueuedWithdrawals, totalQueuedWithdrawals, _ := services.GlobalBeaconService.GetWithdrawalQueueByFilter(queueFilter, (pageIdx-1)*pageSize, uint32(pageSize))
	chainState := services.GlobalBeaconService.GetChainState()

	for _, queueEntry := range dbQueuedWithdrawals {
		withdrawalData := &models.QueuedWithdrawalsPageDataWithdrawal{
			ValidatorIndex:    uint64(queueEntry.ValidatorIndex),
			ValidatorName:     queueEntry.ValidatorName,
			Amount:            uint64(queueEntry.Amount),
			WithdrawableEpoch: uint64(queueEntry.WithdrawableEpoch),
			PublicKey:         queueEntry.Validator.Validator.PublicKey[:],
		}

		if strings.HasPrefix(queueEntry.Validator.Status.String(), "pending") {
			withdrawalData.ValidatorStatus = "Pending"
		} else if queueEntry.Validator.Status == v1.ValidatorStateActiveOngoing {
			withdrawalData.ValidatorStatus = "Active"
			withdrawalData.ShowUpcheck = true
		} else if queueEntry.Validator.Status == v1.ValidatorStateActiveExiting {
			withdrawalData.ValidatorStatus = "Exiting"
			withdrawalData.ShowUpcheck = true
		} else if queueEntry.Validator.Status == v1.ValidatorStateActiveSlashed {
			withdrawalData.ValidatorStatus = "Slashed"
			withdrawalData.ShowUpcheck = true
		} else if queueEntry.Validator.Status == v1.ValidatorStateExitedUnslashed {
			withdrawalData.ValidatorStatus = "Exited"
		} else if queueEntry.Validator.Status == v1.ValidatorStateExitedSlashed {
			withdrawalData.ValidatorStatus = "Slashed"
		} else {
			withdrawalData.ValidatorStatus = queueEntry.Validator.Status.String()
		}

		if withdrawalData.ShowUpcheck {
			withdrawalData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(queueEntry.Validator.Index, 3))
			withdrawalData.UpcheckMaximum = uint8(3)
		}

		// Use the calculated EstimatedWithdrawalTime from the queue entry
		withdrawalData.EstimatedTime = chainState.SlotToTime(queueEntry.EstimatedWithdrawalTime)

		pageData.QueuedWithdrawals = append(pageData.QueuedWithdrawals, withdrawalData)
	}

	pageData.WithdrawalCount = uint64(len(pageData.QueuedWithdrawals))

	if pageData.WithdrawalCount > 0 {
		pageData.FirstIndex = pageData.QueuedWithdrawals[0].ValidatorIndex
		if len(pageData.QueuedWithdrawals) > 0 {
			pageData.LastIndex = pageData.QueuedWithdrawals[pageData.WithdrawalCount-1].ValidatorIndex
		}
	}

	pageData.TotalPages = totalQueuedWithdrawals / pageSize
	if totalQueuedWithdrawals%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	// Populate UrlParams for page jump functionality
	pageData.UrlParams = make(map[string]string)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams[key] = values[0]
		}
	}
	pageData.UrlParams["c"] = fmt.Sprintf("%v", pageData.PageSize)

	pageData.FirstPageLink = fmt.Sprintf("/validators/queued_withdrawals?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/queued_withdrawals?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/queued_withdrawals?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/queued_withdrawals?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
