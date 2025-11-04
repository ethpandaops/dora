package execution

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
)

// BalanceIndexer handles address balance tracking
type BalanceIndexer struct {
	indexerCtx  *IndexerCtx
	logger      logrus.FieldLogger
	updateQueue chan *balanceUpdate
}

type balanceUpdate struct {
	address     common.Address
	blockNumber uint64
}

// NewBalanceIndexer creates a new balance indexer
func NewBalanceIndexer(indexerCtx *IndexerCtx, logger logrus.FieldLogger) *BalanceIndexer {
	bi := &BalanceIndexer{
		indexerCtx:  indexerCtx,
		logger:      logger,
		updateQueue: make(chan *balanceUpdate, 10000),
	}

	// Start worker pool
	for i := 0; i < 5; i++ {
		go bi.balanceUpdateWorker()
	}

	return bi
}

// QueueUpdate queues a balance update for an address
func (bi *BalanceIndexer) QueueUpdate(address common.Address, blockNumber uint64) {
	select {
	case bi.updateQueue <- &balanceUpdate{address: address, blockNumber: blockNumber}:
	default:
		// Queue full, skip this update
	}
}

// balanceUpdateWorker processes balance updates from the queue
func (bi *BalanceIndexer) balanceUpdateWorker() {
	for update := range bi.updateQueue {
		if err := bi.updateBalance(update.address, update.blockNumber); err != nil {
			bi.logger.WithError(err).WithField("address", update.address.Hex()).Debug("Error updating balance")
		}
	}
}

// updateBalance fetches and updates the balance for an address
func (bi *BalanceIndexer) updateBalance(address common.Address, blockNumber uint64) error {
	client := bi.indexerCtx.executionPool.GetReadyEndpoint(nil)
	if client == nil {
		return nil
	}

	// Fetch balance at latest block
	ctx := context.Background()
	balance, err := client.GetRPC().BalanceAt(ctx, address, nil)
	if err != nil {
		return err
	}

	balanceBytes := balance.Bytes()
	if balanceBytes == nil {
		balanceBytes = []byte{0}
	}

	// Update in DB
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.UpdateAddressBalance(address.Bytes(), balanceBytes, blockNumber, tx)
	})
}
