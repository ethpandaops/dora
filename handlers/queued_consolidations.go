package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// QueuedConsolidations will return the filtered "queued_consolidations" page using a go template
func QueuedConsolidations(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"queued_consolidations/queued_consolidations.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/queued_consolidations", "Queued Consolidations", templateFiles)

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

	var minSrcIndex uint64
	var maxSrcIndex uint64
	var minTgtIndex uint64
	var maxTgtIndex uint64
	var validatorName string
	var pubkey string

	if urlArgs.Has("f") {
		if urlArgs.Has("f.minsi") {
			minSrcIndex, _ = strconv.ParseUint(urlArgs.Get("f.minsi"), 10, 64)
		}
		if urlArgs.Has("f.maxsi") {
			maxSrcIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxsi"), 10, 64)
		}
		if urlArgs.Has("f.minti") {
			minTgtIndex, _ = strconv.ParseUint(urlArgs.Get("f.minti"), 10, 64)
		}
		if urlArgs.Has("f.maxti") {
			maxTgtIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxti"), 10, 64)
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
		data.Data, pageError = getFilteredQueuedConsolidationsPageData(pageIdx, pageSize, minSrcIndex, maxSrcIndex, minTgtIndex, maxTgtIndex, validatorName, pubkey)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "queued_consolidations.go", "Queued Consolidations", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredQueuedConsolidationsPageData(pageIdx uint64, pageSize uint64, minSrcIndex uint64, maxSrcIndex uint64, minTgtIndex uint64, maxTgtIndex uint64, validatorName string, pubkey string) (*models.QueuedConsolidationsPageData, error) {
	pageData := &models.QueuedConsolidationsPageData{}
	pageCacheKey := fmt.Sprintf("queued_consolidations:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSrcIndex, maxSrcIndex, minTgtIndex, maxTgtIndex, validatorName, pubkey)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredQueuedConsolidationsPageData(pageCall.CallCtx, pageIdx, pageSize, minSrcIndex, maxSrcIndex, minTgtIndex, maxTgtIndex, validatorName, pubkey)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.QueuedConsolidationsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredQueuedConsolidationsPageData(ctx context.Context, pageIdx uint64, pageSize uint64, minSrcIndex uint64, maxSrcIndex uint64, minTgtIndex uint64, maxTgtIndex uint64, validatorName string, pubkey string) *models.QueuedConsolidationsPageData {
	filterArgs := url.Values{}
	if minSrcIndex != 0 {
		filterArgs.Add("f.minsi", fmt.Sprintf("%v", minSrcIndex))
	}
	if maxSrcIndex != 0 {
		filterArgs.Add("f.maxsi", fmt.Sprintf("%v", maxSrcIndex))
	}
	if minTgtIndex != 0 {
		filterArgs.Add("f.minti", fmt.Sprintf("%v", minTgtIndex))
	}
	if maxTgtIndex != 0 {
		filterArgs.Add("f.maxti", fmt.Sprintf("%v", maxTgtIndex))
	}
	if validatorName != "" {
		filterArgs.Add("f.vname", validatorName)
	}
	if pubkey != "" {
		filterArgs.Add("f.pubkey", pubkey)
	}

	pageData := &models.QueuedConsolidationsPageData{
		FilterMinSrcIndex:   minSrcIndex,
		FilterMaxSrcIndex:   maxSrcIndex,
		FilterMinTgtIndex:   minTgtIndex,
		FilterMaxTgtIndex:   maxTgtIndex,
		FilterValidatorName: validatorName,
		FilterPublicKey:     pubkey,
	}

	logrus.Debugf("queued_consolidations page called: %v:%v [%v,%v,%v,%v,%v,%v]", pageIdx, pageSize, minSrcIndex, maxSrcIndex, minTgtIndex, maxTgtIndex, validatorName, pubkey)
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
	queueFilter := &services.ConsolidationQueueFilter{
		ValidatorName: validatorName,
		PublicKey:     common.FromHex(pubkey),
	}
	if minSrcIndex > 0 {
		queueFilter.MinSrcIndex = &minSrcIndex
	}
	if maxSrcIndex > 0 {
		queueFilter.MaxSrcIndex = &maxSrcIndex
	}
	if minTgtIndex > 0 {
		queueFilter.MinTgtIndex = &minTgtIndex
	}
	if maxTgtIndex > 0 {
		queueFilter.MaxTgtIndex = &maxTgtIndex
	}

	dbQueuedConsolidations, totalQueuedConsolidations := services.GlobalBeaconService.GetConsolidationQueueByFilter(ctx, queueFilter, (pageIdx-1)*pageSize, pageSize)
	chainState := services.GlobalBeaconService.GetChainState()

	for _, queueEntry := range dbQueuedConsolidations {
		consolidationData := &models.QueuedConsolidationsPageDataConsolidation{}

		if queueEntry.SrcValidator != nil {
			consolidationData.SourceValidatorExists = true
			consolidationData.SourceValidatorIndex = uint64(queueEntry.SrcValidator.Index)
			consolidationData.SourceValidatorName = queueEntry.SrcValidatorName
			consolidationData.SourceEffectiveBalance = uint64(queueEntry.SrcValidator.Validator.EffectiveBalance)

			validator := services.GlobalBeaconService.GetValidatorByIndex(queueEntry.SrcValidator.Index, false)
			if strings.HasPrefix(validator.Status.String(), "pending") {
				consolidationData.SourceValidatorStatus = "Pending"
			} else if validator.Status == v1.ValidatorStateActiveOngoing {
				consolidationData.SourceValidatorStatus = "Active"
				consolidationData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateActiveExiting {
				consolidationData.SourceValidatorStatus = "Exiting"
				consolidationData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateActiveSlashed {
				consolidationData.SourceValidatorStatus = "Slashed"
				consolidationData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateExitedUnslashed {
				consolidationData.SourceValidatorStatus = "Exited"
			} else if validator.Status == v1.ValidatorStateExitedSlashed {
				consolidationData.SourceValidatorStatus = "Slashed"
			} else {
				consolidationData.SourceValidatorStatus = validator.Status.String()
			}

			if consolidationData.ShowUpcheck {
				consolidationData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				consolidationData.UpcheckMaximum = uint8(3)
			}

			// Get public key from validator
			consolidationData.SourcePublicKey = queueEntry.SrcValidator.Validator.PublicKey[:]

			if queueEntry.SrcValidator.Validator.WithdrawableEpoch != math.MaxUint64 {
				consolidationData.EstimatedTime = chainState.EpochToTime(queueEntry.SrcValidator.Validator.WithdrawableEpoch)
			} else {
				// WithdrawableEpoch not set yet for pending consolidation
				consolidationData.EstimatedTime = time.Time{}
			}
		}

		if queueEntry.TgtValidator != nil {
			consolidationData.TargetValidatorExists = true
			consolidationData.TargetValidatorIndex = uint64(queueEntry.TgtValidator.Index)
			consolidationData.TargetValidatorName = queueEntry.TgtValidatorName

			// Get public key from validator
			consolidationData.TargetPublicKey = queueEntry.TgtValidator.Validator.PublicKey[:]
		}

		pageData.QueuedConsolidations = append(pageData.QueuedConsolidations, consolidationData)
	}

	pageData.ConsolidationCount = uint64(len(pageData.QueuedConsolidations))

	if pageData.ConsolidationCount > 0 {
		pageData.FirstIndex = pageData.QueuedConsolidations[0].SourceValidatorIndex
		if len(pageData.QueuedConsolidations) > 0 {
			pageData.LastIndex = pageData.QueuedConsolidations[pageData.ConsolidationCount-1].SourceValidatorIndex
		}
	}

	pageData.TotalPages = totalQueuedConsolidations / pageSize
	if totalQueuedConsolidations%pageSize > 0 {
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

	pageData.FirstPageLink = fmt.Sprintf("/validators/queued_consolidations?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/queued_consolidations?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/queued_consolidations?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/queued_consolidations?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
