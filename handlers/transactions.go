package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
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

// Transactions renders the global execution-layer transactions list page.
func Transactions(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"transactions/transactions.html",
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
	var beforeTxUid uint64
	if urlArgs.Has("before") {
		beforeTxUid, _ = strconv.ParseUint(urlArgs.Get("before"), 10, 64)
	}
	reverted := ""
	if urlArgs.Has("reverted") {
		if v := urlArgs.Get("reverted"); v == "0" || v == "1" {
			reverted = v
		}
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getTransactionsPageData(beforeTxUid, pageSize, reverted)
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

func getTransactionsPageData(beforeTxUid uint64, pageSize uint64, reverted string) (*models.TransactionsPageData, error) {
	pageData := &models.TransactionsPageData{}
	pageCacheKey := fmt.Sprintf("transactions:%v:%v:%v", beforeTxUid, pageSize, reverted)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTransactionsPageData(pageCall.CallCtx, beforeTxUid, pageSize, reverted)
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

func buildTransactionsPageData(ctx context.Context, beforeTxUid uint64, pageSize uint64, reverted string) (*models.TransactionsPageData, time.Duration) {
	logrus.Debugf("transactions page called: before=%v size=%v", beforeTxUid, pageSize)

	pageData := &models.TransactionsPageData{
		PageSize:       pageSize,
		IsDefaultPage:  beforeTxUid == 0,
		FilterReverted: reverted,
	}

	filter := &dbtypes.ElTransactionFilter{}
	if reverted == "1" {
		t := true
		filter.Reverted = &t
	} else if reverted == "0" {
		f := false
		filter.Reverted = &f
	}

	dbTxs, hasMore, _ := db.GetElTransactionsFiltered(ctx, filter, beforeTxUid, uint32(pageSize))
	pageData.HasMore = hasMore
	if len(dbTxs) > 0 {
		pageData.NextCursor = dbTxs[len(dbTxs)-1].TxUid
	}

	pageData.Transactions = enrichElTransactionRows(ctx, dbTxs)

	// Build pagination links (preserve filter + page size).
	suffix := fmt.Sprintf("c=%v", pageSize)
	if reverted != "" {
		suffix += "&reverted=" + reverted
	}
	pageData.FirstPageLink = "/transactions?" + suffix
	if hasMore {
		pageData.NextPageLink = fmt.Sprintf("/transactions?%v&before=%v", suffix, pageData.NextCursor)
	}

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
