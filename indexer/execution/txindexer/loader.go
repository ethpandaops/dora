package txindexer

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethpandaops/dora/clients/execution"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// fetchBlockData fetches transactions and receipts for a block with retry logic.
func (t *TxIndexer) fetchBlockData(ctx context.Context, ref *BlockRef) (*blockData, *execution.Client, error) {
	var transactions []*types.Transaction
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
				t.logger.WithError(err).WithFields(logrus.Fields{
					"client": client.GetName(),
					"retry":  retry + 1,
				}).Debug("failed to fetch transactions, retrying")
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
// Returns nil if the block has no execution payload (pre-merge) or transactions cannot be extracted.
func (t *TxIndexer) extractTransactionsFromBeaconBlock(block *beacon.Block) ([]*types.Transaction, uint64, common.Hash) {
	beaconBlock := block.GetBlock(t.ctx)
	if beaconBlock == nil {
		return nil, 0, common.Hash{}
	}

	payload, err := beaconBlock.ExecutionPayload()
	if err != nil || payload == nil {
		return nil, 0, common.Hash{}
	}

	blockNumber, err := payload.BlockNumber()
	if err != nil {
		return nil, 0, common.Hash{}
	}

	blockHash, err := payload.BlockHash()
	if err != nil {
		return nil, 0, common.Hash{}
	}

	payloadTxs, err := payload.Transactions()
	if err != nil {
		return nil, 0, common.Hash{}
	}

	transactions := make([]*types.Transaction, 0, len(payloadTxs))
	for _, txBytes := range payloadTxs {
		tx := &types.Transaction{}
		if err := tx.UnmarshalBinary(txBytes); err != nil {
			t.logger.WithError(err).Debug("failed to unmarshal transaction from beacon block")
			continue
		}
		transactions = append(transactions, tx)
	}

	return transactions, blockNumber, common.Hash(blockHash)
}

// fetchBlockTransactions fetches transactions from an EL client using raw JSON parsing
// for intercompatibility across different EL clients.
func (t *TxIndexer) fetchBlockTransactions(
	ctx context.Context,
	rpcClient *exerpc.ExecutionClient,
	blockHash []byte,
) ([]*types.Transaction, uint64, common.Hash, common.Address, []WithdrawalData, error) {
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

	// Parse header to get block number
	var header struct {
		Number      *hexutil.Big        `json:"number"`
		Hash        common.Hash         `json:"hash"`
		GasLimit    *hexutil.Big        `json:"gasLimit"`
		Coinbase    common.Address      `json:"miner"`
		Withdrawals []*types.Withdrawal `json:"withdrawals"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("unmarshal block header: %w", err)
	}

	if header.Number == nil {
		return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("block number is nil")
	}

	// Parse transactions
	var body struct {
		Transactions []json.RawMessage `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, 0, common.Hash{}, common.Address{}, nil, fmt.Errorf("unmarshal block body: %w", err)
	}

	transactions := make([]*types.Transaction, 0, len(body.Transactions))
	for idx, rawTx := range body.Transactions {
		// Check transaction type for compatibility
		var txHeader struct {
			Type hexutil.Uint64 `json:"type"`
		}

		isValid := false
		if err := json.Unmarshal(rawTx, &txHeader); err == nil {
			switch txHeader.Type {
			case types.LegacyTxType, types.AccessListTxType, types.DynamicFeeTxType,
				types.BlobTxType, types.SetCodeTxType:
				isValid = true
			}
		}

		if !isValid {
			t.logger.WithFields(logrus.Fields{
				"txIndex": idx,
				"txType":  txHeader.Type,
			}).Debug("skipping unsupported transaction type")
			continue
		}

		var tx types.Transaction
		if err := json.Unmarshal(rawTx, &tx); err != nil {
			t.logger.WithError(err).WithField("txIndex", idx).Debug("failed to unmarshal transaction")
			continue
		}

		transactions = append(transactions, &tx)
	}

	withdrawals := make([]WithdrawalData, 0, len(header.Withdrawals))
	for _, w := range header.Withdrawals {
		withdrawals = append(withdrawals, WithdrawalData{
			Index:     uint64(w.Index),
			Validator: uint64(w.Validator),
			Address:   common.Address(w.Address),
			Amount:    uint64(w.Amount), // Already in Gwei
		})
	}

	return transactions, header.Number.ToInt().Uint64(), header.Hash, header.Coinbase, withdrawals, nil
}

// fetchBlockReceipts fetches receipts for a block from an EL client.
func (t *TxIndexer) fetchBlockReceipts(
	ctx context.Context,
	rpcClient *exerpc.ExecutionClient,
	blockHash common.Hash,
) ([]*types.Receipt, error) {
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return nil, fmt.Errorf("ethclient not available")
	}

	receipts, err := ethClient.BlockReceipts(ctx, rpc.BlockNumberOrHash{
		BlockHash: &blockHash,
	})
	if err != nil {
		return nil, fmt.Errorf("BlockReceipts failed: %w", err)
	}

	return receipts, nil
}

// extractBeaconBlockData extracts fee recipient and withdrawals from beacon block.
func (t *TxIndexer) extractBeaconBlockData(block *beacon.Block) (common.Address, []WithdrawalData) {
	if block == nil {
		return common.Address{}, nil
	}

	beaconBlock := block.GetBlock(t.ctx)
	if beaconBlock == nil {
		return common.Address{}, nil
	}

	var feeRecipient common.Address
	var withdrawals []WithdrawalData

	// Extract fee recipient from execution payload
	payload, err := beaconBlock.ExecutionPayload()
	if err == nil && payload != nil {
		if recipient, err := payload.FeeRecipient(); err == nil {
			feeRecipient = common.Address(recipient)
		}

		// Extract withdrawals
		if beaconWithdrawals, err := payload.Withdrawals(); err == nil && beaconWithdrawals != nil {
			withdrawals = make([]WithdrawalData, 0, len(beaconWithdrawals))
			for _, w := range beaconWithdrawals {
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
func (t *TxIndexer) calculateTotalPriorityFees(transactions []*types.Transaction, receipts []*types.Receipt) *big.Int {
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
// Tries the primary client first, then retries with up to 2 other clients on failure.
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

	for i, client := range clients {
		results, err := client.GetRPCClient().TraceBlockByHash(ctx, blockHash)
		if err != nil {
			t.logger.WithError(err).WithFields(logrus.Fields{
				"blockHash": blockHash.Hex(),
				"client":    client.GetName(),
				"attempt":   i + 1,
			}).Debug("failed to fetch block traces, trying next client")

			continue
		}

		return results, nil
	}

	t.logger.WithField("blockHash", blockHash.Hex()).Warn(
		"all clients failed to fetch block traces, proceeding without traces",
	)

	return nil, nil
}

// fetchBlockStateDiffs fetches per-tx state diffs (storage changes) for a block
// using debug_traceBlockByHash with prestateTracer in diffMode.
// Tries the primary client first, then retries with up to 2 other clients on failure.
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

	for i, client := range clients {
		results, err := client.GetRPCClient().TraceBlockStateDiffsByHash(ctx, blockHash)
		if err != nil {
			t.logger.WithError(err).WithFields(logrus.Fields{
				"blockHash": blockHash.Hex(),
				"client":    client.GetName(),
				"attempt":   i + 1,
			}).Debug("failed to fetch block state diffs, trying next client")

			continue
		}

		return results, nil
	}

	t.logger.WithField("blockHash", blockHash.Hex()).Warn(
		"all clients failed to fetch block state diffs, proceeding without state diffs",
	)

	return nil, nil
}

// getTraceClients returns clients to try for trace fetching: the primary client
// first, then up to 2 additional clients sorted by priority.
func (t *TxIndexer) getTraceClients(
	primaryClient *execution.Client,
	ref *BlockRef,
) []*execution.Client {
	const maxTraceClients = 3

	allClients := t.getClientsForBlock(ref)

	sort.Slice(allClients, func(i, j int) bool {
		return t.indexerCtx.SortClients(allClients[i], allClients[j], false)
	})

	clients := make([]*execution.Client, 0, maxTraceClients)
	clients = append(clients, primaryClient)

	for _, c := range allClients {
		if len(clients) >= maxTraceClients {
			break
		}

		if c != primaryClient {
			clients = append(clients, c)
		}
	}

	return clients
}
