package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
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
		"transaction/blobs.html",
	)
	notfoundTemplateFiles := append(layoutTemplateFiles,
		"transaction/notfound.html",
	)

	// Check if execution indexer is enabled
	if !utils.Config.ExecutionIndexer.Enabled {
		data := InitPageData(w, r, "blockchain", "/tx", "Feature Disabled", notfoundTemplateFiles)
		data.Data = "disabled"
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "transaction.go", "Transaction", "disabled", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

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

	// Parse selected block UID (0 = auto-select canonical)
	var selectedBlockUid uint64
	if r.URL.Query().Has("b") {
		if uid, err := strconv.ParseUint(r.URL.Query().Get("b"), 10, 64); err == nil {
			selectedBlockUid = uid
		}
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)

	var pageData *models.TransactionPageData
	if pageError == nil {
		pageData, pageError = getTransactionPageData(txHashBytes, tabView, selectedBlockUid)
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

func getTransactionPageData(txHash []byte, tabView string, selectedBlockUid uint64) (*models.TransactionPageData, error) {
	pageData := &models.TransactionPageData{}
	pageCacheKey := fmt.Sprintf("tx:%x:%v:%v", txHash, tabView, selectedBlockUid)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTransactionPageData(txHash, tabView, selectedBlockUid)
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

func buildTransactionPageData(txHash []byte, tabView string, selectedBlockUid uint64) (*models.TransactionPageData, time.Duration) {
	logrus.Debugf("transaction page called: %x (tab: %v, block: %v)", txHash, tabView, selectedBlockUid)

	chainState := services.GlobalBeaconService.GetChainState()

	pageData := &models.TransactionPageData{
		TxHash:   txHash,
		TabView:  tabView,
		ViewMode: models.TxViewModeNone,
	}

	// Try to get transaction from DB first
	txs, err := db.GetElTransactionsByHash(txHash)
	if err == nil && len(txs) > 0 {
		// Found in DB - build page data from DB entries
		buildTransactionPageDataFromDB(pageData, txs, tabView, chainState, selectedBlockUid)
		blockSlot := txs[0].BlockUid >> 16
		blockTime := chainState.SlotToTime(phase0.Slot(blockSlot))
		cacheTimeout := 1 * time.Minute
		if time.Since(blockTime) > 5*time.Minute {
			cacheTimeout = 15 * time.Minute
		}
		return pageData, cacheTimeout
	}

	// Not in DB - try to fetch from EL client
	if buildTransactionPageDataFromEL(pageData, txHash, chainState) {
		return pageData, 30 * time.Minute
	}

	// Transaction not found anywhere
	pageData.TxNotFound = true
	pageData.ViewMode = models.TxViewModeNone
	return pageData, 1 * time.Minute
}

// buildTransactionPageDataFromDB builds page data from database entries.
// selectedBlockUid: 0 = auto-select canonical, otherwise use the specified block.
func buildTransactionPageDataFromDB(pageData *models.TransactionPageData, txs []*dbtypes.ElTransaction, tabView string, chainState *consensus.ChainState, selectedBlockUid uint64) {
	pageData.ViewMode = models.TxViewModeFull
	pageData.HasReceipt = true

	// Check for multiple versions (reorgs)
	if len(txs) > 1 {
		pageData.TxMultiple = true
	}

	// Collect all block UIDs for loading inclusion blocks
	blockUids := make([]uint64, 0, len(txs))
	for _, tx := range txs {
		blockUids = append(blockUids, tx.BlockUid)
	}

	// Load all inclusion blocks
	blockFilter := &dbtypes.BlockFilter{
		BlockUids:    blockUids,
		WithOrphaned: 1,
	}
	allBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, 0, uint32(len(blockUids)), 0)

	// Build block map for quick lookup by slot
	blockMap := make(map[uint64][]*dbtypes.Slot)
	for _, b := range allBlocks {
		if b.Block != nil {
			blockMap[b.Block.Slot] = append(blockMap[b.Block.Slot], b.Block)
		}
	}

	// Build inclusion blocks list and find canonical/selected block
	var canonicalTx *dbtypes.ElTransaction
	var canonicalBlock *dbtypes.Slot
	var selectedTx *dbtypes.ElTransaction
	var selectedBlock *dbtypes.Slot
	pageData.InclusionBlocks = make([]*models.TransactionPageDataBlock, 0, len(txs))

	finalizedSlot := chainState.GetFinalizedSlot()

	for _, tx := range txs {
		slot := tx.BlockUid >> 16
		blocks := blockMap[slot]
		if len(blocks) == 0 {
			continue
		}

		// Find the matching block by block index within slot
		blockIdx := tx.BlockUid & 0xFFFF
		var block *dbtypes.Slot
		if int(blockIdx) < len(blocks) {
			block = blocks[blockIdx]
		} else if len(blocks) > 0 {
			block = blocks[0]
		}
		if block == nil {
			continue
		}

		blockTime := chainState.SlotToTime(phase0.Slot(slot))
		isOrphaned := block.Status == dbtypes.Orphaned
		isCanonical := !isOrphaned

		inclusionBlock := &models.TransactionPageDataBlock{
			BlockUid:    tx.BlockUid,
			BlockNumber: tx.BlockNumber,
			BlockHash:   block.EthBlockHash,
			BlockRoot:   block.Root,
			Slot:        slot,
			BlockTime:   blockTime,
			IsOrphaned:  isOrphaned,
			IsCanonical: isCanonical,
			TxIndex:     tx.TxIndex,
		}
		pageData.InclusionBlocks = append(pageData.InclusionBlocks, inclusionBlock)

		// Track canonical block (first non-orphaned)
		if isCanonical && canonicalTx == nil {
			canonicalTx = tx
			canonicalBlock = block
		}

		// Track selected block if specified
		if selectedBlockUid != 0 && tx.BlockUid == selectedBlockUid {
			selectedTx = tx
			selectedBlock = block
			inclusionBlock.IsSelected = true
		}
	}

	// Determine which tx/block to use for display
	var tx *dbtypes.ElTransaction
	var displayBlock *dbtypes.Slot

	if selectedBlockUid != 0 && selectedTx != nil {
		// User selected a specific block
		tx = selectedTx
		displayBlock = selectedBlock
		pageData.SelectedBlockUid = selectedBlockUid
	} else {
		// Auto-select canonical, or fall back to first tx if all orphaned
		tx = canonicalTx
		displayBlock = canonicalBlock
		if tx == nil {
			tx = txs[0]
			slot := tx.BlockUid >> 16
			if blocks := blockMap[slot]; len(blocks) > 0 {
				displayBlock = blocks[0]
			}
		}
		// Mark canonical/first as selected if no explicit selection
		for _, ib := range pageData.InclusionBlocks {
			if ib.BlockUid == tx.BlockUid {
				ib.IsSelected = true
				break
			}
		}
		// SelectedBlockUid stays 0 for canonical selection
	}

	// Basic info from selected transaction
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

	if displayBlock != nil {
		pageData.BlockHash = displayBlock.EthBlockHash
		pageData.BlockRoot = displayBlock.Root
		pageData.TxOrphaned = displayBlock.Status == dbtypes.Orphaned

		// Check if finalized
		if !pageData.TxOrphaned && phase0.Slot(displayBlock.Slot) <= finalizedSlot {
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
	pageData.EffGasPrice = tx.EffGasPrice

	// Calculate fee savings for EIP-1559+ transactions
	if tx.TxType >= 2 && tx.GasPrice > 0 && tx.EffGasPrice > 0 && tx.GasPrice > tx.EffGasPrice {
		pageData.FeeSavingsPct = (tx.GasPrice - tx.EffGasPrice) / tx.GasPrice * 100
	}

	// Calculate transaction fee using effective gas price
	effectivePrice := tx.EffGasPrice
	if effectivePrice == 0 {
		effectivePrice = tx.GasPrice // Fallback for legacy or missing data
	}
	txFee := float64(tx.GasUsed) * effectivePrice / 1e9
	pageData.TxFee = txFee
	gasPriceWei := new(big.Int).Mul(big.NewInt(int64(effectivePrice*1e9)), big.NewInt(1))
	txFeeWei := new(big.Int).Mul(gasPriceWei, big.NewInt(int64(tx.GasUsed)))
	pageData.TxFeeRaw = txFeeWei.Bytes()

	// Gas info
	pageData.GasLimit = tx.GasLimit
	pageData.GasUsed = tx.GasUsed
	if tx.GasLimit > 0 {
		pageData.GasUsedPct = float64(tx.GasUsed) / float64(tx.GasLimit) * 100
	}

	// Transaction details
	pageData.TxType = tx.TxType
	if name, ok := txTypeNames[tx.TxType]; ok {
		pageData.TxTypeName = name
	} else {
		pageData.TxTypeName = fmt.Sprintf("Type %d", tx.TxType)
	}
	pageData.Nonce = tx.Nonce
	pageData.TxIndex = tx.TxIndex

	// Method ID from stored data
	if len(tx.MethodID) >= 4 {
		pageData.MethodID = tx.MethodID[:4]
		var sigBytes types.TxSignatureBytes
		copy(sigBytes[:], tx.MethodID[0:4])
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures([]types.TxSignatureBytes{sigBytes})
		if sigLookup, found := sigLookups[sigBytes]; found {
			if sigLookup.Status == types.TxSigStatusFound {
				pageData.MethodName = sigLookup.Name
			} else {
				pageData.MethodName = "call?"
			}
		}
	} else {
		pageData.MethodName = "transfer"
	}

	// Load full transaction data from selected beacon block
	if displayBlock != nil {
		blockFilter := &dbtypes.BlockFilter{
			BlockUids:    []uint64{tx.BlockUid},
			WithOrphaned: 1,
		}
		loadFullTransactionData(pageData, tx, blockFilter)
	}

	// Blobs
	pageData.BlobCount = tx.BlobCount

	// Load event count from event index
	eventIndices, _ := db.GetElEventIndicesByTxHash(pageData.TxHash)
	pageData.EventCount = uint64(len(eventIndices))

	transfers, _ := db.GetElTokenTransfersByBlockUidAndTxHash(tx.BlockUid, pageData.TxHash)
	pageData.TokenTransferCount = uint64(len(transfers))

	// Load tab-specific detailed data
	switch tabView {
	case "events":
		// Full event data (topics, data) will be loaded from blockdb in Phase 5.
		// For now, show event index entries without full data.
		loadTransactionEventsFromIndex(pageData, eventIndices)
	case "transfers":
		loadTransactionTransfersFromData(pageData, transfers)
	}
}

// buildTransactionPageDataFromEL builds page data by fetching from EL client.
// Returns true if transaction was found, false otherwise.
func buildTransactionPageDataFromEL(pageData *models.TransactionPageData, txHash []byte, chainState *consensus.ChainState) bool {
	txIndexer := services.GlobalBeaconService.GetTxIndexer()
	if txIndexer == nil {
		return false
	}

	clients := txIndexer.GetReadyClients()
	if len(clients) == 0 {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to fetch transaction from EL client
	var ethTx *ethtypes.Transaction
	var isPending bool
	var err error

	txHashCommon := common.BytesToHash(txHash)

	for _, client := range clients {
		rpcClient := client.GetRPCClient()
		if rpcClient == nil {
			continue
		}

		ethClient := rpcClient.GetEthClient()
		if ethClient == nil {
			continue
		}

		ethTx, isPending, err = ethClient.TransactionByHash(ctx, txHashCommon)
		if err == nil && ethTx != nil {
			break
		}
	}

	if ethTx == nil {
		return false
	}

	// Transaction found - populate basic fields
	pageData.ViewMode = models.TxViewModePartial // Start with partial, upgrade if receipt found

	// Basic transaction info from ethTx
	pageData.TxType = ethTx.Type()
	if name, ok := txTypeNames[ethTx.Type()]; ok {
		pageData.TxTypeName = name
	} else {
		pageData.TxTypeName = fmt.Sprintf("Type %d", ethTx.Type())
	}

	pageData.Nonce = ethTx.Nonce()
	pageData.GasLimit = ethTx.Gas()

	// Value
	if ethTx.Value() != nil {
		valueFloat, _ := new(big.Float).SetInt(ethTx.Value()).Float64()
		pageData.Amount = valueFloat / 1e18
		pageData.AmountRaw = ethTx.Value().Bytes()
	}

	// Gas price
	if ethTx.GasPrice() != nil {
		gasPriceFloat, _ := new(big.Float).SetInt(ethTx.GasPrice()).Float64()
		pageData.GasPrice = gasPriceFloat / 1e9 // Convert to Gwei
	}

	// EIP-1559 tip price
	if ethTx.Type() >= 2 && ethTx.GasTipCap() != nil {
		tipFloat, _ := new(big.Float).SetInt(ethTx.GasTipCap()).Float64()
		pageData.TipPrice = tipFloat / 1e9
	}

	// From address (need to derive from signature)
	if from, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(ethTx.ChainId()), ethTx); err == nil {
		pageData.FromAddr = from.Bytes()
	}

	// To address
	if ethTx.To() != nil {
		pageData.ToAddr = ethTx.To().Bytes()
		pageData.HasTo = true
	} else {
		pageData.IsCreate = true
	}

	// Input data
	pageData.InputData = ethTx.Data()
	if len(ethTx.Data()) >= 4 {
		pageData.MethodID = ethTx.Data()[:4]
		var sigBytes types.TxSignatureBytes
		copy(sigBytes[:], ethTx.Data()[0:4])
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures([]types.TxSignatureBytes{sigBytes})
		if sigLookup, found := sigLookups[sigBytes]; found {
			if sigLookup.Status == types.TxSigStatusFound {
				pageData.MethodName = sigLookup.Name
			} else {
				pageData.MethodName = "call?"
			}
		}
	} else {
		pageData.MethodName = "transfer"
	}

	// Blob hashes
	pageData.BlobCount = uint32(len(ethTx.BlobHashes()))

	// Generate RLP and JSON
	if rlpData, err := ethTx.MarshalBinary(); err == nil {
		pageData.TxRLP = "0x" + hex.EncodeToString(rlpData)
	}
	generateTxJSON(pageData, ethTx)

	// If pending, we're done
	if isPending {
		pageData.StatusText = "Pending"
		return true
	}

	// Try to fetch receipt for full data
	for _, client := range clients {
		rpcClient := client.GetRPCClient()
		if rpcClient == nil {
			continue
		}

		ethClient := rpcClient.GetEthClient()
		if ethClient == nil {
			continue
		}

		receipt, err := ethClient.TransactionReceipt(ctx, txHashCommon)
		if err == nil && receipt != nil {
			// Receipt found - upgrade to full view mode
			pageData.ViewMode = models.TxViewModeFull
			pageData.HasReceipt = true

			// Status
			pageData.Status = receipt.Status == 1
			if pageData.Status {
				pageData.StatusText = "Success"
			} else {
				pageData.StatusText = "Failed"
			}

			// Gas used
			pageData.GasUsed = receipt.GasUsed
			if pageData.GasLimit > 0 {
				pageData.GasUsedPct = float64(receipt.GasUsed) / float64(pageData.GasLimit) * 100
			}

			// Calculate tx fee using effective gas price
			if receipt.EffectiveGasPrice != nil {
				effectiveGasPrice, _ := new(big.Float).SetInt(receipt.EffectiveGasPrice).Float64()
				pageData.EffGasPrice = effectiveGasPrice / 1e9
				txFee := float64(receipt.GasUsed) * effectiveGasPrice / 1e18
				pageData.TxFee = txFee
				txFeeWei := new(big.Int).Mul(receipt.EffectiveGasPrice, big.NewInt(int64(receipt.GasUsed)))
				pageData.TxFeeRaw = txFeeWei.Bytes()

				// Calculate fee savings for EIP-1559+ transactions
				if pageData.TxType >= 2 && pageData.GasPrice > 0 && pageData.EffGasPrice > 0 && pageData.GasPrice > pageData.EffGasPrice {
					pageData.FeeSavingsPct = (pageData.GasPrice - pageData.EffGasPrice) / pageData.GasPrice * 100
				}
			}

			// Block info
			pageData.BlockNumber = receipt.BlockNumber.Uint64()
			pageData.BlockHash = receipt.BlockHash.Bytes()
			pageData.TxIndex = uint32(receipt.TransactionIndex)

			// Try to find beacon block for this execution block using GetDbBlocksByFilter
			blockFilter := &dbtypes.BlockFilter{
				EthBlockHash: receipt.BlockHash.Bytes(),
				WithOrphaned: 1,
			}
			dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, 0, 10, 0)
			if len(dbBlocks) > 0 && dbBlocks[0].Block != nil {
				block := dbBlocks[0].Block
				pageData.BlockRoot = block.Root
				pageData.Slot = block.Slot
				pageData.BlockTime = chainState.SlotToTime(phase0.Slot(block.Slot))

				// Check if finalized
				finalizedSlot := chainState.GetFinalizedSlot()
				if phase0.Slot(block.Slot) <= finalizedSlot {
					pageData.TxFinalized = true
				}

				// Build inclusion blocks list from all found blocks
				pageData.InclusionBlocks = make([]*models.TransactionPageDataBlock, 0, len(dbBlocks))
				for _, dbBlock := range dbBlocks {
					if dbBlock.Block == nil {
						continue
					}
					b := dbBlock.Block
					isOrphaned := b.Status == dbtypes.Orphaned
					pageData.InclusionBlocks = append(pageData.InclusionBlocks, &models.TransactionPageDataBlock{
						BlockNumber: pageData.BlockNumber,
						BlockHash:   b.EthBlockHash,
						BlockRoot:   b.Root,
						Slot:        b.Slot,
						BlockTime:   chainState.SlotToTime(phase0.Slot(b.Slot)),
						IsOrphaned:  isOrphaned,
						IsCanonical: !isOrphaned,
						TxIndex:     pageData.TxIndex,
					})
				}
			}

			break
		}
	}

	return true
}

// generateTxJSON creates a JSON representation of the transaction using proper marshaling.
func generateTxJSON(pageData *models.TransactionPageData, ethTx *ethtypes.Transaction) {
	// Use the transaction's built-in MarshalJSON for standardized format
	jsonBytes, err := ethTx.MarshalJSON()
	if err != nil {
		return
	}

	// Pretty-print the JSON
	var prettyJSON map[string]any
	if err := json.Unmarshal(jsonBytes, &prettyJSON); err == nil {
		if prettyBytes, err := json.MarshalIndent(prettyJSON, "", "  "); err == nil {
			pageData.TxJSON = string(prettyBytes)
		}
	}
}

// loadTransactionEventsFromIndex populates event tab from the lightweight
// event index. Full event data (all topics + data blob) will be loaded from
// blockdb in a future phase. For now, only source address and topic1 (event
// signature) are shown.
func loadTransactionEventsFromIndex(pageData *models.TransactionPageData, events []*dbtypes.ElEventIndex) {
	if len(events) == 0 {
		return
	}

	// Collect account IDs for batch lookup
	accountIDs := make(map[uint64]bool, len(events))
	for _, e := range events {
		accountIDs[e.SourceID] = true
	}

	// Batch lookup accounts
	accountIDList := make([]uint64, 0, len(accountIDs))
	for id := range accountIDs {
		accountIDList = append(accountIDList, id)
	}
	accountMap := make(map[uint64]*dbtypes.ElAccount, len(accountIDList))
	if len(accountIDList) > 0 {
		if accounts, err := db.GetElAccountsByIDs(accountIDList); err == nil {
			for _, a := range accounts {
				accountMap[a.ID] = a
			}
		}
	}

	// Build events list from index entries
	pageData.Events = make([]*models.TransactionPageDataEvent, 0, len(events))
	for _, e := range events {
		event := &models.TransactionPageDataEvent{
			EventIndex: e.EventIndex,
		}

		// Source address
		if source, ok := accountMap[e.SourceID]; ok {
			event.SourceAddr = source.Address
			event.SourceIsContract = source.IsContract
		}

		// Only topic1 (event signature) is available from the index
		if len(e.Topic1) > 0 {
			event.Topic0 = e.Topic1
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

// loadFullTransactionData loads the full transaction data from the beacon block.
// This retrieves the full input data, RLP, and JSON representation for display.
func loadFullTransactionData(pageData *models.TransactionPageData, tx *dbtypes.ElTransaction, blockFilter *dbtypes.BlockFilter) {
	// Get block info
	blocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, 0, 1, 0)
	if len(blocks) == 0 || blocks[0].Block == nil {
		return
	}

	// Get the block root and load the full beacon block
	var blockRoot phase0.Root
	copy(blockRoot[:], blocks[0].Block.Root)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(ctx, blockRoot)
	if err != nil || blockData == nil || blockData.Block == nil {
		logrus.WithError(err).Debug("failed to load beacon block for transaction details")
		return
	}

	// Get execution transactions from the block
	execTxs, err := blockData.Block.ExecutionTransactions()
	if err != nil {
		logrus.WithError(err).Debug("failed to get execution transactions")
		return
	}

	if int(tx.TxIndex) >= len(execTxs) {
		logrus.Debug("transaction index out of range")
		return
	}

	rlpData := execTxs[tx.TxIndex]

	// Store RLP as hex string for copy button
	pageData.TxRLP = "0x" + hex.EncodeToString(rlpData)

	// Parse the transaction to get input data and JSON representation
	var ethTx ethtypes.Transaction
	if err := ethTx.UnmarshalBinary(rlpData); err != nil {
		logrus.WithError(err).Debug("failed to parse transaction RLP")
		return
	}

	// Set input data from parsed transaction
	pageData.InputData = ethTx.Data()

	// Generate JSON using proper marshaling
	generateTxJSON(pageData, &ethTx)

	// Load blob data for type 3 transactions
	if ethTx.Type() == 3 && len(ethTx.BlobHashes()) > 0 {
		loadBlobData(pageData, &ethTx, blockData)
	}
}

// loadBlobData populates blob-related data for type 3 (blob) transactions.
// It extracts versioned hashes from the transaction, KZG commitments from the beacon block,
// and calculates blob gas fees.
func loadBlobData(pageData *models.TransactionPageData, ethTx *ethtypes.Transaction, blockData *services.CombinedBlockResponse) {
	blobHashes := ethTx.BlobHashes()
	if len(blobHashes) == 0 {
		return
	}

	// Get KZG commitments from beacon block
	var kzgCommitments [][]byte
	if blockData != nil && blockData.Block != nil {
		commitments, err := blockData.Block.BlobKZGCommitments()
		if err == nil {
			// Find the commitments that correspond to this transaction's blobs
			// by matching versioned hashes
			kzgCommitments = matchBlobCommitments(blobHashes, commitments)
		}
	}

	// Build blob list
	pageData.Blobs = make([]*models.TransactionPageDataBlob, len(blobHashes))
	for i, hash := range blobHashes {
		blob := &models.TransactionPageDataBlob{
			Index:         uint64(i),
			VersionedHash: hash[:],
		}

		// Add KZG commitment if available
		if i < len(kzgCommitments) && len(kzgCommitments[i]) > 0 {
			blob.KzgCommitment = kzgCommitments[i]
		}

		pageData.Blobs[i] = blob
	}

	// Calculate blob gas info
	const blobGasPerBlob = 131072 // EIP-4844 constant
	blobCount := uint32(len(blobHashes))
	pageData.BlobCount = blobCount
	pageData.BlobGasUsed = uint64(blobCount) * blobGasPerBlob
	pageData.BlobGasLimit = pageData.BlobGasUsed // For a transaction, limit = used

	// Get blob gas fee cap from transaction
	if ethTx.BlobGasFeeCap() != nil {
		blobFeeCapFloat, _ := new(big.Float).SetInt(ethTx.BlobGasFeeCap()).Float64()
		pageData.BlobGasFeeCap = blobFeeCapFloat / 1e9 // Convert to Gwei
	}

	// Calculate actual blob gas price from block's excess_blob_gas
	if blockData != nil && blockData.Block != nil {
		executionPayload, err := blockData.Block.ExecutionPayload()
		if err == nil && executionPayload != nil {
			excessBlobGas, err := executionPayload.ExcessBlobGas()
			if err == nil {
				timestamp, _ := executionPayload.Timestamp()
				executionChainState := services.GlobalBeaconService.GetExecutionChainState()
				blobSchedule := executionChainState.GetBlobScheduleForTimestamp(time.Unix(int64(timestamp), 0))

				if blobSchedule != nil {
					blobBaseFee := executionChainState.CalcBaseFeePerBlobGas(excessBlobGas, blobSchedule.BaseFeeUpdateFraction)

					// Convert to Gwei for display
					blobBaseFeeFloat, _ := new(big.Float).SetInt(blobBaseFee).Float64()
					pageData.BlobGasPrice = blobBaseFeeFloat / 1e9

					// Calculate total blob fee in ETH
					blobFeeWei := new(big.Int).Mul(blobBaseFee, big.NewInt(int64(pageData.BlobGasUsed)))
					blobFeeFloat, _ := new(big.Float).SetInt(blobFeeWei).Float64()
					pageData.BlobFee = blobFeeFloat / 1e18
					pageData.BlobFeeRaw = blobFeeWei.Bytes()

					// Calculate savings percentage
					if pageData.BlobGasFeeCap > 0 && pageData.BlobGasPrice > 0 && pageData.BlobGasFeeCap > pageData.BlobGasPrice {
						pageData.BlobFeeSavings = (pageData.BlobGasFeeCap - pageData.BlobGasPrice) / pageData.BlobGasFeeCap * 100
					}
				}
			}
		}
	}
}

// matchBlobCommitments finds the KZG commitments from the block that match
// the given versioned hashes from a transaction.
// Since blob commitments in the block are ordered across all transactions,
// we need to find the ones belonging to this transaction by matching versioned hashes.
func matchBlobCommitments(versionedHashes []common.Hash, allCommitments []deneb.KZGCommitment) [][]byte {
	result := make([][]byte, len(versionedHashes))

	// For each versioned hash, find matching commitment
	// A versioned hash is SHA256(commitment) with version byte prefix
	for i, hash := range versionedHashes {
		for _, commitment := range allCommitments {
			// Compute versioned hash from commitment
			computedHash := computeVersionedHash(commitment[:])
			if computedHash == hash {
				result[i] = commitment[:]
				break
			}
		}
	}

	return result
}

// computeVersionedHash computes the versioned hash from a KZG commitment.
// Per EIP-4844: versioned_hash = 0x01 || SHA256(commitment)[1:]
func computeVersionedHash(commitment []byte) common.Hash {
	hash := sha256.Sum256(commitment)
	hash[0] = 0x01 // Version byte for blob commitments
	return common.Hash(hash)
}
