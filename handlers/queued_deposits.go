package handlers

import (
	"fmt"
	"net/http"
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

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getQueuedDepositsPageData(pageIdx, pageSize)
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

func getQueuedDepositsPageData(pageIdx uint64, pageSize uint64) (*models.QueuedDepositsPageData, error) {
	pageData := &models.QueuedDepositsPageData{}
	pageCacheKey := fmt.Sprintf("queued_deposits:%v:%v", pageIdx, pageSize)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildQueuedDepositsPageData(pageIdx, pageSize)
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

func buildQueuedDepositsPageData(pageIdx uint64, pageSize uint64) *models.QueuedDepositsPageData {
	pageData := &models.QueuedDepositsPageData{}

	if pageIdx == 1 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.CurrentPageIndex = pageIdx
	if pageIdx > 1 {
		pageData.PrevPageIndex = pageIdx - 1
	}

	canonicalHead := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
	if canonicalHead == nil {
		return pageData
	}

	queue := services.GlobalBeaconService.GetIndexedDepositQueue(canonicalHead)
	if queue == nil {
		return pageData
	}

	// Calculate total pages
	totalDeposits := uint64(len(queue))
	pageData.TotalPages = totalDeposits / pageSize
	if totalDeposits%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	// Calculate page links
	pageData.FirstPageLink = fmt.Sprintf("/validators/queued_deposits?c=%v", pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/queued_deposits?c=%v&p=%v", pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/queued_deposits?c=%v&p=%v", pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/queued_deposits?c=%v&p=%v", pageData.PageSize, pageData.LastPageIndex)

	// Calculate slice for current page
	start := (pageIdx - 1) * pageSize
	end := start + pageSize
	if end > totalDeposits {
		end = totalDeposits
	}

	depositIndexes := make([]uint64, 0)
	for i := start; i < end; i++ {
		queueEntry := queue[i]
		if queueEntry.DepositIndex == nil {
			continue
		}
		depositIndexes = append(depositIndexes, *queueEntry.DepositIndex)
	}

	txDetailsMap := map[uint64]*dbtypes.DepositTx{}
	for _, txDetail := range db.GetDepositTxsByIndexes(depositIndexes) {
		txDetailsMap[txDetail.Index] = txDetail
	}

	// Process only deposits for current page
	for i := start; i < end; i++ {
		queueEntry := queue[i]

		depositData := &models.QueuedDepositsPageDataDeposit{
			QueuePosition:         queueEntry.QueuePos,
			EstimatedTime:         queueEntry.TimeEstimate,
			PublicKey:             queueEntry.PendingDeposit.Pubkey[:],
			Amount:                uint64(queueEntry.PendingDeposit.Amount),
			Withdrawalcredentials: queueEntry.PendingDeposit.WithdrawalCredentials[:],
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

		pageData.Deposits = append(pageData.Deposits, depositData)
	}

	pageData.DepositsFrom = start + 1
	pageData.DepositsTo = end
	pageData.DepositCount = uint64(len(pageData.Deposits))
	return pageData
}
