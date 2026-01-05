package handlers

import (
	"encoding/hex"
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
	"github.com/ethpandaops/dora/types/models"
)

const (
	defaultAddressPageSize = 25
	maxAddressPageSize     = 100
	tokenTypeERC20         = 1
)

// Address handles the /address/{address} page
func Address(w http.ResponseWriter, r *http.Request) {
	addressTemplateFiles := append(layoutTemplateFiles,
		"address/address.html",
		"address/transactions.html",
		"address/erc20_transfers.html",
		"address/nft_transfers.html",
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
	if balances, count, err := db.GetElBalancesByAccountID(account.ID, 0, 50); err == nil {
		pageData.TokenBalanceCount = count

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

		// Build token balance list
		pageData.TokenBalances = make([]*models.AddressPageDataTokenBalance, 0, len(balances))
		for _, b := range balances {
			if b.TokenID == 0 {
				continue // Skip native token in token list
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
	default:
		loadTransactionsTab(pageData, account, chainState, offset, uint32(pageSize), pageIdx)
	}

	return pageData, 2 * time.Minute
}

func loadTransactionsTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64) {
	// Get sent transactions
	sentTxs, sentCount, _ := db.GetElTransactionsByAccountID(account.ID, true, 0, 10000)
	// Get received transactions
	recvTxs, recvCount, _ := db.GetElTransactionsByAccountID(account.ID, false, 0, 10000)

	// Merge and sort by block_uid descending (we'll use a combined approach with pagination)
	totalCount := sentCount + recvCount
	pageData.TransactionCount = totalCount
	pageData.TxPageIndex = pageIdx
	pageData.TxPageSize = uint64(limit)
	pageData.TxTotalPages = uint64(math.Ceil(float64(totalCount) / float64(limit)))
	pageData.TxFirstItem = (pageIdx-1)*uint64(limit) + 1
	pageData.TxLastItem = min(pageIdx*uint64(limit), totalCount)

	// For simplicity, we'll fetch both directions with pagination
	// This is a simplified approach - for better performance, consider a combined DB query
	var txs []*dbtypes.ElTransaction
	var txDirections []bool // true = outgoing

	// Collect all from both directions into a combined list
	for _, tx := range sentTxs {
		txs = append(txs, tx)
		txDirections = append(txDirections, true)
	}
	for _, tx := range recvTxs {
		txs = append(txs, tx)
		txDirections = append(txDirections, false)
	}

	// Sort by block_uid descending
	for i := 0; i < len(txs)-1; i++ {
		for j := i + 1; j < len(txs); j++ {
			if txs[j].BlockUid > txs[i].BlockUid {
				txs[i], txs[j] = txs[j], txs[i]
				txDirections[i], txDirections[j] = txDirections[j], txDirections[i]
			}
		}
	}

	// Apply pagination
	start := int(offset)
	end := start + int(limit)
	if start > len(txs) {
		start = len(txs)
	}
	if end > len(txs) {
		end = len(txs)
	}
	paginatedTxs := txs[start:end]
	paginatedDirections := txDirections[start:end]

	// Collect account IDs for batch lookup
	accountIDs := make(map[uint64]bool)
	for _, tx := range paginatedTxs {
		accountIDs[tx.FromID] = true
		accountIDs[tx.ToID] = true
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

	// Build transaction list
	pageData.Transactions = make([]*models.AddressPageDataTransaction, 0, len(paginatedTxs))
	for i, tx := range paginatedTxs {
		slot := tx.BlockUid >> 16
		blockTime := chainState.SlotToTime(phase0.Slot(slot))

		// Calculate tx fee: gasUsed * gasPrice (gasPrice is in Gwei)
		txFee := float64(tx.GasUsed) * tx.GasPrice / 1e9

		txData := &models.AddressPageDataTransaction{
			TxHash:      tx.TxHash,
			BlockNumber: tx.BlockNumber,
			BlockTime:   blockTime,
			FromID:      tx.FromID,
			ToID:        tx.ToID,
			IsOutgoing:  paginatedDirections[i],
			Nonce:       tx.Nonce,
			Amount:      tx.Amount,
			AmountRaw:   tx.AmountRaw,
			TxFee:       txFee,
			Reverted:    tx.Reverted,
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
		}

		pageData.Transactions = append(pageData.Transactions, txData)
	}
}

func loadERC20TransfersTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64) {
	loadTokenTransfersTab(pageData, account, chainState, offset, limit, pageIdx, tokenTypeERC20, true)
}

func loadNFTTransfersTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64) {
	loadTokenTransfersTab(pageData, account, chainState, offset, limit, pageIdx, 0, false) // 0 means ERC721 + ERC1155
}

func loadTokenTransfersTab(pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, offset uint64, limit uint32, pageIdx uint64, filterTokenType uint8, isERC20 bool) {
	// Get sent token transfers
	sentTransfers, sentCount, _ := db.GetElTokenTransfersByAccountID(account.ID, true, 0, 10000)
	// Get received token transfers
	recvTransfers, recvCount, _ := db.GetElTokenTransfersByAccountID(account.ID, false, 0, 10000)

	// Merge all transfers
	var allTransfers []*dbtypes.ElTokenTransfer
	var transferDirections []bool

	for _, t := range sentTransfers {
		// Filter by token type
		if filterTokenType > 0 && t.TokenType != filterTokenType {
			continue
		}
		if filterTokenType == 0 && t.TokenType == tokenTypeERC20 {
			continue // For NFT tab, skip ERC20
		}
		allTransfers = append(allTransfers, t)
		transferDirections = append(transferDirections, true)
	}
	for _, t := range recvTransfers {
		// Filter by token type
		if filterTokenType > 0 && t.TokenType != filterTokenType {
			continue
		}
		if filterTokenType == 0 && t.TokenType == tokenTypeERC20 {
			continue // For NFT tab, skip ERC20
		}
		allTransfers = append(allTransfers, t)
		transferDirections = append(transferDirections, false)
	}

	// Sort by block_uid descending
	for i := 0; i < len(allTransfers)-1; i++ {
		for j := i + 1; j < len(allTransfers); j++ {
			if allTransfers[j].BlockUid > allTransfers[i].BlockUid {
				allTransfers[i], allTransfers[j] = allTransfers[j], allTransfers[i]
				transferDirections[i], transferDirections[j] = transferDirections[j], transferDirections[i]
			}
		}
	}

	totalCount := uint64(len(allTransfers))

	// Apply pagination
	start := int(offset)
	end := start + int(limit)
	if start > len(allTransfers) {
		start = len(allTransfers)
	}
	if end > len(allTransfers) {
		end = len(allTransfers)
	}
	paginatedTransfers := allTransfers[start:end]
	paginatedDirections := transferDirections[start:end]

	// Collect IDs for batch lookup
	accountIDs := make(map[uint64]bool)
	tokenIDs := make(map[uint64]bool)
	for _, t := range paginatedTransfers {
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

	// Build transfer list
	transfers := make([]*models.AddressPageDataTokenTransfer, 0, len(paginatedTransfers))
	for i, t := range paginatedTransfers {
		slot := t.BlockUid >> 16
		blockTime := chainState.SlotToTime(phase0.Slot(slot))

		transfer := &models.AddressPageDataTokenTransfer{
			TxHash:     t.TxHash,
			BlockTime:  blockTime,
			FromID:     t.FromID,
			ToID:       t.ToID,
			IsOutgoing: paginatedDirections[i],
			TokenID:    t.TokenID,
			TokenType:  t.TokenType,
			TokenIndex: t.TokenIndex,
			Amount:     t.Amount,
			AmountRaw:  t.AmountRaw,
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

	if isERC20 {
		pageData.ERC20Transfers = transfers
		pageData.ERC20TransferCount = totalCount
		if filterTokenType > 0 {
			pageData.ERC20TransferCount = totalCount
		} else {
			pageData.ERC20TransferCount = sentCount + recvCount
		}
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
