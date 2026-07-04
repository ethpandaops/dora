package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
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
		"address/internal_txs.html",
		"address/system_deposits.html",
		"address/contract.html",
		"address/transfer_filter.html",
		"_shared/pager.html",
		"_shared/el_filter_assets.html",
	)
	notfoundTemplateFiles := append(layoutTemplateFiles,
		"address/notfound.html",
	)

	// Check if execution indexer is enabled
	if !utils.Config.ExecutionIndexer.Enabled {
		data := InitPageData(w, r, "blockchain", "/address", "Feature Disabled", notfoundTemplateFiles)
		data.Data = "disabled"
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "address.go", "Address", "disabled", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	vars := mux.Vars(r)
	addressHex := strings.TrimPrefix(vars["address"], "0x")

	addressBytes, err := hex.DecodeString(addressHex)
	if err != nil || len(addressBytes) != 20 {
		data := InitPageData(w, r, "blockchain", "/address", "Address not found", notfoundTemplateFiles)
		data.Data = "invalid"
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "address.go", "Address", "invalidAddress", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
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

	// Keyset/filter params are tab-specific; pass the whole query down and key
	// the cache on it (excluding the AJAX-only "lazy" flag).
	urlArgs := r.URL.Query()
	cacheArgs := r.URL.Query()
	cacheArgs.Del("lazy")

	var pageData *models.AddressPageData
	if pageError == nil {
		pageData, pageError = getAddressPageData(addressBytes, tabView, pageIdx, pageSize, urlArgs, cacheArgs.Encode())
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

func getAddressPageData(addressBytes []byte, tabView string, pageIdx, pageSize uint64, urlArgs url.Values, cacheKeyArgs string) (*models.AddressPageData, error) {
	pageData := &models.AddressPageData{}
	pageCacheKey := fmt.Sprintf("address:%x:%v", addressBytes, cacheKeyArgs)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildAddressPageData(pageCall.CallCtx, addressBytes, tabView, pageIdx, pageSize, urlArgs)
		pageCall.CacheTimeout = cacheTimeout
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

func buildAddressPageData(ctx context.Context, addressBytes []byte, tabView string, pageIdx, pageSize uint64, urlArgs url.Values) (*models.AddressPageData, time.Duration) {
	logrus.Debugf("address page called: 0x%x (tab: %v)", addressBytes, tabView)

	// Try to get the account from the database, but don't error if not found
	// Addresses not in DB will show as empty addresses with zero balance
	account, _ := db.GetElAccountByAddress(ctx, addressBytes)
	if account == nil {
		// Create a placeholder account for display purposes
		account = &dbtypes.ElAccount{
			ID:      0,
			Address: addressBytes,
		}
	}

	// Queue balance lookups when page is viewed (rate limited to 10min per account)
	// Even for unknown addresses (ID=0), we queue to check if they have balance
	if txIndexer := services.GlobalBeaconService.GetTxIndexer(); txIndexer != nil {
		txIndexer.QueueAddressBalanceLookups(account.ID, account.Address)
	}

	chainState := services.GlobalBeaconService.GetChainState()

	pageData := &models.AddressPageData{
		Address:    account.Address,
		AccountID:  account.ID,
		IsContract: account.IsContract,
		LastNonce:  account.LastNonce,
		TabView:    tabView,
		DataRange:  getElDataRangeInfo(),
	}

	// If this address is a detected token contract, surface a link to its token page.
	if token, err := db.GetElTokenByContract(ctx, addressBytes); err == nil && token != nil {
		pageData.IsToken = true
		pageData.TokenName = token.Name
		pageData.TokenSymbol = token.Symbol
		pageData.TokenType = token.TokenType
	}

	// Get first funded info
	if account.Funded > 0 {
		pageData.FirstFunded = account.Funded >> 16 // Extract slot from block_uid
	}

	// Get funder info
	if account.FunderID > 0 {
		if funder, err := db.GetElAccountByID(ctx, account.FunderID); err == nil {
			pageData.FundedBy = funder.Address
			pageData.FundedByID = funder.ID
			pageData.FundedByIsContract = funder.IsContract
		}
	}

	if account.ID > 0 {
		// Get ETH balance (token_id = 0 is native token)
		if ethBalance, err := db.GetElBalance(ctx, account.ID, 0); err == nil {
			pageData.EthBalance = ethBalance.Balance
			pageData.EthBalanceRaw = ethBalance.BalanceRaw
		}

		// Get token balances for sidebar (limit to 50 for the sidebar)
		if balances, _, err := db.GetElBalancesByAccountID(ctx, account.ID, 0, 50); err == nil {
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
				if tokens, err := db.GetElTokensByIDs(ctx, tokenIDs); err == nil {
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

		// Check if address has any internal transactions (for tab visibility)
		if has, err := db.HasElTransactionsInternalByAccount(ctx, account.ID); err == nil && has {
			pageData.HasInternalTxs = true
		}

		// Check if address has any withdrawals (for tab visibility) - use combined accessor to include cached blocks
		if _, count := services.GlobalBeaconService.GetWithdrawalsByFilter(ctx, &dbtypes.WithdrawalFilter{
			AccountID:    &account.ID,
			WithOrphaned: 1,
		}, 0, 1); count > 0 {
			pageData.HasWithdrawals = true
		}

		// Check if address has any block fees (for tab visibility)
		if has, err := db.HasBlockFeesByAccountID(ctx, account.ID); err == nil && has {
			pageData.HasBlockFees = true
		}

		// Load tab-specific data
		switch tabView {
		case "transactions":
			loadTransactionsTab(ctx, pageData, account, chainState, pageSize, urlArgs)
		case "erc20":
			loadERC20TransfersTab(ctx, pageData, account, chainState, pageSize, urlArgs)
		case "nft":
			loadNFTTransfersTab(ctx, pageData, account, chainState, pageSize, urlArgs)
		case "internaltxs":
			loadInternalTxsTab(ctx, pageData, account, chainState, pageSize, urlArgs)
		case "withdrawals":
			loadWithdrawalsTab(ctx, pageData, account, chainState, uint32(pageSize), pageIdx)
		case "blockfees":
			loadBlockFeesTab(ctx, pageData, account, chainState, pageSize, urlArgs)
		case "contract":
			loadContractTab(ctx, pageData, account)
		default:
			loadTransactionsTab(ctx, pageData, account, chainState, pageSize, urlArgs)
		}
	}

	// Collect execution addresses shown on the page for ENS name resolution.
	ensAddrs := make([][]byte, 0, len(pageData.Transactions)*2+len(pageData.TokenBalances)+2)
	ensAddrs = append(ensAddrs, pageData.Address, pageData.FundedBy)
	for _, tb := range pageData.TokenBalances {
		ensAddrs = append(ensAddrs, tb.Contract)
	}
	for _, tx := range pageData.Transactions {
		ensAddrs = append(ensAddrs, tx.FromAddr, tx.ToAddr)
	}
	for _, t := range pageData.ERC20Transfers {
		ensAddrs = append(ensAddrs, t.FromAddr, t.ToAddr, t.Contract)
	}
	for _, t := range pageData.NFTTransfers {
		ensAddrs = append(ensAddrs, t.FromAddr, t.ToAddr, t.Contract)
	}
	ensNames := resolveEnsNames(ctx, ensAddrs)
	pageData.SetEnsNames(ensNames)
	// The page's own address is rendered full (not as a swappable link), so surface its
	// name explicitly for the header to show alongside the address.
	pageData.AddressEnsName = ensNames[strings.ToLower(common.BytesToAddress(pageData.Address).Hex())]

	return pageData, 2 * time.Minute
}

// getReadyEthClient returns an EL JSON-RPC eth client from a ready execution
// client, or nil if none is available.
func getReadyEthClient() *ethclient.Client {
	txIndexer := services.GlobalBeaconService.GetTxIndexer()
	if txIndexer == nil {
		return nil
	}
	for _, client := range txIndexer.GetReadyClients() {
		rpcClient := client.GetRPCClient()
		if rpcClient == nil {
			continue
		}
		if ethClient := rpcClient.GetEthClient(); ethClient != nil {
			return ethClient
		}
	}
	return nil
}

// loadContractTab fetches the deployed (runtime) bytecode via eth_getCode.
func loadContractTab(ctx context.Context, pageData *models.AddressPageData, account *dbtypes.ElAccount) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ethClient := getReadyEthClient()
	if ethClient == nil {
		pageData.ContractRpcUnavailable = true
		return
	}

	if code, err := ethClient.CodeAt(ctx, common.BytesToAddress(account.Address), nil); err == nil {
		pageData.ContractBytecode = code
	}
}

// parseAccountDirection maps the "dir" query value to a direction constant.
func parseAccountDirection(s string) uint8 {
	switch s {
	case "out":
		return db.AccountDirectionOut
	case "in":
		return db.AccountDirectionIn
	default:
		return db.AccountDirectionAll
	}
}

// accountDirectionStr is the inverse of parseAccountDirection.
func accountDirectionStr(d uint8) string {
	switch d {
	case db.AccountDirectionOut:
		return "out"
	case db.AccountDirectionIn:
		return "in"
	default:
		return ""
	}
}

func loadTransactionsTab(ctx context.Context, pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, pageSize uint64, urlArgs url.Values) {
	limit := uint32(pageSize)
	beforeTxUid, _, pageNum := parseElPageParam(urlArgs.Get("p"))
	direction := parseAccountDirection(urlArgs.Get("dir"))
	filterForm, filterSuffix := parseTransactionsFilterForm(urlArgs)
	filterForm.Direction = accountDirectionStr(direction)
	filter := resolveTransactionFilter(ctx, filterForm)

	dbTxs, hasNext, _ := db.GetElTransactionsByAccount(ctx, account.ID, direction, filter, beforeTxUid, limit)

	pageData.TxPageSize = pageSize
	pageData.TxFilter = filterForm

	// Keyset pager (links are relative to the address page; the tab AJAX follows them).
	suffix := "v=transactions&c=" + strconv.FormatUint(pageSize, 10)
	if d := accountDirectionStr(direction); d != "" {
		suffix += "&dir=" + d
	}
	if filterSuffix != "" {
		suffix += "&" + filterSuffix
	}
	var nextA uint64
	hasPrev, atFirst, prevA := false, true, uint64(0)
	if len(dbTxs) > 0 {
		nextA = dbTxs[len(dbTxs)-1].TxUid
		prevA, hasPrev, atFirst, _ = db.GetElTransactionsByAccountPrevAnchor(ctx, account.ID, direction, filter, dbTxs[0].TxUid, limit)
	}
	pageData.TxPager = buildElPager("", suffix, pageNum, hasNext, nextA, 0, hasPrev, atFirst, prevA, 0, false)

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
		if accounts, err := db.GetElAccountsByIDs(ctx, accountIDList); err == nil {
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
		blocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, filter, 0, uint32(len(blockUids)), 0)
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
	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()
	revertIDMap := map[uint32][]*models.AddressPageDataTransaction{}

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
			Reverted:    tx.RevertID > 0,
		}
		if tx.RevertID > 0 {
			revertIDMap[tx.RevertID] = append(revertIDMap[tx.RevertID], txData)
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
		// Deployment is flagged on tx_type at index time (raw recipient was null).
		isCreate := tx.TxType&dbtypes.ElTxFlagCreate != 0

		// Extract method ID from stored method_id field (first 4 bytes only)
		if len(tx.MethodID) >= 4 {
			txData.MethodID = tx.MethodID[:4]

			// Skip fn signature lookup for deployments, precompiles, and system contracts
			if skip, altName := utils.ShouldSkipSignatureLookup(txData.ToAddr, isCreate, sysContracts); skip {
				txData.MethodName = altName
			} else {
				// Collect function signature for lookup
				var sigBytes types.TxSignatureBytes
				copy(sigBytes[:], tx.MethodID[0:4])
				if sigLookupMap[sigBytes] == nil {
					sigLookupMap[sigBytes] = []*models.AddressPageDataTransaction{txData}
					sigLookupBytes = append(sigLookupBytes, sigBytes)
				} else {
					sigLookupMap[sigBytes] = append(sigLookupMap[sigBytes], txData)
				}
			}
		} else {
			// No data or insufficient data for method signature
			txData.MethodName = "transfer"
		}

		pageData.Transactions = append(pageData.Transactions, txData)
	}

	// Lookup function signatures
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

	// Batch-load revert reasons for the displayed reverted txs.
	if len(revertIDMap) > 0 {
		ids := make([]uint32, 0, len(revertIDMap))
		for id := range revertIDMap {
			ids = append(ids, id)
		}
		if reasons, rerr := db.GetElRevertReasonsByIDs(ctx, ids); rerr == nil {
			for id, reason := range reasons {
				for _, txData := range revertIDMap[id] {
					txData.RevertReason = reason
				}
			}
		}
	}
}

func loadERC20TransfersTab(ctx context.Context, pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, pageSize uint64, urlArgs url.Values) {
	loadTokenTransfersTab(ctx, pageData, account, chainState, pageSize, urlArgs, tokenTypeERC20, true)
}

func loadNFTTransfersTab(ctx context.Context, pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, pageSize uint64, urlArgs url.Values) {
	loadTokenTransfersTab(ctx, pageData, account, chainState, pageSize, urlArgs, 0, false) // 0 means ERC721 + ERC1155
}

func loadTokenTransfersTab(ctx context.Context, pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, pageSize uint64, urlArgs url.Values, filterTokenType uint8, isERC20 bool) {
	limit := uint32(pageSize)
	beforeTxUid, beforeTxIdx, pageNum := parseElPageParam(urlArgs.Get("p"))
	direction := parseAccountDirection(urlArgs.Get("dir"))
	filterForm, filterSuffix := parseTransfersFilterForm(urlArgs)
	filterForm.Direction = accountDirectionStr(direction)
	filter := resolveTransferFilter(ctx, filterForm)
	// Token type is fixed per tab (ERC20, or NFT = ERC721 + ERC1155).
	if filterTokenType > 0 {
		filter.TokenTypes = []uint8{filterTokenType}
	} else {
		filter.TokenTypes = []uint8{tokenTypeERC721, tokenTypeERC1155}
	}

	dbTransfers, hasNext, _ := db.GetElTokenTransfersByAccount(ctx, account.ID, direction, filter, beforeTxUid, beforeTxIdx, limit)

	// Collect IDs for batch lookup
	accountIDs := make(map[uint64]bool, len(dbTransfers)*2)
	tokenIDs := make(map[uint64]bool, len(dbTransfers))
	blockUidSet := make(map[uint64]bool, len(dbTransfers))
	blockUids := make([]uint64, 0, len(dbTransfers))
	txUidSet := make(map[uint64]bool, len(dbTransfers))
	txUids := make([]uint64, 0, len(dbTransfers))
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

	// Batch lookup accounts
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

	// Batch lookup tokens
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

	// Batch lookup blocks using GetDbBlocksByFilter (includes cached blocks)
	blockMap := make(map[uint64]*dbtypes.AssignedSlot, len(blockUids))
	if len(blockUids) > 0 {
		filter := &dbtypes.BlockFilter{
			BlockUids:    blockUids,
			WithOrphaned: 1, // Include both canonical and orphaned
		}
		blocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, filter, 0, uint32(len(blockUids)), 0)
		for _, b := range blocks {
			if b.Block != nil {
				blockMap[b.Block.BlockUid] = b
			}
		}
	}

	// Batch fetch transaction data by tx_uid for tx_hash and method signature lookup
	type txInfo struct {
		TxHash   []byte
		MethodID []byte
		ToID     uint64
	}
	txInfoMap := make(map[uint64]*txInfo, len(txUids))
	if len(txUids) > 0 {
		if txs, err := db.GetElTransactionsByTxUids(ctx, txUids); err == nil {
			for _, tx := range txs {
				txInfoMap[tx.TxUid] = &txInfo{
					TxHash:   tx.TxHash,
					MethodID: tx.MethodID,
					ToID:     tx.ToID,
				}
			}
		}
	}

	// Supplementary account lookup for transaction ToIDs not already in accountMap
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

	// Collect function signatures for batch lookup
	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()
	sigLookupBytes := []types.TxSignatureBytes{}
	sigLookupMap := make(map[types.TxSignatureBytes][]uint64) // signature -> tx_uids
	methodNameMap := make(map[uint64]string)                  // tx_uid -> method_name
	for txUid, info := range txInfoMap {
		if len(info.MethodID) < 4 {
			methodNameMap[txUid] = "transfer"
			continue
		}

		// Determine to address for skip check
		isCreate := info.ToID == 0
		var toAddr []byte
		if acc, ok := accountMap[info.ToID]; ok {
			toAddr = acc.Address
		}

		// Skip fn signature lookup for deployments, precompiles, and system contracts
		if skip, altName := utils.ShouldSkipSignatureLookup(toAddr, isCreate, sysContracts); skip {
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

	// Lookup function signatures
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

	// Build transfer list
	transfers := make([]*models.AddressPageDataTokenTransfer, 0, len(dbTransfers))
	for _, t := range dbTransfers {
		blockUid := t.TxUid >> 16
		slot := t.TxUid >> 32
		blockTime := chainState.SlotToTime(phase0.Slot(slot))

		var txHash []byte
		if info, ok := txInfoMap[t.TxUid]; ok {
			txHash = info.TxHash
		}

		transfer := &models.AddressPageDataTokenTransfer{
			TxHash:     txHash,
			BlockUid:   blockUid,
			BlockTime:  blockTime,
			FromID:     t.FromID,
			ToID:       t.ToID,
			IsOutgoing: t.FromID == account.ID,
			TokenID:    t.TokenID,
			TokenType:  t.TokenType,
			TokenIndex: t.TokenIndex,
			Amount:     t.Amount,
			AmountRaw:  t.AmountRaw,
			MethodName: methodNameMap[t.TxUid],
		}

		// Set block root and orphaned status from block lookup
		if blockInfo, ok := blockMap[blockUid]; ok && blockInfo.Block != nil {
			transfer.BlockRoot = blockInfo.Block.Root
			transfer.BlockOrphaned = blockInfo.Block.Status == dbtypes.Orphaned
			if blockInfo.Block.EthBlockNumber != nil {
				transfer.BlockNumber = *blockInfo.Block.EthBlockNumber
			}
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

	tabView := "erc20"
	if !isERC20 {
		tabView = "nft"
	}
	suffix := "v=" + tabView + "&c=" + strconv.FormatUint(pageSize, 10)
	if d := accountDirectionStr(direction); d != "" {
		suffix += "&dir=" + d
	}
	if filterSuffix != "" {
		suffix += "&" + filterSuffix
	}
	var nextA, prevA uint64
	var nextB, prevB uint32
	hasPrev, atFirst := false, true
	if len(dbTransfers) > 0 {
		last := dbTransfers[len(dbTransfers)-1]
		nextA, nextB = last.TxUid, last.TxIdx
		first := dbTransfers[0]
		prevA, prevB, hasPrev, atFirst, _ = db.GetElTokenTransfersByAccountPrevAnchor(ctx, account.ID, direction, filter, first.TxUid, first.TxIdx, limit)
	}
	pager := buildElPager("", suffix, pageNum, hasNext, nextA, nextB, hasPrev, atFirst, prevA, prevB, true)

	if isERC20 {
		pageData.ERC20Transfers = transfers
		pageData.ERC20PageSize = pageSize
		pageData.ERC20Filter = filterForm
		pageData.ERC20Pager = pager
	} else {
		pageData.NFTTransfers = transfers
		pageData.NFTPageSize = pageSize
		pageData.NFTFilter = filterForm
		pageData.NFTPager = pager
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

func loadInternalTxsTab(ctx context.Context, pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, pageSize uint64, urlArgs url.Values) {
	limit := uint32(pageSize)
	beforeTxUid, _, pageNum := parseElPageParam(urlArgs.Get("p"))
	// Get per-tx aggregate rows where this account was involved
	dbEntries, hasNext, _ := db.GetElTransactionsInternalByAccountKeyset(ctx, account.ID, beforeTxUid, limit)

	pageData.InternalTxPageSize = pageSize
	suffix := "v=internaltxs&c=" + strconv.FormatUint(pageSize, 10)
	var nextA, prevA uint64
	hasPrev, atFirst := false, true
	if len(dbEntries) > 0 {
		nextA = dbEntries[len(dbEntries)-1].TxUid
		prevA, hasPrev, atFirst, _ = db.GetElTransactionsInternalByAccountPrevAnchor(ctx, account.ID, dbEntries[0].TxUid, limit)
	}
	pageData.InternalTxPager = buildElPager("", suffix, pageNum, hasNext, nextA, 0, hasPrev, atFirst, prevA, 0, false)

	// Collect block UIDs and tx UIDs for batch lookup (account_id is implicit = page address)
	blockUidSet := make(map[uint64]bool, len(dbEntries))
	blockUids := make([]uint64, 0, len(dbEntries))
	txUidSet := make(map[uint64]bool, len(dbEntries))
	txUids := make([]uint64, 0, len(dbEntries))
	for _, e := range dbEntries {
		blockUid := e.TxUid >> 16
		if !blockUidSet[blockUid] {
			blockUidSet[blockUid] = true
			blockUids = append(blockUids, blockUid)
		}
		if !txUidSet[e.TxUid] {
			txUidSet[e.TxUid] = true
			txUids = append(txUids, e.TxUid)
		}
	}

	// Batch lookup blocks
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

	// Batch lookup tx_hash from el_transactions by tx_uid
	txHashMap := make(map[uint64][]byte, len(txUids))
	if len(txUids) > 0 {
		if txs, err := db.GetElTransactionsByTxUids(ctx, txUids); err == nil {
			for _, tx := range txs {
				txHashMap[tx.TxUid] = tx.TxHash
			}
		}
	}

	// Build internal transaction list
	pageData.InternalTxs = make([]*models.AddressPageDataInternalTransaction, 0, len(dbEntries))
	for _, e := range dbEntries {
		blockUid := e.TxUid >> 16
		slot := e.TxUid >> 32
		blockTime := chainState.SlotToTime(phase0.Slot(slot))

		itx := &models.AddressPageDataInternalTransaction{
			TxHash:    txHashMap[e.TxUid],
			BlockUid:  blockUid,
			BlockTime: blockTime,
			InCount:   e.InCount,
			OutCount:  e.OutCount,
			CallTypes: expandCallTypeMask(e.CallTypeMask),
			ValueIn:   e.ValueIn,
			ValueOut:  e.ValueOut,
			GasUsed:   e.GasUsed,
		}

		if blockInfo, ok := blockMap[blockUid]; ok && blockInfo.Block != nil {
			itx.BlockRoot = blockInfo.Block.Root
			itx.BlockOrphaned = blockInfo.Block.Status == dbtypes.Orphaned
			if blockInfo.Block.EthBlockNumber != nil {
				itx.BlockNumber = *blockInfo.Block.EthBlockNumber
			}
		}

		pageData.InternalTxs = append(pageData.InternalTxs, itx)
	}
}

// callTypeBitNames maps a CallTypeMask bit position to its display name. Keep
// in sync with exerpc.CallTypeFromString.
var callTypeBitNames = []string{
	"CALL",         // 0
	"STATICCALL",   // 1
	"DELEGATECALL", // 2
	"CREATE",       // 3
	"CREATE2",      // 4
	"SELFDESTRUCT", // 5
}

func expandCallTypeMask(mask uint16) []models.AddressPageDataInternalTransactionCallType {
	if mask == 0 {
		return nil
	}
	types := make([]models.AddressPageDataInternalTransactionCallType, 0, 4)
	for bit := uint8(0); bit < 16; bit++ {
		if mask&(1<<bit) == 0 {
			continue
		}
		name := fmt.Sprintf("TYPE_%d", bit)
		if int(bit) < len(callTypeBitNames) {
			name = callTypeBitNames[bit]
		}
		types = append(types, models.AddressPageDataInternalTransactionCallType{
			Type: bit,
			Name: name,
		})
	}
	return types
}

func loadWithdrawalsTab(ctx context.Context, pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, limit uint32, pageIdx uint64) {
	withdrawalFilter := &dbtypes.WithdrawalFilter{
		AccountID:    &account.ID,
		WithOrphaned: 1,
	}
	dbWithdrawals, totalCount := services.GlobalBeaconService.GetWithdrawalsByFilter(ctx, withdrawalFilter, pageIdx-1, limit)

	// Withdrawals come through the cache-merging beacon service (offset-based), so
	// they use the shared offset pager rather than a keyset cursor.
	pageData.WdPageSize = uint64(limit)
	totalPages := uint64(math.Ceil(float64(totalCount) / float64(limit)))
	pageData.WdPager = buildOffsetPager("", []models.UrlParam{
		{Key: "v", Value: "withdrawals"},
		{Key: "c", Value: strconv.FormatUint(uint64(limit), 10)},
	}, pageIdx, totalPages)

	// Collect block UIDs for batch lookup
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

	pageData.Withdrawals = make([]*models.AddressPageDataWithdrawal, 0, len(dbWithdrawals))
	for _, w := range dbWithdrawals {
		slot := w.BlockUid >> 16
		entry := &models.AddressPageDataWithdrawal{
			BlockUid:      w.BlockUid,
			BlockTime:     chainState.SlotToTime(phase0.Slot(slot)),
			Type:          w.Type,
			Amount:        w.Amount,
			ValidatorName: services.GlobalBeaconService.GetValidatorName(w.Validator),
		}
		if w.Validator&services.BuilderIndexFlag != 0 {
			entry.IsBuilder = true
			entry.ValidatorIndex = w.Validator &^ services.BuilderIndexFlag
		} else {
			entry.ValidatorIndex = w.Validator
		}

		if blockInfo, ok := blockMap[w.BlockUid]; ok && blockInfo.Block != nil {
			entry.BlockRoot = blockInfo.Block.Root
			entry.BlockOrphaned = blockInfo.Block.Status == dbtypes.Orphaned
			if blockInfo.Block.EthBlockNumber != nil {
				entry.BlockNumber = *blockInfo.Block.EthBlockNumber
			}
		}

		pageData.Withdrawals = append(pageData.Withdrawals, entry)
	}
}

func loadBlockFeesTab(ctx context.Context, pageData *models.AddressPageData, account *dbtypes.ElAccount, chainState *consensus.ChainState, pageSize uint64, urlArgs url.Values) {
	limit := uint32(pageSize)
	beforeBlockUid, _, pageNum := parseElPageParam(urlArgs.Get("p"))
	dbBlocks, hasNext, _ := db.GetBlockFeesByAccountKeyset(ctx, account.ID, beforeBlockUid, limit)

	pageData.BfPageSize = pageSize
	suffix := "v=blockfees&c=" + strconv.FormatUint(pageSize, 10)
	var nextA, prevA uint64
	hasPrev, atFirst := false, true
	if len(dbBlocks) > 0 {
		nextA = dbBlocks[len(dbBlocks)-1].BlockUid
		prevA, hasPrev, atFirst, _ = db.GetBlockFeesByAccountPrevAnchor(ctx, account.ID, dbBlocks[0].BlockUid, limit)
	}
	pageData.BfPager = buildElPager("", suffix, pageNum, hasNext, nextA, 0, hasPrev, atFirst, prevA, 0, false)

	// Collect block UIDs for batch lookup (to get roots)
	blockUids := make([]uint64, 0, len(dbBlocks))
	for _, b := range dbBlocks {
		blockUids = append(blockUids, b.BlockUid)
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

	pageData.BlockFees = make([]*models.AddressPageDataBlockFee, 0, len(dbBlocks))
	for _, elBlock := range dbBlocks {
		slot := elBlock.BlockUid >> 16
		entry := &models.AddressPageDataBlockFee{
			BlockUid:  elBlock.BlockUid,
			BlockTime: chainState.SlotToTime(phase0.Slot(slot)),
			Amount:    elBlock.FeeAmount,
			AmountRaw: elBlock.FeeAmountRaw,
		}

		if blockInfo, ok := blockMap[elBlock.BlockUid]; ok && blockInfo.Block != nil {
			entry.BlockRoot = blockInfo.Block.Root
			entry.BlockOrphaned = blockInfo.Block.Status == dbtypes.Orphaned
			if blockInfo.Block.EthBlockNumber != nil {
				entry.BlockNumber = *blockInfo.Block.EthBlockNumber
			}
		}

		pageData.BlockFees = append(pageData.BlockFees, entry)
	}
}
