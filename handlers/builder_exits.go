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

// BuilderExits will return the filtered "builder_exits" page using a go template
func BuilderExits(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"builder_exits/builder_exits.html",
		"_shared/txDetailsModal.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "builders", "/builders/exits", "Builder Exits", templateFiles)

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

	var minSlot, maxSlot, minIndex, maxIndex uint64
	var pubkey, sourceAddr string

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
		if urlArgs.Has("f.source") {
			sourceAddr = urlArgs.Get("f.source")
		}
		if urlArgs.Has("f.mini") {
			minIndex, _ = strconv.ParseUint(urlArgs.Get("f.mini"), 10, 64)
		}
		if urlArgs.Has("f.maxi") {
			maxIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxi"), 10, 64)
		}
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getBuilderExitsPageData(pageIdx, pageSize, minSlot, maxSlot, pubkey, sourceAddr, minIndex, maxIndex)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "builder_exits.go", "BuilderExits", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getBuilderExitsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, pubkey string, sourceAddr string, minIndex uint64, maxIndex uint64) (*models.BuilderExitsPageData, error) {
	pageData := &models.BuilderExitsPageData{}
	pageCacheKey := fmt.Sprintf("builder_exits:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSlot, maxSlot, pubkey, sourceAddr, minIndex, maxIndex)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		return buildBuilderExitsPageData(pageCall.CallCtx, pageIdx, pageSize, minSlot, maxSlot, pubkey, sourceAddr, minIndex, maxIndex)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.BuilderExitsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildBuilderExitsPageData(ctx context.Context, pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, pubkey string, sourceAddr string, minIndex uint64, maxIndex uint64) *models.BuilderExitsPageData {
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
	if sourceAddr != "" {
		filterArgs.Add("f.source", sourceAddr)
	}
	if minIndex != 0 {
		filterArgs.Add("f.mini", fmt.Sprintf("%v", minIndex))
	}
	if maxIndex != 0 {
		filterArgs.Add("f.maxi", fmt.Sprintf("%v", maxIndex))
	}

	pageData := &models.BuilderExitsPageData{
		FilterMinSlot:    minSlot,
		FilterMaxSlot:    maxSlot,
		FilterPubKey:     pubkey,
		FilterSourceAddr: sourceAddr,
		FilterMinIndex:   minIndex,
		FilterMaxIndex:   maxIndex,
	}
	logrus.Debugf("builder_exits page called: %v:%v [%v,%v,%v]", pageIdx, pageSize, minSlot, maxSlot, pubkey)
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

	filter := &dbtypes.BuilderExitFilter{
		MinSlot:       minSlot,
		MaxSlot:       maxSlot,
		PublicKey:     common.FromHex(pubkey),
		SourceAddress: common.FromHex(sourceAddr),
		MinIndex:      minIndex,
		MaxIndex:      maxIndex,
		WithOrphaned:  1,
	}

	offset := (pageIdx - 1) * pageSize
	combined, totalTxRows, totalReqRows := services.GlobalBeaconService.GetBuilderExitsByFilter(ctx, filter, offset, uint32(pageSize))
	totalRows := totalTxRows + totalReqRows

	chainState := services.GlobalBeaconService.GetChainState()

	// builderIdxOf returns the builder index recorded for an exit (CL request preferred, else the
	// pending EL tx), if any.
	builderIdxOf := func(exit *services.CombinedBuilderExit) *uint64 {
		if exit.Request != nil && exit.Request.BuilderIndex != nil {
			return exit.Request.BuilderIndex
		}
		if exit.Transaction != nil && exit.Transaction.BuilderIndex != nil {
			return exit.Transaction.BuilderIndex
		}
		return nil
	}

	// collect the builder indexes to resolve so we can batch-load the builders and tell whether
	// each pubkey still owns its (reusable) index or was superseded.
	indexes := make([]gloas.BuilderIndex, 0, len(combined))
	for _, exit := range combined {
		if idx := builderIdxOf(exit); idx != nil {
			indexes = append(indexes, gloas.BuilderIndex(*idx))
		}
	}
	builders := services.GlobalBeaconService.GetActiveBuildersByIndexes(ctx, indexes)

	for _, exit := range combined {
		exitData := &models.BuilderExitsPageDataExit{}

		if exit.Request != nil {
			exitData.IsIncluded = true
			exitData.SlotNumber = exit.Request.SlotNumber
			exitData.SlotRoot = exit.Request.SlotRoot
			exitData.Time = chainState.SlotToTime(phase0.Slot(exit.Request.SlotNumber))
			exitData.Orphaned = exit.RequestOrphaned
			exitData.SourceAddress = exit.Request.SourceAddress
			exitData.PublicKey = exit.Request.PublicKey
			exitData.Result = exit.Request.Result
			exitData.BlockNumber = exit.Request.BlockNumber
		} else if exit.Transaction != nil {
			exitData.SourceAddress = exit.Transaction.SourceAddress
			exitData.PublicKey = exit.Transaction.PublicKey
			exitData.BlockNumber = exit.Transaction.BlockNumber
		}

		if idx := builderIdxOf(exit); idx != nil {
			if b := builders[gloas.BuilderIndex(*idx)]; b != nil && bytes.Equal(b.PublicKey[:], exitData.PublicKey) {
				exitData.HasBuilderIndex = true
				exitData.BuilderIndex = *idx
			} else {
				exitData.IsInactiveBuilder = true
			}
		}

		if exit.Transaction != nil {
			exitData.HasTransaction = true
			exitData.TransactionHash = exit.Transaction.TxHash
			exitData.TransactionOrphaned = exit.TransactionOrphaned
			exitData.TransactionDetails = &models.BuilderPageDataExitTxDetails{
				BlockNumber: exit.Transaction.BlockNumber,
				BlockHash:   fmt.Sprintf("%#x", exit.Transaction.BlockRoot),
				BlockTime:   exit.Transaction.BlockTime,
				TxOrigin:    common.Address(exit.Transaction.TxSender).Hex(),
				TxTarget:    common.Address(exit.Transaction.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("%#x", exit.Transaction.TxHash),
			}
		}

		pageData.Exits = append(pageData.Exits, exitData)
	}
	pageData.ExitCount = uint64(len(pageData.Exits))

	if pageData.ExitCount > 0 {
		pageData.FirstIndex = pageData.Exits[0].SlotNumber
		pageData.LastIndex = pageData.Exits[pageData.ExitCount-1].SlotNumber
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

	pageData.FirstPageLink = fmt.Sprintf("/builders/exits?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/builders/exits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/builders/exits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/builders/exits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
