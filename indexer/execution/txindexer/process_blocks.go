package txindexer

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb"
	"github.com/ethpandaops/dora/clients/execution"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
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
	Stats             *blockStats

	// Call traces per transaction (Mode Full + tracesEnabled only, nil otherwise).
	// Indexed by position matching Transactions slice.
	TraceResults []exerpc.CallTraceResult

	// State diffs per transaction (Mode Full + tracesEnabled only, nil otherwise).
	// Indexed by position matching Transactions slice.
	StateDiffResults []exerpc.StateDiffResult
}

// processing stats
type blockStats struct {
	client       *execution.Client
	events       uint32
	transactions uint32
	transfers    uint32
	processing   []time.Duration
}

// WithdrawalData represents a withdrawal from the beacon chain
type WithdrawalData struct {
	Index     uint64
	Validator uint64
	Address   common.Address
	Amount    uint64 // Amount in Gwei
}

type dbCommitCallback func(ctx context.Context, tx *sqlx.Tx) error

// processElBlock processes a single block reference for EL transaction indexing.
// Each processing phase uses its own context to prevent cascading timeouts.
func (t *TxIndexer) processElBlock(ref *BlockRef) (*blockStats, error) {
	t2 := time.Now()
	stats := &blockStats{}

	defer func() {
		stats.processing = append(stats.processing, time.Since(t2))
	}()

	// Phase 1: Fetch block data (transactions and receipts)
	fetchCtx, fetchCancel := context.WithTimeout(t.ctx, 60*time.Second)
	data, client, err := t.fetchBlockData(fetchCtx, ref)
	fetchCancel()

	if err != nil {
		return nil, fmt.Errorf("failed to fetch block data: %w", err)
	}

	stats.client = client

	if data == nil {
		// No data to process (e.g., pre-merge block)
		t.logger.WithFields(logrus.Fields{
			"slot":      ref.Slot,
			"blockUid":  ref.BlockUID,
			"blockHash": fmt.Sprintf("%x", ref.BlockHash),
		}).Debug("no EL data for block")
		return stats, nil
	}

	data.Stats = stats

	t.logger.WithFields(logrus.Fields{
		"slot":         ref.Slot,
		"blockUid":     ref.BlockUID,
		"blockHash":    data.BlockHash.Hex(),
		"blockNumber":  data.BlockNumber,
		"transactions": len(data.Transactions),
		"receipts":     len(data.Receipts),
	}).Debug("fetched EL block data")

	// Fetch call traces if in Full mode with traces enabled
	if t.mode == ModeFull && utils.Config.ExecutionIndexer.TracesEnabled {
		traceCtx, traceCancel := context.WithTimeout(t.ctx, 5*time.Minute)
		traceResults, traceErr := t.fetchBlockTraces(traceCtx, client, ref, data.BlockHash)
		traceCancel()

		if traceErr != nil {
			t.logger.WithError(traceErr).Debug("trace fetch failed, proceeding without traces")
		} else if traceResults != nil {
			data.TraceResults = traceResults
		}

		stateCtx, stateCancel := context.WithTimeout(t.ctx, 5*time.Minute)
		stateResults, stateErr := t.fetchBlockStateDiffs(stateCtx, client, ref, data.BlockHash)
		stateCancel()
		if stateErr != nil {
			t.logger.WithError(stateErr).Debug("state diff fetch failed, proceeding without state diffs")
		} else if stateResults != nil {
			data.StateDiffResults = stateResults
		}
	}

	// Build trace lookup map (txHash → call trace) for O(1) access per tx
	traceMap := make(map[common.Hash]*exerpc.CallTraceCall, len(data.TraceResults))
	for i := range data.TraceResults {
		tr := &data.TraceResults[i]
		if tr.Result != nil {
			traceMap[tr.TxHash] = tr.Result
		}
	}

	// Build state diff lookup map (txHash → state diff) for O(1) access per tx
	stateDiffMap := make(map[common.Hash]*exerpc.StateDiff, len(data.StateDiffResults))
	for i := range data.StateDiffResults {
		dr := &data.StateDiffResults[i]
		if dr.Result != nil {
			stateDiffMap[dr.TxHash] = dr.Result
		}
	}

	// Release original trace/state-diff slices now that the lookup maps
	// hold references to the inner result pointers. This allows GC to
	// collect the slice backing arrays and wrapper structs.
	data.TraceResults = nil
	data.StateDiffResults = nil

	// Phase 2: Process transactions (pure computation, no I/O)
	procCtx := newTxProcessingContext(t.ctx, client, t, ref, data)

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

		// Look up trace for this transaction (may be nil)
		callTrace := traceMap[tx.Hash()]
		stateDiff := stateDiffMap[tx.Hash()]

		dbCommitCallback, err := procCtx.processTransaction(tx, receipt, callTrace, stateDiff)
		if err != nil {
			return stats, fmt.Errorf("failed to process EL transaction: %w", err)
		}
		if dbCommitCallback != nil {
			dbCommitCallbacks = append(dbCommitCallbacks, dbCommitCallback)
		}
	}

	// Process fee recipient and withdrawals if beacon block data is available
	// (this must happen before batch resolution so these accounts are included)
	if err := t.processBlockRewards(procCtx, data); err != nil {
		return stats, fmt.Errorf("failed to process block rewards: %w", err)
	}

	// Phase 3: Resolve accounts and tokens (DB + RPC lookups, own context)
	resolveCtx, resolveCancel := context.WithTimeout(t.ctx, 60*time.Second)
	procCtx.ctx = resolveCtx

	var blockErr error
	if err := procCtx.resolveAccountsFromDB(); err != nil {
		blockErr = fmt.Errorf("failed to resolve accounts: %w", err)
	}

	if blockErr == nil {
		if err := procCtx.resolveTokensFromDB(); err != nil {
			blockErr = fmt.Errorf("failed to resolve tokens: %w", err)
		}
	}
	resolveCancel()

	// Get account nonce updates (only meaningful if resolve succeeded)
	var accountNonceUpdates []*dbtypes.ElAccount
	if blockErr == nil {
		accountNonceUpdates = procCtx.getAccountNonceUpdates()
	}

	// Phase 4: Process pending balance lookups (independent of block resolution).
	// These are balance verification requests queued from previous blocks.
	// They must be committed even if the current block's resolution failed.
	var balanceCommitCallback dbCommitCallback
	if t.balanceLookup != nil && t.balanceLookup.HasPendingLookups() {
		balanceCtx, balanceCancel := context.WithTimeout(t.ctx, 30*time.Second)
		callback, balanceErr := t.balanceLookup.ProcessPendingLookups(balanceCtx)
		balanceCancel()

		if balanceErr != nil {
			t.logger.WithError(balanceErr).Debug("failed to process pending balance lookups")
		}
		balanceCommitCallback = callback
	}

	stats.processing = append(stats.processing, time.Since(t2))
	t2 = time.Now()

	// If block resolution failed, still commit pending balance lookups, then return error
	if blockErr != nil {
		if balanceCommitCallback != nil {
			balCommitCtx, balCommitCancel := context.WithTimeout(t.ctx, 30*time.Second)
			commitErr := db.RunDBTransaction(func(tx *sqlx.Tx) error {
				return balanceCommitCallback(balCommitCtx, tx)
			})
			balCommitCancel()

			if commitErr != nil {
				t.logger.WithError(commitErr).Warn("failed to commit balance lookups after block error")
			}
		}
		return stats, blockErr
	}

	// Phase 5: Commit to database (fresh context, independent of earlier phases)
	commitCtx, commitCancel := context.WithTimeout(t.ctx, 30*time.Second)
	defer commitCancel()

	// Update procCtx to use commit context for getBalanceUpdates (reads from DB)
	procCtx.ctx = commitCtx

	err = db.RunDBTransaction(func(tx *sqlx.Tx) error {
		for _, cb := range dbCommitCallbacks {
			if err := cb(commitCtx, tx); err != nil {
				return err
			}
		}

		if balanceCommitCallback != nil {
			if err := balanceCommitCallback(commitCtx, tx); err != nil {
				return err
			}
		}

		// Batch update account nonces at the end of block processing
		if len(accountNonceUpdates) > 0 {
			if err := db.UpdateElAccountsLastNonce(commitCtx, tx, accountNonceUpdates); err != nil {
				return err
			}
		}

		// Build balance deltas from pending transfers (IDs are now resolved)
		procCtx.buildBalanceDeltas()

		// Get balance updates and lookup requests
		balanceUpdates, lookupRequests := procCtx.getBalanceUpdates()

		// Insert balance updates
		if len(balanceUpdates) > 0 {
			if err := db.InsertElBalances(commitCtx, tx, balanceUpdates); err != nil {
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
		// BlockIndex 0 is reserved for fee recipients, withdrawals use 1+.
		if len(procCtx.systemDeposits) > 0 {
			// Resolve account IDs and create final withdrawal records
			systemWithdrawals := make([]*dbtypes.ElWithdrawal, 0, len(procCtx.systemDeposits))
			withdrawalIdx := uint16(1)
			for _, pending := range procCtx.systemDeposits {
				if pending.account.id == 0 {
					continue // Skip if account ID not resolved
				}

				var blockIndex uint16
				if pending.depositType == dbtypes.WithdrawalTypeFeeRecipient {
					blockIndex = 0
				} else {
					blockIndex = withdrawalIdx
					withdrawalIdx++
				}

				systemWithdrawals = append(systemWithdrawals, &dbtypes.ElWithdrawal{
					BlockUid:   ref.BlockUID,
					BlockIndex: blockIndex,
					AccountID:  pending.account.id,
					Type:       pending.depositType,
					Amount:     pending.amount,
					AmountRaw:  pending.amountRaw,
					Validator:  pending.validator,
				})
			}

			if len(systemWithdrawals) > 0 {
				if err := db.InsertElWithdrawals(commitCtx, tx, systemWithdrawals); err != nil {
					return fmt.Errorf("failed to insert system deposits: %w", err)
				}
			}
		}

		return db.InsertElBlock(commitCtx, tx, &dbtypes.ElBlock{
			BlockUid:     ref.BlockUID,
			Status:       0x01,
			Events:       data.Stats.events,
			Transactions: data.Stats.transactions,
			Transfers:    data.Stats.transfers,
		})
	})

	if err != nil {
		return stats, fmt.Errorf("failed to insert block: %w", err)
	}

	// Build and write execution data object to blockdb (Mode Full only)
	if t.mode == ModeFull {
		execData, dataStatus := procCtx.buildExecDataObject()
		if execData != nil && blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsExecData() {
			blockdbCtx, blockdbCancel := context.WithTimeout(t.ctx, 30*time.Second)
			dataSize, writeErr := blockdb.GlobalBlockDb.AddExecData(
				blockdbCtx, uint64(ref.Slot), ref.BlockRoot, execData,
			)
			blockdbCancel()

			if writeErr != nil {
				t.logger.WithError(writeErr).WithFields(logrus.Fields{
					"slot":     ref.Slot,
					"blockUid": ref.BlockUID,
				}).Warn("failed to write exec data to blockdb")
			} else {
				// Update el_block with data_status and data_size
				updateCtx, updateCancel := context.WithTimeout(t.ctx, 30*time.Second)
				updateErr := db.RunDBTransaction(func(tx *sqlx.Tx) error {
					return db.UpdateElBlockDataStatus(updateCtx, tx, ref.BlockUID, dataStatus, dataSize)
				})
				updateCancel()
				if updateErr != nil {
					t.logger.WithError(updateErr).Warn("failed to update block data status")
				}
			}
		}
	}

	return stats, nil
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
