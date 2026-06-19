package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

// BuilderDeposits will return the filtered "builder_deposits" page using a go template
func BuilderDeposits(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"builder_deposits/builder_deposits.html",
		"_shared/txDetailsModal.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "builders", "/builders/deposits", "Builder Deposits", templateFiles)

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

	var minSlot, maxSlot, minIndex, maxIndex, minAmount, maxAmount uint64
	var pubkey string

	if urlArgs.Has("f") {
		if urlArgs.Has("f.mins") {
			minSlot, _ = strconv.ParseUint(urlArgs.Get("f.mins"), 10, 64)
		}
		if urlArgs.Has("f.maxs") {
			maxSlot, _ = strconv.ParseUint(urlArgs.Get("f.maxs"), 10, 64)
		}
		if urlArgs.Has("f.pubkey") {
			pubkey = urlArgs.Get("f.pubkey")
		}
		if urlArgs.Has("f.mini") {
			minIndex, _ = strconv.ParseUint(urlArgs.Get("f.mini"), 10, 64)
		}
		if urlArgs.Has("f.maxi") {
			maxIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxi"), 10, 64)
		}
		if urlArgs.Has("f.mina") {
			minAmount, _ = strconv.ParseUint(urlArgs.Get("f.mina"), 10, 64)
		}
		if urlArgs.Has("f.maxa") {
			maxAmount, _ = strconv.ParseUint(urlArgs.Get("f.maxa"), 10, 64)
		}
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getBuilderDepositsPageData(pageIdx, pageSize, minSlot, maxSlot, pubkey, minIndex, maxIndex, minAmount, maxAmount)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "builder_deposits.go", "BuilderDeposits", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getBuilderDepositsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, pubkey string, minIndex uint64, maxIndex uint64, minAmount uint64, maxAmount uint64) (*models.BuilderDepositsPageData, error) {
	pageData := &models.BuilderDepositsPageData{}
	pageCacheKey := fmt.Sprintf("builder_deposits:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSlot, maxSlot, pubkey, minIndex, maxIndex, minAmount, maxAmount)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		return buildBuilderDepositsPageData(pageCall.CallCtx, pageIdx, pageSize, minSlot, maxSlot, pubkey, minIndex, maxIndex, minAmount, maxAmount)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.BuilderDepositsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildBuilderDepositsPageData(ctx context.Context, pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, pubkey string, minIndex uint64, maxIndex uint64, minAmount uint64, maxAmount uint64) *models.BuilderDepositsPageData {
	filterArgs := url.Values{}
	if minSlot != 0 {
		filterArgs.Add("f.mins", fmt.Sprintf("%v", minSlot))
	}
	if maxSlot != 0 {
		filterArgs.Add("f.maxs", fmt.Sprintf("%v", maxSlot))
	}
	if pubkey != "" {
		filterArgs.Add("f.pubkey", pubkey)
	}
	if minIndex != 0 {
		filterArgs.Add("f.mini", fmt.Sprintf("%v", minIndex))
	}
	if maxIndex != 0 {
		filterArgs.Add("f.maxi", fmt.Sprintf("%v", maxIndex))
	}
	if minAmount != 0 {
		filterArgs.Add("f.mina", fmt.Sprintf("%v", minAmount))
	}
	if maxAmount != 0 {
		filterArgs.Add("f.maxa", fmt.Sprintf("%v", maxAmount))
	}

	pageData := &models.BuilderDepositsPageData{
		FilterMinSlot:   minSlot,
		FilterMaxSlot:   maxSlot,
		FilterPubKey:    pubkey,
		FilterMinIndex:  minIndex,
		FilterMaxIndex:  maxIndex,
		FilterMinAmount: minAmount,
		FilterMaxAmount: maxAmount,
	}
	logrus.Debugf("builder_deposits page called: %v:%v [%v,%v,%v]", pageIdx, pageSize, minSlot, maxSlot, pubkey)
	if pageIdx == 1 {
		pageData.IsDefaultPage = true
	}
	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.CurrentPageIndex = pageIdx
	if pageIdx > 1 {
		pageData.PrevPageIndex = pageIdx - 1
	}

	filter := &dbtypes.BuilderDepositFilter{
		MinSlot:      minSlot,
		MaxSlot:      maxSlot,
		PublicKey:    common.FromHex(pubkey),
		MinIndex:     minIndex,
		MaxIndex:     maxIndex,
		WithOrphaned: 1,
	}
	if minAmount != 0 {
		filter.MinAmount = &minAmount
	}
	if maxAmount != 0 {
		filter.MaxAmount = &maxAmount
	}

	offset := (pageIdx - 1) * pageSize
	combined, totalTxRows, totalReqRows := services.GlobalBeaconService.GetBuilderDepositsByFilter(ctx, filter, offset, uint32(pageSize))
	totalRows := totalTxRows + totalReqRows

	chainState := services.GlobalBeaconService.GetChainState()

	// builderIdxOf returns the builder index recorded for a deposit (CL request preferred, else
	// the pending EL tx), if any.
	builderIdxOf := func(deposit *services.CombinedBuilderDeposit) *uint64 {
		if deposit.Request != nil && deposit.Request.BuilderIndex != nil {
			return deposit.Request.BuilderIndex
		}
		if deposit.Transaction != nil && deposit.Transaction.BuilderIndex != nil {
			return deposit.Transaction.BuilderIndex
		}
		return nil
	}

	// collect the builder indexes to resolve so we can batch-load the builders and tell whether
	// each pubkey still owns its (reusable) index or was superseded.
	indexes := make([]gloas.BuilderIndex, 0, len(combined))
	for _, deposit := range combined {
		if idx := builderIdxOf(deposit); idx != nil {
			indexes = append(indexes, gloas.BuilderIndex(*idx))
		}
	}
	builders := services.GlobalBeaconService.GetActiveBuildersByIndexes(ctx, indexes)

	for _, deposit := range combined {
		depositData := &models.BuilderDepositsPageDataDeposit{}

		if deposit.Request != nil {
			depositData.IsIncluded = true
			depositData.SlotNumber = deposit.Request.SlotNumber
			depositData.SlotRoot = deposit.Request.SlotRoot
			depositData.Time = chainState.SlotToTime(phase0.Slot(deposit.Request.SlotNumber))
			depositData.Orphaned = deposit.RequestOrphaned
			depositData.PublicKey = deposit.Request.PublicKey
			depositData.WithdrawalCredentials = deposit.Request.WithdrawalCredentials
			depositData.Amount = deposit.Request.Amount
			depositData.Result = deposit.Request.Result
			depositData.BlockNumber = deposit.Request.BlockNumber
		} else if deposit.Transaction != nil {
			depositData.PublicKey = deposit.Transaction.PublicKey
			depositData.WithdrawalCredentials = deposit.Transaction.WithdrawalCredentials
			depositData.Amount = deposit.Transaction.Amount
			depositData.BlockNumber = deposit.Transaction.BlockNumber
		}

		if idx := builderIdxOf(deposit); idx != nil {
			if b := builders[gloas.BuilderIndex(*idx)]; b != nil && bytes.Equal(b.PublicKey[:], depositData.PublicKey) {
				depositData.HasBuilderIndex = true
				depositData.BuilderIndex = *idx
			} else {
				depositData.IsInactiveBuilder = true
			}
		}

		if deposit.Transaction != nil {
			depositData.HasTransaction = true
			depositData.TransactionHash = deposit.Transaction.TxHash
			depositData.TransactionOrphaned = deposit.TransactionOrphaned
			depositData.TransactionDetails = &models.BuilderPageDataDepositTxDetails{
				BlockNumber: deposit.Transaction.BlockNumber,
				BlockHash:   fmt.Sprintf("%#x", deposit.Transaction.BlockRoot),
				BlockTime:   deposit.Transaction.BlockTime,
				TxOrigin:    common.Address(deposit.Transaction.TxSender).Hex(),
				TxTarget:    common.Address(deposit.Transaction.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("%#x", deposit.Transaction.TxHash),
			}
		}

		pageData.Deposits = append(pageData.Deposits, depositData)
	}
	pageData.DepositCount = uint64(len(pageData.Deposits))

	if pageData.DepositCount > 0 {
		pageData.FirstIndex = pageData.Deposits[0].SlotNumber
		pageData.LastIndex = pageData.Deposits[pageData.DepositCount-1].SlotNumber
	}

	pageData.TotalPages = totalRows / pageSize
	if totalRows%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	pageData.UrlParams = make([]models.UrlParam, 0)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams = append(pageData.UrlParams, models.UrlParam{Key: key, Value: values[0]})
		}
	}
	pageData.UrlParams = append(pageData.UrlParams, models.UrlParam{Key: "c", Value: fmt.Sprintf("%v", pageData.PageSize)})

	pageData.FirstPageLink = fmt.Sprintf("/builders/deposits?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/builders/deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/builders/deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/builders/deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
