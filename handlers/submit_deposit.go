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
		PublicRPCUrl:               utils.Config.Frontend.PublicRPCUrl,
		RainbowkitProjectId:        utils.Config.Frontend.RainbowkitProjectId,
		ChainId:                    specs.DepositChainId,
		GenesisForkVersion:         specs.GenesisForkVersion[:],
		ExplorerUrl:                utils.Config.Frontend.EthExplorerLink,
		MaxEffectiveBalance:        fmt.Sprintf("%d", specs.MaxEffectiveBalance),
		MaxEffectiveBalanceElectra: fmt.Sprintf("%d", specs.MaxEffectiveBalanceElectra),
	}

	return pageData, 1 * time.Hour
}

func handleSubmitDepositPageDataAjax(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	var pageData interface{}

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

		canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()

		deposits, depositCount, err := db.GetDepositTxsFiltered(0, 1000, canonicalForkIds, &dbtypes.DepositTxFilter{
			PublicKeys:   pubkeys,
			WithOrphaned: 0,
		})
		if err != nil {
			return fmt.Errorf("failed to get deposits: %v", err)
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

		pageData = result

	case "load_validators":
		address := query.Get("address")
		addressBytes := common.HexToAddress(address)
		validators, _ := services.GlobalBeaconService.GetFilteredValidatorSet(&dbtypes.ValidatorFilter{
			WithdrawalAddress: addressBytes[:],
		}, true)

		result := []models.SubmitDepositPageDataValidator{}
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

			result = append(result, models.SubmitDepositPageDataValidator{
				Index:    uint64(validator.Index),
				Pubkey:   validator.Validator.PublicKey.String(),
				Balance:  uint64(validator.Balance),
				CredType: fmt.Sprintf("%02x", validator.Validator.WithdrawalCredentials[0]),
				Status:   status,
			})
		}

		pageData = result

	case "search_validators":
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

		result := []models.SubmitDepositPageDataValidator{}
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

			result = append(result, models.SubmitDepositPageDataValidator{
				Index:    uint64(validator.Index),
				Pubkey:   validator.Validator.PublicKey.String(),
				Balance:  uint64(validator.Balance),
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
