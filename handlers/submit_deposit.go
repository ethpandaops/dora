package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

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
		NetworkName:         specs.ConfigName,
		DepositContract:     specs.DepositContractAddress,
		PublicRPCUrl:        utils.Config.Frontend.PublicRPCUrl,
		RainbowkitProjectId: utils.Config.Frontend.RainbowkitProjectId,
		ChainId:             specs.DepositChainId,
		GenesisForkVersion:  specs.GenesisForkVersion[:],
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

		depositSyncState := dbtypes.DepositIndexerState{}
		db.GetExplorerState("indexer.depositstate", &depositSyncState)

		deposits, depositCount, err := db.GetDepositTxsFilteredLegacy(0, 1000, depositSyncState.FinalBlock, &dbtypes.DepositTxFilter{
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
