package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// SubmitBuilderDeposit renders the submit builder deposit page.
func SubmitBuilderDeposit(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"submit_builder_deposit/submit_builder_deposit.html",
	)
	var pageTemplate = templates.GetTemplate(templateFiles...)

	if !utils.Config.Frontend.ShowSubmitDeposit {
		handlePageError(w, r, errors.New("submit deposit is not enabled"))
		return
	}

	if r.Method != http.MethodGet {
		handlePageError(w, r, errors.New("invalid method"))
		return
	}

	pageData, pageError := getSubmitBuilderDepositPageData()
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	data := InitPageData(w, r, "builders", "/builders/submit_deposit", "Submit Builder Deposit", templateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "submit_builder_deposit.go", "SubmitBuilderDeposit", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getSubmitBuilderDepositPageData() (*models.SubmitBuilderDepositPageData, error) {
	pageData := &models.SubmitBuilderDepositPageData{}
	pageCacheKey := "submit_builder_deposit"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildSubmitBuilderDepositPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.SubmitBuilderDepositPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildSubmitBuilderDepositPageData() (*models.SubmitBuilderDepositPageData, time.Duration) {
	logrus.Debugf("submit builder deposit page called")

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	builderDepositContract := services.GlobalBeaconService.GetSystemContractAddress(rpc.BuilderDepositRequestContract)

	pageData := &models.SubmitBuilderDepositPageData{
		NetworkName:            specs.ConfigName,
		PublicRPCUrl:           utils.GetFrontendRPCUrl(),
		RainbowkitProjectId:    utils.Config.Frontend.RainbowkitProjectId,
		ChainId:                specs.DepositChainId,
		BuilderDepositContract: builderDepositContract.String(),
		GenesisForkVersion:     specs.GenesisForkVersion[:],
		ExplorerUrl:            utils.Config.Frontend.EthExplorerLink,
	}

	if utils.Config.Chain.DisplayName != "" {
		pageData.NetworkName = utils.Config.Chain.DisplayName
	}

	return pageData, 1 * time.Hour
}
