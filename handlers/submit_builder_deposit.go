package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/dbtypes"
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

	if r.URL.Query().Has("ajax") {
		if err := handleSubmitBuilderDepositAjax(w, r); err != nil {
			handlePageError(w, r, err)
		}
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

func handleSubmitBuilderDepositAjax(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	var pageData interface{}

	switch query.Get("ajax") {
	case "load_builders":
		address := query.Get("address")
		pageCacheKey := fmt.Sprintf("submit_builder_deposit:load_builders:%s", address)
		var cached []models.SubmitDepositPageDataValidator
		pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, &cached, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
			result := buildSubmitBuilderDepositLoadBuilders(pageCall.CallCtx, address)
			pageCall.CacheTimeout = 1 * time.Minute
			return result
		})
		if pageErr != nil {
			return pageErr
		}
		pageData = pageRes

	case "search_builders":
		searchTerm := query.Get("search")
		limit := 50
		if query.Has("limit") {
			limit, _ = strconv.Atoi(query.Get("limit"))
			if limit > 100 {
				limit = 100
			}
		}
		pageCacheKey := fmt.Sprintf("submit_builder_deposit:search_builders:%s:%d", searchTerm, limit)
		var cached []models.SubmitDepositPageDataValidator
		pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, &cached, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
			result := buildSubmitBuilderDepositSearchBuilders(pageCall.CallCtx, searchTerm, limit)
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
		logrus.WithError(err).Error("error encoding builder ajax data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
	return nil
}

// buildSubmitBuilderDepositLoadBuilders returns the builders whose execution address
// matches, in the same row shape the validator topup selector uses.
func buildSubmitBuilderDepositLoadBuilders(ctx context.Context, address string) []models.SubmitDepositPageDataValidator {
	if !common.IsHexAddress(address) {
		return []models.SubmitDepositPageDataValidator{}
	}
	addressBytes := common.HexToAddress(address)
	builders, _ := services.GlobalBeaconService.GetFilteredBuilderSet(ctx, &dbtypes.BuilderFilter{
		ExecutionAddress: addressBytes[:],
	}, true)
	return buildSubmitBuilderDepositBuilderModels(builders, 0)
}

// buildSubmitBuilderDepositSearchBuilders searches the builder registry (not the
// validator set) by builder index or pubkey prefix.
func buildSubmitBuilderDepositSearchBuilders(ctx context.Context, searchTerm string, limit int) []models.SubmitDepositPageDataValidator {
	var builders []services.BuilderWithIndex
	if searchTerm == "" {
		return []models.SubmitDepositPageDataValidator{}
	} else if index, err := strconv.ParseUint(searchTerm, 10, 64); err == nil {
		builders, _ = services.GlobalBeaconService.GetFilteredBuilderSet(ctx, &dbtypes.BuilderFilter{
			MinIndex: &index, MaxIndex: &index,
		}, true)
	} else if regexp.MustCompile(`^(0x)?[0-9a-fA-F]+$`).MatchString(searchTerm) {
		pubkeyHex := strings.TrimPrefix(searchTerm, "0x")
		if len(pubkeyHex)%2 != 0 {
			pubkeyHex = pubkeyHex[:len(pubkeyHex)-1]
		}
		pubkeyBytes, err := hex.DecodeString(pubkeyHex)
		if err == nil && len(pubkeyBytes) > 0 {
			builders, _ = services.GlobalBeaconService.GetFilteredBuilderSet(ctx, &dbtypes.BuilderFilter{
				PubKey: pubkeyBytes,
			}, true)
		}
	}
	return buildSubmitBuilderDepositBuilderModels(builders, limit)
}

func buildSubmitBuilderDepositBuilderModels(builders []services.BuilderWithIndex, limit int) []models.SubmitDepositPageDataValidator {
	chainState := services.GlobalBeaconService.GetChainState()
	currentEpoch := chainState.CurrentEpoch()

	result := make([]models.SubmitDepositPageDataValidator, 0, len(builders))
	for _, builder := range builders {
		if limit > 0 && len(result) >= limit {
			break
		}
		if builder.Builder == nil || builder.Superseded {
			continue
		}
		status := "Active"
		if builder.Builder.WithdrawableEpoch <= currentEpoch {
			status = "Exited"
		}
		result = append(result, models.SubmitDepositPageDataValidator{
			Index:    uint64(builder.Index),
			Pubkey:   fmt.Sprintf("0x%x", builder.Builder.PublicKey[:]),
			Balance:  uint64(builder.Builder.Balance),
			CredType: "b0",
			Status:   status,
		})
	}
	return result
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

	faucetService := services.GetFaucetService()

	pageData := &models.SubmitBuilderDepositPageData{
		NetworkName:            specs.ConfigName,
		PublicRPCUrl:           utils.GetFrontendRPCUrl(),
		RainbowkitProjectId:    utils.Config.Frontend.RainbowkitProjectId,
		ChainId:                specs.DepositChainId,
		BuilderDepositContract: builderDepositContract.String(),
		GenesisForkVersion:     specs.GenesisForkVersion[:],
		ExplorerUrl:            utils.Config.Frontend.EthExplorerLink,
		FaucetEnabled:          faucetService.IsEnabled(),
		FaucetAmount:           faucetService.GetFundingAmountEth(),
		ShowGenerator:          !utils.Config.Frontend.DisableDepositGenerator,
	}

	if utils.Config.Chain.DisplayName != "" {
		pageData.NetworkName = utils.Config.Chain.DisplayName
	}

	return pageData, 1 * time.Hour
}
