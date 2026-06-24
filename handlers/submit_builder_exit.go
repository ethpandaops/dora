package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// SubmitBuilderExit renders the submit builder exit page.
func SubmitBuilderExit(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"submit_builder_exit/submit_builder_exit.html",
	)
	var pageTemplate = templates.GetTemplate(templateFiles...)

	if !utils.Config.Frontend.ShowSubmitElRequests {
		handlePageError(w, r, errors.New("submit el requests is not enabled"))
		return
	}

	query := r.URL.Query()
	if query.Has("ajax") {
		err := handleSubmitBuilderExitPageDataAjax(w, r)
		if err != nil {
			handlePageError(w, r, err)
		}
		return
	}

	pageData, pageError := getSubmitBuilderExitPageData()
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	data := InitPageData(w, r, "builders", "/builders/submit_exit", "Submit Builder Exit", templateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "submit_builder_exit.go", "SubmitBuilderExit", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getSubmitBuilderExitPageData() (*models.SubmitBuilderExitPageData, error) {
	pageData := &models.SubmitBuilderExitPageData{}
	pageCacheKey := "submit_builder_exit"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildSubmitBuilderExitPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.SubmitBuilderExitPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildSubmitBuilderExitPageData() (*models.SubmitBuilderExitPageData, time.Duration) {
	logrus.Debugf("submit builder exit page called")

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	builderExitContract := services.GlobalBeaconService.GetSystemContractAddress(rpc.BuilderExitRequestContract)

	pageData := &models.SubmitBuilderExitPageData{
		NetworkName:         specs.ConfigName,
		PublicRPCUrl:        utils.GetFrontendRPCUrl(),
		RainbowkitProjectId: utils.Config.Frontend.RainbowkitProjectId,
		ChainId:             specs.DepositChainId,
		BuilderExitContract: builderExitContract.String(),
		ExplorerUrl:         utils.Config.Frontend.EthExplorerLink,
	}

	if utils.Config.Chain.DisplayName != "" {
		pageData.NetworkName = utils.Config.Chain.DisplayName
	}

	return pageData, 1 * time.Hour
}

func handleSubmitBuilderExitPageDataAjax(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	var pageData interface{}

	switch query.Get("ajax") {
	case "load_builders":
		address := query.Get("address")
		pageCacheKey := fmt.Sprintf("submit_builder_exit:load_builders:%s", address)
		var cached []models.SubmitBuilderExitPageDataBuilder
		pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, &cached, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
			result := buildSubmitBuilderExitLoadBuilders(pageCall.CallCtx, address)
			pageCall.CacheTimeout = 1 * time.Minute
			return result
		})
		if pageErr != nil {
			return pageErr
		}
		pageData = pageRes
	default:
		return errors.New("invalid ajax request")
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(pageData); err != nil {
		logrus.WithError(err).Error("error encoding submit builder exit data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
	return nil
}

func buildSubmitBuilderExitLoadBuilders(ctx context.Context, address string) []models.SubmitBuilderExitPageDataBuilder {
	addressBytes := common.HexToAddress(address)

	builders, _ := services.GlobalBeaconService.GetFilteredBuilderSet(ctx, &dbtypes.BuilderFilter{
		ExecutionAddress: addressBytes[:],
	}, false)

	result := make([]models.SubmitBuilderExitPageDataBuilder, 0, len(builders))
	for _, b := range builders {
		if b.Superseded || b.Builder == nil {
			continue
		}
		// Only active builders (not yet exiting) can be exited.
		if b.Builder.WithdrawableEpoch != beacon.FarFutureEpoch {
			continue
		}
		result = append(result, models.SubmitBuilderExitPageDataBuilder{
			Index:            uint64(b.Index),
			Pubkey:           fmt.Sprintf("0x%x", b.Builder.PublicKey[:]),
			ExecutionAddress: fmt.Sprintf("0x%x", b.Builder.ExecutionAddress[:]),
			Status:           "active",
		})
	}
	return result
}
