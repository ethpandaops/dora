package handlers

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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

	var entity string
	var minIndex uint64
	var maxIndex uint64
	var vname string
	var address string
	var minAmount string
	var maxAmount string
	var withType string
	var withOrphaned uint64

	if urlArgs.Has("f") {
		if urlArgs.Has("f.entity") {
			entity = urlArgs.Get("f.entity")
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
		if urlArgs.Has("f.address") {
			address = urlArgs.Get("f.address")
		}
		if urlArgs.Has("f.type") {
			withType = strings.Join(urlArgs["f.type"], ",")
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

	// Apply builder flag to index filters when entity=builder
	if entity == "builder" {
		if minIndex > 0 {
			minIndex |= services.BuilderIndexFlag
		}
		if maxIndex > 0 {
			maxIndex |= services.BuilderIndexFlag
		}
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredWithdrawalsListPageData(pageIdx, pageSize, entity, minIndex, maxIndex, vname, address, withType, minAmount, maxAmount, uint8(withOrphaned))
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

func getFilteredWithdrawalsListPageData(pageIdx uint64, pageSize uint64, entity string, minIndex uint64, maxIndex uint64, vname string, address string, withType string, minAmount string, maxAmount string, withOrphaned uint8) (*models.WithdrawalsListPageData, error) {
	pageData := &models.WithdrawalsListPageData{}
	pageCacheKey := fmt.Sprintf("withdrawals_list:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, entity, minIndex, maxIndex, vname, address, withType, minAmount, maxAmount, withOrphaned)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildFilteredWithdrawalsListPageData(pageCall.CallCtx, pageIdx, pageSize, entity, minIndex, maxIndex, vname, address, withType, minAmount, maxAmount, withOrphaned)
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

func buildFilteredWithdrawalsListPageData(ctx context.Context, pageIdx uint64, pageSize uint64, entity string, minIndex uint64, maxIndex uint64, vname string, address string, withType string, minAmount string, maxAmount string, withOrphaned uint8) (*models.WithdrawalsListPageData, time.Duration) {
	if entity == "" {
		entity = "all"
	}

	filterArgs := url.Values{}
	if entity != "all" {
		filterArgs.Add("f.entity", entity)
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
	if address != "" {
		filterArgs.Add("f.address", address)
	}
	if withType != "" {
		filterArgs.Add("f.type", withType)
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

	// Display indices without the builder flag for the filter UI
	displayMinIndex := minIndex
	displayMaxIndex := maxIndex
	if entity == "builder" {
		displayMinIndex = minIndex &^ services.BuilderIndexFlag
		displayMaxIndex = maxIndex &^ services.BuilderIndexFlag
	}

	pageData := &models.WithdrawalsListPageData{
		FilterEntity:        entity,
		FilterMinIndex:      displayMinIndex,
		FilterMaxIndex:      displayMaxIndex,
		FilterValidatorName: vname,
		FilterAddress:       address,
		FilterWithType:      withType,
		FilterMinAmount:     minAmount,
		FilterMaxAmount:     maxAmount,
		FilterWithOrphaned:  withOrphaned,
	}
	cacheTimeout := 5 * time.Minute
	logrus.Debugf("withdrawals_list page called: %v:%v [%v,%v,%v,%v,%v,%v,%v,%v]", pageIdx, pageSize, entity, minIndex, maxIndex, vname, address, withType, minAmount, maxAmount)
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
		MinIndex:     minIndex,
		MaxIndex:     maxIndex,
		WithOrphaned: withOrphaned,
	}

	// Only apply name filter when a specific entity is selected
	if entity != "all" && vname != "" {
		withdrawalFilter.ValidatorName = vname
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

	// Parse type filter (comma-separated list of type values 1-3)
	if withType != "" {
		for _, t := range strings.Split(withType, ",") {
			if v, err := strconv.ParseUint(strings.TrimSpace(t), 10, 8); err == nil && v >= 1 && v <= 6 {
				withdrawalFilter.Types = append(withdrawalFilter.Types, uint8(v))
			}
		}
	}

	// Parse amount filters (input is in ETH, convert to Gwei)
	if minAmount != "" {
		if v, err := strconv.ParseFloat(minAmount, 64); err == nil {
			gwei := uint64(v * 1e9)
			withdrawalFilter.MinAmount = &gwei
		}
	}
	if maxAmount != "" {
		if v, err := strconv.ParseFloat(maxAmount, 64); err == nil {
			gwei := uint64(v * 1e9)
			withdrawalFilter.MaxAmount = &gwei
		}
	}

	// Fetch withdrawals
	dbWithdrawals, totalRows := services.GlobalBeaconService.GetWithdrawalsByFilter(ctx, withdrawalFilter, pageIdx-1, uint32(pageSize))
	chainState := services.GlobalBeaconService.GetChainState()

	// Batch resolve account IDs to addresses
	accountIDs := make([]uint64, 0, len(dbWithdrawals))
	accountIDSet := make(map[uint64]bool, len(dbWithdrawals))
	for _, w := range dbWithdrawals {
		if w.AccountID > 0 && !accountIDSet[w.AccountID] {
			accountIDSet[w.AccountID] = true
			accountIDs = append(accountIDs, w.AccountID)
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
		}

		withdrawalData.HasValidator = true
		if withdrawal.Validator&services.BuilderIndexFlag != 0 {
			withdrawalData.IsBuilder = true
			withdrawalData.ValidatorIndex = withdrawal.Validator &^ services.BuilderIndexFlag
		} else {
			withdrawalData.ValidatorIndex = withdrawal.Validator
		}
		withdrawalData.ValidatorName = services.GlobalBeaconService.GetValidatorName(withdrawal.Validator)

		// Resolve address from account_id
		if withdrawal.AccountID > 0 {
			if acct, ok := accountMap[withdrawal.AccountID]; ok {
				withdrawalData.Address = acct.Address
			}
		} else if withdrawal.Address != nil {
			withdrawalData.Address = withdrawal.Address
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
