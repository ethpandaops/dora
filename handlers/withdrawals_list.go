package handlers

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// WithdrawalsList will return the filtered "withdrawals list" page using a go template.
func WithdrawalsList(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"withdrawals_list/withdrawals_list.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/withdrawals/filtered", "Withdrawals", templateFiles)

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

	var validator string
	var address string
	var minAmount string
	var maxAmount string
	var withType uint64
	var withOrphaned uint64

	if urlArgs.Has("f") {
		if urlArgs.Has("f.validator") {
			validator = urlArgs.Get("f.validator")
		}
		if urlArgs.Has("f.address") {
			address = urlArgs.Get("f.address")
		}
		if urlArgs.Has("f.type") {
			withType, _ = strconv.ParseUint(urlArgs.Get("f.type"), 10, 64)
		}
		if urlArgs.Has("f.mina") {
			minAmount = urlArgs.Get("f.mina")
		}
		if urlArgs.Has("f.maxa") {
			maxAmount = urlArgs.Get("f.maxa")
		}
		if urlArgs.Has("f.orphaned") {
			withOrphaned, _ = strconv.ParseUint(urlArgs.Get("f.orphaned"), 10, 64)
		}
	} else {
		withOrphaned = 1
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredWithdrawalsListPageData(pageIdx, pageSize, validator, address, uint8(withType), minAmount, maxAmount, uint8(withOrphaned))
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "withdrawals_list.go", "WithdrawalsList", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getFilteredWithdrawalsListPageData(pageIdx uint64, pageSize uint64, validator string, address string, withType uint8, minAmount string, maxAmount string, withOrphaned uint8) (*models.WithdrawalsListPageData, error) {
	pageData := &models.WithdrawalsListPageData{}
	pageCacheKey := fmt.Sprintf("withdrawals_list:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, validator, address, withType, minAmount, maxAmount, withOrphaned)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildFilteredWithdrawalsListPageData(pageCall.CallCtx, pageIdx, pageSize, validator, address, withType, minAmount, maxAmount, withOrphaned)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.WithdrawalsListPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredWithdrawalsListPageData(ctx context.Context, pageIdx uint64, pageSize uint64, validator string, address string, withType uint8, minAmount string, maxAmount string, withOrphaned uint8) (*models.WithdrawalsListPageData, time.Duration) {
	filterArgs := url.Values{}
	if validator != "" {
		filterArgs.Add("f.validator", validator)
	}
	if address != "" {
		filterArgs.Add("f.address", address)
	}
	if withType != 0 {
		filterArgs.Add("f.type", fmt.Sprintf("%v", withType))
	}
	if minAmount != "" {
		filterArgs.Add("f.mina", minAmount)
	}
	if maxAmount != "" {
		filterArgs.Add("f.maxa", maxAmount)
	}
	if withOrphaned != 0 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}

	pageData := &models.WithdrawalsListPageData{
		FilterValidator:    validator,
		FilterAddress:      address,
		FilterWithType:     withType,
		FilterMinAmount:    minAmount,
		FilterMaxAmount:    maxAmount,
		FilterWithOrphaned: withOrphaned,
	}
	cacheTimeout := 5 * time.Minute
	logrus.Debugf("withdrawals_list page called: %v:%v [%v,%v,%v,%v,%v,%v]", pageIdx, pageSize, validator, address, withType, minAmount, maxAmount, withOrphaned)
	if pageIdx == 1 {
		pageData.IsDefaultPage = true
	} else {
		cacheTimeout = 15 * time.Minute
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

	// Build filter
	withdrawalFilter := &dbtypes.WithdrawalFilter{
		WithOrphaned: withOrphaned,
	}

	// Parse validator filter (index or name)
	if validator != "" {
		validatorIndex, err := strconv.ParseUint(validator, 10, 64)
		if err == nil {
			withdrawalFilter.Validator = &validatorIndex
		} else {
			withdrawalFilter.ValidatorName = validator
		}
	}

	// Parse address filter -> resolve to account_id
	if address != "" && utils.Config.ExecutionIndexer.Enabled {
		addrBytes, err := parseAddress(address)
		if err == nil {
			account, err := db.GetElAccountByAddress(ctx, addrBytes)
			if err == nil && account != nil {
				withdrawalFilter.AccountID = &account.ID
			}
		}
	}

	// Parse type filter (URL values 1-4 map to DB types 0-3)
	if withType >= 1 && withType <= 4 {
		t := withType - 1
		withdrawalFilter.Type = &t
	}

	// Parse amount filters
	if minAmount != "" {
		if v, err := strconv.ParseFloat(minAmount, 64); err == nil {
			withdrawalFilter.MinAmount = &v
		}
	}
	if maxAmount != "" {
		if v, err := strconv.ParseFloat(maxAmount, 64); err == nil {
			withdrawalFilter.MaxAmount = &v
		}
	}

	// Fetch withdrawals
	dbWithdrawals, totalRows := services.GlobalBeaconService.GetWithdrawalsByFilter(ctx, withdrawalFilter, pageIdx-1, uint32(pageSize))
	chainState := services.GlobalBeaconService.GetChainState()

	// Batch resolve account IDs to addresses
	accountIDs := make([]uint64, 0, len(dbWithdrawals))
	validatorIDs := make([]phase0.ValidatorIndex, 0, len(dbWithdrawals))
	accountIDSet := make(map[uint64]bool, len(dbWithdrawals))
	validatorIDSet := make(map[uint64]bool, len(dbWithdrawals))
	for _, w := range dbWithdrawals {
		if w.AccountID != nil && *w.AccountID > 0 && !accountIDSet[*w.AccountID] {
			accountIDSet[*w.AccountID] = true
			accountIDs = append(accountIDs, *w.AccountID)
		} else if w.Address == nil && w.Validator != nil && !validatorIDSet[*w.Validator] {
			validatorIDSet[*w.Validator] = true
			validatorIDs = append(validatorIDs, phase0.ValidatorIndex(*w.Validator))
		}
	}
	accountMap := make(map[uint64]*dbtypes.ElAccount, len(accountIDs))
	if len(accountIDs) > 0 {
		accounts, err := db.GetElAccountsByIDs(ctx, accountIDs)
		if err == nil {
			for _, acct := range accounts {
				accountMap[acct.ID] = acct
			}
		}
	}
	validatorMap := make(map[uint64]v1.Validator, len(validatorIDs))
	if len(validatorIDs) > 0 {
		validatorSetRsp, _ := services.GlobalBeaconService.GetFilteredValidatorSet(ctx, &dbtypes.ValidatorFilter{
			Indices: validatorIDs,
		}, false)

		for _, validator := range validatorSetRsp {
			validatorMap[uint64(validator.Index)] = validator
		}
	}

	// Batch resolve blocks for block root and number
	blockUids := make([]uint64, 0, len(dbWithdrawals))
	blockUidSet := make(map[uint64]bool, len(dbWithdrawals))
	for _, w := range dbWithdrawals {
		if !blockUidSet[w.BlockUid] {
			blockUidSet[w.BlockUid] = true
			blockUids = append(blockUids, w.BlockUid)
		}
	}
	blockMap := make(map[uint64]*dbtypes.AssignedSlot, len(blockUids))
	if len(blockUids) > 0 {
		filter := &dbtypes.BlockFilter{
			BlockUids:    blockUids,
			WithOrphaned: 1,
		}
		blocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, filter, 0, uint32(len(blockUids)), 0)
		for _, b := range blocks {
			if b.Block != nil {
				blockMap[b.Block.BlockUid] = b
			}
		}
	}

	for _, withdrawal := range dbWithdrawals {
		slot := withdrawal.BlockUid >> 16

		withdrawalData := &models.WithdrawalsListPageDataWithdrawal{
			SlotNumber: slot,
			Time:       chainState.SlotToTime(phase0.Slot(slot)),
			Orphaned:   withdrawal.Orphaned,
			Type:       withdrawal.Type,
			Amount:     withdrawal.Amount,
			AmountRaw:  withdrawal.AmountRaw,
		}

		if withdrawal.Validator != nil {
			withdrawalData.HasValidator = true
			withdrawalData.ValidatorIndex = *withdrawal.Validator
			withdrawalData.ValidatorName = services.GlobalBeaconService.GetValidatorName(*withdrawal.Validator)
		}

		// Resolve address from account_id
		hasAddress := false
		if withdrawal.AccountID != nil {
			if acct, ok := accountMap[*withdrawal.AccountID]; ok {
				withdrawalData.Address = acct.Address
				hasAddress = true
			}
		}
		if !hasAddress && withdrawal.Address != nil {
			withdrawalData.Address = withdrawal.Address
			hasAddress = true
		}
		if !hasAddress && withdrawal.Validator != nil {
			if validator, ok := validatorMap[*withdrawal.Validator]; ok && (validator.Validator.WithdrawalCredentials[0] == 0x01 || validator.Validator.WithdrawalCredentials[0] == 0x02) {
				withdrawalData.Address = validator.Validator.WithdrawalCredentials[12:]
				hasAddress = true
			}
		}

		// Resolve block info
		if blockInfo, ok := blockMap[withdrawal.BlockUid]; ok && blockInfo.Block != nil {
			withdrawalData.BlockRoot = blockInfo.Block.Root
			if blockInfo.Block.EthBlockNumber != nil {
				withdrawalData.BlockNumber = *blockInfo.Block.EthBlockNumber
			}
		}

		pageData.Withdrawals = append(pageData.Withdrawals, withdrawalData)
	}
	pageData.WithdrawalCount = uint64(len(pageData.Withdrawals))

	if pageData.WithdrawalCount > 0 {
		pageData.FirstIndex = pageData.Withdrawals[0].SlotNumber
		pageData.LastIndex = pageData.Withdrawals[pageData.WithdrawalCount-1].SlotNumber
	}

	pageData.TotalPages = totalRows / pageSize
	if totalRows%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	pageData.UrlParams = make([]models.UrlParam, 0)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams = append(pageData.UrlParams, models.UrlParam{Key: key, Value: values[0]})
		}
	}
	pageData.UrlParams = append(pageData.UrlParams, models.UrlParam{Key: "c", Value: fmt.Sprintf("%v", pageData.PageSize)})

	pageData.FirstPageLink = fmt.Sprintf("/validators/withdrawals/filtered?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/withdrawals/filtered?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/withdrawals/filtered?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/withdrawals/filtered?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData, cacheTimeout
}

func parseAddress(addr string) ([]byte, error) {
	if len(addr) > 2 && addr[:2] == "0x" {
		addr = addr[2:]
	}
	addrBytes, err := hex.DecodeString(addr)
	if err != nil {
		return nil, err
	}
	if len(addrBytes) != 20 {
		return nil, fmt.Errorf("invalid address length: %d", len(addrBytes))
	}
	return addrBytes, nil
}
