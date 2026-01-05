package txindexer

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethpandaops/dora/clients/execution"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/sirupsen/logrus"
)

// fetchBlockData fetches transactions and receipts for a block with retry logic.
func (t *TxIndexer) fetchBlockData(ctx context.Context, ref *BlockRef) (*blockData, error) {
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
		return nil, fmt.Errorf("no available EL clients")
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

		// Fetch transactions if not already available from beacon block
		if transactions == nil {
			txs, bn, bh, err := t.fetchBlockTransactions(ctx, rpcClient, ref.BlockHash)
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
				return nil, nil
			}

			transactions = txs
			blockNumber = bn
			blockHash = bh
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

		// Success
		return &blockData{
			BlockNumber:  blockNumber,
			BlockHash:    blockHash,
			Transactions: transactions,
			Receipts:     receipts,
		}, nil
	}

	return nil, fmt.Errorf("all retries failed: %w", lastErr)
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
	}

	// Fall back to finalized clients
	return t.indexerCtx.GetFinalizedClients(execution.AnyClient)
}

// extractTransactionsFromBeaconBlock extracts transactions from a beacon block's execution payload.
// Returns nil if the block has no execution payload (pre-merge) or transactions cannot be extracted.
func (t *TxIndexer) extractTransactionsFromBeaconBlock(block *beacon.Block) ([]*types.Transaction, uint64, common.Hash) {
	beaconBlock := block.GetBlock()
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
) ([]*types.Transaction, uint64, common.Hash, error) {
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return nil, 0, common.Hash{}, fmt.Errorf("ethclient not available")
	}

	hash := common.BytesToHash(blockHash)

	// Use raw JSON parsing for intercompatibility
	var raw json.RawMessage
	err := ethClient.Client().CallContext(ctx, &raw, "eth_getBlockByHash", hash, true)
	if err != nil {
		return nil, 0, common.Hash{}, fmt.Errorf("eth_getBlockByHash failed: %w", err)
	}

	// Check if block exists
	if len(raw) == 0 || string(raw) == "null" {
		return nil, 0, common.Hash{}, nil
	}

	// Parse header to get block number
	var header struct {
		Number   *hexutil.Big `json:"number"`
		Hash     common.Hash  `json:"hash"`
		GasLimit *hexutil.Big `json:"gasLimit"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, 0, common.Hash{}, fmt.Errorf("unmarshal block header: %w", err)
	}

	if header.Number == nil {
		return nil, 0, common.Hash{}, fmt.Errorf("block number is nil")
	}

	// Parse transactions
	var body struct {
		Transactions []json.RawMessage `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, 0, common.Hash{}, fmt.Errorf("unmarshal block body: %w", err)
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

	return transactions, header.Number.ToInt().Uint64(), header.Hash, nil
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
