package handlers

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
)

const (
	defaultAddressPageSize = 25
	maxAddressPageSize     = 100
	tokenTypeERC20         = 1
	tokenTypeERC721        = 2
	tokenTypeERC1155       = 3
)

// Address handles the /address/{address} page
func Address(w http.ResponseWriter, r *http.Request) {
	addressTemplateFiles := append(layoutTemplateFiles,
		"address/address.html",
		"address/transactions.html",
		"address/erc20_transfers.html",
		"address/nft_transfers.html",
		"address/system_deposits.html",
	)
	notfoundTemplateFiles := append(layoutTemplateFiles,
		"address/notfound.html",
	)

	vars := mux.Vars(r)
	addressHex := strings.TrimPrefix(vars["address"], "0x")

	addressBytes, err := hex.DecodeString(addressHex)
	if err != nil || len(addressBytes) != 20 {
		data := InitPageData(w, r, "blockchain", "/address", "Address not found", notfoundTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "address.go", "Address", "invalidAddress", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	// Try to get the account from the database, but don't error if not found
	// Addresses not in DB will show as empty addresses with zero balance
	account, _ := db.GetElAccountByAddress(addressBytes)
	if account == nil {
		// Create a placeholder account for display purposes
		account = &dbtypes.ElAccount{
			ID:      0,
			Address: addressBytes,
		}
	}

	tabView := "transactions"
	if r.URL.Query().Has("v") {
		tabView = r.URL.Query().Get("v")
	}

	pageIdx := uint64(1)
	if r.URL.Query().Has("p") {
		if pi, err := strconv.ParseUint(r.URL.Query().Get("p"), 10, 64); err == nil && pi > 0 {
			pageIdx = pi
		}
	}

	pageSize := uint64(defaultAddressPageSize)
	if r.URL.Query().Has("c") {
		if ps, err := strconv.ParseUint(r.URL.Query().Get("c"), 10, 64); err == nil && ps > 0 {
			pageSize = ps
			if pageSize > maxAddressPageSize {
				pageSize = maxAddressPageSize
			}
		}
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)

	var pageData *models.AddressPageData
	if pageError == nil {
		pageData, pageError = getAddressPageData(account, tabView, pageIdx, pageSize)
	}

	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	pageTemplate := templates.GetTemplate(addressTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/address", fmt.Sprintf("Address %s", common.BytesToAddress(addressBytes).Hex()), addressTemplateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		handleTemplateError(w, r, "address.go", "Address", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "address.go", "Address", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

// AddressBalances handles the /address/{address}/balances AJAX endpoint
// Returns JSON balance data for auto-refresh functionality
func AddressBalances(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	addressHex := strings.TrimPrefix(vars["address"], "0x")

	addressBytes, err := hex.DecodeString(addressHex)
	if err != nil || len(addressBytes) != 20 {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	// Rate limit check
	if err := services.GlobalCallRateLimiter.CheckCallLimit(r, 1); err != nil {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	// Try to get the account from the database
	account, _ := db.GetElAccountByAddress(addressBytes)
	if account == nil {
		// Create a placeholder account
		account = &dbtypes.ElAccount{
			ID:      0,
			Address: addressBytes,
		}
	}

	// Queue balance lookups (rate limited to 10min per account)
	if account.ID > 0 {
		if txIndexer := services.GlobalBeaconService.GetTxIndexer(); txIndexer != nil {
			txIndexer.QueueAddressBalanceLookups(account.ID, account.Address)
		}
	}

	// Build response
	response := buildBalancesResponse(account)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode balance response")
	}
}

// AddressBalancesResponse is the JSON response for the balances endpoint
type AddressBalancesResponse struct {
	EthBalance    float64                        `json:"eth_balance"`
	EthBalanceRaw string                         `json:"eth_balance_raw"`
	TokenBalances []*AddressBalanceTokenResponse `json:"token_balances"`
}

// AddressBalanceTokenResponse represents a single token balance in the response
type AddressBalanceTokenResponse struct {
	TokenID    uint64  `json:"token_id"`
	Contract   string  `json:"contract"`
	Name       string  `json:"name"`
	Symbol     string  `json:"symbol"`
	Decimals   uint8   `json:"decimals"`
	Balance    float64 `json:"balance"`
	BalanceRaw string  `json:"balance_raw"`
}

func buildBalancesResponse(account *dbtypes.ElAccount) *AddressBalancesResponse {
	response := &AddressBalancesResponse{
		TokenBalances: make([]*AddressBalanceTokenResponse, 0),
	}

	if account.ID == 0 {
		return response
	}

	// Get ETH balance (token_id = 0 is native token)
	if ethBalance, err := db.GetElBalance(account.ID, 0); err == nil {
		response.EthBalance = ethBalance.Balance
		response.EthBalanceRaw = hex.EncodeToString(ethBalance.BalanceRaw)
	}

	// Get token balances
	if balances, _, err := db.GetElBalancesByAccountID(account.ID, 0, 50); err == nil {
		// Collect token IDs for batch lookup
		tokenIDs := make([]uint64, 0, len(balances))
		for _, b := range balances {
			if b.TokenID > 0 { // Skip native token
				tokenIDs = append(tokenIDs, b.TokenID)
			}
		}

		// Batch lookup tokens
		tokenMap := make(map[uint64]*dbtypes.ElToken)
		if len(tokenIDs) > 0 {
			if tokens, err := db.GetElTokensByIDs(tokenIDs); err == nil {
				for _, t := range tokens {
					tokenMap[t.ID] = t
				}
			}
		}

		// Build token balance list (skip zero balances)
		for _, b := range balances {
			if b.TokenID == 0 {
				continue // Skip native token in token list
			}
			if b.Balance == 0 {
				continue // Skip zero balances
			}
			tb := &AddressBalanceTokenResponse{
				TokenID:    b.TokenID,
				Balance:    b.Balance,
				BalanceRaw: hex.EncodeToString(b.BalanceRaw),
			}
			if token, ok := tokenMap[b.TokenID]; ok {
				tb.Contract = hex.EncodeToString(token.Contract)
				tb.Name = token.Name
				tb.Symbol = token.Symbol
				tb.Decimals = token.Decimals
			}
			response.TokenBalances = append(response.TokenBalances, tb)
		}
	}

	return response
}

func getAddressPageData(account *dbtypes.ElAccount, tabView string, pageIdx, pageSize uint64) (*models.AddressPageData, error) {
	pageData := &models.AddressPageData{}
	pageCacheKey := fmt.Sprintf("address:%v:%v:%v:%v", account.ID, tabView, pageIdx, pageSize)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildAddressPageData(account, tabView, pageIdx, pageSize)
		_ = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.AddressPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildAddressPageData(account *dbtypes.ElAccount, tabView string, pageIdx, pageSize uint64) (*models.AddressPageData, time.Duration) {
	logrus.Debugf("address page called: %v (tab: %v)", account.ID, tabView)

	// Queue balance lookups when page is viewed (rate limited to 10min per account)
	if account.ID > 0 {
		if txIndexer := services.GlobalBeaconService.GetTxIndexer(); txIndexer != nil {
			txIndexer.QueueAddressBalanceLookups(account.ID, account.Address)
		}
	}

	chainState := services.GlobalBeaconService.GetChainState()

	pageData := &models.AddressPageData{
		Address:    account.Address,
		AccountID:  account.ID,
		IsContract: account.IsContract,
		LastNonce:  account.LastNonce,
		TabView:    tabView,
	}

	// Get first funded info
	if account.Funded > 0 {
		pageData.FirstFunded = account.Funded >> 16 // Extract slot from block_uid
	}

	// Get funder info
	if account.FunderID > 0 {
		if funder, err := db.GetElAccountByID(account.FunderID); err == nil {
			pageData.FundedBy = funder.Address
			pageData.FundedByID = funder.ID
			pageData.FundedByIsContract = funder.IsContract
		}
	}

	// Get ETH balance (token_id = 0 is native token)
	if ethBalance, err := db.GetElBalance(account.ID, 0); err == nil {
		pageData.EthBalance = ethBalance.Balance
		pageData.EthBalanceRaw = ethBalance.BalanceRaw
	}

	// Get token balances for sidebar (limit to 50 for the sidebar)
	if balances, _, err := db.GetElBalancesByAccountID(account.ID, 0, 50); err == nil {
		// Collect token IDs for batch lookup (skip native token)
		tokenIDs := make([]uint64, 0, len(balances))
		for _, b := range balances {
			if b.TokenID > 0 { // Skip native token
				tokenIDs = append(tokenIDs, b.TokenID)
			}
		}

		// Batch lookup tokens
		tokenMap := make(map[uint64]*dbtypes.ElToken)
		if len(tokenIDs) > 0 {
			if tokens, err := db.GetElTokensByIDs(tokenIDs); err == nil {
				for _, t := range tokens {
					tokenMap[t.ID] = t
				}
			}
		}

		// Build token balance list (skip zero balances)
		pageData.TokenBalances = make([]*models.AddressPageDataTokenBalance, 0, len(balances))
		for _, b := range balances {
			if b.TokenID == 0 {
				continue // Skip native token in token list
			}
			if b.Balance == 0 {
				continue // Skip zero balances
			}
			tb := &models.AddressPageDataTokenBalance{
				TokenID:    b.TokenID,
				Balance:    b.Balance,
				BalanceRaw: b.BalanceRaw,
			}
			if token, ok := tokenMap[b.TokenID]; ok {
				tb.Contract = token.Contract
				tb.Name = token.Name
				tb.Symbol = token.Symbol
				tb.Decimals = token.Decimals
			}
			pageData.TokenBalances = append(pageData.TokenBalances, tb)
		}
		// Update count to reflect actual non-zero tokens displayed
		pageData.TokenBalanceCount = uint64(len(pageData.TokenBalances))
	}

	// Check if address has any system deposits (for tab visibility)
	if account.ID > 0 {
		if _, count, err := db.GetElWithdrawalsByAccountID(account.ID, 0, 1); err == nil && count > 0 {
			pageData.HasSystemDeposits = true
		}
	}

	offset := (pageIdx - 1) * pageSize

	// Load tab-specific data
	switch tabView {
	case "transactions":
		loadTransactionsTab(pageData, account, chainState, offset, uint32(pageSize), pageIdx)
	case "erc20":
		loadERC20TransfersTab(pageData, account, chainState, offset, uint32(pageSize), pageIdx)
	case "nft":
		loadNFTTransfersTab(pageData, account, chainState, offset, uint32(pageSize), pageIdx)
	case "system":
		loadSystemDepositsTab(pageData, account, chainState, offset, uint32(pageSize), pageIdx)
	default:
		loadTransactionsTab(pageData, account, chainState, offset, uint32(pageSize), pageIdx)
	}

	return pageData, 2 * time.Minute
}

func loadTransactionsTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64) {
	// Get transactions using combined query (sorted by block_uid DESC, tx_index DESC)
	dbTxs, totalCount, _ := db.GetElTransactionsByAccountIDCombined(account.ID, offset, limit)

	pageData.TransactionCount = totalCount
	pageData.TxPageIndex = pageIdx
	pageData.TxPageSize = uint64(limit)
	pageData.TxTotalPages = uint64(math.Ceil(float64(totalCount) / float64(limit)))
	pageData.TxFirstItem = (pageIdx-1)*uint64(limit) + 1
	pageData.TxLastItem = min(pageIdx*uint64(limit), totalCount)

	// Collect account IDs and block UIDs for batch lookup
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

	// Batch lookup blocks using GetDbBlocksByFilter (includes cached blocks)
	blockMap := make(map[uint64]*dbtypes.AssignedSlot, len(blockUids))
	if len(blockUids) > 0 {
		filter := &dbtypes.BlockFilter{
			BlockUids:    blockUids,
			WithOrphaned: 1, // Include both canonical and orphaned
		}
		blocks := services.GlobalBeaconService.GetDbBlocksByFilter(filter, 0, uint32(len(blockUids)), 0)
		for _, b := range blocks {
			if b.Block != nil {
				blockMap[b.Block.BlockUid] = b
			}
		}
	}

	// Build transaction list and collect function signatures
	pageData.Transactions = make([]*models.AddressPageDataTransaction, 0, len(dbTxs))
	sigLookupBytes := []types.TxSignatureBytes{}
	sigLookupMap := map[types.TxSignatureBytes][]*models.AddressPageDataTransaction{}

	for _, tx := range dbTxs {
		slot := tx.BlockUid >> 16
		blockTime := chainState.SlotToTime(phase0.Slot(slot))

		// Calculate tx fee: gasUsed * gasPrice (gasPrice is in Gwei)
		txFee := float64(tx.GasUsed) * tx.GasPrice / 1e9

		txData := &models.AddressPageDataTransaction{
			TxHash:      tx.TxHash,
			BlockNumber: tx.BlockNumber,
			BlockUid:    tx.BlockUid,
			BlockTime:   blockTime,
			FromID:      tx.FromID,
			ToID:        tx.ToID,
			IsOutgoing:  tx.FromID == account.ID,
			Nonce:       tx.Nonce,
			Amount:      tx.Amount,
			AmountRaw:   tx.AmountRaw,
			TxFee:       txFee,
			Reverted:    tx.Reverted,
		}

		// Set block root and orphaned status from block lookup
		if blockInfo, ok := blockMap[tx.BlockUid]; ok && blockInfo.Block != nil {
			txData.BlockRoot = blockInfo.Block.Root
			txData.BlockOrphaned = blockInfo.Block.Status == dbtypes.Orphaned
		}

		// Set addresses from account map
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

		// Extract method ID from data
		if len(tx.Data) >= 4 {
			txData.MethodID = tx.Data[:4]

			// Collect function signature for lookup
			var sigBytes types.TxSignatureBytes
			copy(sigBytes[:], tx.Data[0:4])
			if sigLookupMap[sigBytes] == nil {
				sigLookupMap[sigBytes] = []*models.AddressPageDataTransaction{txData}
				sigLookupBytes = append(sigLookupBytes, sigBytes)
			} else {
				sigLookupMap[sigBytes] = append(sigLookupMap[sigBytes], txData)
			}
		} else {
			// No data or insufficient data for method signature
			txData.MethodName = "transfer"
		}

		pageData.Transactions = append(pageData.Transactions, txData)
	}

	// Lookup function signatures
	if len(sigLookupBytes) > 0 {
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures(sigLookupBytes)
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
}

func loadERC20TransfersTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64) {
	loadTokenTransfersTab(pageData, account, chainState, offset, limit, pageIdx, tokenTypeERC20, true)
}

func loadNFTTransfersTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64) {
	loadTokenTransfersTab(pageData, account, chainState, offset, limit, pageIdx, 0, false) // 0 means ERC721 + ERC1155
}

func loadTokenTransfersTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64, filterTokenType uint8, isERC20 bool) {
	// Build token type filter
	var tokenTypes []uint8
	if filterTokenType > 0 {
		tokenTypes = []uint8{filterTokenType}
	} else {
		// NFT tab: ERC721 + ERC1155
		tokenTypes = []uint8{tokenTypeERC721, tokenTypeERC1155}
	}

	// Get token transfers using combined query (sorted by block_uid DESC, tx_pos DESC, tx_idx DESC)
	dbTransfers, totalCount, _ := db.GetElTokenTransfersByAccountIDCombined(account.ID, tokenTypes, offset, limit)

	// Collect IDs for batch lookup
	accountIDs := make(map[uint64]bool, len(dbTransfers)*2)
	tokenIDs := make(map[uint64]bool, len(dbTransfers))
	for _, t := range dbTransfers {
		accountIDs[t.FromID] = true
		accountIDs[t.ToID] = true
		tokenIDs[t.TokenID] = true
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

	// Batch lookup tokens
	tokenIDList := make([]uint64, 0, len(tokenIDs))
	for id := range tokenIDs {
		tokenIDList = append(tokenIDList, id)
	}
	tokenMap := make(map[uint64]*dbtypes.ElToken, len(tokenIDList))
	if len(tokenIDList) > 0 {
		if tokens, err := db.GetElTokensByIDs(tokenIDList); err == nil {
			for _, t := range tokens {
				tokenMap[t.ID] = t
			}
		}
	}

	// Collect unique transaction hashes for method signature lookup
	txHashSet := make(map[string]bool)
	for _, t := range dbTransfers {
		txHashKey := string(t.TxHash)
		txHashSet[txHashKey] = true
	}

	// Look up transaction data for method signatures
	txHashList := make([][]byte, 0, len(txHashSet))
	for txHashKey := range txHashSet {
		txHashList = append(txHashList, []byte(txHashKey))
	}

	// Get transaction data for signature lookup
	txDataMap := make(map[string][]byte) // tx_hash -> tx_data
	if len(txHashList) > 0 {
		for _, txHash := range txHashList {
			if txs, err := db.GetElTransactionsByHash(txHash); err == nil && len(txs) > 0 {
				txDataMap[string(txHash)] = txs[0].Data
			}
		}
	}

	// Collect function signatures for batch lookup
	sigLookupBytes := []types.TxSignatureBytes{}
	sigLookupMap := make(map[types.TxSignatureBytes][]string) // signature -> tx_hashes
	for txHashKey, txData := range txDataMap {
		if len(txData) >= 4 {
			var sigBytes types.TxSignatureBytes
			copy(sigBytes[:], txData[0:4])
			if sigLookupMap[sigBytes] == nil {
				sigLookupMap[sigBytes] = []string{txHashKey}
				sigLookupBytes = append(sigLookupBytes, sigBytes)
			} else {
				sigLookupMap[sigBytes] = append(sigLookupMap[sigBytes], txHashKey)
			}
		}
	}

	// Lookup function signatures
	methodNameMap := make(map[string]string) // tx_hash -> method_name
	if len(sigLookupBytes) > 0 {
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures(sigLookupBytes)
		for sigBytes, sigLookup := range sigLookups {
			methodName := "call?"
			if sigLookup.Status == types.TxSigStatusFound {
				methodName = sigLookup.Name
			}
			for _, txHashKey := range sigLookupMap[sigBytes] {
				methodNameMap[txHashKey] = methodName
			}
		}
	}

	// Set default method name for transactions without data
	for txHashKey, txData := range txDataMap {
		if len(txData) < 4 && methodNameMap[txHashKey] == "" {
			methodNameMap[txHashKey] = "transfer"
		}
	}

	// Build transfer list
	transfers := make([]*models.AddressPageDataTokenTransfer, 0, len(dbTransfers))
	for _, t := range dbTransfers {
		slot := t.BlockUid >> 16
		blockTime := chainState.SlotToTime(phase0.Slot(slot))

		transfer := &models.AddressPageDataTokenTransfer{
			TxHash:     t.TxHash,
			BlockTime:  blockTime,
			FromID:     t.FromID,
			ToID:       t.ToID,
			IsOutgoing: t.FromID == account.ID,
			TokenID:    t.TokenID,
			TokenType:  t.TokenType,
			TokenIndex: t.TokenIndex,
			Amount:     t.Amount,
			AmountRaw:  t.AmountRaw,
			MethodName: methodNameMap[string(t.TxHash)],
		}

		// Set addresses from account map
		if from, ok := accountMap[t.FromID]; ok {
			transfer.FromAddr = from.Address
			transfer.FromIsContract = from.IsContract
		}
		if to, ok := accountMap[t.ToID]; ok {
			transfer.ToAddr = to.Address
			transfer.ToIsContract = to.IsContract
		}

		// Set token info
		if token, ok := tokenMap[t.TokenID]; ok {
			transfer.Contract = token.Contract
			transfer.TokenName = token.Name
			transfer.TokenSymbol = token.Symbol
			transfer.Decimals = token.Decimals
		}

		transfers = append(transfers, transfer)
	}

	// Calculate TxHashRowspan for consecutive transfers with same txhash
	calculateTxHashRowspans(transfers)

	if isERC20 {
		pageData.ERC20Transfers = transfers
		pageData.ERC20TransferCount = totalCount
		pageData.ERC20PageIndex = pageIdx
		pageData.ERC20PageSize = uint64(limit)
		pageData.ERC20TotalPages = uint64(math.Ceil(float64(totalCount) / float64(limit)))
		pageData.ERC20FirstItem = (pageIdx-1)*uint64(limit) + 1
		pageData.ERC20LastItem = min(pageIdx*uint64(limit), totalCount)
	} else {
		pageData.NFTTransfers = transfers
		pageData.NFTTransferCount = totalCount
		pageData.NFTPageIndex = pageIdx
		pageData.NFTPageSize = uint64(limit)
		pageData.NFTTotalPages = uint64(math.Ceil(float64(totalCount) / float64(limit)))
		pageData.NFTFirstItem = (pageIdx-1)*uint64(limit) + 1
		pageData.NFTLastItem = min(pageIdx*uint64(limit), totalCount)
	}
}

// calculateTxHashRowspans sets TxHashRowspan for consecutive transfers with the same txhash.
// The first occurrence gets the rowspan count, subsequent occurrences get 0 (meaning skip rendering the cell).
func calculateTxHashRowspans(transfers []*models.AddressPageDataTokenTransfer) {
	if len(transfers) == 0 {
		return
	}

	// Process from the end to count consecutive same txhashes
	i := len(transfers) - 1
	for i >= 0 {
		// Find the start of the current txhash group
		currentTxHash := transfers[i].TxHash
		groupStart := i

		// Look backwards to find all consecutive rows with the same txhash
		for groupStart > 0 && bytes.Equal(transfers[groupStart-1].TxHash, currentTxHash) {
			groupStart--
		}

		// Calculate rowspan for this group
		rowspan := i - groupStart + 1

		// Set rowspan on first row of group, 0 on subsequent rows
		transfers[groupStart].TxHashRowspan = rowspan
		for j := groupStart + 1; j <= i; j++ {
			transfers[j].TxHashRowspan = 0
		}

		// Move to the previous group
		i = groupStart - 1
	}
}

func loadSystemDepositsTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64) {
	// Get system deposits (withdrawals and fee recipient rewards)
	dbDeposits, totalCount, _ := db.GetElWithdrawalsByAccountID(account.ID, offset, limit)

	pageData.SystemDepositCount = totalCount
	pageData.SystemPageIndex = pageIdx
	pageData.SystemPageSize = uint64(limit)
	pageData.SystemTotalPages = uint64(math.Ceil(float64(totalCount) / float64(limit)))
	pageData.SystemFirstItem = (pageIdx-1)*uint64(limit) + 1
	pageData.SystemLastItem = min(pageIdx*uint64(limit), totalCount)

	// Collect block UIDs for batch lookup
	blockUids := make([]uint64, 0, len(dbDeposits))
	blockUidSet := make(map[uint64]bool, len(dbDeposits))
	for _, deposit := range dbDeposits {
		if !blockUidSet[deposit.BlockUid] {
			blockUidSet[deposit.BlockUid] = true
			blockUids = append(blockUids, deposit.BlockUid)
		}
	}

	// Batch lookup blocks using GetDbBlocksByFilter (includes cached blocks)
	blockMap := make(map[uint64]*dbtypes.AssignedSlot, len(blockUids))
	if len(blockUids) > 0 {
		filter := &dbtypes.BlockFilter{
			BlockUids:    blockUids,
			WithOrphaned: 1, // Include both canonical and orphaned
		}
		blocks := services.GlobalBeaconService.GetDbBlocksByFilter(filter, 0, uint32(len(blockUids)), 0)
		for _, b := range blocks {
			if b.Block != nil {
				blockMap[b.Block.BlockUid] = b
			}
		}
	}

	// Build system deposits list
	pageData.SystemDeposits = make([]*models.AddressPageDataSystemDeposit, 0, len(dbDeposits))
	for _, deposit := range dbDeposits {
		slot := deposit.BlockUid >> 16
		blockTime := chainState.SlotToTime(phase0.Slot(slot))

		systemDeposit := &models.AddressPageDataSystemDeposit{
			BlockUid:  deposit.BlockUid,
			BlockTime: blockTime,
			Type:      deposit.Type,
			Amount:    deposit.Amount,
			AmountRaw: deposit.AmountRaw,
			Validator: deposit.Validator,
		}

		// Set block info from block lookup
		if blockInfo, ok := blockMap[deposit.BlockUid]; ok && blockInfo.Block != nil {
			systemDeposit.BlockRoot = blockInfo.Block.Root
			systemDeposit.BlockOrphaned = blockInfo.Block.Status == dbtypes.Orphaned
			if blockInfo.Block.EthBlockNumber != nil {
				systemDeposit.BlockNumber = *blockInfo.Block.EthBlockNumber
			}
		}

		pageData.SystemDeposits = append(pageData.SystemDeposits, systemDeposit)
	}
}
