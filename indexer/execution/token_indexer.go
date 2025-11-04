package execution

import (
	"bytes"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
)

// TokenIndexer handles token contract and transfer indexing
type TokenIndexer struct {
	indexerCtx *IndexerCtx
	logger     logrus.FieldLogger

	// Known event signatures
	transferEventSig common.Hash // Transfer(address,address,uint256)
	approvalEventSig common.Hash // Approval(address,address,uint256)
}

// NewTokenIndexer creates a new token indexer
func NewTokenIndexer(indexerCtx *IndexerCtx, logger logrus.FieldLogger) *TokenIndexer {
	return &TokenIndexer{
		indexerCtx:       indexerCtx,
		logger:           logger,
		transferEventSig: crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")),
		approvalEventSig: crypto.Keccak256Hash([]byte("Approval(address,address,uint256)")),
	}
}

// ProcessLogs processes logs for token-related events
func (ti *TokenIndexer) ProcessLogs(logs []*types.Log, txHash common.Hash, block *types.Block, forkId uint64) error {
	for _, log := range logs {
		if len(log.Topics) == 0 {
			continue
		}

		topic0 := log.Topics[0]

		// Check if this is a Transfer event
		if bytes.Equal(topic0.Bytes(), ti.transferEventSig.Bytes()) {
			if err := ti.processTransferEvent(log, txHash, block, forkId); err != nil {
				ti.logger.WithError(err).Error("Error processing transfer event")
			}
		}

		// Discover token contracts
		if err := ti.discoverTokenContract(log.Address, block.NumberU64()); err != nil {
			ti.logger.WithError(err).Debug("Error discovering token contract")
		}
	}

	return nil
}

// processTransferEvent processes a Transfer event
func (ti *TokenIndexer) processTransferEvent(log *types.Log, txHash common.Hash, block *types.Block, forkId uint64) error {
	if len(log.Topics) < 3 {
		return nil
	}

	// Topics: [Transfer, from, to]
	// Data: [value] for ERC20 or [tokenId] for ERC721
	from := common.BytesToAddress(log.Topics[1].Bytes())
	to := common.BytesToAddress(log.Topics[2].Bytes())

	// Assume ERC20 for now (can be enhanced with contract detection)
	tokenType := "ERC20"

	transfer := &dbtypes.ElTokenTransfer{
		TransactionHash: txHash.Bytes(),
		BlockNumber:     block.NumberU64(),
		BlockTimestamp:  block.Time(),
		LogIndex:        uint(log.Index),
		TokenAddress:    log.Address.Bytes(),
		TokenType:       tokenType,
		FromAddress:     from.Bytes(),
		ToAddress:       to.Bytes(),
		Value:           log.Data,
		ForkId:          forkId,
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertElTokenTransfers([]*dbtypes.ElTokenTransfer{transfer}, tx)
	})
}

// discoverTokenContract attempts to identify and save token contract metadata
func (ti *TokenIndexer) discoverTokenContract(address common.Address, blockNumber uint64) error {
	// Check if we already know this token
	if _, err := db.GetElTokenContract(address.Bytes()); err == nil {
		return nil // Already known
	}

	// Create a basic token entry
	// Full metadata fetching (name, symbol, decimals) can be added later
	timestamp := uint64(time.Now().Unix())
	token := &dbtypes.ElTokenContract{
		Address:             address.Bytes(),
		TokenType:           "ERC20", // Default assumption
		DiscoveredBlock:     blockNumber,
		DiscoveredTimestamp: timestamp,
		LastUpdatedBlock:    blockNumber,
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.UpsertElTokenContract(token, tx)
	})
}
