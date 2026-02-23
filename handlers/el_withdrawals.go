package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// ElWithdrawals will return the filtered "el_withdrawals" page using a go template
func ElWithdrawals(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"el_withdrawals/el_withdrawals.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/el_withdrawals", "Withdrawal Requests", templateFiles)

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

	var minSlot uint64
	var maxSlot uint64
	var sourceAddr string
	var minIndex uint64
	var maxIndex uint64
	var vname string
	var withOrphaned uint64
	var withType uint64
	var pubkey string

	if urlArgs.Has("f") {
		if urlArgs.Has("f.mins") {
			minSlot, _ = strconv.ParseUint(urlArgs.Get("f.mins"), 10, 64)
		}
		if urlArgs.Has("f.maxs") {
			maxSlot, _ = strconv.ParseUint(urlArgs.Get("f.maxs"), 10, 64)
		}
		if urlArgs.Has("f.address") {
			sourceAddr = urlArgs.Get("f.address")
		}
		if urlArgs.Has("f.mini") {
			minIndex, _ = strconv.ParseUint(urlArgs.Get("f.mini"), 10, 64)
		}
		if urlArgs.Has("f.maxi") {
			maxIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxi"), 10, 64)
		}
		if urlArgs.Has("f.vname") {
			vname = urlArgs.Get("f.vname")
		}
		if urlArgs.Has("f.orphaned") {
			withOrphaned, _ = strconv.ParseUint(urlArgs.Get("f.orphaned"), 10, 64)
		}
		if urlArgs.Has("f.type") {
			withType, _ = strconv.ParseUint(urlArgs.Get("f.type"), 10, 64)
		}
		if urlArgs.Has("f.pubkey") {
			pubkey = urlArgs.Get("f.pubkey")
		}
	} else {
		withOrphaned = 1
	}
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredElWithdrawalsPageData(pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minIndex, maxIndex, vname, uint8(withOrphaned), uint8(withType), pubkey)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "el_withdrawals.go", "ElWithdrawals", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredElWithdrawalsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, sourceAddr string, minIndex uint64, maxIndex uint64, vname string, withOrphaned uint8, withType uint8, pubkey string) (*models.ElWithdrawalsPageData, error) {
	pageData := &models.ElWithdrawalsPageData{}
	pageCacheKey := fmt.Sprintf("el_withdrawals:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minIndex, maxIndex, vname, withOrphaned, withType, pubkey)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredElWithdrawalsPageData(pageCall.CallCtx, pageIdx, pageSize, minSlot, maxSlot, sourceAddr, minIndex, maxIndex, vname, withOrphaned, withType, pubkey)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ElWithdrawalsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredElWithdrawalsPageData(ctx context.Context, pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, sourceAddr string, minIndex uint64, maxIndex uint64, vname string, withOrphaned uint8, withType uint8, pubkey string) *models.ElWithdrawalsPageData {
	filterArgs := url.Values{}
	if minSlot != 0 {
		filterArgs.Add("f.mins", fmt.Sprintf("%v", minSlot))
	}
	if maxSlot != 0 {
		filterArgs.Add("f.maxs", fmt.Sprintf("%v", maxSlot))
	}
	if sourceAddr != "" {
		filterArgs.Add("f.address", sourceAddr)
	}
	if minIndex != 0 {
		filterArgs.Add("f.mini", fmt.Sprintf("%v", minIndex))
	}
	if maxIndex != 0 {
		filterArgs.Add("f.maxi", fmt.Sprintf("%v", maxIndex))
	}
	if vname != "" {
		filterArgs.Add("f.vname", vname)
	}
	if withOrphaned != 1 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}
	if withType != 0 {
		filterArgs.Add("f.type", fmt.Sprintf("%v", withType))
	}
	if pubkey != "" {
		filterArgs.Add("f.pubkey", pubkey)
	}

	pageData := &models.ElWithdrawalsPageData{
		FilterAddress:       sourceAddr,
		FilterMinSlot:       minSlot,
		FilterMaxSlot:       maxSlot,
		FilterMinIndex:      minIndex,
		FilterMaxIndex:      maxIndex,
		FilterValidatorName: vname,
		FilterWithOrphaned:  withOrphaned,
		FilterWithType:      withType,
		FilterPublicKey:     pubkey,
	}
	logrus.Debugf("el_withdrawals page called: %v:%v [%v,%v,%v,%v,%v]", pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname)
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

	// Create combined filter
	withdrawalRequestFilter := &services.CombinedWithdrawalRequestFilter{
		Filter: &dbtypes.WithdrawalRequestFilter{
			MinSlot:       minSlot,
			MaxSlot:       maxSlot,
			SourceAddress: common.FromHex(sourceAddr),
			MinIndex:      minIndex,
			MaxIndex:      maxIndex,
			ValidatorName: vname,
			WithOrphaned:  withOrphaned,
			PublicKey:     common.FromHex(pubkey),
		},
	}

	switch withType {
	case 1: // withdrawals
		minAmount := uint64(1)
		withdrawalRequestFilter.Filter.MinAmount = &minAmount
	case 2: // exits
		maxAmount := uint64(0)
		withdrawalRequestFilter.Filter.MaxAmount = &maxAmount
	}

	dbElWithdrawals, totalPendingTxRows, totalRequests := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(ctx, withdrawalRequestFilter, (pageIdx-1)*pageSize, uint32(pageSize))
	chainState := services.GlobalBeaconService.GetChainState()
	headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
	headBlockNum := uint64(0)
	if headBlock != nil && headBlock.GetBlockIndex(ctx) != nil {
		headBlockNum = uint64(headBlock.GetBlockIndex(ctx).ExecutionNumber)
	}

	for _, elWithdrawal := range dbElWithdrawals {
		elWithdrawalData := &models.ElWithdrawalsPageDataWithdrawal{
			SourceAddr: elWithdrawal.SourceAddress(),
			Amount:     elWithdrawal.Amount(),
			PublicKey:  elWithdrawal.ValidatorPubkey(),
		}

		if validatorIndex := elWithdrawal.ValidatorIndex(); validatorIndex != nil {
			elWithdrawalData.ValidatorIndex = *validatorIndex
			elWithdrawalData.ValidatorName = services.GlobalBeaconService.GetValidatorName(*validatorIndex)
			elWithdrawalData.ValidatorValid = true
		}

		if request := elWithdrawal.Request; request != nil {
			elWithdrawalData.IsIncluded = true
			elWithdrawalData.SlotNumber = request.SlotNumber
			elWithdrawalData.SlotRoot = request.SlotRoot
			elWithdrawalData.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber))
			elWithdrawalData.Status = uint64(1)
			elWithdrawalData.Result = request.Result
			elWithdrawalData.ResultMessage = getWithdrawalResultMessage(request.Result, chainState.GetSpecs())
			if elWithdrawal.RequestOrphaned {
				elWithdrawalData.Status = uint64(2)
			}
		}

		if transaction := elWithdrawal.Transaction; transaction != nil {
			elWithdrawalData.TransactionHash = transaction.TxHash
			elWithdrawalData.LinkedTransaction = true
			elWithdrawalData.TransactionDetails = &models.ElWithdrawalsPageDataWithdrawalTxDetails{
				BlockNumber: transaction.BlockNumber,
				BlockHash:   fmt.Sprintf("%#x", transaction.BlockRoot),
				BlockTime:   transaction.BlockTime,
				TxOrigin:    common.Address(transaction.TxSender).Hex(),
				TxTarget:    common.Address(transaction.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("%#x", transaction.TxHash),
			}
			elWithdrawalData.TxStatus = uint64(1)
			if elWithdrawal.TransactionOrphaned {
				elWithdrawalData.TxStatus = uint64(2)
			}

			if !elWithdrawalData.IsIncluded {
				queuePos := int64(transaction.DequeueBlock) - int64(headBlockNum)
				targetSlot := int64(chainState.CurrentSlot()) + queuePos
				if targetSlot > 0 {
					elWithdrawalData.SlotNumber = uint64(targetSlot)
					elWithdrawalData.Time = chainState.SlotToTime(phase0.Slot(targetSlot))
				}
			}
		}

		pageData.ElRequests = append(pageData.ElRequests, elWithdrawalData)
	}

	pageData.RequestCount = uint64(len(pageData.ElRequests))

	if pageData.RequestCount > 0 {
		pageData.FirstIndex = pageData.ElRequests[0].SlotNumber
		pageData.LastIndex = pageData.ElRequests[pageData.RequestCount-1].SlotNumber
	}

	totalRows := totalPendingTxRows + totalRequests
	pageData.TotalPages = totalRows / pageSize
	if totalRows%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	// Populate UrlParams for page jump functionality
	pageData.UrlParams = make(map[string]string)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams[key] = values[0]
		}
	}
	pageData.UrlParams["c"] = fmt.Sprintf("%v", pageData.PageSize)

	pageData.FirstPageLink = fmt.Sprintf("/validators/el_withdrawals?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/el_withdrawals?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/el_withdrawals?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/el_withdrawals?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}

func getWithdrawalResultMessage(result uint8, specs *consensus.ChainSpec) string {
	switch result {
	case dbtypes.WithdrawalRequestResultUnknown:
		return "Unknown result"
	case dbtypes.WithdrawalRequestResultSuccess:
		return "Success"
	case dbtypes.WithdrawalRequestResultQueueFull:
		return "Error: Withdrawal queue is full"
	case dbtypes.WithdrawalRequestResultValidatorNotFound:
		return "Error: Validator not found"
	case dbtypes.WithdrawalRequestResultValidatorInvalidCredentials:
		return "Error: Validator has invalid credentials"
	case dbtypes.WithdrawalRequestResultValidatorInvalidSender:
		return "Error: Validator withdrawal address does not match tx sender"
	case dbtypes.WithdrawalRequestResultValidatorNotActive:
		return "Error: Validator is not active"
	case dbtypes.WithdrawalRequestResultValidatorNotOldEnough:
		return fmt.Sprintf("Error: Validator is not old enough (min. %v epochs)", specs.ShardCommitteePeriod)
	case dbtypes.WithdrawalRequestResultValidatorNotCompounding:
		return "Error: Validator is not compounding"
	case dbtypes.WithdrawalRequestResultValidatorHasPendingWithdrawal:
		return "Error: Validator has pending partial withdrawal"
	case dbtypes.WithdrawalRequestResultValidatorBalanceTooLow:
		return "Error: Validator balance too low"
	default:
		return fmt.Sprintf("Unknown error code: %d", result)
	}
}
