package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

// parseTransfersFilterForm parses the transfer filter query params into a form
// model (for repopulating inputs) and a canonical query suffix (cache key +
// pager links). Token/from/to remain hex strings here; they are resolved to IDs
// in resolveTransferFilter (which needs DB access).
func parseTransfersFilterForm(q url.Values) (*models.TransfersFilter, string) {
	form := &models.TransfersFilter{}
	var parts []string
	keep := func(k, v string) { parts = append(parts, k+"="+url.QueryEscape(v)) }

	if v := q.Get("minslot"); v != "" {
		if _, err := strconv.ParseUint(v, 10, 64); err == nil {
			form.MinSlot = v
			keep("minslot", v)
		}
	}
	if v := q.Get("maxslot"); v != "" {
		if _, err := strconv.ParseUint(v, 10, 64); err == nil {
			form.MaxSlot = v
			keep("maxslot", v)
		}
	}
	if v := q.Get("minamount"); v != "" {
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			form.MinAmount = v
			keep("minamount", v)
		}
	}
	if v := q.Get("maxamount"); v != "" {
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			form.MaxAmount = v
			keep("maxamount", v)
		}
	}
	if v := q.Get("token"); v != "" {
		form.Token = v
		keep("token", v)
	}
	if v := q.Get("from"); v != "" {
		form.From = v
		keep("from", v)
	}
	if v := q.Get("to"); v != "" {
		form.To = v
		keep("to", v)
	}
	// Token type is a bitmask (bit N = type N+1), matching the multiselect encoding.
	if tv := q.Get("ttype"); tv != "" {
		if tb, err := strconv.ParseUint(strings.TrimPrefix(tv, "0x"), 16, 64); err == nil && tb != 0 {
			form.TypeERC20 = tb&0x1 != 0
			form.TypeERC721 = tb&0x2 != 0
			form.TypeERC1155 = tb&0x4 != 0
			keep("ttype", tv)
		}
	}

	form.Active = len(parts) > 0
	return form, strings.Join(parts, "&")
}

// resolveTransferFilter converts the string form into a DB filter, resolving
// the token contract and from/to addresses to their internal IDs.
func resolveTransferFilter(ctx context.Context, form *models.TransfersFilter) *dbtypes.ElTokenTransferFilter {
	filter := &dbtypes.ElTokenTransferFilter{}
	if form == nil {
		return filter
	}
	if form.MinSlot != "" {
		if n, err := strconv.ParseUint(form.MinSlot, 10, 64); err == nil {
			filter.MinSlot = &n
		}
	}
	if form.MaxSlot != "" {
		if n, err := strconv.ParseUint(form.MaxSlot, 10, 64); err == nil {
			filter.MaxSlot = &n
		}
	}
	if form.MinAmount != "" {
		if f, err := strconv.ParseFloat(form.MinAmount, 64); err == nil {
			filter.MinAmount = &f
		}
	}
	if form.MaxAmount != "" {
		if f, err := strconv.ParseFloat(form.MaxAmount, 64); err == nil {
			filter.MaxAmount = &f
		}
	}
	if form.Token != "" {
		if b, err := hex.DecodeString(strings.TrimPrefix(form.Token, "0x")); err == nil && len(b) == 20 {
			if tok, err := db.GetElTokenByContract(ctx, b); err == nil && tok != nil {
				id := tok.ID
				filter.TokenID = &id
			}
		}
	}
	if form.From != "" {
		if b, err := hex.DecodeString(strings.TrimPrefix(form.From, "0x")); err == nil && len(b) == 20 {
			if acc, err := db.GetElAccountByAddress(ctx, b); err == nil && acc != nil {
				filter.FromID = acc.ID
			}
		}
	}
	if form.To != "" {
		if b, err := hex.DecodeString(strings.TrimPrefix(form.To, "0x")); err == nil && len(b) == 20 {
			if acc, err := db.GetElAccountByAddress(ctx, b); err == nil && acc != nil {
				filter.ToID = acc.ID
			}
		}
	}
	if form.TypeERC20 {
		filter.TokenTypes = append(filter.TokenTypes, 1)
	}
	if form.TypeERC721 {
		filter.TokenTypes = append(filter.TokenTypes, 2)
	}
	if form.TypeERC1155 {
		filter.TokenTypes = append(filter.TokenTypes, 3)
	}
	return filter
}

// Transfers renders the global token-transfers list page.
func Transfers(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"transfers/transfers.html",
		"_shared/pager.html",
		"_shared/el_filter_assets.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "blockchain", "/transfers", "Token Transfers", templateFiles)

	urlArgs := r.URL.Query()
	pageSize := uint64(defaultElListPageSize)
	if urlArgs.Has("c") {
		if c, err := strconv.ParseUint(urlArgs.Get("c"), 10, 64); err == nil && c > 0 {
			pageSize = c
			if pageSize > maxElListPageSize {
				pageSize = maxElListPageSize
			}
		}
	}
	beforeTxUid, beforeTxIdx, pageNum := parseElPageParam(urlArgs.Get("p"))
	filterForm, filterSuffix := parseTransfersFilterForm(urlArgs)
	colMask := utils.DecodeUint64BitfieldFromQuery(r.URL.RawQuery, "d")

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getTransfersPageData(beforeTxUid, beforeTxIdx, pageNum, pageSize, filterForm, filterSuffix, colMask)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "transfers.go", "Transfers", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getTransfersPageData(beforeTxUid uint64, beforeTxIdx uint32, pageNum uint64, pageSize uint64, filterForm *models.TransfersFilter, filterSuffix string, colMask uint64) (*models.TransfersPageData, error) {
	pageData := &models.TransfersPageData{}
	pageCacheKey := fmt.Sprintf("transfers:%v:%v:%v:%v:%v:%v", beforeTxUid, beforeTxIdx, pageNum, pageSize, filterSuffix, colMask)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTransfersPageData(pageCall.CallCtx, beforeTxUid, beforeTxIdx, pageNum, pageSize, filterForm, filterSuffix, colMask)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.TransfersPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildTransfersPageData(ctx context.Context, beforeTxUid uint64, beforeTxIdx uint32, pageNum uint64, pageSize uint64, filterForm *models.TransfersFilter, filterSuffix string, colMask uint64) (*models.TransfersPageData, time.Duration) {
	logrus.Debugf("transfers page called: before=%v/%v page=%v size=%v", beforeTxUid, beforeTxIdx, pageNum, pageSize)

	// Default columns when none specified: Block, Age, Method, Amount, Token, Status.
	const defaultColMask = 0x3f
	if colMask == 0 {
		colMask = defaultColMask
	}

	pageData := &models.TransfersPageData{
		PageSize:         pageSize,
		Filter:           filterForm,
		ColumnMask:       colMask,
		DisplayBlock:     colMask&0x1 != 0,
		DisplayAge:       colMask&0x2 != 0,
		DisplayMethod:    colMask&0x4 != 0,
		DisplayAmount:    colMask&0x8 != 0,
		DisplayToken:     colMask&0x10 != 0,
		DisplayStatus:    colMask&0x20 != 0,
		DisplayTokenType: colMask&0x40 != 0,
	}

	filter := resolveTransferFilter(ctx, filterForm)
	dbTransfers, hasNext, _ := db.GetElTokenTransfersFiltered(ctx, filter, beforeTxUid, beforeTxIdx, uint32(pageSize))
	pageData.Transfers = enrichElTokenTransferRows(ctx, dbTransfers)

	suffix := fmt.Sprintf("c=%v", pageSize)
	if filterSuffix != "" {
		suffix += "&" + filterSuffix
	}
	if colMask != defaultColMask {
		suffix += fmt.Sprintf("&d=0x%x", colMask)
	}
	var nextA uint64
	var nextB uint32
	hasPrev, atFirst := false, true
	var prevA uint64
	var prevB uint32
	if len(dbTransfers) > 0 {
		last := dbTransfers[len(dbTransfers)-1]
		nextA, nextB = last.TxUid, last.TxIdx
		first := dbTransfers[0]
		prevA, prevB, hasPrev, atFirst, _ = db.GetElTokenTransfersPrevAnchor(ctx, filter, first.TxUid, first.TxIdx, uint32(pageSize))
	}
	pageData.Pager = buildElPager("/transfers", suffix, pageNum, hasNext, nextA, nextB, hasPrev, atFirst, prevA, prevB, true)

	cacheTimeout := 5 * time.Minute
	if beforeTxUid == 0 {
		cacheTimeout = 12 * time.Second
	}
	return pageData, cacheTimeout
}

// enrichElTokenTransferRows resolves token metadata, account addresses, block
// info and method names for a set of el_token_transfers rows and builds the
// shared transfer row models. Used by both the global transfers list and the
// token detail page.
func enrichElTokenTransferRows(ctx context.Context, dbTransfers []*dbtypes.ElTokenTransfer) []*models.TransferRow {
	chainState := services.GlobalBeaconService.GetChainState()

	accountIDs := make(map[uint64]bool, len(dbTransfers)*2)
	tokenIDs := make(map[uint64]bool, len(dbTransfers))
	blockUids := make([]uint64, 0, len(dbTransfers))
	blockUidSet := make(map[uint64]bool, len(dbTransfers))
	txUids := make([]uint64, 0, len(dbTransfers))
	txUidSet := make(map[uint64]bool, len(dbTransfers))
	for _, t := range dbTransfers {
		accountIDs[t.FromID] = true
		accountIDs[t.ToID] = true
		tokenIDs[t.TokenID] = true
		blockUid := t.TxUid >> 16
		if !blockUidSet[blockUid] {
			blockUidSet[blockUid] = true
			blockUids = append(blockUids, blockUid)
		}
		if !txUidSet[t.TxUid] {
			txUidSet[t.TxUid] = true
			txUids = append(txUids, t.TxUid)
		}
	}

	accountIDList := make([]uint64, 0, len(accountIDs))
	for id := range accountIDs {
		accountIDList = append(accountIDList, id)
	}
	accountMap := make(map[uint64]*dbtypes.ElAccount, len(accountIDList))
	if len(accountIDList) > 0 {
		if accounts, err := db.GetElAccountsByIDs(ctx, accountIDList); err == nil {
			for _, a := range accounts {
				accountMap[a.ID] = a
			}
		}
	}

	tokenIDList := make([]uint64, 0, len(tokenIDs))
	for id := range tokenIDs {
		tokenIDList = append(tokenIDList, id)
	}
	tokenMap := make(map[uint64]*dbtypes.ElToken, len(tokenIDList))
	if len(tokenIDList) > 0 {
		if tokens, err := db.GetElTokensByIDs(ctx, tokenIDList); err == nil {
			for _, t := range tokens {
				tokenMap[t.ID] = t
			}
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

	// Fetch transaction data by tx_uid for tx_hash and method signature lookup.
	type txInfo struct {
		TxHash   []byte
		MethodID []byte
		ToID     uint64
	}
	txInfoMap := make(map[uint64]*txInfo, len(txUids))
	if len(txUids) > 0 {
		if txs, err := db.GetElTransactionsByTxUids(ctx, txUids); err == nil {
			for _, tx := range txs {
				txInfoMap[tx.TxUid] = &txInfo{TxHash: tx.TxHash, MethodID: tx.MethodID, ToID: tx.ToID}
			}
		}
	}

	// Supplementary account lookup for tx ToIDs not already resolved.
	var extraAccountIDs []uint64
	for _, info := range txInfoMap {
		if info.ToID > 0 && accountMap[info.ToID] == nil {
			extraAccountIDs = append(extraAccountIDs, info.ToID)
		}
	}
	if len(extraAccountIDs) > 0 {
		if accounts, err := db.GetElAccountsByIDs(ctx, extraAccountIDs); err == nil {
			for _, a := range accounts {
				accountMap[a.ID] = a
			}
		}
	}

	// Resolve method names per tx_uid.
	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()
	sigLookupBytes := []types.TxSignatureBytes{}
	sigLookupMap := make(map[types.TxSignatureBytes][]uint64)
	methodNameMap := make(map[uint64]string)
	for txUid, info := range txInfoMap {
		if len(info.MethodID) < 4 {
			methodNameMap[txUid] = "transfer"
			continue
		}
		var toAddr []byte
		if acc, ok := accountMap[info.ToID]; ok {
			toAddr = acc.Address
		}
		if skip, altName := utils.ShouldSkipSignatureLookup(toAddr, info.ToID == 0, sysContracts); skip {
			methodNameMap[txUid] = altName
			continue
		}
		var sigBytes types.TxSignatureBytes
		copy(sigBytes[:], info.MethodID[0:4])
		if sigLookupMap[sigBytes] == nil {
			sigLookupMap[sigBytes] = []uint64{txUid}
			sigLookupBytes = append(sigLookupBytes, sigBytes)
		} else {
			sigLookupMap[sigBytes] = append(sigLookupMap[sigBytes], txUid)
		}
	}
	if len(sigLookupBytes) > 0 {
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures(ctx, sigLookupBytes)
		for sigBytes, sigLookup := range sigLookups {
			methodName := "call?"
			if sigLookup.Status == types.TxSigStatusFound {
				methodName = sigLookup.Name
			}
			for _, txUid := range sigLookupMap[sigBytes] {
				methodNameMap[txUid] = methodName
			}
		}
	}

	result := make([]*models.TransferRow, 0, len(dbTransfers))
	for _, t := range dbTransfers {
		blockUid := t.TxUid >> 16
		slot := t.TxUid >> 32

		row := &models.TransferRow{
			BlockTime:  chainState.SlotToTime(phase0.Slot(slot)),
			TokenType:  t.TokenType,
			TokenIndex: t.TokenIndex,
			Amount:     t.Amount,
			AmountRaw:  t.AmountRaw,
			MethodName: methodNameMap[t.TxUid],
		}
		if info, ok := txInfoMap[t.TxUid]; ok {
			row.TxHash = info.TxHash
			if len(info.MethodID) >= 4 {
				row.MethodID = info.MethodID[:4]
			}
		}
		if blockInfo, ok := blockMap[blockUid]; ok && blockInfo.Block != nil {
			row.BlockRoot = blockInfo.Block.Root
			row.BlockOrphaned = blockInfo.Block.Status == dbtypes.Orphaned
			if blockInfo.Block.EthBlockNumber != nil {
				row.BlockNumber = *blockInfo.Block.EthBlockNumber
			}
		}
		if from, ok := accountMap[t.FromID]; ok {
			row.FromAddr = from.Address
			row.FromIsContract = from.IsContract
		}
		if to, ok := accountMap[t.ToID]; ok {
			row.ToAddr = to.Address
			row.ToIsContract = to.IsContract
		}
		if token, ok := tokenMap[t.TokenID]; ok {
			row.Contract = token.Contract
			row.TokenName = token.Name
			row.TokenSymbol = token.Symbol
		}
		result = append(result, row)
	}

	calculateTransferRowspans(result)
	return result
}

// calculateTransferRowspans sets TxHashRowspan on the first row of each run of
// consecutive transfers sharing a tx hash (rows are already grouped by tx_uid),
// and 0 on the rest, so the tx/block/age cells can be merged with rowspan.
func calculateTransferRowspans(transfers []*models.TransferRow) {
	i := len(transfers) - 1
	for i >= 0 {
		currentTxHash := transfers[i].TxHash
		groupStart := i
		for groupStart > 0 && bytes.Equal(transfers[groupStart-1].TxHash, currentTxHash) {
			groupStart--
		}
		transfers[groupStart].TxHashRowspan = i - groupStart + 1
		for j := groupStart + 1; j <= i; j++ {
			transfers[j].TxHashRowspan = 0
		}
		i = groupStart - 1
	}
}
