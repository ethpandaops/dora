package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// SubmitDeposit will submit a deposit to the beacon node
func SubmitDeposit(w http.ResponseWriter, r *http.Request) {
	var submitDepositTemplateFiles = append(layoutTemplateFiles,
		"submit_deposit/submit_deposit.html",
	)
	var pageTemplate = templates.GetTemplate(submitDepositTemplateFiles...)

	if !utils.Config.Frontend.ShowSubmitDeposit {
		handlePageError(w, r, errors.New("submit deposit is not enabled"))
		return
	}

	pageData, pageError := getSubmitDepositPageData()
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	if pageData == nil {
		data := InitPageData(w, r, "blockchain", "/submit_deposit", "Submit Deposit", submitDepositTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		if handleTemplateError(w, r, "submit_deposit.go", "Submit Deposit", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	}

	data := InitPageData(w, r, "blockchain", "/submit_deposit", "Submit Deposit", submitDepositTemplateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "submit_deposit.go", "Submit Deposit", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getSubmitDepositPageData() (*models.SubmitDepositPageData, error) {
	pageData := &models.SubmitDepositPageData{}
	pageCacheKey := "submit_deposit"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildSubmitDepositPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.SubmitDepositPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildSubmitDepositPageData() (*models.SubmitDepositPageData, time.Duration) {
	logrus.Debugf("submit deposit page called")

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	pageData := &models.SubmitDepositPageData{
		NetworkName:         specs.ConfigName,
		DepositContract:     specs.DepositContractAddress,
		PublicRPCUrl:        utils.Config.Frontend.PublicRPCUrl,
		RainbowkitProjectId: utils.Config.Frontend.RainbowkitProjectId,
		ChainId:             specs.DepositChainId,
		GenesisForkVersion:  specs.GenesisForkVersion[:],
	}

	return pageData, 1 * time.Hour
}
