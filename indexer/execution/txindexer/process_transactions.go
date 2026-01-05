package txindexer

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
)

// Token type constants
const (
	TokenTypeERC20   = 1
	TokenTypeERC721  = 2
	TokenTypeERC1155 = 3
)

// Event topic signatures for token transfers
var (
	// Transfer(address indexed from, address indexed to, uint256 value)
	// Used by ERC20 and ERC721 (same signature)
	topicTransfer = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")

	// TransferSingle(address indexed operator, address indexed from, address indexed to, uint256 id, uint256 value)
	topicTransferSingle = common.HexToHash("0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62")

	// TransferBatch(address indexed operator, address indexed from, address indexed to, uint256[] ids, uint256[] values)
	topicTransferBatch = common.HexToHash("0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb")
)

// txProcessingContext holds state for processing a block's transactions.
// This context is shared across all transactions in a block.
type txProcessingContext struct {
	ctx       context.Context
	client    *execution.Client
	indexer   *TxIndexer
	block     *BlockRef
	blockData *blockData

	// Block-level tracking of accounts and tokens (shared across all transactions)
	accounts map[common.Address]*pendingAccount
	tokens   map[common.Address]*pendingToken

	// Track sender nonces for batch update at end of block
	senderNonces map[common.Address]uint64
}

// pendingAccount represents an account that may need insertion.
type pendingAccount struct {
	account *dbtypes.ElAccount
	id      uint64 // Set from DB lookup or after insertion
	isNew   bool   // true if this account needs to be inserted
}

// pendingToken represents a token that may need insertion.
type pendingToken struct {
	token *dbtypes.ElToken
	id    uint64 // Set from DB lookup or after insertion
	isNew bool   // true if this token needs to be inserted
}

// txProcessingResult holds the result of processing a single transaction.
type txProcessingResult struct {
	transaction    *dbtypes.ElTransaction
	events         []*pendingTxEvent
	tokenTransfers []*pendingTokenTransfer
	fromAccount    *pendingAccount
	toAccount      *pendingAccount
}

// pendingTxEvent represents an event with a reference to its source account.
type pendingTxEvent struct {
	event         *dbtypes.ElTxEvent
	sourceAccount *pendingAccount
}

// pendingTokenTransfer represents a token transfer with references to its token and accounts.
type pendingTokenTransfer struct {
	transfer    *dbtypes.ElTokenTransfer
	token       *pendingToken
	fromAccount *pendingAccount
	toAccount   *pendingAccount
}

// newTxProcessingContext creates a new transaction processing context.
func newTxProcessingContext(
	ctx context.Context,
	client *execution.Client,
	indexer *TxIndexer,
	block *BlockRef,
	blockData *blockData,
) *txProcessingContext {
	return &txProcessingContext{
		ctx:          ctx,
		client:       client,
		indexer:      indexer,
		block:        block,
		blockData:    blockData,
		accounts:     make(map[common.Address]*pendingAccount, 32),
		tokens:       make(map[common.Address]*pendingToken, 16),
		senderNonces: make(map[common.Address]uint64, 32),
	}
}

// processTransaction processes a single transaction within the context.
func (ctx *txProcessingContext) processTransaction(
	tx *types.Transaction,
	receipt *types.Receipt,
) (dbCommitCallback, error) {
	result := &txProcessingResult{
		events:         make([]*pendingTxEvent, 0, len(receipt.Logs)),
		tokenTransfers: make([]*pendingTokenTransfer, 0),
	}

	txHash := tx.Hash()
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil {
		return nil, err
	}

	// 1. First ensure "from" account exists (no funder for sender)
	fromAccount := ctx.ensureAccount(from, nil, false)

	// 2. Process "to" account (funder is the "from" account)
	var toAddr common.Address
	var toAccount *pendingAccount
	isContractCreation := tx.To() == nil

	if isContractCreation {
		// Calculate contract address for contract creation
		toAddr = crypto.CreateAddress(from, tx.Nonce())
		toAccount = ctx.ensureAccount(toAddr, fromAccount, true)
	} else {
		toAddr = *tx.To()
		toAccount = ctx.ensureAccount(toAddr, fromAccount, false)
	}

	// 3. Create transaction entity
	txValue := tx.Value()
	txNonce := tx.Nonce()

	// Track sender's highest nonce for batch update
	if existingNonce, exists := ctx.senderNonces[from]; !exists || txNonce > existingNonce {
		ctx.senderNonces[from] = txNonce
	}

	// Calculate gas prices in Gwei (1 Gwei = 10^9 wei)
	gasPrice := weiToFloat(tx.GasPrice(), 9)
	tipPrice := 0.0
	if tx.GasTipCap() != nil {
		tipPrice = weiToFloat(tx.GasTipCap(), 9)
	}

	// Max fee for EIP-1559+ transactions
	maxFee := 0.0
	if tx.GasFeeCap() != nil {
		maxFee = weiToFloat(tx.GasFeeCap(), 9)
	}

	// Count blobs (EIP-4844)
	blobCount := uint32(len(tx.BlobHashes()))

	result.transaction = &dbtypes.ElTransaction{
		BlockUid:    ctx.block.BlockUID,
		TxHash:      txHash[:],
		FromID:      fromAccount.id,
		ToID:        toAccount.id,
		Nonce:       txNonce,
		Reverted:    receipt.Status == 0,
		Amount:      weiToFloat(txValue, 18), // ETH uses 18 decimals
		AmountRaw:   txValue.Bytes(),
		Data:        tx.Data(),
		GasLimit:    tx.Gas(),
		GasUsed:     receipt.GasUsed,
		GasPrice:    gasPrice,
		TipPrice:    tipPrice,
		BlobCount:   blobCount,
		BlockNumber: receipt.BlockNumber.Uint64(),
		TxType:      tx.Type(),
		TxIndex:     uint32(receipt.TransactionIndex),
		MaxFee:      maxFee,
	}

	// Store pending accounts for resolving IDs at commit time
	result.fromAccount = fromAccount
	result.toAccount = toAccount

	// 4. Process events (logs)
	for i, log := range receipt.Logs {
		event := ctx.processEvent(uint32(i), log, fromAccount)
		result.events = append(result.events, event)

		// 5. Check for token transfers
		transfers := ctx.detectTokenTransfers(uint32(i), log, fromAccount)
		result.tokenTransfers = append(result.tokenTransfers, transfers...)
	}

	// Update block stats
	ctx.blockData.Stats.Transactions++
	ctx.blockData.Stats.Events += uint32(len(result.events))
	ctx.blockData.Stats.Transfers += uint32(len(result.tokenTransfers))

	// Return commit callback
	return func(dbTx *sqlx.Tx) error {
		return ctx.commitTransaction(dbTx, result)
	}, nil
}

// ensureAccount ensures an account is tracked, checking DB only once per block.
// For new accounts, isContract determines behavior:
// - If true (contract creation tx): account is marked as contract without checking
// - If false: checks eth_getCode to determine if address is a contract
// funderAccount can be nil for accounts that don't have a known funder.
func (ctx *txProcessingContext) ensureAccount(
	address common.Address,
	funderAccount *pendingAccount,
	isContract bool,
) *pendingAccount {
	// Check if already tracked in this block
	if pending, exists := ctx.accounts[address]; exists {
		return pending
	}

	// Check if exists in database (only done once per address per block)
	existing, _ := db.GetElAccountByAddress(address[:])
	if existing != nil {
		// Account exists - track it but don't insert
		pending := &pendingAccount{
			account: existing,
			id:      existing.ID,
			isNew:   false,
		}
		ctx.accounts[address] = pending
		return pending
	}

	// For non-contract-creation transactions, check if address has code
	if !isContract {
		isContract = ctx.checkAddressIsContract(address)
	}

	// Create new account
	var funderID uint64
	if funderAccount != nil {
		funderID = funderAccount.id
	}

	pending := &pendingAccount{
		account: &dbtypes.ElAccount{
			Address:    address[:],
			FunderID:   funderID,
			Funded:     ctx.block.BlockUID,
			IsContract: isContract,
		},
		id:    0, // Will be set after insertion
		isNew: true,
	}

	ctx.accounts[address] = pending
	return pending
}

// checkAddressIsContract checks if an address has code deployed.
func (ctx *txProcessingContext) checkAddressIsContract(address common.Address) bool {
	ethClient := ctx.client.GetRPCClient().GetEthClient()
	if ethClient == nil {
		return false
	}

	code, err := ethClient.CodeAt(ctx.ctx, address, nil)
	if err != nil {
		return false
	}

	return len(code) > 0
}

// ensureToken ensures a token is tracked, checking DB only once per block.
// Always returns a pendingToken with the ID set (either from DB or to be assigned after insert).
func (ctx *txProcessingContext) ensureToken(address common.Address) *pendingToken {
	// Check if already tracked in this block
	if pending, exists := ctx.tokens[address]; exists {
		return pending
	}

	// Check if exists in database (only done once per address per block)
	existing, _ := db.GetElTokenByContract(address[:])
	if existing != nil {
		// Token exists - track it with existing ID
		pending := &pendingToken{
			token: existing,
			id:    existing.ID,
			isNew: false,
		}
		ctx.tokens[address] = pending
		return pending
	}

	// Create new token
	pending := &pendingToken{
		token: &dbtypes.ElToken{
			Contract: address[:],
			Name:     "",
			Symbol:   "",
			Decimals: 18, // Default to 18
		},
		id:    0, // Will be set after insertion
		isNew: true,
	}

	// Fetch token metadata (name, symbol, decimals) from the network
	ctx.indexer.fetchTokenMetadata(ctx.ctx, pending.token)

	ctx.tokens[address] = pending
	return pending
}

// processEvent creates an event entity from a log.
func (ctx *txProcessingContext) processEvent(index uint32, log *types.Log, funderAccount *pendingAccount) *pendingTxEvent {
	// Ensure source account exists
	sourceAccount := ctx.ensureAccount(log.Address, funderAccount, false)

	event := &dbtypes.ElTxEvent{
		BlockUid:   ctx.block.BlockUID,
		TxHash:     log.TxHash[:],
		EventIndex: index,
		SourceID:   sourceAccount.id,
		Data:       log.Data,
	}

	// Copy topics
	if len(log.Topics) > 0 {
		event.Topic1 = log.Topics[0][:]
	}
	if len(log.Topics) > 1 {
		event.Topic2 = log.Topics[1][:]
	}
	if len(log.Topics) > 2 {
		event.Topic3 = log.Topics[2][:]
	}
	if len(log.Topics) > 3 {
		event.Topic4 = log.Topics[3][:]
	}
	if len(log.Topics) > 4 {
		event.Topic5 = log.Topics[4][:]
	}

	return &pendingTxEvent{
		event:         event,
		sourceAccount: sourceAccount,
	}
}

// detectTokenTransfers checks if a log represents a token transfer.
func (ctx *txProcessingContext) detectTokenTransfers(
	eventIndex uint32,
	log *types.Log,
	funderAccount *pendingAccount,
) []*pendingTokenTransfer {
	if len(log.Topics) == 0 {
		return nil
	}

	topic0 := log.Topics[0]
	transfers := make([]*pendingTokenTransfer, 0)

	switch topic0 {
	case topicTransfer:
		// ERC20 or ERC721 Transfer
		transfer := ctx.parseERC20or721Transfer(eventIndex, log, funderAccount)
		if transfer != nil {
			transfers = append(transfers, transfer)
		}

	case topicTransferSingle:
		// ERC1155 TransferSingle
		transfer := ctx.parseERC1155TransferSingle(eventIndex, log, funderAccount)
		if transfer != nil {
			transfers = append(transfers, transfer)
		}

	case topicTransferBatch:
		// ERC1155 TransferBatch
		batchTransfers := ctx.parseERC1155TransferBatch(eventIndex, log, funderAccount)
		transfers = append(transfers, batchTransfers...)
	}

	return transfers
}

// parseERC20or721Transfer parses ERC20 or ERC721 Transfer event.
// ERC20: Transfer(address indexed from, address indexed to, uint256 value) - value in data
// ERC721: Transfer(address indexed from, address indexed to, uint256 indexed tokenId) - tokenId in topic3
func (ctx *txProcessingContext) parseERC20or721Transfer(
	eventIndex uint32,
	log *types.Log,
	funderAccount *pendingAccount,
) *pendingTokenTransfer {
	// Need at least 3 topics for ERC20/721
	if len(log.Topics) < 3 {
		return nil
	}

	from := common.BytesToAddress(log.Topics[1][:])
	to := common.BytesToAddress(log.Topics[2][:])

	// Ensure accounts exist
	fromAccount := ctx.ensureAccount(from, funderAccount, false)
	toAccount := ctx.ensureAccount(to, fromAccount, false)

	var tokenType uint8
	var amount *big.Int
	var tokenIndex []byte

	if len(log.Topics) >= 4 {
		// ERC721: tokenId is in topic3
		tokenType = TokenTypeERC721
		amount = big.NewInt(1)
		tokenIndex = log.Topics[3][:]
	} else if len(log.Data) >= 32 {
		// ERC20: value is in data
		tokenType = TokenTypeERC20
		amount = new(big.Int).SetBytes(log.Data[:32])
	} else {
		return nil
	}

	return ctx.createTokenTransfer(eventIndex, log.Address, tokenType, fromAccount, toAccount, amount, tokenIndex)
}

// parseERC1155TransferSingle parses ERC1155 TransferSingle event.
// TransferSingle(address indexed operator, address indexed from, address indexed to, uint256 id, uint256 value)
func (ctx *txProcessingContext) parseERC1155TransferSingle(
	eventIndex uint32,
	log *types.Log,
	funderAccount *pendingAccount,
) *pendingTokenTransfer {
	// Need 4 topics and at least 64 bytes of data
	if len(log.Topics) < 4 || len(log.Data) < 64 {
		return nil
	}

	from := common.BytesToAddress(log.Topics[2][:])
	to := common.BytesToAddress(log.Topics[3][:])

	// Ensure accounts exist
	fromAccount := ctx.ensureAccount(from, funderAccount, false)
	toAccount := ctx.ensureAccount(to, fromAccount, false)

	tokenIndex := log.Data[:32]
	amount := new(big.Int).SetBytes(log.Data[32:64])

	return ctx.createTokenTransfer(eventIndex, log.Address, TokenTypeERC1155, fromAccount, toAccount, amount, tokenIndex)
}

// parseERC1155TransferBatch parses ERC1155 TransferBatch event.
// TransferBatch(address indexed operator, address indexed from, address indexed to, uint256[] ids, uint256[] values)
func (ctx *txProcessingContext) parseERC1155TransferBatch(
	eventIndex uint32,
	log *types.Log,
	funderAccount *pendingAccount,
) []*pendingTokenTransfer {
	// Need 4 topics and data for arrays
	if len(log.Topics) < 4 || len(log.Data) < 128 {
		return nil
	}

	from := common.BytesToAddress(log.Topics[2][:])
	to := common.BytesToAddress(log.Topics[3][:])

	// Ensure accounts exist
	fromAccount := ctx.ensureAccount(from, funderAccount, false)
	toAccount := ctx.ensureAccount(to, fromAccount, false)

	// Parse dynamic arrays from data
	// Data layout: offset_ids (32) | offset_values (32) | ids_length | ids... | values_length | values...
	if len(log.Data) < 64 {
		return nil
	}

	idsOffset := new(big.Int).SetBytes(log.Data[:32]).Uint64()
	valuesOffset := new(big.Int).SetBytes(log.Data[32:64]).Uint64()

	if uint64(len(log.Data)) < idsOffset+32 || uint64(len(log.Data)) < valuesOffset+32 {
		return nil
	}

	idsLength := new(big.Int).SetBytes(log.Data[idsOffset : idsOffset+32]).Uint64()
	valuesLength := new(big.Int).SetBytes(log.Data[valuesOffset : valuesOffset+32]).Uint64()

	if idsLength != valuesLength || idsLength == 0 {
		return nil
	}

	// Verify we have enough data
	requiredIdsEnd := idsOffset + 32 + (idsLength * 32)
	requiredValuesEnd := valuesOffset + 32 + (valuesLength * 32)
	if uint64(len(log.Data)) < requiredIdsEnd || uint64(len(log.Data)) < requiredValuesEnd {
		return nil
	}

	transfers := make([]*pendingTokenTransfer, 0, idsLength)
	for i := uint64(0); i < idsLength; i++ {
		idStart := idsOffset + 32 + (i * 32)
		valueStart := valuesOffset + 32 + (i * 32)

		tokenIndex := log.Data[idStart : idStart+32]
		amount := new(big.Int).SetBytes(log.Data[valueStart : valueStart+32])

		// Use eventIndex + sub-index for batch transfers
		subIndex := eventIndex<<16 | uint32(i)
		transfer := ctx.createTokenTransfer(subIndex, log.Address, TokenTypeERC1155, fromAccount, toAccount, amount, tokenIndex)
		if transfer != nil {
			transfers = append(transfers, transfer)
		}
	}

	return transfers
}

// createTokenTransfer creates a pending token transfer, ensuring the token exists.
func (ctx *txProcessingContext) createTokenTransfer(
	eventIndex uint32,
	tokenAddress common.Address,
	tokenType uint8,
	fromAccount, toAccount *pendingAccount,
	amount *big.Int,
	tokenIndex []byte,
) *pendingTokenTransfer {
	// Get or create token (always returns a pendingToken)
	pendingToken := ctx.ensureToken(tokenAddress)

	transfer := &dbtypes.ElTokenTransfer{
		BlockUid:   ctx.block.BlockUID,
		TxHash:     make([]byte, 32), // Will be set in commit
		TxIdx:      eventIndex,
		TokenID:    0, // Will be set in commit from pendingToken.id
		TokenType:  tokenType,
		TokenIndex: tokenIndex,
		FromID:     fromAccount.id,
		ToID:       toAccount.id,
		Amount:     weiToFloat(amount, pendingToken.token.Decimals),
		AmountRaw:  amount.Bytes(),
	}

	return &pendingTokenTransfer{
		transfer:    transfer,
		token:       pendingToken,
		fromAccount: fromAccount,
		toAccount:   toAccount,
	}
}

// weiToFloat converts a big.Int amount to float64, dividing by 10^decimals.
func weiToFloat(amount *big.Int, decimals uint8) float64 {
	if amount == nil || amount.Sign() == 0 {
		return 0
	}

	// Convert to float and divide by 10^decimals
	amountFloat := new(big.Float).SetInt(amount)
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	result, _ := new(big.Float).Quo(amountFloat, divisor).Float64()
	return result
}

// getAccountNonceUpdates returns accounts that need their last_nonce and last_block_uid updated.
// Note: This must be called after accounts have been inserted (so IDs are resolved).
func (ctx *txProcessingContext) getAccountNonceUpdates() []*dbtypes.ElAccount {
	accounts := make([]*dbtypes.ElAccount, 0, len(ctx.senderNonces))
	for addr, nonce := range ctx.senderNonces {
		pending, exists := ctx.accounts[addr]
		if !exists || pending.id == 0 {
			continue // Skip if account not found or ID not resolved
		}
		accounts = append(accounts, &dbtypes.ElAccount{
			ID:           pending.id,
			LastNonce:    nonce,
			LastBlockUid: ctx.block.BlockUID,
		})
	}
	return accounts
}

// commitTransaction commits the transaction processing result to the database.
func (ctx *txProcessingContext) commitTransaction(dbTx *sqlx.Tx, result *txProcessingResult) error {
	// 1. Insert new accounts and get their IDs (collect from block-level map, only new ones)
	for _, pending := range ctx.accounts {
		if pending.isNew {
			id, err := db.InsertElAccount(pending.account, dbTx)
			if err != nil {
				return err
			}
			pending.id = id
			pending.account.ID = id
			pending.isNew = false // Mark as inserted to avoid duplicates
		}
	}

	// 2. Insert new tokens and get their IDs (collect from block-level map, only new ones)
	for _, pending := range ctx.tokens {
		if pending.isNew {
			id, err := db.InsertElToken(pending.token, dbTx)
			if err != nil {
				return err
			}
			pending.id = id
			pending.isNew = false // Mark as inserted to avoid duplicates
		}
	}

	// 3. Insert transaction (resolve account IDs now that they're set)
	if result.transaction != nil {
		result.transaction.FromID = result.fromAccount.id
		result.transaction.ToID = result.toAccount.id

		if err := db.InsertElTransactions([]*dbtypes.ElTransaction{result.transaction}, dbTx); err != nil {
			return err
		}
	}

	// 4. Insert events (resolve source account IDs)
	if len(result.events) > 0 {
		events := make([]*dbtypes.ElTxEvent, 0, len(result.events))
		for _, pe := range result.events {
			pe.event.SourceID = pe.sourceAccount.id
			events = append(events, pe.event)
		}

		if err := db.InsertElTxEvents(events, dbTx); err != nil {
			return err
		}
	}

	// 5. Insert token transfers with resolved token and account IDs
	if len(result.tokenTransfers) > 0 {
		transfers := make([]*dbtypes.ElTokenTransfer, 0, len(result.tokenTransfers))
		for _, pt := range result.tokenTransfers {
			// Set tx hash from result
			if result.transaction != nil {
				pt.transfer.TxHash = result.transaction.TxHash
			}

			// Resolve token ID from pendingToken (always set now)
			pt.transfer.TokenID = pt.token.id

			// Resolve account IDs
			pt.transfer.FromID = pt.fromAccount.id
			pt.transfer.ToID = pt.toAccount.id

			transfers = append(transfers, pt.transfer)
		}

		if len(transfers) > 0 {
			if err := db.InsertElTokenTransfers(transfers, dbTx); err != nil {
				return err
			}
		}
	}

	return nil
}
