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

const (
	defaultElListPageSize = 25
	maxElListPageSize     = 100
)

// parseTransactionsFilterForm parses the transaction list filter query params
// into a form model (for repopulating the inputs) and a canonical query suffix
// (cache key + pager links). from/to stay as hex strings here; they are
// resolved to account IDs in resolveTransactionFilter (which needs DB access).
func parseTransactionsFilterForm(q url.Values) (*models.TransactionsFilter, string) {
	form := &models.TransactionsFilter{}
	var parts []string
	keep := func(key, val string) { parts = append(parts, key+"="+url.QueryEscape(val)) }

	num := func(key string, set *string) {
		if v := q.Get(key); v != "" {
			if _, err := strconv.ParseFloat(v, 64); err == nil {
				*set = v
				keep(key, v)
			}
		}
	}
	num("minslot", &form.MinSlot)
	num("maxslot", &form.MaxSlot)
	num("minamount", &form.MinAmount)
	num("maxamount", &form.MaxAmount)
	num("mingas", &form.MinGas)
	num("maxgas", &form.MaxGas)
	num("mintip", &form.MinTip)
	num("maxtip", &form.MaxTip)

	if v := q.Get("from"); v != "" {
		form.From = v
		keep("from", v)
	}
	if v := q.Get("to"); v != "" {
		form.To = v
		keep("to", v)
	}
	switch q.Get("reverted") {
	case "1":
		form.Reverted = "1"
		keep("reverted", "1")
	case "0":
		form.Reverted = "0"
		keep("reverted", "0")
	}
	for _, v := range q["type"] {
		switch v {
		case "0":
			form.Type0 = true
		case "1":
			form.Type1 = true
		case "2":
			form.Type2 = true
		case "3":
			form.Type3 = true
		case "4":
			form.Type4 = true
		default:
			continue
		}
		keep("type", v)
	}

	form.Active = len(parts) > 0
	return form, strings.Join(parts, "&")
}

// resolveTransactionFilter converts the string form into a DB filter, resolving
// the from/to addresses to their account IDs.
func resolveTransactionFilter(ctx context.Context, form *models.TransactionsFilter) *dbtypes.ElTransactionFilter {
	filter := &dbtypes.ElTransactionFilter{}
	if form == nil {
		return filter
	}
	if n, err := strconv.ParseUint(form.MinSlot, 10, 64); err == nil {
		filter.MinSlot = &n
	}
	if n, err := strconv.ParseUint(form.MaxSlot, 10, 64); err == nil {
		filter.MaxSlot = &n
	}
	if f, err := strconv.ParseFloat(form.MinAmount, 64); err == nil {
		filter.MinAmount = &f
	}
	if f, err := strconv.ParseFloat(form.MaxAmount, 64); err == nil {
		filter.MaxAmount = &f
	}
	if n, err := strconv.ParseUint(form.MinGas, 10, 64); err == nil {
		filter.MinGasUsed = &n
	}
	if n, err := strconv.ParseUint(form.MaxGas, 10, 64); err == nil {
		filter.MaxGasUsed = &n
	}
	if f, err := strconv.ParseFloat(form.MinTip, 64); err == nil {
		filter.MinTip = &f
	}
	if f, err := strconv.ParseFloat(form.MaxTip, 64); err == nil {
		filter.MaxTip = &f
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
	switch form.Reverted {
	case "1":
		t := true
		filter.Reverted = &t
	case "0":
		f := false
		filter.Reverted = &f
	}
	if form.Type0 {
		filter.TxTypes = append(filter.TxTypes, 0)
	}
	if form.Type1 {
		filter.TxTypes = append(filter.TxTypes, 1)
	}
	if form.Type2 {
		filter.TxTypes = append(filter.TxTypes, 2)
	}
	if form.Type3 {
		filter.TxTypes = append(filter.TxTypes, 3)
	}
	if form.Type4 {
		filter.TxTypes = append(filter.TxTypes, 4)
	}
	return filter
}

// Transactions renders the global execution-layer transactions list page.
func Transactions(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"transactions/transactions.html",
		"_shared/pager.html",
		"_shared/el_filter_assets.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "blockchain", "/transactions", "Transactions", templateFiles)

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
	beforeTxUid, _, pageNum := parseElPageParam(urlArgs.Get("p"))
	filterForm, filterSuffix := parseTransactionsFilterForm(urlArgs)
	colMask := utils.DecodeUint64BitfieldFromQuery(r.URL.RawQuery, "d")

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getTransactionsPageData(beforeTxUid, pageNum, pageSize, filterForm, filterSuffix, colMask)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "transactions.go", "Transactions", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getTransactionsPageData(beforeTxUid uint64, pageNum uint64, pageSize uint64, filterForm *models.TransactionsFilter, filterSuffix string, colMask uint64) (*models.TransactionsPageData, error) {
	pageData := &models.TransactionsPageData{}
	pageCacheKey := fmt.Sprintf("transactions:%v:%v:%v:%v:%v", beforeTxUid, pageNum, pageSize, filterSuffix, colMask)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTransactionsPageData(pageCall.CallCtx, beforeTxUid, pageNum, pageSize, filterForm, filterSuffix, colMask)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.TransactionsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildTransactionsPageData(ctx context.Context, beforeTxUid uint64, pageNum uint64, pageSize uint64, filterForm *models.TransactionsFilter, filterSuffix string, colMask uint64) (*models.TransactionsPageData, time.Duration) {
	logrus.Debugf("transactions page called: before=%v page=%v size=%v", beforeTxUid, pageNum, pageSize)

	// Default columns when none specified: Block, Age, Method, Value, Fee.
	const defaultColMask = 0x1f
	if colMask == 0 {
		colMask = defaultColMask
	}

	pageData := &models.TransactionsPageData{
		PageSize:          pageSize,
		Filter:            filterForm,
		ColumnMask:        colMask,
		DisplayBlock:      colMask&0x1 != 0,
		DisplayAge:        colMask&0x2 != 0,
		DisplayMethod:     colMask&0x4 != 0,
		DisplayValue:      colMask&0x8 != 0,
		DisplayFee:        colMask&0x10 != 0,
		DisplayGasUsed:    colMask&0x20 != 0,
		DisplayType:       colMask&0x40 != 0,
		DisplayNonce:      colMask&0x80 != 0,
		DisplayInclStatus: colMask&0x100 != 0,
	}

	filter := resolveTransactionFilter(ctx, filterForm)
	dbTxs, hasNext, _ := db.GetElTransactionsFiltered(ctx, filter, beforeTxUid, uint32(pageSize))
	pageData.Transactions = enrichElTransactionRows(ctx, dbTxs)

	suffix := fmt.Sprintf("c=%v", pageSize)
	if filterSuffix != "" {
		suffix += "&" + filterSuffix
	}
	if colMask != defaultColMask {
		suffix += fmt.Sprintf("&d=0x%x", colMask)
	}

	var nextA uint64
	hasPrev, atFirst, prevA := false, true, uint64(0)
	if len(dbTxs) > 0 {
		nextA = dbTxs[len(dbTxs)-1].TxUid
		prevA, hasPrev, atFirst, _ = db.GetElTransactionsPrevAnchor(ctx, filter, dbTxs[0].TxUid, uint32(pageSize))
	}
	pageData.Pager = buildElPager("/transactions", suffix, pageNum, hasNext, nextA, 0, hasPrev, atFirst, prevA, 0, false)

	// First page changes constantly; deeper (older) pages are effectively final.
	cacheTimeout := 5 * time.Minute
	if beforeTxUid == 0 {
		cacheTimeout = 12 * time.Second
	}
	return pageData, cacheTimeout
}

// enrichElTransactionRows resolves account addresses, block info and method
// names for a set of el_transactions rows and builds the page row models.
func enrichElTransactionRows(ctx context.Context, dbTxs []*dbtypes.ElTransaction) []*models.TransactionsPageDataTransaction {
	chainState := services.GlobalBeaconService.GetChainState()

	// Collect account IDs and block UIDs for batch lookup.
	accountIDs := make(map[uint64]bool, len(dbTxs)*2)
	blockUids := make([]uint64, 0, len(dbTxs))
	blockUidSet := make(map[uint64]bool, len(dbTxs))
	for _, tx := range dbTxs {
		accountIDs[tx.FromID] = true
		accountIDs[tx.ToID] = true
		if !blockUidSet[tx.BlockUid] {
			blockUidSet[tx.BlockUid] = true
			blockUids = append(blockUids, tx.BlockUid)
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

	result := make([]*models.TransactionsPageDataTransaction, 0, len(dbTxs))
	sigLookupBytes := []types.TxSignatureBytes{}
	sigLookupMap := map[types.TxSignatureBytes][]*models.TransactionsPageDataTransaction{}
	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()

	for _, tx := range dbTxs {
		slot := tx.BlockUid >> 16
		txFee := float64(tx.GasUsed) * tx.GasPrice / 1e9

		txData := &models.TransactionsPageDataTransaction{
			TxHash:      tx.TxHash,
			BlockNumber: tx.BlockNumber,
			BlockTime:   chainState.SlotToTime(phase0.Slot(slot)),
			Nonce:       tx.Nonce,
			Amount:      tx.Amount,
			TxFee:       txFee,
			GasUsed:     tx.GasUsed,
			TxType:      tx.TxType,
			Reverted:    tx.Reverted,
		}

		if blockInfo, ok := blockMap[tx.BlockUid]; ok && blockInfo.Block != nil {
			txData.BlockRoot = blockInfo.Block.Root
			txData.BlockOrphaned = blockInfo.Block.Status == dbtypes.Orphaned
		}

		if from, ok := accountMap[tx.FromID]; ok {
			txData.FromAddr = from.Address
			txData.FromIsContract = from.IsContract
		}
		if tx.ToID > 0 {
			if to, ok := accountMap[tx.ToID]; ok {
				txData.ToAddr = to.Address
				txData.ToIsContract = to.IsContract
				txData.HasTo = true
			}
		}
		txData.IsCreate = !txData.HasTo

		if len(tx.MethodID) >= 4 {
			txData.MethodID = tx.MethodID[:4]
			if skip, altName := utils.ShouldSkipSignatureLookup(txData.ToAddr, txData.IsCreate, sysContracts); skip {
				txData.MethodName = altName
			} else {
				var sigBytes types.TxSignatureBytes
				copy(sigBytes[:], tx.MethodID[0:4])
				if sigLookupMap[sigBytes] == nil {
					sigLookupMap[sigBytes] = []*models.TransactionsPageDataTransaction{txData}
					sigLookupBytes = append(sigLookupBytes, sigBytes)
				} else {
					sigLookupMap[sigBytes] = append(sigLookupMap[sigBytes], txData)
				}
			}
		} else {
			txData.MethodName = "transfer"
		}

		result = append(result, txData)
	}

	if len(sigLookupBytes) > 0 {
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures(ctx, sigLookupBytes)
		for sigBytes, sigLookup := range sigLookups {
			for _, txData := range sigLookupMap[sigBytes] {
				if sigLookup.Status == types.TxSigStatusFound {
					txData.MethodName = sigLookup.Name
				} else {
					txData.MethodName = "call?"
				}
			}
		}
	}

	return result
}
