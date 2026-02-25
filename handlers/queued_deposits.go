package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
)

// QueuedDeposits will return the "queued_deposits" page using a go template
func QueuedDeposits(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"queued_deposits/queued_deposits.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/queued_deposits", "Queued Deposits", templateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var pageIdx uint64 = 1
	if urlArgs.Has("p") {
		pageIdx, _ = strconv.ParseUint(urlArgs.Get("p"), 10, 64)
		if pageIdx < 1 {
			pageIdx = 1
		}
	}

	var minIndex uint64
	var maxIndex uint64
	var publickey string
	var minAmount uint64
	var maxAmount uint64

	if urlArgs.Has("f") {
		if urlArgs.Has("f.mini") {
			minIndex, _ = strconv.ParseUint(urlArgs.Get("f.mini"), 10, 64)
		}
		if urlArgs.Has("f.maxi") {
			maxIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxi"), 10, 64)
		}
		if urlArgs.Has("f.pubkey") {
			publickey = urlArgs.Get("f.pubkey")
		}
		if urlArgs.Has("f.mina") {
			minAmount, _ = strconv.ParseUint(urlArgs.Get("f.mina"), 10, 64)
		}
		if urlArgs.Has("f.maxa") {
			maxAmount, _ = strconv.ParseUint(urlArgs.Get("f.maxa"), 10, 64)
		}
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getQueuedDepositsPageData(pageIdx, pageSize, minIndex, maxIndex, publickey, minAmount, maxAmount)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "queued_deposits.go", "QueuedDeposits", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getQueuedDepositsPageData(pageIdx uint64, pageSize uint64, minIndex uint64, maxIndex uint64, publickey string, minAmount uint64, maxAmount uint64) (*models.QueuedDepositsPageData, error) {
	pageData := &models.QueuedDepositsPageData{
		FilterMinIndex:  minIndex,
		FilterMaxIndex:  maxIndex,
		FilterPubKey:    publickey,
		FilterMinAmount: minAmount,
		FilterMaxAmount: maxAmount,
	}
	pageCacheKey := fmt.Sprintf("queued_deposits:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minIndex, maxIndex, publickey, minAmount, maxAmount)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		return buildQueuedDepositsPageData(pageCall.CallCtx, pageIdx, pageSize, minIndex, maxIndex, publickey, minAmount, maxAmount)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.QueuedDepositsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildQueuedDepositsPageData(ctx context.Context, pageIdx uint64, pageSize uint64, minIndex uint64, maxIndex uint64, publickey string, minAmount uint64, maxAmount uint64) *models.QueuedDepositsPageData {
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	pageData := &models.QueuedDepositsPageData{
		FilterMinIndex:      minIndex,
		FilterMaxIndex:      maxIndex,
		FilterPubKey:        publickey,
		FilterMinAmount:     minAmount,
		FilterMaxAmount:     maxAmount,
		MaxEffectiveBalance: specs.MaxEffectiveBalance,
	}

	if pageIdx == 1 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 1000 {
		pageSize = 1000
	}
	pageData.PageSize = pageSize
	pageData.CurrentPageIndex = pageIdx
	if pageIdx > 1 {
		pageData.PrevPageIndex = pageIdx - 1
	}

	filteredQueue := services.GlobalBeaconService.GetFilteredQueuedDeposits(ctx, &services.QueuedDepositFilter{
		MinIndex:  minIndex,
		MaxIndex:  maxIndex,
		PublicKey: common.FromHex(publickey),
		MinAmount: minAmount,
		MaxAmount: maxAmount,
	})

	// Calculate total pages
	totalDeposits := uint64(len(filteredQueue))
	pageData.TotalPages = totalDeposits / pageSize
	if totalDeposits%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	// Calculate page links
	filterArgs := url.Values{}
	if minIndex > 0 {
		filterArgs.Add("f.mini", fmt.Sprintf("%v", minIndex))
	}
	if maxIndex > 0 {
		filterArgs.Add("f.maxi", fmt.Sprintf("%v", maxIndex))
	}
	if publickey != "" {
		filterArgs.Add("f.pubkey", publickey)
	}
	if minAmount > 0 {
		filterArgs.Add("f.mina", fmt.Sprintf("%v", minAmount))
	}
	if maxAmount > 0 {
		filterArgs.Add("f.maxa", fmt.Sprintf("%v", maxAmount))
	}

	// Populate UrlParams for page jump functionality
	pageData.UrlParams = make(map[string]string)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams[key] = values[0]
		}
	}
	pageData.UrlParams["c"] = fmt.Sprintf("%v", pageData.PageSize)

	pageData.FirstPageLink = fmt.Sprintf("/validators/queued_deposits?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/queued_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/queued_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/queued_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	// Calculate slice for current page
	start := (pageIdx - 1) * pageSize
	end := start + pageSize
	if end > totalDeposits {
		end = totalDeposits
	}

	depositIndexes := make([]uint64, 0)
	for i := start; i < end; i++ {
		queueEntry := filteredQueue[i]
		if queueEntry.DepositIndex == nil {
			continue
		}
		depositIndexes = append(depositIndexes, *queueEntry.DepositIndex)
	}

	txDetailsMap := map[uint64]*dbtypes.DepositTx{}
	for _, txDetail := range db.GetDepositTxsByIndexes(ctx, depositIndexes) {
		txDetailsMap[txDetail.Index] = txDetail
	}

	g2AtInfinity := bytes.Repeat([]byte{0x00}, 96)
	g2AtInfinity[0] = 0xc0

	// Process only deposits for current page
	for i := start; i < end; i++ {
		queueEntry := filteredQueue[i]

		wdCreds := queueEntry.PendingDeposit.WithdrawalCredentials[:]
		isBuilder := len(wdCreds) > 0 && wdCreds[0] == 0x03

		depositData := &models.QueuedDepositsPageDataDeposit{
			QueuePosition:         queueEntry.QueuePos,
			EstimatedTime:         chainState.EpochToTime(queueEntry.EpochEstimate),
			PublicKey:             queueEntry.PendingDeposit.Pubkey[:],
			Amount:                uint64(queueEntry.PendingDeposit.Amount),
			Withdrawalcredentials: wdCreds,
			IsBuilder:             isBuilder,
		}

		// Get validator status if exists
		if validatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(depositData.PublicKey)); !found {
			depositData.ValidatorStatus = "Deposited"
		} else {
			depositData.ValidatorExists = true
			depositData.ValidatorIndex = uint64(validatorIdx)
			depositData.ValidatorName = services.GlobalBeaconService.GetValidatorName(uint64(validatorIdx))

			validator := services.GlobalBeaconService.GetValidatorByIndex(validatorIdx, false)
			if strings.HasPrefix(validator.Status.String(), "pending") {
				depositData.ValidatorStatus = "Pending"
			} else if validator.Status == v1.ValidatorStateActiveOngoing {
				depositData.ValidatorStatus = "Active"
				depositData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateActiveExiting {
				depositData.ValidatorStatus = "Exiting"
				depositData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateActiveSlashed {
				depositData.ValidatorStatus = "Slashed"
				depositData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateExitedUnslashed {
				depositData.ValidatorStatus = "Exited"
			} else if validator.Status == v1.ValidatorStateExitedSlashed {
				depositData.ValidatorStatus = "Slashed"
			} else {
				depositData.ValidatorStatus = validator.Status.String()
			}

			if depositData.ShowUpcheck {
				depositData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				depositData.UpcheckMaximum = uint8(3)
			}
		}

		if queueEntry.DepositIndex != nil {
			depositData.HasIndex = true
			depositData.Index = *queueEntry.DepositIndex

			if tx, txFound := txDetailsMap[depositData.Index]; txFound {
				depositData.HasTransaction = true
				depositData.TransactionHash = tx.TxHash
				depositData.Withdrawalcredentials = tx.WithdrawalCredentials
				depositData.TransactionDetails = &models.QueuedDepositsPageDataDepositTxDetails{
					BlockNumber: tx.BlockNumber,
					BlockHash:   fmt.Sprintf("%#x", tx.BlockRoot),
					BlockTime:   tx.BlockTime,
					TxOrigin:    common.Address(tx.TxSender).Hex(),
					TxTarget:    common.Address(tx.TxTarget).Hex(),
					TxHash:      fmt.Sprintf("%#x", tx.TxHash),
				}
			}
		}

		if queueEntry.PendingDeposit.Slot == 0 && queueEntry.PendingDeposit.WithdrawalCredentials[0] == 0x02 && bytes.Equal(queueEntry.PendingDeposit.Signature[:], g2AtInfinity) {
			depositData.ExcessDeposit = true
		}

		pageData.Deposits = append(pageData.Deposits, depositData)
	}

	pageData.DepositsFrom = start + 1
	pageData.DepositsTo = end
	pageData.DepositCount = uint64(len(pageData.Deposits))
	return pageData
}
