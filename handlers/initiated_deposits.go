package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// InitiatedDeposits will return the filtered "initiated_deposits" page using a go template
func InitiatedDeposits(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"initiated_deposits/initiated_deposits.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/initiated_deposits", "Initiated Deposits", templateFiles)

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

	var address string
	var publickey string
	var vname string
	var minAmount uint64
	var maxAmount uint64
	var withOrphaned uint64
	var withValid uint64

	if urlArgs.Has("f") {
		if urlArgs.Has("f.address") {
			address = urlArgs.Get("f.address")
		}
		if urlArgs.Has("f.pubkey") {
			publickey = urlArgs.Get("f.pubkey")
		}
		if urlArgs.Has("f.vname") {
			vname = urlArgs.Get("f.vname")
		}
		if urlArgs.Has("f.mina") {
			minAmount, _ = strconv.ParseUint(urlArgs.Get("f.mina"), 10, 64)
		}
		if urlArgs.Has("f.maxa") {
			maxAmount, _ = strconv.ParseUint(urlArgs.Get("f.maxa"), 10, 64)
		}
		if urlArgs.Has("f.orphaned") {
			withOrphaned, _ = strconv.ParseUint(urlArgs.Get("f.orphaned"), 10, 64)
		}
		if urlArgs.Has("f.valid") {
			withValid, _ = strconv.ParseUint(urlArgs.Get("f.valid"), 10, 64)
		}
	} else {
		withOrphaned = 1
		withValid = 1
	}
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredInitiatedDepositsPageData(pageIdx, pageSize, address, publickey, vname, minAmount, maxAmount, uint8(withOrphaned), uint8(withValid))
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

func getFilteredInitiatedDepositsPageData(pageIdx uint64, pageSize uint64, address string, publickey string, vname string, minAmount uint64, maxAmount uint64, withOrphaned uint8, withValid uint8) (*models.InitiatedDepositsPageData, error) {
	pageData := &models.InitiatedDepositsPageData{}
	pageCacheKey := fmt.Sprintf("initiated_deposits:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, address, publickey, vname, minAmount, maxAmount, withOrphaned, withValid)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredInitiatedDepositsPageData(pageIdx, pageSize, address, publickey, vname, minAmount, maxAmount, withOrphaned, withValid)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.InitiatedDepositsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredInitiatedDepositsPageData(pageIdx uint64, pageSize uint64, address string, publickey string, vname string, minAmount uint64, maxAmount uint64, withOrphaned uint8, withValid uint8) *models.InitiatedDepositsPageData {
	filterArgs := url.Values{}
	if address != "" {
		filterArgs.Add("f.address", address)
	}
	if publickey != "" {
		filterArgs.Add("f.pubkey", publickey)
	}
	if vname != "" {
		filterArgs.Add("f.vname", vname)
	}
	if minAmount != 0 {
		filterArgs.Add("f.mina", fmt.Sprintf("%v", minAmount))
	}
	if maxAmount != 0 {
		filterArgs.Add("f.maxa", fmt.Sprintf("%v", maxAmount))
	}
	if withOrphaned != 0 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}
	if withValid != 0 {
		filterArgs.Add("f.valid", fmt.Sprintf("%v", withValid))
	}

	pageData := &models.InitiatedDepositsPageData{
		FilterAddress:       address,
		FilterPubKey:        publickey,
		FilterValidatorName: vname,
		FilterMinAmount:     minAmount,
		FilterMaxAmount:     maxAmount,
		FilterWithOrphaned:  withOrphaned,
		FilterWithValid:     withValid,
	}
	logrus.Debugf("initiated_deposits page called: %v:%v [%v,%v,%v,%v,%v]", pageIdx, pageSize, address, publickey, vname, minAmount, maxAmount)
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

	// load initiated deposits
	depositFilter := &dbtypes.DepositTxFilter{
		Address:       common.FromHex(address),
		PublicKey:     common.FromHex(publickey),
		ValidatorName: vname,
		MinAmount:     minAmount,
		MaxAmount:     maxAmount,
		WithOrphaned:  withOrphaned,
		WithValid:     withValid,
	}

	offset := (pageIdx - 1) * pageSize
	depositSyncState := dbtypes.DepositIndexerState{}
	db.GetExplorerState("indexer.depositstate", &depositSyncState)

	dbDepositTxs, totalRows, err := db.GetDepositTxsFiltered(offset, uint32(pageSize), depositSyncState.FinalBlock, depositFilter)
	if err != nil {
		panic(err)
	}

	for _, depositTx := range dbDepositTxs {
		depositTxData := &models.DepositsPageDataInitiatedDeposit{
			Index:                 depositTx.Index,
			Address:               depositTx.TxSender,
			PublicKey:             depositTx.PublicKey,
			Withdrawalcredentials: depositTx.WithdrawalCredentials,
			Amount:                depositTx.Amount,
			TxHash:                depositTx.TxHash,
			Time:                  time.Unix(int64(depositTx.BlockTime), 0),
			Block:                 depositTx.BlockNumber,
			BlockHash:             depositTx.BlockRoot,
			Orphaned:              depositTx.Orphaned,
			Valid:                 depositTx.ValidSignature,
		}
		pageData.Deposits = append(pageData.Deposits, depositTxData)
	}
	pageData.DepositCount = uint64(len(pageData.Deposits))

	if pageData.DepositCount > 0 {
		pageData.FirstIndex = pageData.Deposits[0].Index
		pageData.LastIndex = pageData.Deposits[pageData.DepositCount-1].Index
	}

	pageData.TotalPages = totalRows / pageSize
	if totalRows%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	pageData.FirstPageLink = fmt.Sprintf("/validators/initiated_deposits?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/initiated_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/initiated_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/initiated_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
