package txindexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/execution"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/sirupsen/logrus"
)

// fetchBlockData fetches transactions and receipts for a block with retry logic.
func (t *TxIndexer) fetchBlockData(ctx context.Context, ref *BlockRef) (*blockData, *execution.Client, error) {
	var transactions []*txtypes.Transaction
	var blockNumber uint64
	var blockHash common.Hash

	// Try to extract transactions from beacon block if available
	if ref.Block != nil {
		txs, bn, bh := t.extractTransactionsFromBeaconBlock(ref.Block)
		if txs != nil {
			transactions = txs
			blockNumber = bn
			blockHash = bh
		}
	}

	// Get clients for data fetching
	clients := t.getClientsForBlock(ref)
	if len(clients) == 0 {
		return nil, nil, fmt.Errorf("no available EL clients")
	}

	// Sort clients by priority
	sort.Slice(clients, func(i, j int) bool {
		return t.indexerCtx.SortClients(clients[i], clients[j], false)
	})

	var lastErr error

	// Retry loop for fetching data
	for retry := 0; retry < maxRetries; retry++ {
		// Select client (cycle through clients on retries)
		client := clients[retry%len(clients)]
		rpcClient := client.GetRPCClient()
		feeRecipient, withdrawals := t.extractBeaconBlockData(ref.Block)

		// Fetch transactions if not already available from beacon block
		if transactions == nil {
			txs, bn, bh, coinbase, wdt, err := t.fetchBlockTransactions(ctx, rpcClient, ref.BlockHash)
			if err != nil {
				lastErr = fmt.Errorf("fetch transactions from %s: %w", client.GetName(), err)
				entry := t.logger.WithError(err).WithFields(logrus.Fields{
					"client": client.GetName(),
					"retry":  retry + 1,
				})

				// A fetch that simply failed is routine and retried quietly. A client that
				// answered with a transaction it encodes differently from the canonical form
				// is a defect in that client and stays hidden unless it is reported.
				if errors.Is(err, errTxHashMismatch) {
					entry.Warn("client returned a transaction that does not match its own hash, retrying with another client")
				} else {
					entry.Debug("failed to fetch transactions, retrying")
				}

				continue
			}

			if txs == nil {
				// Block not found, might be pre-merge or not yet propagated
				return nil, nil, nil
			}

			transactions = txs
			blockNumber = bn
			blockHash = bh
			feeRecipient = coinbase
			withdrawals = wdt
		}
		// blockHash is now set either from beacon block extraction or EL fetch

		// Fetch receipts
		receipts, err := t.fetchBlockReceipts(ctx, rpcClient, blockHash)
		if err != nil {
			lastErr = fmt.Errorf("fetch receipts from %s: %w", client.GetName(), err)
			t.logger.WithError(err).WithFields(logrus.Fields{
				"client": client.GetName(),
				"retry":  retry + 1,
			}).Debug("failed to fetch receipts, retrying")
			continue
		}

		// Extract additional data from beacon block if available
		totalPriorityFees := t.calculateTotalPriorityFees(transactions, receipts)

		// Success
		return &blockData{
			BlockNumber:       blockNumber,
			BlockHash:         blockHash,
			Transactions:      transactions,
			Receipts:          receipts,
			FeeRecipient:      feeRecipient,
			Withdrawals:       withdrawals,
			TotalPriorityFees: totalPriorityFees,
		}, client, nil
	}

	return nil, nil, fmt.Errorf("all retries failed: %w", lastErr)
}

// getClientsForBlock returns appropriate EL clients for fetching block data.
// For blocks with known fork ID, use clients on that fork.
// For historical/unknown blocks, use finalized clients.
func (t *TxIndexer) getClientsForBlock(ref *BlockRef) []*execution.Client {
	if ref.Block != nil {
		forkID := ref.Block.GetForkId()
		clients := t.indexerCtx.GetClientsOnFork(forkID, execution.AnyClient)
		if len(clients) > 0 {
			return clients
		}

		// use clients that saw the block (match by name)
		clClients := ref.Block.GetSeenBy()
		if len(clClients) > 0 {
			allElClients := t.indexerCtx.ExecutionPool.GetAllEndpoints()
			elClientMap := make(map[string]*execution.Client)
			for _, client := range allElClients {
				elClientMap[client.GetName()] = client
			}

			elClients := make([]*execution.Client, 0, len(clClients))
			for _, client := range clClients {
				elClient, ok := elClientMap[client.GetClient().GetName()]
				if ok {
					elClients = append(elClients, elClient)
				}
			}

			if len(elClients) > 0 {
				return elClients
			}
		}
	}

	// Fall back to finalized clients
	return t.indexerCtx.GetFinalizedClients(execution.AnyClient)
}

// extractTransactionsFromBeaconBlock extracts transactions from a beacon block's execution payload.
// Returns nil if the block has no execution payload (pre-merge) or if any transaction in the
// payload could not be decoded, in which case the caller fetches the block from an EL client
// instead.
func (t *TxIndexer) extractTransactionsFromBeaconBlock(block *beacon.Block) ([]*txtypes.Transaction, uint64, common.Hash) {
	beaconBlock := block.GetBlock(t.ctx)
	if beaconBlock == nil || beaconBlock.Message == nil || beaconBlock.Message.Body == nil {
		return nil, 0, common.Hash{}
	}

	payload := beaconBlock.Message.Body.ExecutionPayload
	if payload == nil {
		return nil, 0, common.Hash{}
	}

	transactions := make([]*txtypes.Transaction, 0, len(payload.Transactions))
	for idx, txBytes := range payload.Transactions {
		tx, err := txtypes.DecodeTx(txBytes)
		if err != nil {
			// The payload carries a transaction this build cannot parse from its wire
			// bytes. Keeping the rest would index the block short of transactions, so
			// the payload is discarded in favour of asking an EL client, where a type
			// with no decoder still yields the generic fields the node reports.
			t.logger.WithError(err).WithFields(logrus.Fields{
				"blockNumber": payload.BlockNumber,
				"txIndex":     idx,
			}).Debug("cannot decode transaction from beacon block, falling back to EL client")

			return nil, 0, common.Hash{}
		}

		transactions = append(transactions, tx)
	}

	return transactions, payload.BlockNumber, common.Hash(payload.BlockHash)
}

// fetchBlockTransactions fetches transactions from an EL client using raw JSON parsing
// for intercompatibility across different EL clients.
func (t *TxIndexer) fetchBlockTransactions(
	ctx context.Context,
	rpcClient *exerpc.ExecutionClient,
	blockHash []byte,
) ([]*txtypes.Transaction, uint64, common.Hash, common.Address, []WithdrawalData, error) {
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("ethclient not available")
	}

	hash := common.BytesToHash(blockHash)

	// Use raw JSON parsing for intercompatibility
	var raw json.RawMessage
	err := ethClient.Client().CallContext(ctx, &raw, "eth_getBlockByHash", hash, true)
	if err != nil {
		return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("eth_getBlockByHash failed: %w", err)
	}

	// Check if block exists
	if len(raw) == 0 || string(raw) == "null" {
		return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("block not found")
	}

	// Parse header fields and transactions in a single pass to avoid
	// unmarshaling the (potentially large) raw JSON twice.
	var block struct {
		Number       *hexutil.Big        `json:"number"`
		Hash         common.Hash         `json:"hash"`
		GasLimit     *hexutil.Big        `json:"gasLimit"`
		Coinbase     common.Address      `json:"miner"`
		Withdrawals  []*types.Withdrawal `json:"withdrawals"`
		Transactions []json.RawMessage   `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("unmarshal block: %w", err)
	}

	// Free the raw JSON now that we've parsed what we need.
	raw = nil

	if block.Number == nil {
		return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("block number is nil")
	}

	transactions := make([]*txtypes.Transaction, 0, len(block.Transactions))
	for idx, rawTx := range block.Transactions {
		tx, err := decodeBlockTransaction(rawTx)
		if err != nil {
			// A client whose transaction encoding disagrees with the canonical one cannot
			// be trusted for the rest of the block either, so the whole response is
			// rejected and the caller retries against another client.
			if errors.Is(err, errTxHashMismatch) {
				return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("block %s transaction %d: %w", hash.Hex(), idx, err)
			}

			t.logger.WithError(err).WithFields(logrus.Fields{
				"blockHash": hash.Hex(),
				"txIndex":   idx,
			}).Debug("skipping transaction")

			continue
		}

		transactions = append(transactions, tx)
	}

	withdrawals := make([]WithdrawalData, 0, len(block.Withdrawals))
	for _, w := range block.Withdrawals {
		withdrawals = append(withdrawals, WithdrawalData{
			Index:     uint64(w.Index),
			Validator: uint64(w.Validator),
			Address:   common.Address(w.Address),
			Amount:    uint64(w.Amount), // Already in Gwei
		})
	}

	return transactions, block.Number.ToInt().Uint64(), block.Hash, block.Coinbase, withdrawals, nil
}

// errTxHashMismatch marks a transaction that does not survive the round trip through
// the client's JSON representation.
var errTxHashMismatch = errors.New("transaction does not match the hash reported for it")

// decodeBlockTransaction rebuilds a transaction from one entry of an eth_getBlockBy*
// response.
//
// The decoder adopts the hash the client reported, so the decoded fields are re-encoded
// here to find out whether they agree with it. When they do not, the client's encoding of
// the transaction disagrees with the canonical one and everything derived from the decoded
// fields is wrong with it: the hash, the sender recovered from the signature, and whether
// the transaction is a contract creation. Such a transaction is rejected rather than
// indexed, because the corruption is silent otherwise and produces plausible-looking rows
// that belong to no real transaction.
//
// A transaction of a type this build has no decoder for carries only the generic fields
// the node reported and cannot be re-encoded. It is taken at its word, which keeps it
// counted and indexed instead of shifting every receipt index behind it.
func decodeBlockTransaction(rawTx json.RawMessage) (*txtypes.Transaction, error) {
	tx, err := txtypes.UnmarshalJSONTx(rawTx)
	if err != nil {
		return nil, fmt.Errorf("unmarshal transaction: %w", err)
	}

	encoded, err := tx.MarshalBinary()
	if err != nil {
		if errors.Is(err, txtypes.ErrTxTypeNotSupported) {
			return tx, nil
		}

		return nil, fmt.Errorf("re-encode transaction: %w", err)
	}

	if derived := crypto.Keccak256Hash(encoded); derived != tx.Hash() {
		return nil, fmt.Errorf("%w: reported %s, decodes to %s", errTxHashMismatch, tx.Hash().Hex(), derived.Hex())
	}

	return tx, nil
}

// fetchBlockReceipts fetches receipts for a block from an EL client.
//
// The response is decoded from raw JSON rather than through the typed ethclient so that
// type-specific receipt content survives: an EIP-8141 frame transaction reports its result
// per frame, and names the payer that actually settled it, neither of which a
// go-ethereum receipt can hold.
func (t *TxIndexer) fetchBlockReceipts(
	ctx context.Context,
	rpcClient *exerpc.ExecutionClient,
	blockHash common.Hash,
) ([]*txtypes.Receipt, error) {
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return nil, fmt.Errorf("ethclient not available")
	}

	var raw json.RawMessage
	if err := ethClient.Client().CallContext(ctx, &raw, "eth_getBlockReceipts", blockHash); err != nil {
		return nil, fmt.Errorf("eth_getBlockReceipts failed: %w", err)
	}

	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("block receipts not found")
	}

	receipts := []*txtypes.Receipt{}
	if err := json.Unmarshal(raw, &receipts); err != nil {
		return nil, fmt.Errorf("unmarshal block receipts: %w", err)
	}

	return receipts, nil
}

// extractBeaconBlockData extracts fee recipient and withdrawals from beacon block.
func (t *TxIndexer) extractBeaconBlockData(block *beacon.Block) (common.Address, []WithdrawalData) {
	if block == nil {
		return common.Address{}, nil
	}

	beaconBlock := block.GetBlock(t.ctx)
	if beaconBlock == nil || beaconBlock.Message == nil || beaconBlock.Message.Body == nil {
		return common.Address{}, nil
	}

	var feeRecipient common.Address
	var withdrawals []WithdrawalData

	// Extract fee recipient from execution payload
	if payload := beaconBlock.Message.Body.ExecutionPayload; payload != nil {
		feeRecipient = common.Address(payload.FeeRecipient)

		if len(payload.Withdrawals) > 0 {
			withdrawals = make([]WithdrawalData, 0, len(payload.Withdrawals))
			for _, w := range payload.Withdrawals {
				withdrawals = append(withdrawals, WithdrawalData{
					Index:     uint64(w.Index),
					Validator: uint64(w.ValidatorIndex),
					Address:   common.Address(w.Address),
					Amount:    uint64(w.Amount), // Already in Gwei
				})
			}
		}
	}

	return feeRecipient, withdrawals
}

// calculateTotalPriorityFees calculates the total priority fees paid in the block.
func (t *TxIndexer) calculateTotalPriorityFees(transactions []*txtypes.Transaction, receipts []*txtypes.Receipt) *big.Int {
	if len(transactions) != len(receipts) {
		return big.NewInt(0)
	}

	totalPriorityFees := big.NewInt(0)

	for i, tx := range transactions {
		receipt := receipts[i]
		if receipt.TxHash != tx.Hash() {
			// Receipts and transactions don't match, skip calculation
			continue
		}

		// Calculate priority fee = min(tip, gasFeeCap - baseFee) * gasUsed
		// For legacy transactions, priority fee = 0
		if tx.GasTipCap() != nil && tx.GasFeeCap() != nil {
			// EIP-1559+ transaction
			tipCap := tx.GasTipCap()

			// Priority fee per gas = min(tipCap, gasFeeCap - baseFee)
			// Since we don't have baseFee here, we use the effective gas price from receipt
			// effectiveGasPrice = baseFee + min(tipCap, gasFeeCap - baseFee)
			// So: priorityFeePerGas = effectiveGasPrice - baseFee
			// But without baseFee, we approximate with tipCap (which is the max priority fee)
			priorityFeePerGas := new(big.Int).Set(tipCap)

			// Total priority fee for this transaction
			txPriorityFee := new(big.Int).Mul(priorityFeePerGas, big.NewInt(int64(receipt.GasUsed)))
			totalPriorityFees.Add(totalPriorityFees, txPriorityFee)
		}
	}

	return totalPriorityFees
}

// fetchBlockTraces fetches call traces for a block using debug_traceBlockByHash.
// Tries the primary client first (unless Besu), then other clients in priority order.
// Besu clients are de-prioritized due to high resource usage for trace calls.
// Returns nil (no error) if traces are not configured or if all clients fail,
// allowing the block to proceed with events only.
func (t *TxIndexer) fetchBlockTraces(
	ctx context.Context,
	primaryClient *execution.Client,
	ref *BlockRef,
	blockHash common.Hash,
) ([]exerpc.CallTraceResult, error) {
	if !utils.Config.ExecutionIndexer.TracesEnabled {
		return nil, nil
	}

	clients := t.getTraceClients(primaryClient, ref)

	var lastErr error

	for i, client := range clients {
		results, err := client.GetRPCClient().TraceBlockByHash(
			ctx, blockHash, bdbtypes.TracePayloadLimit,
		)
		if err == nil {
			return results, nil
		}

		lastErr = err

		t.logger.WithError(err).WithFields(logrus.Fields{
			"blockHash": blockHash.Hex(),
			"client":    client.GetName(),
			"attempt":   i + 1,
		}).Debug("failed to fetch block traces, trying next client")

		if !shouldRetryOnOtherClient(ctx, err) {
			break
		}
	}

	t.logger.WithError(lastErr).WithField("blockHash", blockHash.Hex()).Warn(
		"could not fetch block traces, proceeding without traces",
	)

	return nil, nil
}

// shouldRetryOnOtherClient reports whether a failed tracer call is worth
// repeating against another client. A cancelled context means the deadline
// shared by all attempts is already gone, and a decode failure means the
// response arrived but could not be parsed - the next client returns the same
// shape and would only burn another timeout on it.
func shouldRetryOnOtherClient(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}

	var decodeErr *exerpc.ResponseDecodeError

	return !errors.As(err, &decodeErr)
}

// fetchBlockStateDiffs fetches per-tx state diffs (storage changes) for a block
// using debug_traceBlockByHash with prestateTracer in diffMode.
// Tries the primary client first (unless Besu), then other clients in priority order.
// Besu clients are de-prioritized due to high resource usage for trace calls.
// Returns nil (no error) if traces are not configured or if all clients fail,
// allowing the block to proceed without state diffs.
func (t *TxIndexer) fetchBlockStateDiffs(
	ctx context.Context,
	primaryClient *execution.Client,
	ref *BlockRef,
	blockHash common.Hash,
) ([]exerpc.StateDiffResult, error) {
	if !utils.Config.ExecutionIndexer.TracesEnabled {
		return nil, nil
	}

	clients := t.getTraceClients(primaryClient, ref)

	var lastErr error

	for i, client := range clients {
		results, err := client.GetRPCClient().TraceBlockStateDiffsByHash(ctx, blockHash)
		if err == nil {
			return results, nil
		}

		lastErr = err

		t.logger.WithError(err).WithFields(logrus.Fields{
			"blockHash": blockHash.Hex(),
			"client":    client.GetName(),
			"attempt":   i + 1,
		}).Debug("failed to fetch block state diffs, trying next client")

		if !shouldRetryOnOtherClient(ctx, err) {
			break
		}
	}

	t.logger.WithError(lastErr).WithField("blockHash", blockHash.Hex()).Warn(
		"could not fetch block state diffs, proceeding without state diffs",
	)

	return nil, nil
}

// getTraceClients returns clients to try for trace fetching.
// The primary client is preferred first (since it already has block data loaded),
// unless it's a Besu client - in which case it's used as the last option.
// Besu clients are de-prioritized due to high resource usage for trace calls.
func (t *TxIndexer) getTraceClients(
	primaryClient *execution.Client,
	ref *BlockRef,
) []*execution.Client {
	const maxTraceClients = 3

	allClients := t.getClientsForBlock(ref)

	// Separate clients into non-Besu and Besu lists (excluding primaryClient).
	// Besu clients are de-prioritized for trace calls due to high resource usage.
	nonBesuClients := make([]*execution.Client, 0, len(allClients))
	besuClients := make([]*execution.Client, 0)

	for _, c := range allClients {
		if c == primaryClient {
			continue
		}

		if c.GetClientType() == execution.BesuClient {
			besuClients = append(besuClients, c)
		} else {
			nonBesuClients = append(nonBesuClients, c)
		}
	}

	// Sort each list by priority.
	sort.Slice(nonBesuClients, func(i, j int) bool {
		return t.indexerCtx.SortClients(nonBesuClients[i], nonBesuClients[j], false)
	})
	sort.Slice(besuClients, func(i, j int) bool {
		return t.indexerCtx.SortClients(besuClients[i], besuClients[j], false)
	})

	// Build result list based on whether primaryClient is Besu or not.
	// Primary client is always included since it's the only one guaranteed to have the block.
	clients := make([]*execution.Client, 0, maxTraceClients)
	primaryIsBesu := primaryClient.GetClientType() == execution.BesuClient

	// If primary is not Besu, use it first.
	if !primaryIsBesu {
		clients = append(clients, primaryClient)
	}

	// Determine how many slots to fill before adding primary (if Besu).
	// Reserve one slot for primary if it's Besu so it's always included.
	fillLimit := maxTraceClients
	if primaryIsBesu {
		fillLimit = maxTraceClients - 1
	}

	// Add non-Besu clients.
	for _, c := range nonBesuClients {
		if len(clients) >= fillLimit {
			break
		}

		clients = append(clients, c)
	}

	// Add Besu clients (excluding primary - handled separately).
	for _, c := range besuClients {
		if len(clients) >= fillLimit {
			break
		}

		clients = append(clients, c)
	}

	// If primary is Besu, add it as the last option.
	if primaryIsBesu {
		clients = append(clients, primaryClient)
	}

	return clients
}
