package handlers

import (
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
	"github.com/attestantio/go-eth2-client/spec/phase0"
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

	// Get consolidation contract address from client config, fallback to default
	consolidationContract := services.GlobalBeaconService.GetSystemContractAddress(rpc.ConsolidationRequestContract)

	pageData := &models.SubmitConsolidationPageData{
		NetworkName:           specs.ConfigName,
		PublicRPCUrl:          utils.Config.Frontend.PublicRPCUrl,
		RainbowkitProjectId:   utils.Config.Frontend.RainbowkitProjectId,
		ChainId:               specs.DepositChainId,
		ConsolidationContract: consolidationContract.String(),
		ExplorerUrl:           utils.Config.Frontend.EthExplorerLink,
	}

	if utils.Config.Chain.DisplayName != "" {
		pageData.NetworkName = utils.Config.Chain.DisplayName
	}

	return pageData, 1 * time.Hour
}

func handleSubmitConsolidationPageDataAjax(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	var pageData interface{}

	switch query.Get("ajax") {
	case "load_validators":
		chainState := services.GlobalBeaconService.GetChainState()
		chainSpecs := chainState.GetSpecs()
		address := query.Get("address")
		addressBytes := common.HexToAddress(address)
		validators, _ := services.GlobalBeaconService.GetFilteredValidatorSet(&dbtypes.ValidatorFilter{
			WithdrawalAddress: addressBytes[:],
		}, true)

		result := []models.SubmitConsolidationPageDataValidator{}
		for _, validator := range validators {
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

			consolidable := false
			if validator.Validator.ActivationEpoch < beacon.FarFutureEpoch && validator.Validator.ActivationEpoch+phase0.Epoch(chainSpecs.ShardCommitteePeriod) < chainState.CurrentEpoch() {
				consolidable = true
			}

			result = append(result, models.SubmitConsolidationPageDataValidator{
				Index:          uint64(validator.Index),
				Pubkey:         validator.Validator.PublicKey.String(),
				Balance:        uint64(validator.Balance),
				CredType:       fmt.Sprintf("%02x", validator.Validator.WithdrawalCredentials[0]),
				Status:         status,
				IsConsolidable: consolidable,
			})
		}

		pageData = result
	case "search_validators":
		chainState := services.GlobalBeaconService.GetChainState()
		chainSpecs := chainState.GetSpecs()
		searchTerm := query.Get("search")
		limit := 50
		if query.Has("limit") {
			limit, _ = strconv.Atoi(query.Get("limit"))
			if limit > 100 {
				limit = 100
			}
		}

		var validators []v1.Validator
		// Check if search term is a pubkey or index
		if searchTerm == "" {
			// If no search term, return empty result
			validators = []v1.Validator{}
		} else if index, err := strconv.ParseUint(searchTerm, 10, 64); err == nil {
			// Search by index
			validators, _ = services.GlobalBeaconService.GetFilteredValidatorSet(&dbtypes.ValidatorFilter{
				MinIndex: &index,
				MaxIndex: &index,
				Limit:    uint64(limit),
			}, true)
		} else if regexp.MustCompile(`^(0x)?[0-9a-fA-F]+$`).MatchString(searchTerm) {
			// Search by pubkey
			pubkey := searchTerm
			if !strings.HasPrefix(pubkey, "0x") {
				pubkey = "0x" + pubkey
			}
			pubkeyBytes, err := hex.DecodeString(strings.TrimPrefix(pubkey, "0x"))
			if err == nil {
				validators, _ = services.GlobalBeaconService.GetFilteredValidatorSet(&dbtypes.ValidatorFilter{
					PubKey: pubkeyBytes,
					Limit:  uint64(limit),
				}, true)
			}
		} else {
			// Search by name
			validators, _ = services.GlobalBeaconService.GetFilteredValidatorSet(&dbtypes.ValidatorFilter{
				ValidatorName: searchTerm,
				Limit:         uint64(limit),
			}, true)
		}

		result := []models.SubmitConsolidationPageDataValidator{}
		for _, validator := range validators {
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

			consolidable := false
			if validator.Validator.ActivationEpoch < beacon.FarFutureEpoch && validator.Validator.ActivationEpoch+phase0.Epoch(chainSpecs.ShardCommitteePeriod) < chainState.CurrentEpoch() {
				consolidable = true
			}

			result = append(result, models.SubmitConsolidationPageDataValidator{
				Index:          uint64(validator.Index),
				Pubkey:         validator.Validator.PublicKey.String(),
				Balance:        uint64(validator.Balance),
				CredType:       fmt.Sprintf("%02x", validator.Validator.WithdrawalCredentials[0]),
				Status:         status,
				IsConsolidable: consolidable,
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
