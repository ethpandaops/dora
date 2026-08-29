package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/golang/snappy"
	"github.com/gorilla/mux"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/spamoor/txtypes"
)

// EIP-7708: ETH Transfer logger address — emits Transfer(address,address,uint256) on every ETH move.
var ethTransferLogger = common.HexToAddress("0xfffffffffffffffffffffffffffffffffffffffe")

// Transaction type names
var txTypeNames = map[uint8]string{
	0: "Legacy",
	1: "Access List (EIP-2930)",
	2: "Dynamic Fee (EIP-1559)",
	3: "Blob (EIP-4844)",
	4: "Set Code (EIP-7702)",
	6: "Frame (EIP-8141)",
}

// Transaction handles the /tx/{hash} page
func Transaction(w http.ResponseWriter, r *http.Request) {
	txTemplateFiles := append(layoutTemplateFiles,
		"transaction/transaction.html",
		"transaction/events.html",
		"transaction/statechanges.html",
		"transaction/transfers.html",
		"transaction/internaltxs.html",
		"transaction/authorizations.html",
		"transaction/blobs.html",
		"transaction/frames.html",
		"transaction/signatures.html",
	)
	notfoundTemplateFiles := append(layoutTemplateFiles,
		"transaction/notfound.html",
	)

	// Check if execution indexer is enabled
	if !utils.Config.ExecutionIndexer.Enabled {
		data := InitPageData(w, r, "blockchain", "/tx", "Feature Disabled", notfoundTemplateFiles)
		data.Data = &models.TransactionNotFoundData{Reason: "disabled"}
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "transaction.go", "Transaction", "disabled", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	vars := mux.Vars(r)
	txHashHex := strings.TrimPrefix(vars["hash"], "0x")

	txHashBytes, err := hex.DecodeString(txHashHex)
	if err != nil || len(txHashBytes) != 32 {
		data := InitPageData(w, r, "blockchain", "/tx", "Transaction not found", notfoundTemplateFiles)
		data.Data = &models.TransactionNotFoundData{Reason: "notfound"}
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
		nf := &models.TransactionNotFoundData{Reason: "notfound"}
		if ti := services.GlobalBeaconService.GetTxIndexer(); ti != nil {
			if ps := ti.GetPruningStatus(); ps.DetailsEnabled && ps.DetailsPrunedEpoch > 0 {
				nf.DetailsEnabled = true
				nf.DetailsPrunedEpoch = ps.DetailsPrunedEpoch
			}
		}
		data.Data = nf
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
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTransactionPageData(pageCall.CallCtx, txHash, tabView, selectedBlockUid)
		pageCall.CacheTimeout = cacheTimeout
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

func buildTransactionPageData(ctx context.Context, txHash []byte, tabView string, selectedBlockUid uint64) (*models.TransactionPageData, time.Duration) {
	logrus.Debugf("transaction page called: %x (tab: %v, block: %v)", txHash, tabView, selectedBlockUid)

	chainState := services.GlobalBeaconService.GetChainState()

	pageData := &models.TransactionPageData{
		TxHash:   txHash,
		TabView:  tabView,
		ViewMode: models.TxViewModeNone,
	}

	// Try to get transaction from DB first
	txs, err := db.GetElTransactionsByHash(ctx, txHash)
	if err == nil && len(txs) > 0 {
		// Found in DB - build page data from DB entries
		buildTransactionPageDataFromDB(ctx, pageData, txs, tabView, chainState, selectedBlockUid)
		blockSlot := txs[0].BlockUid >> 16
		blockTime := chainState.SlotToTime(phase0.Slot(blockSlot))
		cacheTimeout := 1 * time.Minute
		if time.Since(blockTime) > 5*time.Minute {
			cacheTimeout = 15 * time.Minute
		}
		finalizeTransactionPage(ctx, pageData)
		return pageData, cacheTimeout
	}

	// Not in DB - reconstruct from blockdb (relational row pruned but still
	// within the longer blockdb/details retention).
	if buildTransactionPageDataFromBlockdb(ctx, pageData, txHash, chainState) {
		finalizeTransactionPage(ctx, pageData)
		return pageData, 15 * time.Minute
	}

	// Not in DB or blockdb - try to fetch from EL client
	if buildTransactionPageDataFromEL(ctx, pageData, txHash, chainState) {
		finalizeTransactionPage(ctx, pageData)
		return pageData, 30 * time.Minute
	}

	// Transaction not found anywhere
	pageData.TxNotFound = true
	pageData.ViewMode = models.TxViewModeNone
	return pageData, 1 * time.Minute
}

// finalizeTransactionPage does the work that depends on the whole page being built,
// whichever source it came from.
func finalizeTransactionPage(ctx context.Context, pageData *models.TransactionPageData) {
	// Naming what each frame calls costs a signature lookup, so it is done for the tab
	// that shows the calls rather than on every view of the transaction.
	if pageData.TabView == "frames" && len(pageData.Frames) > 0 {
		resolveFrameCalldata(ctx, pageData)
	}

	applyExpiryMargin(pageData)
	applySignatureRoles(pageData)
	setTransactionEnsNames(ctx, pageData)
}

// applyExpiryMargin states an expiry deadline against the time the transaction was
// included rather than against now.
//
// The expiry verifier frame checks the deadline when the transaction executes, so once it
// is on chain the only thing the deadline says is how much room it had left. Counting
// down to it from the present says nothing: a transaction included an hour ago with a
// thirty minute deadline was never late, and reading "expired 30 min. ago" would suggest
// it was.
func applyExpiryMargin(pageData *models.TransactionPageData) {
	if !pageData.HasExpiry || pageData.BlockTime.IsZero() {
		return
	}

	margin := pageData.ExpiryTime.Sub(pageData.BlockTime)

	pageData.ExpiryPassed = margin < 0
	pageData.ExpiryMargin = shortDuration(margin.Abs())
}

// shortDuration renders a duration at the coarsest unit that still says something.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d sec.", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min.", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr.", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

// setTransactionEnsNames collects every execution address shown on the transaction
// detail page (main from/to plus the events, token-transfer, internal-tx, access-list,
// state-change and authorization sub-lists of the active tab) and resolves their ENS
// names once for client-side display.
func setTransactionEnsNames(ctx context.Context, pageData *models.TransactionPageData) {
	ensAddrs := make([][]byte, 0, 8)
	ensAddrs = append(ensAddrs, pageData.FromAddr, pageData.ToAddr)
	for _, event := range pageData.Events {
		ensAddrs = append(ensAddrs, event.SourceAddr, event.EthTransferFrom, event.EthTransferTo)
	}
	for _, transfer := range pageData.TokenTransfers {
		ensAddrs = append(ensAddrs, transfer.Contract, transfer.FromAddr, transfer.ToAddr)
	}
	for _, itx := range pageData.InternalTxs {
		ensAddrs = append(ensAddrs, itx.FromAddr, itx.ToAddr)
	}
	for _, entry := range pageData.AccessListEntries {
		ensAddrs = append(ensAddrs, entry.Address)
	}
	for _, change := range pageData.StateChanges {
		ensAddrs = append(ensAddrs, change.Address)
	}
	for _, auth := range pageData.Authorizations {
		ensAddrs = append(ensAddrs, auth.AuthorityAddr, auth.DelegateAddr)
	}
	for _, frame := range pageData.Frames {
		ensAddrs = append(ensAddrs, frame.TargetAddr, frame.CallerAddr)
	}
	for _, sig := range pageData.Signatures {
		ensAddrs = append(ensAddrs, sig.SignerAddr)
	}
	ensAddrs = append(ensAddrs, pageData.PayerAddr, pageData.FeeRecipientAddr)
	pageData.SetEnsNames(resolveEnsNames(ctx, ensAddrs))
}

// buildTransactionPageDataFromDB builds page data from database entries.
// selectedBlockUid: 0 = auto-select canonical, otherwise use the specified block.
func buildTransactionPageDataFromDB(ctx context.Context, pageData *models.TransactionPageData, txs []*dbtypes.ElTransaction, tabView string, chainState *consensus.ChainState, selectedBlockUid uint64) {
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
	allBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, blockFilter, 0, uint32(len(blockUids)), 0)

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
			TxIndex:     uint32(tx.TxUid & 0xFFFF),
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

	pageData.Status = tx.RevertID == 0
	if tx.RevertID > 0 {
		pageData.StatusText = "Failed"
		if reasons, err := db.GetElRevertReasonsByIDs(ctx, []uint32{tx.RevertID}); err == nil {
			pageData.RevertReason = reasons[tx.RevertID]
		}
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
		if fromAccount, err := db.GetElAccountByID(ctx, tx.FromID); err == nil {
			pageData.FromAddr = fromAccount.Address
			pageData.FromIsContract = fromAccount.IsContract
		}
	}
	if tx.ToID > 0 {
		if toAccount, err := db.GetElAccountByID(ctx, tx.ToID); err == nil {
			pageData.ToAddr = toAccount.Address
			pageData.ToIsContract = toAccount.IsContract
			pageData.HasTo = true
		}
	} else if !dbtypes.IsMultiTarget(tx.TxType) {
		// No recipient means a contract creation - unless the transaction addresses
		// several, in which case it has none of its own and the row's to_id is the 0
		// that says so.
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

	// Transaction details. The create flag shares the byte with the type.
	pageData.TxType = tx.TxType & dbtypes.ElTxTypeMask
	if name, ok := txTypeNames[pageData.TxType]; ok {
		pageData.TxTypeName = name
	} else {
		pageData.TxTypeName = fmt.Sprintf("Type %d", pageData.TxType)
	}
	pageData.Nonce = tx.Nonce
	pageData.TxIndex = uint32(tx.TxUid & 0xFFFF)

	// Load full transaction data from selected beacon block (must come before
	// method resolution so InputData is available for calldata decoding).
	if displayBlock != nil {
		blockFilter := &dbtypes.BlockFilter{
			BlockUids:    []uint64{tx.BlockUid},
			WithOrphaned: 1,
		}
		loadFullTransactionData(ctx, pageData, tx, blockFilter)
	}

	// Resolve call target type and method info
	applyCallTargetResolution(ctx, pageData, tx.MethodID)

	// Blobs
	pageData.BlobCount = tx.BlobCount

	// Check data_status for this block (for blockdb availability), and who the block paid
	// its transaction fees to, so a balance moving for that reason can say so.
	if elBlock, err := db.GetElBlock(ctx, tx.BlockUid); err == nil {
		pageData.DataStatus = elBlock.DataStatus

		if elBlock.FeeAccountID != nil && *elBlock.FeeAccountID != 0 {
			if accounts, err := db.GetElAccountsByIDs(ctx, []uint64{*elBlock.FeeAccountID}); err == nil && len(accounts) > 0 {
				pageData.FeeRecipientAddr = accounts[0].Address
			}
		}
	}
	// A call trace exists for this tx whenever its block stored call traces, even
	// if it has only the single root frame (no internal calls aggregated).
	pageData.HasTrace = pageData.DataStatus&dbtypes.ElBlockDataCallTraces != 0

	// Frames of a frame transaction. They are the transaction's recipients, values and
	// statuses, so they belong on the overview rather than behind a tab that has to be
	// opened. The frames themselves come from the transaction, which loadFullTransactionData
	// has already read; the payer and the per-frame results are on the receipt, which
	// blockdb keeps.
	if dbtypes.IsMultiTarget(tx.TxType) {
		pageData.IsFrameTx = true

		if displayBlock != nil {
			loadFrameReceiptFromBlockdb(ctx, pageData, displayBlock.Slot, displayBlock.Root, tx.TxHash)
		}
	}

	// Event count comes straight off the tx row (logs emitted); full event
	// data is loaded from blockdb when the events tab is opened.
	pageData.EventCount = uint64(tx.EventCount)

	// Load remaining tab badge counts using lightweight COUNT queries instead
	// of loading all rows. This avoids multi-second sequential scans for
	// transactions with many transfers or internal calls.
	transferCount, _ := db.GetElTokenTransferCountByTxUid(ctx, tx.TxUid)
	pageData.TokenTransferCount = transferCount

	// A frame transaction's per-account rows are built from its frames rather than from a
	// call trace, so counting them would put a call count on the tab that is really a
	// count of accounts the frames touched. The tab reports the real number once loaded.
	if !pageData.IsFrameTx {
		internalTxCount, _ := db.GetElTransactionsInternalCountByTxUid(ctx, tx.TxUid)
		pageData.InternalTxCount = internalTxCount
	}

	// Load tab-specific detailed data. Full row data is only loaded for
	// the active tab to avoid unnecessary I/O. Events and internal txs
	// are loaded from blockdb when available, falling back to DB.
	switch tabView {
	case "events":
		loadTransactionEventsFromBlockdb(ctx, pageData, tx.BlockUid)
	case "transfers":
		transfers, _ := db.GetElTokenTransfersByTxUid(ctx, tx.TxUid)
		loadTransactionTransfersFromData(ctx, pageData, transfers)
		attributeTokenTransfersToFrames(pageData)
	case "internaltxs":
		loadTransactionInternalTxsFromBlockdb(ctx, pageData, tx.BlockUid, tx.TxUid)
		computeInternalTxIndent(pageData)
	case "statechanges":
		loadTransactionStateChangesFromBlockdb(ctx, pageData, tx.BlockUid)
		annotateStateChangeRoles(pageData)
	case "authorizations":
		if pageData.TxType == txtypes.SetCodeTxType && len(pageData.Authorizations) > 0 {
			resolveAuthorizationValidity(ctx, pageData, tx.BlockUid)
		}
	}
}

// buildTransactionPageDataFromEL builds page data by fetching from EL client.
// Returns true if transaction was found, false otherwise.
func buildTransactionPageDataFromEL(ctx context.Context, pageData *models.TransactionPageData, txHash []byte, chainState *consensus.ChainState) bool {
	txIndexer := services.GlobalBeaconService.GetTxIndexer()
	if txIndexer == nil {
		return false
	}

	clients := txIndexer.GetReadyClients()
	if len(clients) == 0 {
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Try to fetch transaction from EL client
	var ethTx *txtypes.Transaction
	var isPending bool
	var err error

	txHashCommon := common.BytesToHash(txHash)

	for _, client := range clients {
		rpcClient := client.GetRPCClient()
		if rpcClient == nil {
			continue
		}

		ethTx, isPending, err = rpcClient.GetTransactionByHash(ctx, txHashCommon)
		if err == nil && ethTx != nil {
			break
		}
	}

	if ethTx == nil {
		return false
	}

	// Transaction found - populate basic fields
	pageData.ViewMode = models.TxViewModePartial // Start with partial, upgrade if receipt found

	applyEthTxFields(ctx, pageData, ethTx)

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

		receipt, err := rpcClient.GetTransactionReceipt(ctx, txHashCommon)
		if err == nil && receipt != nil {
			// Receipt found - upgrade to full view mode
			pageData.ViewMode = models.TxViewModeFull
			pageData.HasReceipt = true

			applyFrameReceiptExtra(pageData, receipt)

			// Status
			pageData.Status = receipt.Status == 1
			if pageData.Status {
				pageData.StatusText = "Success"
			} else {
				pageData.StatusText = "Failed"
			}

			// A frame transaction's status is derived from its frames, not taken from
			// the client's own derived one, so it is stated after the generic status
			// rather than before it.
			applyFrameTxStatus(pageData)

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
			dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, blockFilter, 0, 10, 0)
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

// applyEthTxFields populates the page-data fields derived from a parsed
// transaction envelope. Shared by the EL-client and blockdb-reconstruction paths.
func applyEthTxFields(ctx context.Context, pageData *models.TransactionPageData, ethTx *txtypes.Transaction) {
	pageData.TxType = ethTx.Type()
	if name, ok := txTypeNames[ethTx.Type()]; ok {
		pageData.TxTypeName = name
	} else {
		pageData.TxTypeName = fmt.Sprintf("Type %d", ethTx.Type())
	}

	pageData.Nonce = ethTx.Nonce()
	pageData.GasLimit = ethTx.Gas()

	if ethTx.Value() != nil {
		bigFloat := new(big.Float).SetInt(ethTx.Value())
		bigFloat.Quo(bigFloat, big.NewFloat(1e18))
		valueFloat, _ := bigFloat.Float64()
		pageData.Amount = valueFloat
		pageData.AmountRaw = ethTx.Value().Bytes()
	}

	if ethTx.GasPrice() != nil {
		gasPriceFloat, _ := new(big.Float).SetInt(ethTx.GasPrice()).Float64()
		pageData.GasPrice = gasPriceFloat / 1e9 // Convert to Gwei
	}

	if ethTx.Type() >= 2 && ethTx.GasTipCap() != nil {
		tipFloat, _ := new(big.Float).SetInt(ethTx.GasTipCap()).Float64()
		pageData.TipPrice = tipFloat / 1e9
	}

	if from, err := ethTx.From(ethTx.ChainId()); err == nil {
		pageData.FromAddr = from.Bytes()
	}

	buildTxSignature(pageData, ethTx)

	// A frame transaction reports the first SENDER frame's target, which is one of
	// several and not the transaction's recipient - and its absence is not a creation.
	switch {
	case ethTx.Type() == txtypes.FrameTxType:
		pageData.IsFrameTx = true

		if frameTx, ok := ethTx.Inner().(*txtypes.FrameTx); ok {
			buildFramesFromEnvelope(pageData, frameTx)
			applyFrameTxEnvelope(pageData, frameTx)
		}
	case ethTx.To() != nil:
		pageData.ToAddr = ethTx.To().Bytes()
		pageData.HasTo = true
	default:
		pageData.IsCreate = true
	}

	pageData.InputData = ethTx.Data()
	applyCalldataCosts(pageData)
	methodID := []byte(nil)
	if len(ethTx.Data()) >= 4 {
		methodID = ethTx.Data()[:4]
	}
	applyCallTargetResolution(ctx, pageData, methodID)

	// EIP-7976: calldata floor gas = 21000 + 64 × len(calldata)
	if len(pageData.InputData) > 0 {
		pageData.CalldataFloorGas = 21000 + uint64(len(pageData.InputData))*64
	}

	pageData.BlobCount = uint32(len(ethTx.BlobHashes()))

	if ethTx.Type() == txtypes.SetCodeTxType {
		loadAuthorizationData(pageData, ethTx)
	}
	if ethTx.Type() == txtypes.AccessListTxType {
		loadAccessListData(pageData, ethTx)
	}
}

// extractExecTransactions returns the execution-layer transactions from a loaded
// beacon block (Gloas+ envelope or pre-Gloas payload).
func extractExecTransactions(blockData *services.CombinedBlockResponse) []bellatrix.Transaction {
	if blockData.Payload != nil && blockData.Payload.Message != nil && blockData.Payload.Message.Payload != nil {
		return blockData.Payload.Message.Payload.Transactions
	}
	if blockData.Block != nil && blockData.Block.Message != nil && blockData.Block.Message.Body != nil {
		if ep := blockData.Block.Message.Body.ExecutionPayload; ep != nil {
			return ep.Transactions
		}
	}
	return nil
}

// buildTransactionPageDataFromBlockdb reconstructs a transaction from blockdb
// when its relational row has been pruned: the tx-hash index gives candidate
// tx_uids, the envelope is decoded from the block's execution payload (and
// disambiguated by full hash), and receipt metadata is read from blockdb.
// Returns true if the transaction was reconstructed.
func buildTransactionPageDataFromBlockdb(ctx context.Context, pageData *models.TransactionPageData, txHash []byte, chainState *consensus.ChainState) bool {
	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsTxHashIndex() {
		return false
	}

	uids, err := blockdb.GlobalBlockDb.LookupTxHash(ctx, bdbtypes.HashPrefix(txHash))
	if err != nil || len(uids) == 0 {
		return false
	}

	for _, txUid := range uids {
		blockUid := txUid >> 16
		txIndex := uint32(txUid & 0xFFFF)

		blocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, &dbtypes.BlockFilter{
			BlockUids:    []uint64{blockUid},
			WithOrphaned: 1,
		}, 0, 1, 0)
		if len(blocks) == 0 || blocks[0].Block == nil {
			continue
		}
		block := blocks[0].Block

		var blockRoot phase0.Root
		copy(blockRoot[:], block.Root)

		loadCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		blockData, berr := services.GlobalBeaconService.GetSlotDetailsByBlockroot(loadCtx, blockRoot)
		cancel()
		if berr != nil || blockData == nil || blockData.Block == nil {
			continue
		}

		execTxs := extractExecTransactions(blockData)
		if int(txIndex) >= len(execTxs) {
			continue
		}
		rlpData := execTxs[txIndex]

		ethTx, err := txtypes.DecodeTx(rlpData)
		if err != nil {
			continue
		}
		if !bytes.Equal(ethTx.Hash().Bytes(), txHash) {
			continue // prefix collision or wrong inclusion block
		}

		// Match found - reconstruct the page from the envelope.
		pageData.ViewMode = models.TxViewModePartial
		applyEthTxFields(ctx, pageData, ethTx)
		pageData.TxRLP = "0x" + hex.EncodeToString(rlpData)
		generateTxJSON(pageData, ethTx)

		// Block info.
		pageData.Slot = block.Slot
		pageData.BlockRoot = block.Root
		pageData.BlockHash = block.EthBlockHash
		if block.EthBlockNumber != nil {
			pageData.BlockNumber = *block.EthBlockNumber
		}
		pageData.TxIndex = txIndex
		pageData.BlockTime = chainState.SlotToTime(phase0.Slot(block.Slot))
		if phase0.Slot(block.Slot) <= chainState.GetFinalizedSlot() {
			pageData.TxFinalized = true
		}
		isOrphaned := block.Status == dbtypes.Orphaned
		pageData.TxOrphaned = isOrphaned
		pageData.InclusionBlocks = []*models.TransactionPageDataBlock{{
			BlockUid:    blockUid,
			BlockNumber: pageData.BlockNumber,
			BlockHash:   block.EthBlockHash,
			BlockRoot:   block.Root,
			Slot:        block.Slot,
			BlockTime:   pageData.BlockTime,
			IsOrphaned:  isOrphaned,
			IsCanonical: !isOrphaned,
			TxIndex:     txIndex,
		}}

		// Receipt metadata from blockdb (upgrades to full view if available).
		applyReceiptMetaFromBlockdb(ctx, pageData, block.Slot, block.Root, txHash)

		// Blob data. A frame transaction may carry blobs too, so what decides this is
		// whether the transaction has any, not which type it is.
		if len(ethTx.BlobHashes()) > 0 {
			loadBlobData(pageData, ethTx, blockData)
		}

		return true
	}

	return false
}

// applyReceiptMetaFromBlockdb upgrades a reconstructed page to a full view by
// applying receipt metadata (status, gas used, effective gas price, fee) read
// from blockdb. No-op if the receipt section is unavailable.
func applyReceiptMetaFromBlockdb(ctx context.Context, pageData *models.TransactionPageData, slot uint64, blockRoot []byte, txHash []byte) {
	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsExecData() {
		return
	}

	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	sections, err := blockdb.GlobalBlockDb.GetExecDataTxSections(rctx, slot, blockRoot, txHash, bdbtypes.ExecDataSectionReceiptMeta)
	if err != nil || sections == nil || sections.ReceiptMetaData == nil {
		return
	}

	metaRaw, err := snappy.Decode(nil, sections.ReceiptMetaData)
	if err != nil {
		return
	}

	meta, frameData, err := bdbtypes.DecodeReceiptMetaSection(metaRaw)
	if err != nil {
		return
	}

	if frameData != nil {
		applyFramePayer(pageData, frameData)
		applyFrameResults(pageData, frameData.Frames)
	}

	pageData.ViewMode = models.TxViewModeFull
	pageData.HasReceipt = true

	pageData.Status = meta.Status == 1
	if pageData.Status {
		pageData.StatusText = "Success"
	} else {
		pageData.StatusText = "Failed"
	}

	// As on the EL path: a frame transaction's own status comes from its frames and has
	// to be stated after the generic one, or the client's derived status wins.
	applyFrameTxStatus(pageData)

	pageData.GasUsed = meta.GasUsed
	if pageData.GasLimit > 0 {
		pageData.GasUsedPct = float64(meta.GasUsed) / float64(pageData.GasLimit) * 100
	}

	effGasPrice := meta.EffectiveGasPrice.ToBig()
	if effGasPrice.Sign() > 0 {
		effFloat, _ := new(big.Float).SetInt(effGasPrice).Float64()
		pageData.EffGasPrice = effFloat / 1e9
		pageData.TxFee = float64(meta.GasUsed) * effFloat / 1e18
		txFeeWei := new(big.Int).Mul(effGasPrice, big.NewInt(int64(meta.GasUsed)))
		pageData.TxFeeRaw = txFeeWei.Bytes()
		if pageData.TxType >= 2 && pageData.GasPrice > 0 && pageData.EffGasPrice > 0 && pageData.GasPrice > pageData.EffGasPrice {
			pageData.FeeSavingsPct = (pageData.GasPrice - pageData.EffGasPrice) / pageData.GasPrice * 100
		}
	}
}

// generateTxJSON creates a JSON representation of the transaction using proper marshaling.
func generateTxJSON(pageData *models.TransactionPageData, ethTx *txtypes.Transaction) {
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

// loadTransactionEventsFromBlockdb populates the events tab with full event
// data from blockdb (all topics + data blob). If blockdb data is unavailable
// (pruned or not stored), the tab shows the "not available" state.
func loadTransactionEventsFromBlockdb(ctx context.Context, pageData *models.TransactionPageData, blockUid uint64) {
	if pageData.EventCount == 0 {
		return
	}

	// If the block says events data is unavailable (pruned / not stored), show the
	// "not available" state instead of silently degrading to index-only view.
	if pageData.DataStatus&dbtypes.ElBlockDataEvents == 0 {
		pageData.EventsNotAvailable = true
		return
	}

	// Try to load full event data from blockdb
	if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsExecData() {
		slot := blockUid >> 16
		blockRoot := pageData.BlockRoot

		if len(blockRoot) > 0 {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			sections, err := blockdb.GlobalBlockDb.GetExecDataTxSections(
				ctx, slot, blockRoot, pageData.TxHash,
				bdbtypes.ExecDataSectionEvents,
			)
			if err != nil {
				logrus.WithError(err).WithField("slot", slot).Debug("failed to get exec data tx sections for events")
			}
			if err == nil && sections != nil && sections.EventsData != nil {
				uncompData, err := snappy.Decode(nil, sections.EventsData)
				if err != nil {
					logrus.WithError(err).Debug("failed to decompress events section")
				} else {
					var events bdbtypes.EventDataList

					err := dynssz.GetGlobalDynSsz().UnmarshalSSZ(&events, uncompData)
					if err != nil {
						logrus.WithError(err).Debug("failed to decode events section")
					} else {
						pageData.Events = buildEventsFromBlockdb(events)
						pageData.EventCount = uint64(len(pageData.Events))
						attributeEventsToFrames(pageData)

						return
					}
				}
			}
		}
	}

	// blockdb is the only source of event data; if it didn't yield anything,
	// surface the "not available" state rather than an empty list.
	pageData.EventsNotAvailable = true
}

// buildEventsFromBlockdb converts decoded blockdb events to page model events.
func buildEventsFromBlockdb(events bdbtypes.EventDataList) []*models.TransactionPageDataEvent {
	result := make([]*models.TransactionPageDataEvent, 0, len(events))
	for i := range events {
		ev := &events[i]
		event := &models.TransactionPageDataEvent{
			EventIndex: ev.EventIndex,
			SourceAddr: ev.Source[:],
			Data:       ev.Data,
		}

		// Map topics to fixed fields
		if len(ev.Topics) > 0 {
			event.Topic0 = ev.Topics[0]
		}
		if len(ev.Topics) > 1 {
			event.Topic1 = ev.Topics[1]
		}
		if len(ev.Topics) > 2 {
			event.Topic2 = ev.Topics[2]
		}
		if len(ev.Topics) > 3 {
			event.Topic3 = ev.Topics[3]
		}
		if len(ev.Topics) > 4 {
			event.Topic4 = ev.Topics[4]
		}

		// EIP-7708: ETH transfers emit a Transfer(address,address,uint256) event from
		// 0xfffffffffffffffffffffffffffffffffffffffe (the ETH Transfer logger).
		// Topic0 = keccak256("Transfer(address,address,uint256)") = 0xddf252ad...
		// Topic1 = from address (padded), Topic2 = to address (padded), Data = uint256 wei.
		if bytes.Equal(ev.Source[:], ethTransferLogger[:]) {
			event.EventName = "ETH Transfer (EIP-7708)"
			// Decode from/to/value from the Transfer event
			if len(ev.Topics) >= 3 && len(ev.Topics[1]) == 32 && len(ev.Topics[2]) == 32 {
				event.EthTransferFrom = ev.Topics[1][12:] // last 20 bytes
				event.EthTransferTo = ev.Topics[2][12:]
			}
			if len(ev.Data) >= 32 {
				weiVal := new(big.Int).SetBytes(ev.Data[:32])
				// Format as ETH with up to 6 decimal places, trimming trailing zeros
				eth := new(big.Float).Quo(new(big.Float).SetInt(weiVal), new(big.Float).SetInt(big.NewInt(1e18)))
				event.EthTransferValue = fmt.Sprintf("%.6f", eth)
				// Trim trailing zeros after decimal point
				if strings.Contains(event.EthTransferValue, ".") {
					event.EthTransferValue = strings.TrimRight(event.EthTransferValue, "0")
					event.EthTransferValue = strings.TrimRight(event.EthTransferValue, ".")
				}
				event.EthTransferValue += " ETH"
			}
		}

		result = append(result, event)
	}
	return result
}

// loadTransactionStateChangesFromBlockdb populates the state changes tab with
// per-account storage/balance/nonce/code diffs from blockdb.
func loadTransactionStateChangesFromBlockdb(ctx context.Context, pageData *models.TransactionPageData, blockUid uint64) {
	// If the block says state change data is unavailable (pruned / not stored),
	// show the "not available" state.
	if pageData.DataStatus&dbtypes.ElBlockDataStateChanges == 0 {
		pageData.StateChangesNotAvailable = true
		return
	}

	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsExecData() {
		pageData.StateChangesNotAvailable = true
		return
	}

	slot := blockUid >> 16
	blockRoot := pageData.BlockRoot
	if len(blockRoot) == 0 {
		pageData.StateChangesNotAvailable = true
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	sections, err := blockdb.GlobalBlockDb.GetExecDataTxSections(
		ctx, slot, blockRoot, pageData.TxHash,
		bdbtypes.ExecDataSectionStateChange,
	)
	if err != nil {
		logrus.WithError(err).WithField("slot", slot).Debug("failed to get exec data tx sections for state changes")
		pageData.StateChangesNotAvailable = true
		return
	}
	if sections == nil || sections.StateChangeData == nil {
		pageData.StateChangesNotAvailable = true
		return
	}

	uncompData, err := snappy.Decode(nil, sections.StateChangeData)
	if err != nil {
		logrus.WithError(err).Debug("failed to decompress state changes section")
		pageData.StateChangesNotAvailable = true
		return
	}

	var accounts []bdbtypes.StateChangeAccount

	err = dynssz.GetGlobalDynSsz().UnmarshalSSZ(&accounts, uncompData)
	if err != nil {
		logrus.WithError(err).Debug("failed to decode state changes section")
		pageData.StateChangesNotAvailable = true
		return
	}

	pageData.StateChanges = buildStateChangesFromBlockdb(accounts)
}

// annotateStateChangeRoles marks the accounts whose balance moved for a reason the
// numbers do not show: the sender for what it spent, the fee recipient for what the block
// paid it, and a frame transaction's payer for a fee its sender did not owe.
func annotateStateChangeRoles(pageData *models.TransactionPageData) {
	sameAddress := func(a, b []byte) bool {
		return len(a) > 0 && len(b) > 0 && bytes.Equal(a, b)
	}

	for _, account := range pageData.StateChanges {
		account.IsSender = sameAddress(account.Address, pageData.FromAddr)
		account.IsFeeRecipient = sameAddress(account.Address, pageData.FeeRecipientAddr)

		// The sender paying its own fee is the ordinary case and says nothing.
		account.IsPayer = !pageData.PayerIsSender && sameAddress(account.Address, pageData.PayerAddr)

		account.PredeployName = framePredeployNames[common.BytesToAddress(account.Address)]
	}
}

func buildStateChangesFromBlockdb(accounts []bdbtypes.StateChangeAccount) []*models.TransactionPageDataStateChangeAccount {
	result := make([]*models.TransactionPageDataStateChangeAccount, 0, len(accounts))

	for i := range accounts {
		a := &accounts[i]

		preBal := new(big.Int)
		postBal := new(big.Int)
		if len(a.PreBalance) > 0 {
			preBal.SetBytes(a.PreBalance.Bytes())
		}
		if len(a.PostBalance) > 0 {
			postBal.SetBytes(a.PostBalance.Bytes())
		}
		balDiff := new(big.Int).Sub(postBal, preBal)

		acct := &models.TransactionPageDataStateChangeAccount{
			Address: a.Address[:],

			AccountCreated: (a.Flags & bdbtypes.StateChangeFlagAccountCreated) != 0,
			AccountKilled:  (a.Flags & bdbtypes.StateChangeFlagAccountKilled) != 0,

			BalanceChanged: (a.Flags & bdbtypes.StateChangeFlagBalanceChanged) != 0,
			PreBalance:     a.PreBalance.Bytes(),
			PostBalance:    a.PostBalance.Bytes(),
			PreBalanceWei:  preBal.String(),
			PostBalanceWei: postBal.String(),
			BalanceDiffWei: balDiff.String(),

			NonceChanged: (a.Flags & bdbtypes.StateChangeFlagNonceChanged) != 0,
			PreNonce:     a.PreNonce,
			PostNonce:    a.PostNonce,

			CodeChanged: (a.Flags & bdbtypes.StateChangeFlagCodeChanged) != 0,
			PreCode:     a.PreCode,
			PostCode:    a.PostCode,
			PreCodeLen:  uint64(len(a.PreCode)),
			PostCodeLen: uint64(len(a.PostCode)),

			StorageChanged: (a.Flags & bdbtypes.StateChangeFlagStorageChanged) != 0,
		}

		if acct.StorageChanged && len(a.Slots) > 0 {
			acct.Slots = make([]*models.TransactionPageDataStateChangeSlot, 0, len(a.Slots))

			zeroValue := [32]byte{}
			for j := range a.Slots {
				s := &a.Slots[j]

				slot := &models.TransactionPageDataStateChangeSlot{
					Slot: s.Slot[:],
				}

				if bytes.Equal(s.PreValue[:], zeroValue[:]) {
					slot.ChangeType = "created"
					slot.PostValue = s.PostValue[:]
				} else if bytes.Equal(s.PostValue[:], zeroValue[:]) {
					slot.ChangeType = "deleted"
					slot.PreValue = s.PreValue[:]
				} else {
					slot.ChangeType = "modified"
					slot.PreValue = s.PreValue[:]
					slot.PostValue = s.PostValue[:]
				}

				acct.Slots = append(acct.Slots, slot)
			}
		}

		result = append(result, acct)
	}

	return result
}

// callTypeNames maps call type constants to display names.
var callTypeNames = map[uint8]string{
	0: "CALL",
	1: "STATICCALL",
	2: "DELEGATECALL",
	3: "CREATE",
	4: "CREATE2",
	5: "SELFDESTRUCT",
}

// loadTransactionInternalTxsFromBlockdb populates the internal transactions tab
// with rich call trace data from blockdb (depth, input, output, gas, status).
// Falls back to loading from the DB index if blockdb data is unavailable.
func loadTransactionInternalTxsFromBlockdb(ctx context.Context, pageData *models.TransactionPageData, blockUid uint64, txUid uint64) {
	// If the block says call trace data is unavailable (pruned / not stored),
	// show the "not available" state. Otherwise load the trace even when it has
	// only the single root frame (no aggregated internal calls).
	if pageData.DataStatus&dbtypes.ElBlockDataCallTraces == 0 {
		pageData.InternalTxsNotAvailable = true
		return
	}

	// Try to load rich call trace from blockdb
	if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsExecData() {
		slot := blockUid >> 16
		blockRoot := pageData.BlockRoot

		if len(blockRoot) > 0 {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			sections, err := blockdb.GlobalBlockDb.GetExecDataTxSections(
				ctx, slot, blockRoot, pageData.TxHash,
				bdbtypes.ExecDataSectionCallTrace,
			)
			if err != nil {
				logrus.WithError(err).WithField("slot", slot).Debug("failed to get exec data tx sections for call trace")
			}
			if err == nil && sections != nil && sections.CallTraceData != nil {
				uncompData, err := snappy.Decode(nil, sections.CallTraceData)
				if err != nil {
					logrus.WithError(err).Debug("failed to decompress call trace section")
				} else {
					var frames []bdbtypes.FlatCallFrame
					err = dynssz.GetGlobalDynSsz().UnmarshalSSZ(&frames, uncompData)
					if err != nil {
						logrus.WithError(err).Debug("failed to decode call trace section")
					} else {
						buildInternalTxsFromBlockdb(ctx, pageData, frames)
						pageData.InternalTxCount = uint64(len(pageData.InternalTxs))
						attributeInternalTxsToFrames(pageData)

						return
					}
				}
			}
		}
	}

	// No per-call detail in the DB index (it stores per-account aggregates),
	// so when blockdb is unavailable we have nothing to render. Surface the
	// "not available" state so the template shows the archive notice.
	//
	// For a frame transaction the block's trace was stored - the check above passed -
	// and this transaction still has none, which means the client did not decompose it
	// into its frames. That is a statement about the client, not about the data being
	// gone, and the count taken from the per-account rows is not a call count either.
	_ = txUid

	if pageData.IsFrameTx {
		pageData.FrameCallsNotTraced = true
		pageData.InternalTxCount = 0

		return
	}

	pageData.InternalTxsNotAvailable = true
}

// buildInternalTxsFromBlockdb converts decoded blockdb call frames to page
// model internal transactions with full trace data.
func buildInternalTxsFromBlockdb(ctx context.Context, pageData *models.TransactionPageData, frames []bdbtypes.FlatCallFrame) {
	pageData.InternalTxs = make([]*models.TransactionPageDataInternalTx, 0, len(frames))

	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()

	// Collect unique 4-byte method selectors for batch lookup,
	// but skip lookups for CREATE/CREATE2, precompiles, and system contracts.
	sigSet := make(map[types.TxSignatureBytes]struct{}, len(frames))
	for i := range frames {
		f := &frames[i]
		if len(f.Input) < 4 {
			continue
		}
		// Skip CREATE/CREATE2, precompiles, non-deposit system contracts
		isCreate := f.Type == 3 || f.Type == 4
		if skip, _ := utils.ShouldSkipSignatureLookup(f.To[:], isCreate, sysContracts); skip {
			continue
		}

		var sig types.TxSignatureBytes
		copy(sig[:], f.Input[:4])
		sigSet[sig] = struct{}{}
	}

	sigBytes := make([]types.TxSignatureBytes, 0, len(sigSet))
	for sig := range sigSet {
		sigBytes = append(sigBytes, sig)
	}

	var sigLookups map[types.TxSignatureBytes]*services.TxSignaturesLookup
	if len(sigBytes) > 0 {
		sigLookups = services.GlobalTxSignaturesService.LookupSignatures(ctx, sigBytes)
	}

	for i := range frames {
		f := &frames[i]

		bigFloat := new(big.Float).SetInt(f.Value.ToBig())
		bigFloat.Quo(bigFloat, big.NewFloat(1e18))
		valueFloat, _ := bigFloat.Float64()
		valueRaw := f.Value.Bytes()

		input, inputPruned := bdbtypes.TrimPrunedPayload(f.Input)
		output, outputPruned := bdbtypes.TrimPrunedPayload(f.Output)

		itx := &models.TransactionPageDataInternalTx{
			CallIndex:    uint32(i),
			Depth:        f.Depth,
			CallType:     f.Type,
			FromAddr:     f.From[:],
			ToAddr:       f.To[:],
			Amount:       valueFloat,
			AmountRaw:    valueRaw,
			Gas:          f.Gas,
			GasUsed:      f.GasUsed,
			Status:       f.Status,
			ErrorText:    f.Error,
			Input:        input,
			InputPruned:  inputPruned,
			Output:       output,
			OutputPruned: outputPruned,
			HasTraceData: true,
		}

		if name, ok := callTypeNames[f.Type]; ok {
			itx.TypeName = name
		} else {
			itx.TypeName = fmt.Sprintf("TYPE_%d", f.Type)
		}

		// Method ID, name, and decoded calldata from input data. A pruned input
		// only carries its leading bytes, and ABI decoding follows offsets that
		// may point past them, so it stays on the selector-derived fields.
		if len(input) >= 4 {
			isCreate := f.Type == 3 || f.Type == 4
			precompileInfo := utils.GetPrecompileInfo(f.To[:])
			sysName, isSysContract := sysContracts[f.To]
			isNonDepositSys := isSysContract && sysName != "Deposit Contract"

			if isCreate {
				itx.MethodName = "deploy"
			} else if precompileInfo != nil {
				itx.MethodName = precompileInfo.Name
				if !inputPruned {
					itx.DecodedCalldata = utils.DecodePrecompileInput(precompileInfo.Index, input)
				}
			} else if isNonDepositSys {
				itx.MethodName = sysName
				if !inputPruned {
					switch sysName {
					case "Withdrawal Request (EIP-7002)":
						itx.DecodedCalldata = utils.DecodeWithdrawalRequestInput(input)
					case "Consolidation Request (EIP-7251)":
						itx.DecodedCalldata = utils.DecodeConsolidationRequestInput(input)
					}
				}
			} else {
				// Normal call: use fn signature lookup
				itx.MethodID = input[:4]
				var sig types.TxSignatureBytes
				copy(sig[:], input[:4])
				if sigLookups != nil {
					if lookup, found := sigLookups[sig]; found && lookup.Status == types.TxSigStatusFound {
						itx.MethodName = lookup.Name
						itx.MethodSignature = lookup.Signature
						if len(input) > 4 && lookup.Signature != "" && !inputPruned {
							itx.DecodedCalldata = utils.DecodeCalldata(lookup.Signature, input)
						}
					}
				}
			}
		}

		pageData.InternalTxs = append(pageData.InternalTxs, itx)
	}
}

// computeInternalTxIndent sets InternalTxIndentPx based on the maximum
// nesting depth so that deeply nested trees compress to ~300px total.
func computeInternalTxIndent(pageData *models.TransactionPageData) {
	if len(pageData.InternalTxs) == 0 {
		return
	}

	var maxDepth uint16
	for _, itx := range pageData.InternalTxs {
		if itx.Depth > maxDepth {
			maxDepth = itx.Depth
		}
	}

	if maxDepth == 0 {
		pageData.InternalTxIndentPx = 18.0
		return
	}

	indent := 300.0 / float64(maxDepth)
	if indent > 18.0 {
		indent = 18.0
	}
	if indent < 2.0 {
		indent = 2.0
	}
	pageData.InternalTxIndentPx = indent
}

func loadTransactionTransfersFromData(ctx context.Context, pageData *models.TransactionPageData, transfers []*dbtypes.ElTokenTransfer) {
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
		if accounts, err := db.GetElAccountsByIDs(ctx, accountIDList); err == nil {
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
		if tokens, err := db.GetElTokensByIDs(ctx, tokenIDList); err == nil {
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
			EventIndex:    t.TxIdx,
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
func loadFullTransactionData(ctx context.Context, pageData *models.TransactionPageData, tx *dbtypes.ElTransaction, blockFilter *dbtypes.BlockFilter) {
	// Get block info
	blocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, blockFilter, 0, 1, 0)
	if len(blocks) == 0 || blocks[0].Block == nil {
		return
	}

	// Get the block root and load the full beacon block
	var blockRoot phase0.Root
	copy(blockRoot[:], blocks[0].Block.Root)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(ctx, blockRoot)
	if err != nil || blockData == nil || blockData.Block == nil || blockData.Block.Message == nil || blockData.Block.Message.Body == nil {
		logrus.WithError(err).Debug("failed to load beacon block for transaction details")
		return
	}

	// Get execution transactions from the block (or envelope for Gloas+)
	var execTxs []bellatrix.Transaction
	if blockData.Payload != nil && blockData.Payload.Message != nil && blockData.Payload.Message.Payload != nil {
		execTxs = blockData.Payload.Message.Payload.Transactions
	} else if ep := blockData.Block.Message.Body.ExecutionPayload; ep != nil {
		execTxs = ep.Transactions
	} else {
		logrus.Debug("block has no execution payload")
		return
	}

	txIndex := tx.TxUid & 0xFFFF
	if int(txIndex) >= len(execTxs) {
		logrus.Debug("transaction index out of range")
		return
	}

	rlpData := execTxs[txIndex]

	// Store RLP as hex string for copy button
	pageData.TxRLP = "0x" + hex.EncodeToString(rlpData)

	// Parse the transaction to get input data and JSON representation
	ethTx, err := txtypes.DecodeTx(rlpData)
	if err != nil {
		logrus.WithError(err).Debug("failed to parse transaction RLP")
		return
	}

	// Set input data from parsed transaction
	pageData.InputData = ethTx.Data()
	applyCalldataCosts(pageData)

	// EIP-7976: calldata floor gas = 21000 + 64 × len(calldata)
	if len(pageData.InputData) > 0 {
		pageData.CalldataFloorGas = 21000 + uint64(len(pageData.InputData))*64
	}

	// Generate JSON using proper marshaling
	generateTxJSON(pageData, ethTx)

	buildTxSignature(pageData, ethTx)

	// A frame transaction's nonce domain and expiry deadline live in the envelope, so
	// they are only available while the block it came in is still retained.
	if frameTx, ok := ethTx.Inner().(*txtypes.FrameTx); ok {
		if len(pageData.Frames) == 0 {
			buildFramesFromEnvelope(pageData, frameTx)
		}

		applyFrameTxEnvelope(pageData, frameTx)
	}

	// Load blob data. EIP-8141 gives a frame transaction blob hashes and a blob fee cap
	// of its own, so the type is not what decides whether there are blobs to show.
	if len(ethTx.BlobHashes()) > 0 {
		loadBlobData(pageData, ethTx, blockData)
	}

	// Load authorization data for type 4 (EIP-7702) transactions
	if ethTx.Type() == txtypes.SetCodeTxType {
		loadAuthorizationData(pageData, ethTx)
	}

	// Load access list data for type 1 (EIP-2930) transactions
	if ethTx.Type() == txtypes.AccessListTxType {
		loadAccessListData(pageData, ethTx)
	}
}

// loadBlobData populates blob-related data for type 3 (blob) transactions.
// It extracts versioned hashes from the transaction, KZG commitments from the beacon block,
// and calculates blob gas fees.
func loadBlobData(pageData *models.TransactionPageData, ethTx *txtypes.Transaction, blockData *services.CombinedBlockResponse) {
	blobHashes := ethTx.BlobHashes()
	if len(blobHashes) == 0 {
		return
	}

	// Get KZG commitments from beacon block
	var kzgCommitments [][]byte
	if blockData != nil && blockData.Block != nil && blockData.Block.Message != nil && blockData.Block.Message.Body != nil {
		commitments := utils.BlockBodyBlobCommitments(blockData.Block.Message.Body)
		// Find the commitments that correspond to this transaction's blobs
		// by matching versioned hashes
		kzgCommitments = utils.MatchBlobCommitments(blobHashes, commitments)
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
	if blockData != nil && blockData.Block != nil && blockData.Block.Message != nil && blockData.Block.Message.Body != nil {
		executionPayload := blockData.Block.Message.Body.ExecutionPayload
		if executionPayload != nil {
			executionChainState := services.GlobalBeaconService.GetExecutionChainState()
			blobSchedule := executionChainState.GetBlobScheduleForTimestamp(time.Unix(int64(executionPayload.Timestamp), 0))

			if blobSchedule != nil {
				blobBaseFee := executionChainState.CalcBaseFeePerBlobGas(executionPayload.ExcessBlobGas, blobSchedule.BaseFeeUpdateFraction)

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

// applyCallTargetResolution resolves the call target type and method info
// for the transaction, then maps the result to the page data.
func applyCallTargetResolution(ctx context.Context, pageData *models.TransactionPageData, methodID []byte) {
	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()
	lookupFn := func(sigBytes [4]byte) *utils.SignatureLookupResult {
		var tbytes types.TxSignatureBytes
		copy(tbytes[:], sigBytes[:])
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures(ctx, []types.TxSignatureBytes{tbytes})
		if sigLookup, found := sigLookups[tbytes]; found {
			return &utils.SignatureLookupResult{
				Name:      sigLookup.Name,
				Signature: sigLookup.Signature,
				Found:     sigLookup.Status == types.TxSigStatusFound,
			}
		}
		return nil
	}

	res := utils.ResolveCallTargetAndMethod(pageData.ToAddr, pageData.IsCreate, pageData.InputData, methodID, sysContracts, lookupFn)

	pageData.TargetCallType = res.CallType
	pageData.TargetCallName = res.CallName
	pageData.MethodName = res.MethodName
	pageData.MethodSignature = res.MethodSignature
	pageData.DecodedCalldata = res.DecodedCalldata
	if res.MethodID != nil {
		pageData.MethodID = res.MethodID
	}
}

// applyCalldataCosts computes calldata gas cost fields from pageData.InputData.
// Covers three pricing regimes: pre-Prague standard, EIP-7623 (Prague floor), EIP-7976 (Amsterdam floor).
// Must be called after InputData is set.
func applyCalldataCosts(pageData *models.TransactionPageData) {
	data := pageData.InputData
	if len(data) == 0 {
		return
	}
	z := 0
	for _, b := range data {
		if b == 0 {
			z++
		}
	}
	nz := len(data) - z
	total := uint64(len(data))

	tokens := nz*4 + z
	pageData.CalldataZeroBytes = z
	pageData.CalldataNonZeroBytes = nz
	pageData.CalldataPragueTokens = tokens
	// Standard intrinsic (pre-Prague): TX_BASE + 4×zero + 16×nonzero
	pageData.CalldataStandardGas = 21000 + uint64(z)*4 + uint64(nz)*16
	// EIP-7623 floor (Prague+): tokens = 4×nonzero + zero; floor = TX_BASE + tokens×10
	pageData.CalldataPragueFloor = 21000 + uint64(tokens)*10
	// EIP-7976 floor (Amsterdam+): flat 64 gas per byte regardless of zero/nonzero
	pageData.CalldataAmsterdamFloor = 21000 + total*64
}

// loadAuthorizationData extracts EIP-7702 authorization list entries from a
// parsed transaction and populates pageData.Authorizations.
func loadAuthorizationData(
	pageData *models.TransactionPageData,
	ethTx *txtypes.Transaction,
) {
	authList := ethTx.AuthList()
	if len(authList) == 0 {
		return
	}

	pageData.Authorizations = make(
		[]*models.TransactionPageDataAuthorization,
		len(authList),
	)

	for i := range authList {
		auth := &authList[i]
		entry := &models.TransactionPageDataAuthorization{
			Index:        uint32(i),
			DelegateAddr: auth.Address.Bytes(),
		}

		if authority, err := auth.Authority(); err == nil {
			entry.AuthorityAddr = authority.Bytes()
			entry.AuthorityOk = true
		}

		pageData.Authorizations[i] = entry
	}
}

// loadAccessListData extracts EIP-2930 access list entries from a parsed
// transaction and populates pageData.AccessListEntries and
// pageData.AccessListStorageKeys.
func loadAccessListData(
	pageData *models.TransactionPageData,
	ethTx *txtypes.Transaction,
) {
	al := ethTx.AccessList()
	if len(al) == 0 {
		return
	}

	pageData.AccessListEntries = make([]models.TransactionAccessListEntry, len(al))
	for i, entry := range al {
		keys := make([][]byte, len(entry.StorageKeys))
		for j, k := range entry.StorageKeys {
			keyCopy := k // common.Hash is [32]byte
			keys[j] = keyCopy[:]
		}
		pageData.AccessListEntries[i] = models.TransactionAccessListEntry{
			Address:     entry.Address.Bytes(),
			StorageKeys: keys,
		}
		pageData.AccessListStorageKeys += uint64(len(entry.StorageKeys))
	}

	// EIP-7981 (Amsterdam): ACCESS_LIST_STORAGE_KEY_COST 2400→1900, address cost unchanged at 2400
	addrs := uint64(len(al))
	pageData.AccessListGasAmsterdam = addrs*2400 + pageData.AccessListStorageKeys*1900
	pageData.AccessListGasPrague = addrs*2400 + pageData.AccessListStorageKeys*2400
	pageData.AccessListGasSavings = pageData.AccessListGasPrague - pageData.AccessListGasAmsterdam
}

// resolveAuthorizationValidity loads state diffs from blockdb and checks
// whether each EIP-7702 authorization was actually applied on-chain.
// An authorization is considered applied when the authority address has a code
// change whose post-state matches the delegation designator (0xef0100 + delegate).
func resolveAuthorizationValidity(
	ctx context.Context,
	pageData *models.TransactionPageData,
	blockUid uint64,
) {
	if pageData.DataStatus&dbtypes.ElBlockDataStateChanges == 0 {
		return
	}

	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsExecData() {
		return
	}

	slot := blockUid >> 16
	blockRoot := pageData.BlockRoot
	if len(blockRoot) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	sections, err := blockdb.GlobalBlockDb.GetExecDataTxSections(
		ctx, slot, blockRoot, pageData.TxHash,
		bdbtypes.ExecDataSectionStateChange,
	)
	if err != nil || sections == nil || sections.StateChangeData == nil {
		return
	}

	uncompData, err := snappy.Decode(nil, sections.StateChangeData)
	if err != nil {
		return
	}

	var accounts []bdbtypes.StateChangeAccount
	if err := dynssz.GetGlobalDynSsz().UnmarshalSSZ(&accounts, uncompData); err != nil {
		return
	}

	// Build a lookup of address -> post-code for accounts with code changes.
	type codeInfo struct {
		postCode []byte
	}
	codeByAddr := make(map[common.Address]codeInfo, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if (a.Flags & bdbtypes.StateChangeFlagCodeChanged) != 0 {
			codeByAddr[a.Address] = codeInfo{postCode: a.PostCode}
		}
	}

	for _, auth := range pageData.Authorizations {
		if !auth.AuthorityOk {
			continue
		}

		authorityAddr := common.BytesToAddress(auth.AuthorityAddr)
		ci, found := codeByAddr[authorityAddr]
		if !found {
			auth.Applied = 2 // not applied
			continue
		}

		delegateAddr, ok := ethtypes.ParseDelegation(ci.postCode)
		if ok && delegateAddr == common.BytesToAddress(auth.DelegateAddr) {
			auth.Applied = 1 // applied
		} else {
			auth.Applied = 2 // not applied
		}
	}
}

// frameModeNames names the caller each frame mode runs under.
var frameModeNames = map[uint8]string{
	uint8(txtypes.FrameModeDefault): "Default",
	uint8(txtypes.FrameModeVerify):  "Verify",
	uint8(txtypes.FrameModeSender):  "Sender",
	uint8(txtypes.FrameModePostTx):  "Post-tx",
}

// frameSpeciesNames turn the mempool rules' species names into readable ones.
// framePredeployNames names the addresses EIP-8141 gives a role to, so a frame that
// calls one reads as the protocol step it is rather than as an unknown account.
var framePredeployNames = map[common.Address]string{
	txtypes.EntryPoint:        "ENTRY_POINT",
	txtypes.ExpiryVerifier:    "EXPIRY_VERIFIER",
	txtypes.NonceManager:      "NONCE_MANAGER",
	txtypes.RecentRootAddress: "RECENT_ROOTS",
}

var frameSpeciesNames = map[txtypes.FrameSpecies]string{
	txtypes.SpeciesSelfVerify:   "Self verify",
	txtypes.SpeciesOnlyVerify:   "Execution check",
	txtypes.SpeciesPay:          "Paymaster",
	txtypes.SpeciesExpiryVerify: "Expiry check",
	txtypes.SpeciesDeploy:       "Account deploy",
	txtypes.SpeciesUserOp:       "User operation",
	txtypes.SpeciesPostOp:       "Settlement",
	txtypes.SpeciesPostTx:       "Assertion",
	txtypes.SpeciesOther:        "Other",
}

// frameSpeciesInfo says what each kind of frame is for. The name on the badge is short
// enough to scan a list by; this is what it means.
var frameSpeciesInfo = map[txtypes.FrameSpecies]string{
	txtypes.SpeciesSelfVerify: "Runs the sender's own validation code and approves both execution and payment. " +
		"This is the self-relayed case: the sender vouches for the transaction and pays for it itself.",

	txtypes.SpeciesOnlyVerify: "Runs the sender's own validation code and approves execution, but not payment. " +
		"Someone else settles the fee, in a paymaster frame that follows.",

	txtypes.SpeciesPay: "A paymaster approves payment, which makes it the account charged for the transaction " +
		"rather than the sender. Its own signature entry on the transaction is what authorises that.",

	txtypes.SpeciesExpiryVerify: "Calls the expiry verifier predeploy with a deadline. It reverts once the deadline " +
		"has passed, and a reverting validation frame makes the whole transaction invalid - which is what keeps a " +
		"stale transaction off the chain.",

	txtypes.SpeciesDeploy: "Deploys code to the sender's account before anything validates it, so an account that " +
		"does not exist yet can be used by the same transaction that creates it. It has to lead the validation prefix.",

	txtypes.SpeciesUserOp: "One of the calls the transaction was sent to make. SENDER frames are entered by the " +
		"sender itself, and are the only ones that may carry value.",

	txtypes.SpeciesPostOp: "A call made after the operations have run, entered by the ENTRY_POINT predeploy rather " +
		"than by the sender. It is where a paymaster squares up once the real cost is known.",

	txtypes.SpeciesPostTx: "Runs after the transaction's operations, reading what they did through TXTRACE and " +
		"asserting something about it. If it reverts, the whole execution body is reverted with it - not just " +
		"its own atomic batch - though the transaction still reaches the chain and its fee is still owed.",

	txtypes.SpeciesOther: "The frame's mode and approval flags match none of the shapes the mempool rules name.",
}

// frameStatusText renders a frame's result. Skipped is neither a success nor a failure -
// an earlier frame in its atomic batch failed and this one never ran - and a frame the
// client reported no result for is neither either.
func frameStatusText(status uint8, rolledBack bool) string {
	switch uint64(status) {
	case txtypes.FrameStatusSuccess:
		if rolledBack {
			return "Rolled back"
		}

		return "Success"
	case txtypes.FrameStatusFailed:
		return "Failed"
	case txtypes.FrameStatusSkipped:
		return "Skipped"
	default:
		return "Unknown"
	}
}

// buildFramesFromEnvelope builds the frame list from the transaction itself, for the paths
// that have no relational rows to read from: a transaction reconstructed from blockdb
// after its rows were pruned, or one fetched straight from a client.
//
// The results each frame produced are not in the envelope and are overlaid separately.
func buildFramesFromEnvelope(pageData *models.TransactionPageData, frameTx *txtypes.FrameTx) {
	frames := make([]*models.TransactionPageDataFrame, 0, len(frameTx.Frames))

	validationLen := frameTx.ValidationPrefixLength()

	for i, protocolFrame := range frameTx.Frames {
		target := protocolFrame.ResolvedTarget(frameTx.Sender)
		caller := frameCaller(protocolFrame, frameTx.Sender)
		species := frameSpecies(protocolFrame, frameTx.Sender, i < validationLen)

		frame := &models.TransactionPageDataFrame{
			Index:             uint32(i),
			Mode:              uint8(protocolFrame.Mode),
			ModeName:          frameModeNames[uint8(protocolFrame.Mode)],
			Species:           frameSpeciesNames[species],
			SpeciesInfo:       frameSpeciesInfo[species],
			Flags:             protocolFrame.Flags,
			ApprovesPayment:   protocolFrame.Flags&txtypes.ApprovePayment != 0,
			ApprovesExecution: protocolFrame.Flags&txtypes.ApproveExecution != 0,
			AtomicBatch:       protocolFrame.IsAtomicBatch(),
			IsValidation:      i < validationLen,
			CallerAddr:        caller.Bytes(),
			CallerIsSender:    caller == frameTx.Sender,
			CallerLabel:       framePredeployNames[caller],
			TargetAddr:        target.Bytes(),
			HasTarget:         true,
			TargetIsSender:    target == frameTx.Sender,
			TargetLabel:       framePredeployNames[target],
			DataLen:           uint32(len(protocolFrame.Data)),
			Data:              protocolFrame.Data,
			ExecGasLimit:      protocolFrame.Limits.Execution,
			StateGasLimit:     protocolFrame.Limits.State,
			Status:            bdbtypes.FrameStatusUnknown,
			StatusText:        frameStatusText(bdbtypes.FrameStatusUnknown, false),
			BatchFailedIndex:  -1,
		}

		if frame.ModeName == "" {
			frame.ModeName = "Unknown"
		}

		if protocolFrame.Value != nil {
			frame.Amount = weiToEth(protocolFrame.Value.ToBig())
		}

		if len(protocolFrame.Data) >= 4 {
			frame.MethodID = protocolFrame.Data[:4]
		}

		frames = append(frames, frame)
	}

	assignFrameBatches(frames)

	pageData.Frames = frames
	pageData.FrameCount = uint64(len(frames))
	pageData.FrameShape = frameShapeLabel(frameTx)
	pageData.FrameValidationCount = validationLen

	// The envelope declares the frames; what each of them did is on the receipt, which is
	// overlaid separately and may not have been kept.
	pageData.FrameResultsMissing = true
}

// applyFrameResults overlays the result a receipt reports for each frame.
//
// The receipt reports one result per frame, so it also establishes how many frames the
// transaction had. Where the transaction envelope was unavailable - its block is no
// longer retained - the results are all there is, and the frames are raised from them so
// the page still says what ran, without the targets and budgets only the envelope holds.
func applyFrameResults(pageData *models.TransactionPageData, results []bdbtypes.FrameReceiptEntry) {
	if len(results) == 0 {
		return
	}

	for len(pageData.Frames) < len(results) {
		index := len(pageData.Frames)
		pageData.Frames = append(pageData.Frames, &models.TransactionPageDataFrame{
			Index:            uint32(index),
			ModeName:         "Unknown",
			BatchIndex:       index,
			BatchSize:        1,
			BatchFailedIndex: -1,
		})
	}

	pageData.FrameCount = uint64(len(pageData.Frames))

	for i, result := range results {
		frame := pageData.Frames[i]
		frame.Status = result.Status
		frame.StatusText = frameStatusText(result.Status, frame.RolledBack)
		frame.ExecGasUsed = result.ExecGasUsed
		frame.StateGasUsed = result.StateGasUsed

		logCount := result.LogCount
		if logCount > 0xffff {
			logCount = 0xffff
		}

		frame.LogCount = uint16(logCount)
	}

	// A frame the receipt did not reach keeps no result rather than a stale one.
	for i := len(results); i < len(pageData.Frames); i++ {
		frame := pageData.Frames[i]
		frame.Status = bdbtypes.FrameStatusUnknown
		frame.StatusText = frameStatusText(bdbtypes.FrameStatusUnknown, false)
	}

	markRolledBackFrames(pageData.Frames)
	applyFrameBodyReverted(pageData)
	summarizeFrames(pageData)

	pageData.FrameResultsMissing = false
}

// applyFrameReceiptExtra overlays the frame content a client's receipt reports.
func applyFrameReceiptExtra(pageData *models.TransactionPageData, receipt *txtypes.Receipt) {
	extra := receipt.FrameExtra()
	if extra == nil {
		return
	}

	applyFramePayer(pageData, &bdbtypes.FrameReceiptData{Payer: extra.Payer})

	results := make([]bdbtypes.FrameReceiptEntry, 0, len(extra.Frames))
	for _, frame := range extra.Frames {
		results = append(results, bdbtypes.FrameReceiptEntry{
			Status:       uint8(frame.Status),
			ExecGasUsed:  frame.ExecutionGas,
			StateGasUsed: frame.StateGas,
			LogCount:     uint32(len(frame.Logs)),
		})
	}

	applyFrameResults(pageData, results)
}

// markRolledBackFrames flags the frames whose effects did not survive the transaction.
//
// A frame's status says whether it ran, not whether what it did lasted. Two rules discard
// the effects of a frame that reports success: an atomic batch that fails is unrolled,
// and a failing POST_TX frame reverts the whole execution body. Both live in txtypes,
// which owns the rules, so the shapes and statuses the page already holds are handed back
// to it rather than the rules being restated here.
func markRolledBackFrames(frames []*models.TransactionPageDataFrame) {
	durable := frameDurability(frames)

	// A batch names what undid it, which the durability answer alone does not carry.
	undoneBy := batchFailures(frames)

	for i, frame := range frames {
		if durable[i] || uint64(frame.Status) != txtypes.FrameStatusSuccess {
			continue
		}

		frame.RolledBack = true
		frame.StatusText = frameStatusText(frame.Status, true)

		if idx, ok := undoneBy[i]; ok {
			frame.BatchFailedIndex = int(idx)
		}
	}
}

// frameDurability asks txtypes which frames' effects survived, rebuilding the shapes it
// needs from what the page holds: a frame's mode and flags decide the validation prefix,
// the atomic batches and whether a POST_TX frame is present.
//
// Frames raised from a receipt alone carry no mode or flags, which reads as a transaction
// with no prefix and no batches - and the answer is then simply whether each frame
// succeeded, which is all that can be known without the transaction.
func frameDurability(frames []*models.TransactionPageDataFrame) []bool {
	tx := &txtypes.FrameTx{Frames: make([]*txtypes.Frame, len(frames))}
	extra := &txtypes.FrameReceiptExtra{Frames: make([]*txtypes.FrameReceipt, len(frames))}

	for i, frame := range frames {
		// The batch bit is rebuilt from AtomicBatch rather than read out of Flags: the
		// two are the same fact, and taking the meaning rather than the encoding keeps
		// this right whichever of them a caller filled in.
		flags := frame.Flags & txtypes.ApproveScopeMask
		if frame.AtomicBatch {
			flags |= txtypes.AtomicBatchFlag
		}

		tx.Frames[i] = &txtypes.Frame{
			Mode:  txtypes.FrameMode(frame.Mode),
			Flags: flags,
		}
		extra.Frames[i] = &txtypes.FrameReceipt{Status: uint64(frame.Status)}
	}

	return extra.DurableFrames(tx)
}

// batchFailures maps each frame of a failed atomic batch to the frame whose failure undid
// it, so a rolled-back frame can name its cause.
func batchFailures(frames []*models.TransactionPageDataFrame) map[int]uint32 {
	undoneBy := make(map[int]uint32, len(frames))
	batchStart := 0

	for i, frame := range frames {
		if frame.AtomicBatch && i+1 < len(frames) {
			continue
		}

		batch := frames[batchStart : i+1]
		start := batchStart
		batchStart = i + 1

		for _, member := range batch {
			if uint64(member.Status) != txtypes.FrameStatusFailed {
				continue
			}

			for offset := range batch {
				undoneBy[start+offset] = member.Index
			}

			break
		}
	}

	return undoneBy
}

// frameSpecies classifies a frame for display.
//
// The species names come from the mempool's prefix-matching rules, which only decide
// anything within the validation prefix. A DEFAULT frame there deploys the sender's
// account code; the same frame after the prefix does not, and calling it a deployment
// would name it for a position it does not hold. After the prefix it is a settlement,
// which is what a DEFAULT frame is for once the operations have run.
func frameSpecies(frame *txtypes.Frame, sender common.Address, inValidationPrefix bool) txtypes.FrameSpecies {
	species := frame.Species(sender)
	if species == txtypes.SpeciesDeploy && !inValidationPrefix {
		return txtypes.SpeciesPostOp
	}

	return species
}

// frameCaller is the account a frame's call comes from.
//
// Only a SENDER frame is entered by the sender. DEFAULT and VERIFY frames are entered by
// the ENTRY_POINT predeploy, which is what lets a paymaster or a verifier run without the
// sender calling it - and why the sender does not appear as the caller of most frames.
func frameCaller(frame *txtypes.Frame, sender common.Address) common.Address {
	if frame.Mode == txtypes.FrameModeSender {
		return sender
	}

	// DEFAULT, VERIFY and POST_TX frames are all entered by the predeploy.
	return txtypes.EntryPoint
}

// executedFrames are the frames that ran: a skipped frame never did, and a frame the
// client reported no result for cannot be claimed to have.
func executedFrames(frames []*models.TransactionPageDataFrame) []*models.TransactionPageDataFrame {
	executed := make([]*models.TransactionPageDataFrame, 0, len(frames))

	for _, frame := range frames {
		switch uint64(frame.Status) {
		case txtypes.FrameStatusSuccess, txtypes.FrameStatusFailed:
			executed = append(executed, frame)
		}
	}

	return executed
}

// frameLogOwners maps each of a frame transaction's logs to the frame that emitted it.
//
// A frame transaction's logs are the per-frame lists concatenated in frame order, so the
// per-frame counts partition the flat list. A frame whose atomic batch rolled back had
// its logs discarded by the client, so it owns none of them and its count is already
// zero.
//
// The counts have to account for every log before any of them is claimed: a client that
// reported a partial set would otherwise shift every log after the gap onto the wrong
// frame, and a wrong attribution is worse than none.
func frameLogOwners(pageData *models.TransactionPageData, logCount int) []uint32 {
	if !pageData.IsFrameTx || pageData.FrameResultsMissing || logCount == 0 {
		return nil
	}

	total := 0
	for _, frame := range pageData.Frames {
		total += int(frame.LogCount)
	}

	if total != logCount {
		return nil
	}

	owners := make([]uint32, 0, total)

	for _, frame := range pageData.Frames {
		for i := 0; i < int(frame.LogCount); i++ {
			owners = append(owners, frame.Index)
		}
	}

	return owners
}

// attributeEventsToFrames marks each event with the frame that emitted it.
func attributeEventsToFrames(pageData *models.TransactionPageData) {
	owners := frameLogOwners(pageData, len(pageData.Events))
	if owners == nil {
		return
	}

	for _, event := range pageData.Events {
		if int(event.EventIndex) >= len(owners) {
			continue
		}

		event.FrameIndex = owners[event.EventIndex]
		event.HasFrame = true
	}
}

// attributeTokenTransfersToFrames marks each token transfer with the frame that made it.
//
// A transfer is a decoded log, so it belongs to whichever frame emitted that log. The
// number of logs comes from the transaction row rather than from the transfers, which are
// only the subset of logs that decoded as one.
func attributeTokenTransfersToFrames(pageData *models.TransactionPageData) {
	owners := frameLogOwners(pageData, int(pageData.EventCount))
	if owners == nil {
		return
	}

	for _, transfer := range pageData.TokenTransfers {
		if int(transfer.EventIndex) >= len(owners) {
			continue
		}

		transfer.FrameIndex = owners[transfer.EventIndex]
		transfer.HasFrame = true
	}
}

// attributeInternalTxsToFrames marks each traced call with the frame it was made from.
//
// A client that decomposes a frame transaction traces one top-level call per executed
// frame, in frame order, and the indexer stores the trace only once it has verified that
// shape. So each depth-0 call starts the next executed frame and everything below it
// belongs to that frame.
//
// Nothing specifies this - the callTracer is not part of execution-apis and EIP-8141 says
// nothing about debug tracing - so the shape is checked again here rather than assumed,
// and a trace that does not match is left unattributed.
func attributeInternalTxsToFrames(pageData *models.TransactionPageData) {
	if !pageData.IsFrameTx || len(pageData.InternalTxs) == 0 {
		return
	}

	executed := executedFrames(pageData.Frames)

	roots := 0
	for _, itx := range pageData.InternalTxs {
		if itx.Depth == 0 {
			roots++
		}
	}

	if roots == 0 || roots != len(executed) {
		return
	}

	current := -1

	for _, itx := range pageData.InternalTxs {
		if itx.Depth == 0 {
			current++
		}

		if current < 0 {
			continue
		}

		itx.FrameIndex = executed[current].Index
		itx.HasFrame = true
	}
}

// resolveFrameCalldata names what each frame calls.
//
// A frame carries its own calldata, so each one has its own method rather than the
// transaction having one - which is most of what makes a frame transaction readable:
// without it a frame is an address and a byte count.
func resolveFrameCalldata(ctx context.Context, pageData *models.TransactionPageData) {
	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()

	// One lookup for the whole transaction rather than one per frame.
	sigSet := make(map[types.TxSignatureBytes]struct{}, len(pageData.Frames))

	for _, frame := range pageData.Frames {
		if len(frame.Data) < 4 || !frame.HasTarget {
			continue
		}

		if skip, _ := utils.ShouldSkipSignatureLookup(frame.TargetAddr, false, sysContracts); skip {
			continue
		}

		var sig types.TxSignatureBytes
		copy(sig[:], frame.Data[:4])
		sigSet[sig] = struct{}{}
	}

	sigBytes := make([]types.TxSignatureBytes, 0, len(sigSet))
	for sig := range sigSet {
		sigBytes = append(sigBytes, sig)
	}

	var sigLookups map[types.TxSignatureBytes]*services.TxSignaturesLookup
	if len(sigBytes) > 0 {
		sigLookups = services.GlobalTxSignaturesService.LookupSignatures(ctx, sigBytes)
	}

	for _, frame := range pageData.Frames {
		if len(frame.Data) < 4 || !frame.HasTarget {
			continue
		}

		target := common.BytesToAddress(frame.TargetAddr)

		// The expiry verifier takes a raw deadline rather than a selector, and the page
		// already shows the decoded time.
		if target == txtypes.ExpiryVerifier {
			frame.MethodName = "expiry check"

			continue
		}

		if precompile := utils.GetPrecompileInfo(frame.TargetAddr); precompile != nil {
			frame.MethodName = precompile.Name
			frame.DecodedCalldata = utils.DecodePrecompileInput(precompile.Index, frame.Data)

			continue
		}

		if name, ok := sysContracts[target]; ok && name != "Deposit Contract" {
			frame.MethodName = name

			continue
		}

		var sig types.TxSignatureBytes
		copy(sig[:], frame.Data[:4])

		lookup, found := sigLookups[sig]
		if !found || lookup.Status != types.TxSigStatusFound {
			continue
		}

		frame.MethodName = lookup.Name
		frame.MethodSignature = lookup.Signature

		if len(frame.Data) > 4 && lookup.Signature != "" {
			frame.DecodedCalldata = utils.DecodeCalldata(lookup.Signature, frame.Data)
		}
	}
}

// summarizeFrames totals what the frames did, which is where a frame transaction's gas
// goes and how much of it ran.
func summarizeFrames(pageData *models.TransactionPageData) {
	pageData.FrameExecGasUsed = 0
	pageData.FrameStateGasUsed = 0
	pageData.FrameSuccessCount = 0
	pageData.FrameFailedCount = 0
	pageData.FrameSkippedCount = 0
	pageData.FrameRolledBackCnt = 0

	for _, frame := range pageData.Frames {
		pageData.FrameExecGasUsed += frame.ExecGasUsed
		pageData.FrameStateGasUsed += frame.StateGasUsed

		switch uint64(frame.Status) {
		case txtypes.FrameStatusSuccess:
			pageData.FrameSuccessCount++

			// Every member of a failed batch is marked rolled back, the one that failed
			// and the ones that never ran included. Only a frame that succeeded had
			// anything taken back from it.
			if frame.RolledBack {
				pageData.FrameRolledBackCnt++
			}
		case txtypes.FrameStatusFailed:
			if pageData.FrameFailedCount == 0 {
				pageData.FrameFailedIndex = frame.Index
			}

			pageData.FrameFailedCount++
		case txtypes.FrameStatusSkipped:
			pageData.FrameSkippedCount++
		}
	}

	applyFrameTxStatus(pageData)
}

// applyFrameBodyReverted records whether an assertion frame took the whole execution body
// with it, which the frame statuses alone do not say: the frames it reverted still report
// the success they earned.
func applyFrameBodyReverted(pageData *models.TransactionPageData) {
	for _, frame := range pageData.Frames {
		if frame.Mode == uint8(txtypes.FrameModePostTx) && uint64(frame.Status) == txtypes.FrameStatusFailed {
			pageData.FrameBodyReverted = true

			return
		}
	}
}

// applyFrameTxStatus states the transaction's own outcome from its frames.
//
// A frame transaction that reached the chain ran and paid: its validation prefix
// succeeded, or the transaction would be invalid and never included. Frames within it can
// still fail, and that is not the transaction reverting - nothing the other frames did
// was undone, and the fee was still owed. Reporting it as a revert would say that the
// whole thing came to nothing, which is the opposite of what happened.
//
// So it completed, and how completely is what the tooltip is for.
func applyFrameTxStatus(pageData *models.TransactionPageData) {
	if !pageData.IsFrameTx {
		return
	}

	pageData.Status = true
	pageData.RevertReason = ""

	parts := make([]string, 0, 3)

	if pageData.FrameFailedCount == 1 {
		parts = append(parts, fmt.Sprintf("frame #%d failed", pageData.FrameFailedIndex))
	} else if pageData.FrameFailedCount > 1 {
		parts = append(parts, fmt.Sprintf("%d frames failed, the first being #%d", pageData.FrameFailedCount, pageData.FrameFailedIndex))
	}

	if pageData.FrameRolledBackCnt == 1 {
		parts = append(parts, "1 succeeded but was undone with its atomic batch")
	} else if pageData.FrameRolledBackCnt > 1 {
		parts = append(parts, fmt.Sprintf("%d succeeded but were undone with their atomic batch", pageData.FrameRolledBackCnt))
	}

	if pageData.FrameSkippedCount > 0 {
		parts = append(parts, fmt.Sprintf("%d never ran", pageData.FrameSkippedCount))
	}

	if pageData.FrameBodyReverted {
		pageData.StatusText = "Reverted"
		pageData.FrameIncomplete = true
		pageData.FrameStatusDetail = fmt.Sprintf(
			"An assertion frame failed, which reverts everything the transaction did after its validation "+
				"frames - not just its own atomic batch. The transaction still reached the chain and its fee "+
				"was still owed: of its %d frames, only the validation ones left anything behind.",
			len(pageData.Frames),
		)

		return
	}

	if len(parts) == 0 {
		pageData.StatusText = "Success"
		pageData.FrameIncomplete = false
		pageData.FrameStatusDetail = "Every frame of this transaction succeeded."

		return
	}

	pageData.StatusText = "Complete"
	pageData.FrameIncomplete = true
	pageData.FrameStatusDetail = fmt.Sprintf(
		"The transaction ran and paid its fee - a frame transaction only reaches the chain once its validation frames succeed. Of its %d frames, %s. What the rest did stands.",
		len(pageData.Frames), strings.Join(parts, ", "),
	)
}

// weiToEth converts a wei amount to ether.
func weiToEth(amount *big.Int) float64 {
	if amount == nil {
		return 0
	}

	value, _ := new(big.Float).Quo(new(big.Float).SetInt(amount), big.NewFloat(1e18)).Float64()

	return value
}

// assignFrameBatches groups the frames into atomic batches so they can be shown together.
//
// A batch is a maximal run of frames in which every frame but the last carries the batch
// flag. Frames outside one each form a group of their own.
func assignFrameBatches(frames []*models.TransactionPageDataFrame) {
	batchStart := 0
	batchIndex := 0

	for i, frame := range frames {
		if frame.AtomicBatch && i+1 < len(frames) {
			continue
		}

		size := i + 1 - batchStart
		for _, member := range frames[batchStart : i+1] {
			member.BatchIndex = batchIndex
			member.BatchSize = size
		}

		frames[batchStart].IsBatchStart = true
		frame.IsBatchEnd = true

		batchStart = i + 1
		batchIndex++
	}
}

// frameShapeLabel names a frame transaction by its validation prefix, which is the
// shortest run of leading frames whose success settles who pays.
//
// Only four prefixes propagate on the public mempool, and naming them is the single most
// useful thing to say about a frame transaction. Anything else is left unnamed rather
// than guessed at.
func frameShapeLabel(tx *txtypes.FrameTx) string {
	prefixLen := tx.ValidationPrefixLength()
	if prefixLen == 0 {
		return ""
	}

	species := make([]txtypes.FrameSpecies, 0, prefixLen)

	for i := 0; i < prefixLen; i++ {
		found := tx.Frames[i].Species(tx.Sender)

		// An expiry check may lead any of the shapes and is not part of them.
		if found == txtypes.SpeciesExpiryVerify {
			continue
		}

		species = append(species, found)
	}

	deploys := false
	sponsored := false
	selfRelayed := false

	for _, s := range species {
		switch s {
		case txtypes.SpeciesDeploy:
			deploys = true
		case txtypes.SpeciesPay:
			sponsored = true
		case txtypes.SpeciesSelfVerify:
			selfRelayed = true
		}
	}

	switch {
	case deploys && sponsored:
		return "Account deployment, sponsored"
	case deploys:
		return "Account deployment"
	case sponsored:
		return "Sponsored"
	case selfRelayed:
		return "Self-relayed"
	default:
		return ""
	}
}

// applyFrameTxEnvelope fills in what only the transaction envelope carries: whether its
// nonce is an account nonce, and the deadline of an expiry check.
//
// The frames' calldata is not stored relationally, so the deadline is only available
// while the block the transaction came in is still retained.
func applyFrameTxEnvelope(pageData *models.TransactionPageData, frameTx *txtypes.FrameTx) {
	pageData.IsFrameTx = true
	pageData.NonceIsAccount = frameTx.UsesLegacyNonce()

	buildFrameSignatures(pageData, frameTx)
	buildFrameRecentRoots(pageData, frameTx)

	// Which of EIP-8141's extensions the payload used. A chain can run 8250 and 8272
	// independently, so this is a property of the transaction rather than of the chain.
	pageData.FrameExtensions = frameTx.Extensions.String()
	pageData.FrameHasKeyedNonces = frameTx.HasKeyedNonces()

	if !pageData.NonceIsAccount {
		pageData.NonceKeys = make([]*models.TransactionPageDataNonceKey, 0, len(frameTx.NonceKeys))
		for _, key := range frameTx.NonceKeys {
			if key == nil {
				continue
			}

			hex := key.Hex()
			pageData.NonceKeys = append(pageData.NonceKeys, &models.TransactionPageDataNonceKey{
				Index: uint32(len(pageData.NonceKeys)),
				Key:   hex,
				Short: shortNonceKey(hex),
			})
		}
	}

	for i, frame := range frameTx.Frames {
		deadline, ok := frame.ExpiryDeadline()
		if !ok {
			continue
		}

		pageData.HasExpiry = true
		pageData.ExpiryTime = time.Unix(int64(deadline), 0)

		if i < len(pageData.Frames) {
			pageData.Frames[i].HasExpiry = true
			pageData.Frames[i].ExpiryTime = pageData.ExpiryTime
		}

		break
	}
}

// frameSigSchemeNames name the signature schemes EIP-8141 validates.
var frameSigSchemeNames = map[uint8]string{
	uint8(txtypes.SigSchemeArbitrary): "Arbitrary",
	uint8(txtypes.SigSchemeSecp256k1): "secp256k1",
	uint8(txtypes.SigSchemeP256):      "P256",
}

// shortNonceKey abbreviates a nonce key for inline use.
//
// A key is a 256-bit identifier and applications are expected to derive it from something
// like a nullifier, so the usual case fills the full width. Sixteen of those are allowed
// in one transaction, which no line can hold; the full value stays available where the
// keys are listed.
func shortNonceKey(hex string) string {
	const inlineLimit = 15

	if len(hex) <= inlineLimit {
		return hex
	}

	return hex[:8] + "\u2026" + hex[len(hex)-4:]
}

// buildFrameSignatures lists the authorisations the protocol checked before any frame ran.
//
// A frame transaction does not recover its sender from a signature - the sender is an
// explicit field - so the list is not a formality: it is where an account other than the
// sender agrees to be charged, which is the whole mechanism behind a sponsored
// transaction and is otherwise invisible on the page.
func buildFrameSignatures(pageData *models.TransactionPageData, frameTx *txtypes.FrameTx) {
	if len(frameTx.Signatures) == 0 {
		return
	}

	signatures := make([]*models.TransactionPageDataSignature, 0, len(frameTx.Signatures))

	for i, entry := range frameTx.Signatures {
		if entry == nil {
			continue
		}

		scheme := uint8(entry.Scheme)

		sig := &models.TransactionPageDataSignature{
			Index:      uint32(i),
			Scheme:     scheme,
			SchemeName: frameSigSchemeNames[scheme],
			Msg:        entry.Msg,
			Signature:  entry.Signature,
		}

		if sig.SchemeName == "" {
			sig.SchemeName = fmt.Sprintf("scheme 0x%x", scheme)
		}

		if gas, err := entry.VerificationGas(); err == nil {
			sig.VerificationGas = gas
		}

		sig.Parts = decodeFrameSignature(entry.Scheme, entry.Signature)

		if signer, ok := entry.ResolvedSigner(frameTx.Sender); ok {
			sig.SignerAddr = signer.Bytes()
			sig.HasSigner = true
			sig.SignerIsSender = signer == frameTx.Sender
		}

		signatures = append(signatures, sig)
	}

	pageData.Signatures = signatures
}

// buildTxSignature records the single ECDSA signature every type but a frame transaction
// is signed with.
//
// Unlike a frame transaction's list, this one is not an authorisation the protocol checks
// alongside a named sender - it is what the sender is derived from, so there is no sender
// to state until it has been verified.
func buildTxSignature(pageData *models.TransactionPageData, ethTx *txtypes.Transaction) {
	v, r, sVal := ethTx.RawSignatureValues()
	if v == nil || r == nil || sVal == nil {
		return
	}

	pad := func(n *big.Int) []byte { return common.LeftPadBytes(n.Bytes(), 32) }

	sig := &models.TransactionPageDataSignature{
		Scheme:     uint8(txtypes.SigSchemeSecp256k1),
		SchemeName: "secp256k1",
		Role:       "the sender, whose address is recovered from it",
		Parts: []*models.TransactionPageDataSignaturePart{
			{Name: "v", Value: common.LeftPadBytes(v.Bytes(), 1), Note: "recovery id"},
			{Name: "r", Value: pad(r)},
			{Name: "s", Value: pad(sVal)},
		},
	}

	if len(pageData.FromAddr) > 0 {
		sig.SignerAddr = pageData.FromAddr
		sig.HasSigner = true
		sig.SignerIsSender = true
	}

	if gas, err := (&txtypes.FrameSignature{Scheme: txtypes.SigSchemeSecp256k1}).VerificationGas(); err == nil {
		sig.VerificationGas = gas
	}

	// The canonical encoding is r || s || v here, which is the order the raw bytes are
	// shown in - and the opposite of a frame transaction's entries.
	sig.Signature = append(append(pad(r), pad(sVal)...), common.LeftPadBytes(v.Bytes(), 1)...)

	pageData.Signatures = []*models.TransactionPageDataSignature{sig}
	pageData.SignaturesRecoverSender = true
}

// decodeFrameSignature splits an entry's raw bytes into the fields its scheme defines.
//
// EIP-8141 orders a secp256k1 entry as v || r || s, with v first - the opposite of
// go-ethereum's r || s || v - so the bytes cannot be read by eye without splitting them.
// Bytes that are not the length the scheme expects are left whole rather than carved up
// into fields that would be wrong.
func decodeFrameSignature(scheme txtypes.FrameSigScheme, sig []byte) []*models.TransactionPageDataSignaturePart {
	part := func(name string, value []byte, note string) *models.TransactionPageDataSignaturePart {
		return &models.TransactionPageDataSignaturePart{Name: name, Value: value, Note: note}
	}

	switch scheme {
	case txtypes.SigSchemeSecp256k1:
		if len(sig) != 65 {
			return nil
		}

		return []*models.TransactionPageDataSignaturePart{
			part("v", sig[0:1], "recovery id, 0 or 1"),
			part("r", sig[1:33], ""),
			part("s", sig[33:65], ""),
		}

	case txtypes.SigSchemeP256:
		if len(sig) != 128 {
			return nil
		}

		return []*models.TransactionPageDataSignaturePart{
			part("r", sig[0:32], ""),
			part("s", sig[32:64], ""),
			part("qx", sig[64:96], ""),
			part("qy", sig[96:128], ""),
		}

	default:
		// An arbitrary entry is witness data with no shape the protocol knows.
		return nil
	}
}

// applySignatureRoles names what each entry authorises, as far as the transaction
// itself says. The sender's entry authorises the transaction; the payer's, when the
// receipt names one that is not the sender, is what let the fee be charged elsewhere.
//
// It runs after the receipt has been read, because the payer comes from there.
func applySignatureRoles(pageData *models.TransactionPageData) {
	payer := common.BytesToAddress(pageData.PayerAddr)
	sponsored := len(pageData.PayerAddr) > 0 && !pageData.PayerIsSender

	for _, sig := range pageData.Signatures {
		if sig.Role != "" {
			continue
		}

		switch {
		case !sig.HasSigner:
			sig.Role = "witness for contract code, not checked by the protocol"
		case sig.SignerIsSender:
			sig.Role = "the sender, authorising the transaction"
		case sponsored && common.BytesToAddress(sig.SignerAddr) == payer:
			sig.Role = "the paymaster, agreeing to be charged"
		default:
			sig.Role = "a co-signer"
		}
	}
}

// buildFrameRecentRoots lists the EIP-8272 roots the transaction declared, which is what
// lets a frame read them while it runs.
func buildFrameRecentRoots(pageData *models.TransactionPageData, frameTx *txtypes.FrameTx) {
	if len(frameTx.RecentRoots) == 0 {
		return
	}

	roots := make([]*models.TransactionPageDataFrameRecentRoot, 0, len(frameTx.RecentRoots))

	for i, ref := range frameTx.RecentRoots {
		if ref == nil {
			continue
		}

		roots = append(roots, &models.TransactionPageDataFrameRecentRoot{
			Index:    uint32(i),
			SourceID: ref.SourceID.Bytes(),
			Slot:     ref.Slot,
			Root:     ref.Root.Bytes(),
		})
	}

	pageData.FrameRecentRoots = roots
}

// loadFrameReceiptFromBlockdb overlays what only a frame transaction's receipt reports:
// who settled the fee, and what each frame did.
//
// The transaction itself carries the frames' targets, values and gas budgets, so it is
// the source for those. None of this is held relationally - blockdb keeps a receipt at
// least as long as the transaction's own row survives, which makes a second copy of it
// in the database a duplicate of a longer-lived one.
func loadFrameReceiptFromBlockdb(
	ctx context.Context,
	pageData *models.TransactionPageData,
	slot uint64,
	blockRoot []byte,
	txHash []byte,
) {
	frameData := readFrameReceiptFromBlockdb(ctx, slot, blockRoot, txHash)
	if frameData == nil {
		return
	}

	applyFramePayer(pageData, frameData)
	applyFrameResults(pageData, frameData.Frames)
}

// applyFramePayer records the payer, and whether it differs from the sender.
func applyFramePayer(pageData *models.TransactionPageData, frameData *bdbtypes.FrameReceiptData) {
	payer := common.Address(frameData.Payer)
	if payer == (common.Address{}) {
		return
	}

	pageData.PayerAddr = payer.Bytes()
	pageData.PayerIsSender = common.BytesToAddress(pageData.FromAddr) == payer
}

// readFrameReceiptFromBlockdb fetches the frame content of a receipt, or nil when the
// transaction has none or the object is gone.
func readFrameReceiptFromBlockdb(
	ctx context.Context,
	slot uint64,
	blockRoot []byte,
	txHash []byte,
) *bdbtypes.FrameReceiptData {
	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsExecData() {
		return nil
	}

	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	sections, err := blockdb.GlobalBlockDb.GetExecDataTxSections(rctx, slot, blockRoot, txHash, bdbtypes.ExecDataSectionReceiptMeta)
	if err != nil || sections == nil || sections.ReceiptMetaData == nil {
		return nil
	}

	metaRaw, err := snappy.Decode(nil, sections.ReceiptMetaData)
	if err != nil {
		return nil
	}

	_, frameData, err := bdbtypes.DecodeReceiptMetaSection(metaRaw)
	if err != nil {
		return nil
	}

	return frameData
}
