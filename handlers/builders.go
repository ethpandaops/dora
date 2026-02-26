package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// Builders will return the main "builders" page using a go template
func Builders(w http.ResponseWriter, r *http.Request) {
	var buildersTemplateFiles = append(layoutTemplateFiles,
		"builders/builders.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(buildersTemplateFiles...)
	data := InitPageData(w, r, "builders", "/builders", "Builders", buildersTemplateFiles)

	urlArgs := r.URL.Query()
	var pageNumber uint64 = 1
	if urlArgs.Has("p") {
		pageNumber, _ = strconv.ParseUint(urlArgs.Get("p"), 10, 64)
	}
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	if urlArgs.Has("json") && pageSize > 10000 {
		pageSize = 10000
	} else if !urlArgs.Has("json") && pageSize > 1000 {
		pageSize = 1000
	}

	var filterPubKey string
	var filterIndex string
	var filterExecutionAddr string
	var filterStatus string
	if urlArgs.Has("f") {
		if urlArgs.Has("f.pubkey") {
			filterPubKey = urlArgs.Get("f.pubkey")
		}
		if urlArgs.Has("f.index") {
			filterIndex = urlArgs.Get("f.index")
		}
		if urlArgs.Has("f.execution_addr") {
			filterExecutionAddr = urlArgs.Get("f.execution_addr")
		}
		if urlArgs.Has("f.status") {
			filterStatus = strings.Join(urlArgs["f.status"], ",")
		}
	}
	var sortOrder string
	if urlArgs.Has("o") {
		sortOrder = urlArgs.Get("o")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getBuildersPageData(pageNumber, pageSize, sortOrder, filterPubKey, filterIndex, filterExecutionAddr, filterStatus)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	if urlArgs.Has("json") {
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(data.Data)
		if err != nil {
			logrus.WithError(err).Error("error encoding builders data")
			http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "builders.go", "Builders", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getBuildersPageData(pageNumber uint64, pageSize uint64, sortOrder string, filterPubKey string, filterIndex string, filterExecutionAddr string, filterStatus string) (*models.BuildersPageData, error) {
	pageData := &models.BuildersPageData{}
	pageCacheKey := fmt.Sprintf("builders:%v:%v:%v:%v:%v:%v:%v", pageNumber, pageSize, sortOrder, filterPubKey, filterIndex, filterExecutionAddr, filterStatus)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildBuildersPageData(pageCall.CallCtx, pageNumber, pageSize, sortOrder, filterPubKey, filterIndex, filterExecutionAddr, filterStatus)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.BuildersPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildBuildersPageData(ctx context.Context, pageNumber uint64, pageSize uint64, sortOrder string, filterPubKey string, filterIndex string, filterExecutionAddr string, filterStatus string) (*models.BuildersPageData, time.Duration) {
	logrus.Debugf("builders page called: %v:%v:%v:%v:%v:%v:%v", pageNumber, pageSize, sortOrder, filterPubKey, filterIndex, filterExecutionAddr, filterStatus)
	pageData := &models.BuildersPageData{}
	cacheTime := 10 * time.Minute

	chainState := services.GlobalBeaconService.GetChainState()

	builderFilter := dbtypes.BuilderFilter{
		Limit:  pageSize,
		Offset: (pageNumber - 1) * pageSize,
	}

	filterArgs := url.Values{}
	if filterPubKey != "" || filterIndex != "" || filterExecutionAddr != "" || filterStatus != "" {
		if filterPubKey != "" {
			pageData.FilterPubKey = filterPubKey
			filterArgs.Add("f.pubkey", filterPubKey)
			filterPubKeyVal, _ := hex.DecodeString(strings.Replace(filterPubKey, "0x", "", -1))
			builderFilter.PubKey = filterPubKeyVal
		}
		if filterIndex != "" {
			pageData.FilterIndex = filterIndex
			filterArgs.Add("f.index", filterIndex)
			filterIndexVal, _ := strconv.ParseUint(filterIndex, 10, 64)
			builderFilter.MinIndex = &filterIndexVal
			builderFilter.MaxIndex = &filterIndexVal
		}
		if filterExecutionAddr != "" {
			pageData.FilterExecutionAddr = filterExecutionAddr
			filterArgs.Add("f.execution_addr", filterExecutionAddr)
			filterExecutionAddrVal, _ := hex.DecodeString(strings.Replace(filterExecutionAddr, "0x", "", -1))
			builderFilter.ExecutionAddress = filterExecutionAddrVal
		}
		if filterStatus != "" {
			pageData.FilterStatus = filterStatus
			filterArgs.Add("f.status", filterStatus)
			filterStatusVal := strings.Split(filterStatus, ",")
			builderFilter.Status = make([]dbtypes.BuilderStatus, 0, len(filterStatusVal))
			for _, status := range filterStatusVal {
				switch status {
				case "active":
					builderFilter.Status = append(builderFilter.Status, dbtypes.BuilderStatusActiveFilter)
				case "exited":
					builderFilter.Status = append(builderFilter.Status, dbtypes.BuilderStatusExitedFilter)
				case "superseded":
					builderFilter.Status = append(builderFilter.Status, dbtypes.BuilderStatusSupersededFilter)
				}
			}
		}
	}

	// apply sort order
	switch sortOrder {
	case "index-d":
		builderFilter.OrderBy = dbtypes.BuilderOrderIndexDesc
	case "pubkey":
		builderFilter.OrderBy = dbtypes.BuilderOrderPubKeyAsc
	case "pubkey-d":
		builderFilter.OrderBy = dbtypes.BuilderOrderPubKeyDesc
	case "balance":
		builderFilter.OrderBy = dbtypes.BuilderOrderBalanceAsc
	case "balance-d":
		builderFilter.OrderBy = dbtypes.BuilderOrderBalanceDesc
	case "deposit":
		builderFilter.OrderBy = dbtypes.BuilderOrderDepositEpochAsc
	case "deposit-d":
		builderFilter.OrderBy = dbtypes.BuilderOrderDepositEpochDesc
	case "withdrawable":
		builderFilter.OrderBy = dbtypes.BuilderOrderWithdrawableEpochAsc
	case "withdrawable-d":
		builderFilter.OrderBy = dbtypes.BuilderOrderWithdrawableEpochDesc
	default:
		builderFilter.OrderBy = dbtypes.BuilderOrderIndexAsc
		pageData.IsDefaultSorting = true
		sortOrder = "index"
	}
	pageData.Sorting = sortOrder

	// get latest builder set
	builderSetRsp, builderSetLen := services.GlobalBeaconService.GetFilteredBuilderSet(ctx, &builderFilter, true)
	if len(builderSetRsp) == 0 {
		cacheTime = 5 * time.Minute
	}

	currentEpoch := chainState.CurrentEpoch()

	// get status options
	pageData.FilterStatusOpts = []models.BuildersPageDataStatusOption{
		{Status: "active", Count: 0},
		{Status: "exited", Count: 0},
		{Status: "superseded", Count: 0},
	}

	totalPages := builderSetLen / pageSize
	if (builderSetLen % pageSize) > 0 {
		totalPages++
	}
	if pageNumber == 0 {
		pageData.IsDefaultPage = true
	} else if pageNumber >= totalPages {
		if totalPages == 0 {
			pageNumber = 0
		} else {
			pageNumber = totalPages
		}
	}

	pageData.PageSize = pageSize
	pageData.TotalPages = totalPages
	pageData.CurrentPageIndex = pageNumber
	if pageNumber > 1 {
		pageData.PrevPageIndex = pageNumber - 1
	}
	if pageNumber < totalPages {
		pageData.NextPageIndex = pageNumber + 1
	}
	if totalPages > 1 {
		pageData.LastPageIndex = totalPages
	}

	// get builders
	pageData.Builders = make([]*models.BuildersPageDataBuilder, 0, len(builderSetRsp))

	for _, builder := range builderSetRsp {
		if builder.Builder == nil {
			continue
		}

		builderData := &models.BuildersPageDataBuilder{
			Index:            uint64(builder.Index),
			PublicKey:        builder.Builder.PublicKey[:],
			ExecutionAddress: builder.Builder.ExecutionAddress[:],
			Balance:          uint64(builder.Builder.Balance),
		}

		// Determine state
		if builder.Superseded {
			builderData.State = "Superseded"
		} else if builder.Builder.WithdrawableEpoch <= currentEpoch {
			builderData.State = "Exited"
		} else {
			builderData.State = "Active"
		}

		// Deposit epoch
		if builder.Builder.DepositEpoch < 18446744073709551615 {
			builderData.ShowDeposit = true
			builderData.DepositEpoch = uint64(builder.Builder.DepositEpoch)
			builderData.DepositTs = chainState.EpochToTime(builder.Builder.DepositEpoch)
		}

		// Withdrawable epoch
		if builder.Builder.WithdrawableEpoch < 18446744073709551615 {
			builderData.ShowWithdrawable = true
			builderData.WithdrawableEpoch = uint64(builder.Builder.WithdrawableEpoch)
			builderData.WithdrawableTs = chainState.EpochToTime(builder.Builder.WithdrawableEpoch)
		}

		pageData.Builders = append(pageData.Builders, builderData)
	}
	pageData.BuilderCount = builderSetLen
	pageData.FirstBuilder = pageNumber * pageSize
	pageData.LastBuilder = pageData.FirstBuilder + uint64(len(pageData.Builders))

	// Populate UrlParams for page jump functionality
	pageData.UrlParams = make(map[string]string)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams[key] = values[0]
		}
	}
	pageData.UrlParams["c"] = fmt.Sprintf("%v", pageData.PageSize)

	pageData.FilteredPageLink = fmt.Sprintf("/builders?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)

	// Sort status options alphabetically
	sort.Slice(pageData.FilterStatusOpts, func(a, b int) bool {
		return strings.Compare(pageData.FilterStatusOpts[a].Status, pageData.FilterStatusOpts[b].Status) < 0
	})

	return pageData, cacheTime
}
