package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// IncludedDeposits will return the filtered "included_deposits" page using a go template
func IncludedDeposits(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"included_deposits/included_deposits.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/included_deposits", "Included Deposits", templateFiles)

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
	var vname string
	var minAmount uint64
	var maxAmount uint64
	var withOrphaned uint64
	var address string
	var withValid uint64

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
		if urlArgs.Has("f.vname") {
			vname = urlArgs.Get("f.vname")
		}
		if urlArgs.Has("f.mina") {
			minAmount, _ = strconv.ParseUint(urlArgs.Get("f.mina"), 10, 64)
		}
		if urlArgs.Has("f.maxa") {
			maxAmount, _ = strconv.ParseUint(urlArgs.Get("f.maxa"), 10, 64)
		}
		if urlArgs.Has("f.orphaned") {
			withOrphaned, _ = strconv.ParseUint(urlArgs.Get("f.orphaned"), 10, 64)
		}
		if urlArgs.Has("f.address") {
			address = urlArgs.Get("f.address")
		}
		if urlArgs.Has("f.valid") {
			withValid, _ = strconv.ParseUint(urlArgs.Get("f.valid"), 10, 64)
		}
	} else {
		withOrphaned = 1
		withValid = 1
	}
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredIncludedDepositsPageData(pageIdx, pageSize, minIndex, maxIndex, publickey, vname, minAmount, maxAmount, uint8(withOrphaned), address, uint8(withValid))
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "slots_filtered.go", "SlotsFiltered", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredIncludedDepositsPageData(pageIdx uint64, pageSize uint64, minIndex uint64, maxIndex uint64, publickey string, vname string, minAmount uint64, maxAmount uint64, withOrphaned uint8, address string, withValid uint8) (*models.IncludedDepositsPageData, error) {
	pageData := &models.IncludedDepositsPageData{}
	pageCacheKey := fmt.Sprintf("included_deposits:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minIndex, maxIndex, publickey, vname, minAmount, maxAmount, withOrphaned, address, withValid)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredIncludedDepositsPageData(pageIdx, pageSize, minIndex, maxIndex, publickey, vname, minAmount, maxAmount, withOrphaned, address, withValid)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.IncludedDepositsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredIncludedDepositsPageData(pageIdx uint64, pageSize uint64, minIndex uint64, maxIndex uint64, publickey string, vname string, minAmount uint64, maxAmount uint64, withOrphaned uint8, address string, withValid uint8) *models.IncludedDepositsPageData {
	filterArgs := url.Values{}
	if minIndex != 0 {
		filterArgs.Add("f.mini", fmt.Sprintf("%v", minIndex))
	}
	if maxIndex != 0 {
		filterArgs.Add("f.maxi", fmt.Sprintf("%v", maxIndex))
	}
	if publickey != "" {
		filterArgs.Add("f.pubkey", publickey)
	}
	if vname != "" {
		filterArgs.Add("f.vname", vname)
	}
	if minAmount != 0 {
		filterArgs.Add("f.mina", fmt.Sprintf("%v", minAmount))
	}
	if maxAmount != 0 {
		filterArgs.Add("f.maxa", fmt.Sprintf("%v", maxAmount))
	}
	if withOrphaned != 0 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}
	if address != "" {
		filterArgs.Add("f.address", address)
	}
	if withValid != 0 {
		filterArgs.Add("f.valid", fmt.Sprintf("%v", withValid))
	}

	pageData := &models.IncludedDepositsPageData{
		FilterMinIndex:      minIndex,
		FilterMaxIndex:      maxIndex,
		FilterPubKey:        publickey,
		FilterValidatorName: vname,
		FilterMinAmount:     minAmount,
		FilterMaxAmount:     maxAmount,
		FilterWithOrphaned:  withOrphaned,
		FilterAddress:       address,
		FilterWithValid:     withValid,
	}
	logrus.Debugf("included_deposits page called: %v:%v [%v,%v,%v,%v,%v,%v]", pageIdx, pageSize, minIndex, maxIndex, publickey, vname, minAmount, maxAmount)
	if pageIdx == 1 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pageIdx
	pageData.CurrentPageIndex = pageIdx
	if pageIdx > 1 {
		pageData.PrevPageIndex = pageIdx - 1
	}

	// Calculate offset from page index
	pageOffset := (pageIdx - 1) * pageSize

	// Update to use new filter structure
	depositFilter := &services.CombinedDepositRequestFilter{
		Filter: &dbtypes.DepositTxFilter{
			MinIndex:      minIndex,
			MaxIndex:      maxIndex,
			PublicKey:     common.FromHex(publickey),
			ValidatorName: vname,
			MinAmount:     minAmount,
			MaxAmount:     maxAmount,
			WithOrphaned:  withOrphaned,
			Address:       common.FromHex(address),
			WithValid:     withValid,
		},
	}

	// Get deposits using new service function with offset
	dbDeposits, totalRows := services.GlobalBeaconService.GetDepositRequestsByFilter(depositFilter, pageOffset, uint32(pageSize))

	chainState := services.GlobalBeaconService.GetChainState()

	for _, deposit := range dbDeposits {
		depositData := &models.IncludedDepositsPageDataDeposit{
			PublicKey:             deposit.PublicKey(),
			Withdrawalcredentials: deposit.WithdrawalCredentials(),
			Amount:                deposit.Amount(),
			Time:                  chainState.SlotToTime(phase0.Slot(deposit.Request.SlotNumber)),
			SlotNumber:            deposit.Request.SlotNumber,
			SlotRoot:              deposit.Request.SlotRoot,
			Orphaned:              deposit.RequestOrphaned,
			DepositorAddress:      deposit.SourceAddress(),
		}

		if deposit.Request != nil {
			depositData.HasIndex = true
			depositData.Index = *deposit.Request.Index
		}

		// Add queue status
		if deposit.IsQueued {
			depositData.IsQueued = true
			if !bytes.Equal(deposit.QueueEntry.PendingDeposit.Pubkey[:], deposit.PublicKey()) {
				logrus.Warnf("queue entry public key mismatch: %x != %x", deposit.QueueEntry.PendingDeposit.Pubkey[:], deposit.PublicKey())
			}

			depositData.QueuePosition = deposit.QueueEntry.QueuePos
			depositData.EstimatedTime = chainState.EpochToTime(deposit.QueueEntry.EpochEstimate)
		}

		// Add validator status
		if validatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(deposit.PublicKey())); !found {
			depositData.ValidatorStatus = "Deposited"
			depositData.ValidatorExists = false
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

		// Add transaction details if available
		if deposit.Transaction != nil {
			depositData.HasTransaction = true
			depositData.TransactionHash = deposit.Transaction.TxHash
			depositData.TransactionDetails = &models.IncludedDepositsPageDataDepositTxDetails{
				BlockNumber: deposit.Transaction.BlockNumber,
				BlockHash:   fmt.Sprintf("%#x", deposit.Transaction.BlockRoot),
				BlockTime:   deposit.Transaction.BlockTime,
				TxOrigin:    common.Address(deposit.Transaction.TxSender).Hex(),
				TxTarget:    common.Address(deposit.Transaction.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("%#x", deposit.Transaction.TxHash),
			}
			if deposit.Transaction.ValidSignature == 0 {
				depositData.InvalidSignature = true
			}
		}

		pageData.Deposits = append(pageData.Deposits, depositData)
	}
	pageData.DepositCount = uint64(len(pageData.Deposits))

	if pageData.DepositCount > 0 {
		pageData.FirstIndex = pageData.Deposits[0].Index
		pageData.LastIndex = pageData.Deposits[pageData.DepositCount-1].Index
	}

	pageData.TotalPages = totalRows / pageSize
	if totalRows%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	pageData.FirstPageLink = fmt.Sprintf("/validators/included_deposits?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/included_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/included_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/included_deposits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
