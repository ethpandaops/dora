package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
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

	query := r.URL.Query()
	if query.Has("ajax") {
		err := handleSubmitDepositPageDataAjax(w, r)
		if err != nil {
			handlePageError(w, r, err)
		}
		return
	}

	if r.Method != http.MethodGet {
		handlePageError(w, r, errors.New("invalid method"))
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
		NetworkName:                specs.ConfigName,
		DepositContract:            specs.DepositContractAddress,
		PublicRPCUrl:               utils.GetFrontendRPCUrl(),
		RainbowkitProjectId:        utils.Config.Frontend.RainbowkitProjectId,
		ChainId:                    specs.DepositChainId,
		GenesisForkVersion:         specs.GenesisForkVersion[:],
		ExplorerUrl:                utils.Config.Frontend.EthExplorerLink,
		MaxEffectiveBalance:        fmt.Sprintf("%d", specs.MaxEffectiveBalance),
		MaxEffectiveBalanceElectra: fmt.Sprintf("%d", specs.MaxEffectiveBalanceElectra),
	}

	if utils.Config.Chain.DisplayName != "" {
		pageData.NetworkName = utils.Config.Chain.DisplayName
	}

	return pageData, 1 * time.Hour
}

func handleSubmitDepositPageDataAjax(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	var pageData interface{}

	ctx := r.Context()

	switch query.Get("ajax") {
	case "load_deposits":
		if r.Method != http.MethodPost {
			return fmt.Errorf("invalid method")
		}

		var hexPubkeys []string
		err := json.NewDecoder(r.Body).Decode(&hexPubkeys)
		if err != nil {
			return fmt.Errorf("failed to decode request body: %v", err)
		}

		pubkeys := make([][]byte, 0, len(hexPubkeys))
		for i, hexPubkey := range hexPubkeys {
			pubkey := common.FromHex(hexPubkey)
			if len(pubkey) != 48 {
				return fmt.Errorf("invalid pubkey length (%d) for pubkey %v", len(pubkey), i)
			}
			pubkeys = append(pubkeys, pubkey)
		}

		h := sha256.Sum256([]byte(fmt.Sprintf("%v", hexPubkeys)))
		pageCacheKey := fmt.Sprintf("submit_deposit:load_deposits:%x", h[:16])
		var cached models.SubmitDepositPageDataDeposits
		pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, &cached, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
			result, buildErr := buildSubmitDepositLoadDeposits(pageCall.CallCtx, pubkeys)
			if buildErr != nil {
				pageCall.CacheTimeout = -1
				return nil
			}
			pageCall.CacheTimeout = 1 * time.Minute
			return result
		})
		if pageErr != nil {
			return pageErr
		}
		if pageRes == nil {
			result, buildErr := buildSubmitDepositLoadDeposits(ctx, pubkeys)
			if buildErr != nil {
				return buildErr
			}
			pageData = result
		} else {
			pageData = pageRes
		}

	case "load_validators":
		address := query.Get("address")
		pageCacheKey := fmt.Sprintf("submit_deposit:load_validators:%s", address)
		var cached []models.SubmitDepositPageDataValidator
		pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, &cached, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
			result := buildSubmitDepositLoadValidators(pageCall.CallCtx, address)
			pageCall.CacheTimeout = 1 * time.Minute
			return result
		})
		if pageErr != nil {
			return pageErr
		}
		pageData = pageRes

	case "search_validators":
		searchTerm := query.Get("search")
		limit := 50
		if query.Has("limit") {
			limit, _ = strconv.Atoi(query.Get("limit"))
			if limit > 100 {
				limit = 100
			}
		}
		pageCacheKey := fmt.Sprintf("submit_deposit:search_validators:%s:%d", searchTerm, limit)
		var cached []models.SubmitDepositPageDataValidator
		pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, &cached, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
			result := buildSubmitDepositSearchValidators(pageCall.CallCtx, searchTerm, limit)
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
	err := json.NewEncoder(w).Encode(pageData)
	if err != nil {
		logrus.WithError(err).Error("error encoding index data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
	return nil
}

func buildSubmitDepositLoadDeposits(ctx context.Context, pubkeys [][]byte) (models.SubmitDepositPageDataDeposits, error) {
	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()
	deposits, depositCount, err := db.GetDepositTxsFiltered(ctx, 0, 1000, canonicalForkIds, &dbtypes.DepositTxFilter{
		PublicKeys:   pubkeys,
		WithOrphaned: 0,
	})
	if err != nil {
		return models.SubmitDepositPageDataDeposits{}, fmt.Errorf("failed to get deposits: %v", err)
	}
	result := models.SubmitDepositPageDataDeposits{
		Deposits: make([]models.SubmitDepositPageDataDeposit, 0, len(deposits)),
		Count:    depositCount,
		HaveMore: depositCount > 1000,
	}
	for _, deposit := range deposits {
		result.Deposits = append(result.Deposits, models.SubmitDepositPageDataDeposit{
			Pubkey:      fmt.Sprintf("0x%x", deposit.PublicKey),
			Amount:      deposit.Amount,
			BlockNumber: deposit.BlockNumber,
			BlockHash:   fmt.Sprintf("0x%x", deposit.BlockRoot),
			BlockTime:   deposit.BlockTime,
			TxOrigin:    common.BytesToAddress(deposit.TxSender).String(),
			TxTarget:    common.BytesToAddress(deposit.TxTarget).String(),
			TxHash:      fmt.Sprintf("0x%x", deposit.TxHash),
		})
	}
	return result, nil
}

func buildSubmitDepositLoadValidators(ctx context.Context, address string) []models.SubmitDepositPageDataValidator {
	addressBytes := common.HexToAddress(address)
	validators, _ := services.GlobalBeaconService.GetFilteredValidatorSet(ctx, &dbtypes.ValidatorFilter{
		WithdrawalAddress: addressBytes[:],
	}, true)
	result := make([]models.SubmitDepositPageDataValidator, 0, len(validators))
	for _, validator := range validators {
		result = append(result, buildSubmitDepositValidatorModel(validator))
	}
	return result
}

func buildSubmitDepositSearchValidators(ctx context.Context, searchTerm string, limit int) []models.SubmitDepositPageDataValidator {
	var validators []v1.Validator
	if searchTerm == "" {
		validators = []v1.Validator{}
	} else if index, err := strconv.ParseUint(searchTerm, 10, 64); err == nil {
		validators, _ = services.GlobalBeaconService.GetFilteredValidatorSet(ctx, &dbtypes.ValidatorFilter{
			MinIndex: &index, MaxIndex: &index, Limit: uint64(limit),
		}, true)
	} else if regexp.MustCompile(`^(0x)?[0-9a-fA-F]+$`).MatchString(searchTerm) {
		pubkey := searchTerm
		if !strings.HasPrefix(pubkey, "0x") {
			pubkey = "0x" + pubkey
		}
		pubkeyBytes, err := hex.DecodeString(strings.TrimPrefix(pubkey, "0x"))
		if err == nil {
			validators, _ = services.GlobalBeaconService.GetFilteredValidatorSet(ctx, &dbtypes.ValidatorFilter{
				PubKey: pubkeyBytes, Limit: uint64(limit),
			}, true)
		}
	} else {
		validators, _ = services.GlobalBeaconService.GetFilteredValidatorSet(ctx, &dbtypes.ValidatorFilter{
			ValidatorName: searchTerm, Limit: uint64(limit),
		}, true)
	}
	result := make([]models.SubmitDepositPageDataValidator, 0, len(validators))
	for _, validator := range validators {
		result = append(result, buildSubmitDepositValidatorModel(validator))
	}
	return result
}

func buildSubmitDepositValidatorModel(validator v1.Validator) models.SubmitDepositPageDataValidator {
	var status string
	if strings.HasPrefix(validator.Status.String(), "pending") {
		status = "Pending"
	} else if validator.Status == v1.ValidatorStateActiveOngoing {
		status = "Active"
	} else if validator.Status == v1.ValidatorStateActiveExiting {
		status = "Exiting"
	} else if validator.Status == v1.ValidatorStateActiveSlashed {
		status = "Slashed"
	} else if validator.Status == v1.ValidatorStateExitedUnslashed {
		status = "Exited"
	} else if validator.Status == v1.ValidatorStateExitedSlashed {
		status = "Slashed"
	} else {
		status = validator.Status.String()
	}
	return models.SubmitDepositPageDataValidator{
		Index:    uint64(validator.Index),
		Pubkey:   validator.Validator.PublicKey.String(),
		Balance:  uint64(validator.Balance),
		CredType: fmt.Sprintf("%02x", validator.Validator.WithdrawalCredentials[0]),
		Status:   status,
	}
}
