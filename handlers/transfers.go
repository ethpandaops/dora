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

// Transfers renders the global token-transfers list page.
func Transfers(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"transfers/transfers.html",
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
	var beforeTxUid uint64
	var beforeTxIdx uint64
	if urlArgs.Has("before") {
		beforeTxUid, _ = strconv.ParseUint(urlArgs.Get("before"), 10, 64)
	}
	if urlArgs.Has("beforeidx") {
		beforeTxIdx, _ = strconv.ParseUint(urlArgs.Get("beforeidx"), 10, 32)
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getTransfersPageData(beforeTxUid, uint32(beforeTxIdx), pageSize)
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

func getTransfersPageData(beforeTxUid uint64, beforeTxIdx uint32, pageSize uint64) (*models.TransfersPageData, error) {
	pageData := &models.TransfersPageData{}
	pageCacheKey := fmt.Sprintf("transfers:%v:%v:%v", beforeTxUid, beforeTxIdx, pageSize)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTransfersPageData(pageCall.CallCtx, beforeTxUid, beforeTxIdx, pageSize)
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

func buildTransfersPageData(ctx context.Context, beforeTxUid uint64, beforeTxIdx uint32, pageSize uint64) (*models.TransfersPageData, time.Duration) {
	logrus.Debugf("transfers page called: before=%v/%v size=%v", beforeTxUid, beforeTxIdx, pageSize)

	pageData := &models.TransfersPageData{
		PageSize:      pageSize,
		IsDefaultPage: beforeTxUid == 0,
	}

	dbTransfers, hasMore, _ := db.GetElTokenTransfersFiltered(ctx, &dbtypes.ElTokenTransferFilter{}, beforeTxUid, beforeTxIdx, uint32(pageSize))
	pageData.HasMore = hasMore
	if len(dbTransfers) > 0 {
		last := dbTransfers[len(dbTransfers)-1]
		pageData.NextCursorTxUid = last.TxUid
		pageData.NextCursorTxIdx = last.TxIdx
	}

	pageData.Transfers = enrichElTokenTransferRows(ctx, dbTransfers)

	pageData.FirstPageLink = fmt.Sprintf("/transfers?c=%v", pageSize)
	if hasMore {
		pageData.NextPageLink = fmt.Sprintf("/transfers?c=%v&before=%v&beforeidx=%v", pageSize, pageData.NextCursorTxUid, pageData.NextCursorTxIdx)
	}

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

	return result
}
