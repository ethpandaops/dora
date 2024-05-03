package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// Deposits will return the main "deposits" page using a go template
func Deposits(w http.ResponseWriter, r *http.Request) {
	var indexTemplateFiles = append(layoutTemplateFiles,
		"deposits/deposits.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(indexTemplateFiles...)
	data := InitPageData(w, r, "validators", "/validators/deposits", "Deposits", indexTemplateFiles)

	urlArgs := r.URL.Query()
	var firstEpoch uint64 = math.MaxUint64
	if urlArgs.Has("epoch") {
		firstEpoch, _ = strconv.ParseUint(urlArgs.Get("epoch"), 10, 64)
	}
	var pageSize uint64 = 50
	if urlArgs.Has("count") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("count"), 10, 64)
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getDepositsPageData(firstEpoch, pageSize)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "deposits.go", "Deposits", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getDepositsPageData(firstEpoch uint64, pageSize uint64) (*models.DepositsPageData, error) {
	pageData := &models.DepositsPageData{}
	pageCacheKey := fmt.Sprintf("deposits:%v:%v", firstEpoch, pageSize)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildDepositsPageData(firstEpoch, pageSize)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.DepositsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildDepositsPageData(firstEpoch uint64, pageSize uint64) (*models.DepositsPageData, time.Duration) {
	logrus.Debugf("deposits page called: %v:%v", firstEpoch, pageSize)
	pageData := &models.DepositsPageData{
		InitiatedDeposits: []*models.DepositsPageDataInitiatedDeposit{},
	}

	// load initiated deposits
	dbDepositTxs := db.GetDepositTxs(0, 20)

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
			Orphaned:              depositTx.Orphaned,
			Valid:                 depositTx.ValidSignature,
		}
		pageData.InitiatedDeposits = append(pageData.InitiatedDeposits, depositTxData)
	}
	pageData.InitiatedDepositCount = uint64(len(pageData.InitiatedDeposits))

	// load included deposits
	dbDeposits := db.GetDeposits(0, 20)

	for _, deposit := range dbDeposits {

		depositData := &models.DepositsPageDataIncludedDeposit{
			PublicKey:             deposit.PublicKey,
			Withdrawalcredentials: deposit.WithdrawalCredentials,
			Amount:                deposit.Amount,
			SlotNumber:            deposit.SlotNumber,
			SlotRoot:              deposit.SlotRoot,
			Time:                  utils.SlotToTime(deposit.SlotNumber),
			Orphaned:              deposit.Orphaned,
		}
		pageData.IncludedDeposits = append(pageData.IncludedDeposits, depositData)
	}
	pageData.IncludedDepositCount = uint64(len(pageData.IncludedDeposits))

	return pageData, 1 * time.Minute
}
