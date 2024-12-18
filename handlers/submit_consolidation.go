package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/indexer/execution"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// SubmitConsolidation will submit a consolidation request
func SubmitConsolidation(w http.ResponseWriter, r *http.Request) {
	var submitConsolidationTemplateFiles = append(layoutTemplateFiles,
		"submit_consolidation/submit_consolidation.html",
	)
	var pageTemplate = templates.GetTemplate(submitConsolidationTemplateFiles...)

	if !utils.Config.Frontend.ShowSubmitElRequests {
		handlePageError(w, r, errors.New("submit el requests is not enabled"))
		return
	}

	query := r.URL.Query()
	if query.Has("ajax") {
		err := handleSubmitConsolidationPageDataAjax(w, r)
		if err != nil {
			handlePageError(w, r, err)
		}
		return
	}

	pageData, pageError := getSubmitConsolidationPageData()
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	if pageData == nil {
		data := InitPageData(w, r, "blockchain", "/submit_consolidation", "Submit Consolidation", submitConsolidationTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		if handleTemplateError(w, r, "submit_consolidation.go", "Submit Consolidation", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	}

	data := InitPageData(w, r, "blockchain", "/submit_consolidation", "Submit Consolidation", submitConsolidationTemplateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "submit_consolidation.go", "Submit Consolidation", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getSubmitConsolidationPageData() (*models.SubmitConsolidationPageData, error) {
	pageData := &models.SubmitConsolidationPageData{}
	pageCacheKey := "submit_consolidation"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildSubmitConsolidationPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.SubmitConsolidationPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildSubmitConsolidationPageData() (*models.SubmitConsolidationPageData, time.Duration) {
	logrus.Debugf("submit consolidation page called")

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	pageData := &models.SubmitConsolidationPageData{
		NetworkName:           specs.ConfigName,
		PublicRPCUrl:          utils.Config.Frontend.PublicRPCUrl,
		RainbowkitProjectId:   utils.Config.Frontend.RainbowkitProjectId,
		ChainId:               specs.DepositChainId,
		ConsolidationContract: execution.ConsolidationContractAddr,
		ExplorerUrl:           utils.Config.Frontend.EthExplorerLink,
	}

	return pageData, 1 * time.Hour
}

func handleSubmitConsolidationPageDataAjax(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	var pageData interface{}

	switch query.Get("ajax") {
	case "load_validators":
		address := query.Get("address")
		addressBytes := common.HexToAddress(address)
		currentEpoch := services.GlobalBeaconService.GetChainState().CurrentEpoch()

		validators := services.GlobalBeaconService.GetBeaconIndexer().GetValidatorsByWithdrawalAddress(addressBytes, nil)
		validatorBalances := services.GlobalBeaconService.GetBeaconIndexer().GetRecentValidatorBalances(nil)

		result := []models.SubmitConsolidationPageDataValidator{}
		for _, validator := range validators {
			balance := phase0.Gwei(0)
			if int(validator.Index) < len(validatorBalances) {
				balance = validatorBalances[validator.Index]
			}

			validatorStatus := v1.ValidatorToState(validator.Validator, &balance, currentEpoch, beacon.FarFutureEpoch)

			var status string
			if strings.HasPrefix(validatorStatus.String(), "pending") {
				status = "Pending"
			} else if validatorStatus == v1.ValidatorStateActiveOngoing {
				status = "Active"
			} else if validatorStatus == v1.ValidatorStateActiveExiting {
				status = "Exiting"
			} else if validatorStatus == v1.ValidatorStateActiveSlashed {
				status = "Slashed"
			} else if validatorStatus == v1.ValidatorStateExitedUnslashed {
				status = "Exited"
			} else if validatorStatus == v1.ValidatorStateExitedSlashed {
				status = "Slashed"
			} else {
				status = validatorStatus.String()
			}

			result = append(result, models.SubmitConsolidationPageDataValidator{
				Index:    uint64(validator.Index),
				Pubkey:   validator.Validator.PublicKey.String(),
				Balance:  uint64(balance),
				CredType: fmt.Sprintf("%02x", validator.Validator.WithdrawalCredentials[0]),
				Status:   status,
			})
		}

		pageData = result
	default:
		return errors.New("invalid ajax request")
	}

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(pageData)
	if err != nil {
		logrus.WithError(err).Error("error encoding index data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
	return nil
}
