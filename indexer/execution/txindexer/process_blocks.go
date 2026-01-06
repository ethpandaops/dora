package txindexer

import (
	"context"
	"fmt"
	"math/big"
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
	BlockNumber       uint64
	BlockHash         common.Hash
	Transactions      []*types.Transaction
	Receipts          []*types.Receipt
	FeeRecipient      common.Address   // Fee recipient from beacon block
	Withdrawals       []WithdrawalData // Withdrawals from beacon block
	TotalPriorityFees *big.Int         // Total priority fees in the block
	Stats             struct {
		Events       uint32
		Transactions uint32
		Transfers    uint32
	}
}

// WithdrawalData represents a withdrawal from the beacon chain
type WithdrawalData struct {
	Index     uint64
	Validator uint64
	Address   common.Address
	Amount    uint64 // Amount in Gwei
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

		// Process pending balance lookups after block processing
		// This is done outside the main transaction since it involves RPC calls
		if t.balanceLookup != nil && t.balanceLookup.HasPendingLookups() {
			balanceCtx, balanceCancel := context.WithTimeout(t.ctx, 30*time.Second)
			_, err := t.balanceLookup.ProcessPendingLookups(balanceCtx)
			balanceCancel()
			if err != nil {
				t.logger.WithError(err).Debug("failed to process pending balance lookups")
			}
		}
	}()

	ctx, cancel := context.WithTimeout(t.ctx, 60*time.Second)
	defer cancel()

	// Fetch block data (transactions and receipts)
	data, client, err := t.fetchBlockData(ctx, ref)
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

	// Create processing context for this block (shared across all transactions)
	procCtx := newTxProcessingContext(ctx, client, t, ref, data)

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

		dbCommitCallback, err := procCtx.processTransaction(tx, receipt)
		if err != nil {
			return fmt.Errorf("failed to process EL transaction: %w", err)
		}
		if dbCommitCallback != nil {
			dbCommitCallbacks = append(dbCommitCallbacks, dbCommitCallback)
		}
	}

	// Get account nonce updates (done after all transactions processed)
	accountNonceUpdates := procCtx.getAccountNonceUpdates()

	// Process fee recipient and withdrawals if beacon block data is available
	if err := t.processBlockRewards(procCtx, data); err != nil {
		return fmt.Errorf("failed to process block rewards: %w", err)
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		for _, dbCommitCallback := range dbCommitCallbacks {
			err := dbCommitCallback(tx)
			if err != nil {
				return err
			}
		}

		// Batch update account nonces at the end of block processing
		if len(accountNonceUpdates) > 0 {
			if err := db.UpdateElAccountsLastNonce(accountNonceUpdates, tx); err != nil {
				return err
			}
		}

		// Build balance deltas from pending transfers (IDs are now resolved)
		procCtx.buildBalanceDeltas()

		// Get balance updates and lookup requests
		balanceUpdates, lookupRequests := procCtx.getBalanceUpdates()

		// Insert balance updates
		if len(balanceUpdates) > 0 {
			if err := db.InsertElBalances(balanceUpdates, tx); err != nil {
				t.logger.WithError(err).Warn("failed to insert balance updates")
			}
		}

		// Queue lookup requests for verification (outside transaction)
		if t.balanceLookup != nil && len(lookupRequests) > 0 {
			for _, req := range lookupRequests {
				t.balanceLookup.QueueBalanceLookup(req)
			}
		}

		// Insert system deposits (withdrawals and fee recipient rewards)
		if len(procCtx.systemDeposits) > 0 {
			// Resolve account IDs and create final withdrawal records
			systemWithdrawals := make([]*dbtypes.ElWithdrawal, 0, len(procCtx.systemDeposits))
			for _, pending := range procCtx.systemDeposits {
				if pending.account.id == 0 {
					continue // Skip if account ID not resolved
				}
				systemWithdrawals = append(systemWithdrawals, &dbtypes.ElWithdrawal{
					BlockUid:  ref.BlockUID,
					AccountID: pending.account.id,
					Type:      pending.depositType,
					Amount:    pending.amount,
					AmountRaw: pending.amountRaw,
					Validator: pending.validator,
				})
			}

			if len(systemWithdrawals) > 0 {
				if err := db.InsertElWithdrawals(systemWithdrawals, tx); err != nil {
					return fmt.Errorf("failed to insert system deposits: %w", err)
				}
			}
		}

		return db.InsertElBlock(&dbtypes.ElBlock{
			BlockUid:     ref.BlockUID,
			Status:       0x01,
			Events:       data.Stats.Events,
			Transactions: data.Stats.Transactions,
			Transfers:    data.Stats.Transfers,
		}, tx)
	})
}

// processBlockRewards processes fee recipient rewards and withdrawals from beacon block data.
func (t *TxIndexer) processBlockRewards(procCtx *txProcessingContext, data *blockData) error {
	// Process fee recipient if we have priority fees
	if data.TotalPriorityFees != nil && data.TotalPriorityFees.Sign() > 0 {
		// Ensure fee recipient account exists
		feeRecipientAccount := procCtx.ensureAccount(data.FeeRecipient, nil, false)

		// Create fee recipient withdrawal record (account ID will be resolved later)
		feeAmount := weiToFloat(data.TotalPriorityFees, 18) // ETH uses 18 decimals
		procCtx.systemDeposits = append(procCtx.systemDeposits, &pendingSystemDeposit{
			depositType: dbtypes.WithdrawalTypeFeeRecipient,
			account:     feeRecipientAccount,
			amount:      feeAmount,
			amountRaw:   data.TotalPriorityFees.Bytes(),
			validator:   nil,
		})
	}

	// Process withdrawals if available
	for _, withdrawal := range data.Withdrawals {
		// Convert Gwei to Wei (1 Gwei = 10^9 Wei)
		amountWei := new(big.Int).Mul(big.NewInt(int64(withdrawal.Amount)), big.NewInt(1e9))

		// Ensure withdrawal address account exists
		withdrawalAccount := procCtx.ensureAccount(withdrawal.Address, nil, false)

		// Create withdrawal record (account ID will be resolved later)
		withdrawalAmount := weiToFloat(amountWei, 18) // ETH uses 18 decimals
		procCtx.systemDeposits = append(procCtx.systemDeposits, &pendingSystemDeposit{
			depositType: dbtypes.WithdrawalTypeBeaconWithdrawal,
			account:     withdrawalAccount,
			amount:      withdrawalAmount,
			amountRaw:   amountWei.Bytes(),
			validator:   &withdrawal.Validator,
		})
	}

	return nil
}
