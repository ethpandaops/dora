package txindexer

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

const (
	// maxRetries is the number of retries for data fetching.
	maxRetries = 3
)

// blockData holds the fetched EL block data.
type blockData struct {
	BlockNumber  uint64
	BlockHash    common.Hash
	Transactions []*types.Transaction
	Receipts     []*types.Receipt
	Stats        struct {
		Events       uint32
		Transactions uint32
		Transfers    uint32
	}
}

type dbCommitCallback func(tx *sqlx.Tx) error

// processElBlock processes a single block reference for EL transaction indexing.
func (t *TxIndexer) processElBlock(ref *BlockRef) error {
	t1 := time.Now()
	defer func() {
		// sleep to keep the processing rate at max 1 block per second to avoid overwhelming the db
		if diff := time.Since(t1); diff < time.Second {
			time.Sleep(time.Second - diff)
		}
	}()

	ctx, cancel := context.WithTimeout(t.ctx, 60*time.Second)
	defer cancel()

	// Fetch block data (transactions and receipts)
	data, err := t.fetchBlockData(ctx, ref)
	if err != nil {
		return fmt.Errorf("failed to fetch block data: %w", err)
	}

	if data == nil {
		// No data to process (e.g., pre-merge block)
		t.logger.WithFields(logrus.Fields{
			"slot":      ref.Slot,
			"blockUid":  ref.BlockUID,
			"blockHash": fmt.Sprintf("%x", ref.BlockHash),
		}).Debug("no EL data for block")
		return nil
	}

	t.logger.WithFields(logrus.Fields{
		"slot":         ref.Slot,
		"blockUid":     ref.BlockUID,
		"blockHash":    data.BlockHash.Hex(),
		"blockNumber":  data.BlockNumber,
		"transactions": len(data.Transactions),
		"receipts":     len(data.Receipts),
	}).Debug("fetched EL block data")

	receiptIdx := 0
	dbCommitCallbacks := make([]dbCommitCallback, 0, len(data.Transactions))
	for _, tx := range data.Transactions {
		var receipt *types.Receipt
		for receiptIdx < len(data.Receipts) {
			receipt = data.Receipts[receiptIdx]

			if receipt.TxHash == tx.Hash() {
				break
			}
			receiptIdx++
		}
		if receipt == nil {
			break
		}

		dbCommitCallback, err := t.processElTransaction(ref, tx, receipt)
		if err != nil {
			return fmt.Errorf("failed to process EL transaction: %w", err)
		}
		if dbCommitCallback != nil {
			dbCommitCallbacks = append(dbCommitCallbacks, dbCommitCallback)
		}
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		for _, dbCommitCallback := range dbCommitCallbacks {
			err := dbCommitCallback(tx)
			if err != nil {
				return err
			}
		}

		db.InsertElBlock(&dbtypes.ElBlock{
			BlockUid:     ref.BlockUID,
			Status:       0x01,
			Events:       data.Stats.Events,
			Transactions: data.Stats.Transactions,
			Transfers:    data.Stats.Transfers,
		}, tx)
		return nil
	})
}

// processElBlock processes a single block reference for EL transaction indexing.
func (t *TxIndexer) processElTransaction(block *BlockRef, tx *types.Transaction, receipt *types.Receipt) (dbCommitCallback, error) {

	return nil, nil
}
