package handlers

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
)

// Transaction type names
var txTypeNames = map[uint8]string{
	0: "Legacy",
	1: "Access List (EIP-2930)",
	2: "Dynamic Fee (EIP-1559)",
	3: "Blob (EIP-4844)",
	4: "Set Code (EIP-7702)",
}

// Transaction handles the /tx/{hash} page
func Transaction(w http.ResponseWriter, r *http.Request) {
	txTemplateFiles := append(layoutTemplateFiles,
		"transaction/transaction.html",
		"transaction/events.html",
		"transaction/transfers.html",
	)
	notfoundTemplateFiles := append(layoutTemplateFiles,
		"transaction/notfound.html",
	)

	vars := mux.Vars(r)
	txHashHex := strings.TrimPrefix(vars["hash"], "0x")

	txHashBytes, err := hex.DecodeString(txHashHex)
	if err != nil || len(txHashBytes) != 32 {
		data := InitPageData(w, r, "blockchain", "/tx", "Transaction not found", notfoundTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "transaction.go", "Transaction", "invalidHash", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	tabView := "overview"
	if r.URL.Query().Has("v") {
		tabView = r.URL.Query().Get("v")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)

	var pageData *models.TransactionPageData
	if pageError == nil {
		pageData, pageError = getTransactionPageData(txHashBytes, tabView)
	}

	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	if pageData.TxNotFound {
		data := InitPageData(w, r, "blockchain", "/tx", "Transaction not found", notfoundTemplateFiles)
		data.Data = pageData
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "transaction.go", "Transaction", "notFound", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	pageTemplate := templates.GetTemplate(txTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/tx", fmt.Sprintf("Transaction 0x%x", pageData.TxHash), txTemplateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		handleTemplateError(w, r, "transaction.go", "Transaction", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "transaction.go", "Transaction", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

// TransactionRLP handles the /tx/{hash}/rlp endpoint for downloading transaction RLP
func TransactionRLP(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	txHashHex := strings.TrimPrefix(vars["hash"], "0x")

	txHashBytes, err := hex.DecodeString(txHashHex)
	if err != nil || len(txHashBytes) != 32 {
		http.Error(w, "Invalid transaction hash", http.StatusBadRequest)
		return
	}

	// Get transaction from DB to find block info
	txs, err := db.GetElTransactionsByHash(txHashBytes)
	if err != nil || len(txs) == 0 {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	// Use the first (canonical) transaction
	tx := txs[0]

	// Get block by BlockUid using the filter
	filter := &dbtypes.BlockFilter{
		BlockUids:    []uint64{tx.BlockUid},
		WithOrphaned: 1, // Include orphaned blocks
	}
	blocks := services.GlobalBeaconService.GetDbBlocksByFilter(filter, 0, 1, 0)
	if len(blocks) == 0 || blocks[0].Block == nil {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	// Get the block root and load the full beacon block
	var blockRoot phase0.Root
	copy(blockRoot[:], blocks[0].Block.Root)

	blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(r.Context(), blockRoot)
	if err != nil || blockData == nil || blockData.Block == nil {
		http.Error(w, "Block body not available", http.StatusNotFound)
		return
	}

	// Find the transaction in the execution payload
	execTxs, err := blockData.Block.ExecutionTransactions()
	if err != nil {
		http.Error(w, "Failed to get execution transactions", http.StatusInternalServerError)
		return
	}

	if int(tx.TxIndex) >= len(execTxs) {
		http.Error(w, "Transaction index out of range", http.StatusNotFound)
		return
	}

	rlpData := execTxs[tx.TxIndex]

	// Set headers for download
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.rlp", txHashHex))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(rlpData)))
	w.Write(rlpData)
}

func getTransactionPageData(txHash []byte, tabView string) (*models.TransactionPageData, error) {
	pageData := &models.TransactionPageData{}
	pageCacheKey := fmt.Sprintf("tx:%x:%v", txHash, tabView)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTransactionPageData(txHash, tabView)
		_ = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.TransactionPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildTransactionPageData(txHash []byte, tabView string) (*models.TransactionPageData, time.Duration) {
	logrus.Debugf("transaction page called: %x (tab: %v)", txHash, tabView)

	chainState := services.GlobalBeaconService.GetChainState()

	pageData := &models.TransactionPageData{
		TxHash:  txHash,
		TabView: tabView,
	}

	// Get transaction from DB
	txs, err := db.GetElTransactionsByHash(txHash)
	if err != nil || len(txs) == 0 {
		pageData.TxNotFound = true
		return pageData, 1 * time.Minute
	}

	// Use the first (canonical) transaction
	tx := txs[0]

	// Check for multiple versions (reorgs)
	if len(txs) > 1 {
		pageData.TxMultiple = true
	}

	// Basic info
	slot := tx.BlockUid >> 16
	blockTime := chainState.SlotToTime(phase0.Slot(slot))

	pageData.Status = !tx.Reverted
	if tx.Reverted {
		pageData.StatusText = "Failed"
	} else {
		pageData.StatusText = "Success"
	}

	pageData.BlockNumber = tx.BlockNumber
	pageData.Slot = slot
	pageData.BlockTime = blockTime

	// Get block info using GetDbBlocksByFilter (includes cached blocks)
	blockFilter := &dbtypes.BlockFilter{
		BlockUids:    []uint64{tx.BlockUid},
		WithOrphaned: 1,
	}
	blocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, 0, 1, 0)
	if len(blocks) > 0 && blocks[0].Block != nil {
		slotInfo := blocks[0].Block
		pageData.BlockHash = slotInfo.EthBlockHash
		pageData.BlockRoot = slotInfo.Root
		pageData.TxOrphaned = slotInfo.Status == dbtypes.Orphaned

		// Check if finalized (slot <= finalized slot and not orphaned)
		finalizedSlot := chainState.GetFinalizedSlot()
		if !pageData.TxOrphaned && phase0.Slot(slotInfo.Slot) <= finalizedSlot {
			pageData.TxFinalized = true
		}
	}

	// Get from/to addresses
	if tx.FromID > 0 {
		if fromAccount, err := db.GetElAccountByID(tx.FromID); err == nil {
			pageData.FromAddr = fromAccount.Address
			pageData.FromIsContract = fromAccount.IsContract
		}
	}
	if tx.ToID > 0 {
		if toAccount, err := db.GetElAccountByID(tx.ToID); err == nil {
			pageData.ToAddr = toAccount.Address
			pageData.ToIsContract = toAccount.IsContract
			pageData.HasTo = true
		}
	} else {
		pageData.IsCreate = true
	}

	// Value and fees
	pageData.Amount = tx.Amount
	pageData.AmountRaw = tx.AmountRaw
	pageData.GasPrice = tx.GasPrice
	pageData.TipPrice = tx.TipPrice

	// Calculate transaction fee: gasUsed * effectiveGasPrice (in ETH)
	// GasPrice is in Gwei, convert to ETH
	txFee := float64(tx.GasUsed) * tx.GasPrice / 1e9
	pageData.TxFee = txFee
	// Calculate raw fee in wei
	gasPriceWei := new(big.Int).Mul(big.NewInt(int64(tx.GasPrice*1e9)), big.NewInt(1))
	txFeeWei := new(big.Int).Mul(gasPriceWei, big.NewInt(int64(tx.GasUsed)))
	pageData.TxFeeRaw = txFeeWei.Bytes()

	// Gas info
	pageData.GasLimit = tx.GasLimit
	pageData.GasUsed = tx.GasUsed
	if tx.GasLimit > 0 {
		pageData.GasUsedPct = float64(tx.GasUsed) / float64(tx.GasLimit) * 100
	}

	// EIP-1559 fields
	pageData.MaxFee = tx.MaxFee

	// Transaction details
	pageData.TxType = tx.TxType
	if name, ok := txTypeNames[tx.TxType]; ok {
		pageData.TxTypeName = name
	} else {
		pageData.TxTypeName = fmt.Sprintf("Type %d", tx.TxType)
	}
	pageData.Nonce = tx.Nonce
	pageData.TxIndex = tx.TxIndex

	// Input data
	pageData.InputData = tx.Data
	if len(tx.Data) >= 4 {
		pageData.MethodID = tx.Data[:4]

		// Lookup function signature
		var sigBytes types.TxSignatureBytes
		copy(sigBytes[:], tx.Data[0:4])
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures([]types.TxSignatureBytes{sigBytes})
		if sigLookup, found := sigLookups[sigBytes]; found {
			if sigLookup.Status == types.TxSigStatusFound {
				pageData.MethodName = sigLookup.Name
			} else {
				pageData.MethodName = "call?"
			}
		}
	} else {
		// No data or insufficient data for method signature
		pageData.MethodName = "transfer"
	}

	// Blobs
	pageData.BlobCount = tx.BlobCount

	// Always load counts for tab badges (needed for all views)
	events, _ := db.GetElTxEventsByBlockUidAndTxHash(tx.BlockUid, txHash)
	pageData.EventCount = uint64(len(events))

	transfers, _ := db.GetElTokenTransfersByBlockUidAndTxHash(tx.BlockUid, txHash)
	pageData.TokenTransferCount = uint64(len(transfers))

	// Load tab-specific detailed data
	switch tabView {
	case "events":
		loadTransactionEventsFromData(pageData, events)
	case "transfers":
		loadTransactionTransfersFromData(pageData, transfers)
	}

	return pageData, 2 * time.Minute
}

func loadTransactionEventsFromData(pageData *models.TransactionPageData, events []*dbtypes.ElTxEvent) {
	if len(events) == 0 {
		return
	}

	// Collect account IDs for batch lookup
	accountIDs := make(map[uint64]bool)
	for _, e := range events {
		accountIDs[e.SourceID] = true
	}

	// Batch lookup accounts
	accountIDList := make([]uint64, 0, len(accountIDs))
	for id := range accountIDs {
		accountIDList = append(accountIDList, id)
	}
	accountMap := make(map[uint64]*dbtypes.ElAccount)
	if len(accountIDList) > 0 {
		if accounts, err := db.GetElAccountsByIDs(accountIDList); err == nil {
			for _, a := range accounts {
				accountMap[a.ID] = a
			}
		}
	}

	// Build events list
	pageData.Events = make([]*models.TransactionPageDataEvent, 0, len(events))
	for _, e := range events {
		event := &models.TransactionPageDataEvent{
			EventIndex: e.EventIndex,
			Data:       e.Data,
		}

		// Source address
		if source, ok := accountMap[e.SourceID]; ok {
			event.SourceAddr = source.Address
			event.SourceIsContract = source.IsContract
		}

		// Topics
		if len(e.Topic1) > 0 {
			event.Topic0 = e.Topic1
		}
		if len(e.Topic2) > 0 {
			event.Topic1 = e.Topic2
		}
		if len(e.Topic3) > 0 {
			event.Topic2 = e.Topic3
		}
		if len(e.Topic4) > 0 {
			event.Topic3 = e.Topic4
		}
		if len(e.Topic5) > 0 {
			event.Topic4 = e.Topic5
		}

		pageData.Events = append(pageData.Events, event)
	}
}

func loadTransactionTransfersFromData(pageData *models.TransactionPageData, transfers []*dbtypes.ElTokenTransfer) {
	if len(transfers) == 0 {
		return
	}

	// Collect IDs for batch lookup
	accountIDs := make(map[uint64]bool)
	tokenIDs := make(map[uint64]bool)
	for _, t := range transfers {
		accountIDs[t.FromID] = true
		accountIDs[t.ToID] = true
		tokenIDs[t.TokenID] = true
	}

	// Batch lookup accounts
	accountIDList := make([]uint64, 0, len(accountIDs))
	for id := range accountIDs {
		accountIDList = append(accountIDList, id)
	}
	accountMap := make(map[uint64]*dbtypes.ElAccount)
	if len(accountIDList) > 0 {
		if accounts, err := db.GetElAccountsByIDs(accountIDList); err == nil {
			for _, a := range accounts {
				accountMap[a.ID] = a
			}
		}
	}

	// Batch lookup tokens
	tokenIDList := make([]uint64, 0, len(tokenIDs))
	for id := range tokenIDs {
		tokenIDList = append(tokenIDList, id)
	}
	tokenMap := make(map[uint64]*dbtypes.ElToken)
	if len(tokenIDList) > 0 {
		if tokens, err := db.GetElTokensByIDs(tokenIDList); err == nil {
			for _, t := range tokens {
				tokenMap[t.ID] = t
			}
		}
	}

	// Build transfers list
	pageData.TokenTransfers = make([]*models.TransactionPageDataTokenTransfer, 0, len(transfers))
	for i, t := range transfers {
		transfer := &models.TransactionPageDataTokenTransfer{
			TransferIndex: uint32(i),
			TokenID:       t.TokenID,
			TokenType:     t.TokenType,
			Amount:        t.Amount,
			AmountRaw:     t.AmountRaw,
			TokenIndex:    t.TokenIndex,
		}

		// From/To addresses
		if from, ok := accountMap[t.FromID]; ok {
			transfer.FromAddr = from.Address
			transfer.FromIsContract = from.IsContract
		}
		if to, ok := accountMap[t.ToID]; ok {
			transfer.ToAddr = to.Address
			transfer.ToIsContract = to.IsContract
		}

		// Token info
		if token, ok := tokenMap[t.TokenID]; ok {
			transfer.Contract = token.Contract
			transfer.TokenName = token.Name
			transfer.TokenSymbol = token.Symbol
			transfer.Decimals = token.Decimals
		}

		pageData.TokenTransfers = append(pageData.TokenTransfers, transfer)
	}
}
