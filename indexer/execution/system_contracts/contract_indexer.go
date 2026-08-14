package system_contracts

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/indexer/beacon"
	exectx "github.com/ethpandaops/dora/indexer/execution"
)

// contractIndexer handles the indexing of contract events for a specific system contract
// it crawls logs in order and tracks the queue length to precalculate the dequeue block number where the request will be sent to the beacon chain
type contractIndexer[TxType any] struct {
	indexer *exectx.IndexerCtx
	logger  logrus.FieldLogger
	options *contractIndexerOptions[TxType]
	state   *contractIndexerState
}

// contractIndexerOptions defines the configuration for the contract indexer
type contractIndexerOptions[TxType any] struct {
	stateKey        string                // key to identify the indexer state in the database
	batchSize       int                   // number of logs to fetch per request
	contractAddress func() common.Address // resolves the address of the contract to index (re-evaluated per scan, as client configs may arrive after startup)
	deployBlock     uint64                // block number from where to start crawling logs
	dequeueRate     uint64                // number of logs to dequeue per block, 0 for no queue

	// queueActivationBlock resolves the first el block number where request dequeuing is active
	// on the given fork (nil = finalized canonical view). The queue is locked before that block,
	// so requests logged earlier get dequeue block 0 (not determinable yet) until the activation
	// block is observable. Block numbers shift with missed slots and can differ between forks, so
	// the resolution must come from indexed post-fork blocks. nil = dequeuing active since deployment.
	queueActivationBlock func(fork *exectx.ForkWithClients) (uint64, bool)

	// loadRebaseRows loads all persisted request txs up to the given el block number in queue
	// order for the one-time dequeue rebase (required when queueActivationBlock is set)
	loadRebaseRows func(maxBlockNumber uint64) []*dequeueRebaseRow

	// persistRebaseRows persists rebased dequeue blocks (required when queueActivationBlock is set)
	persistRebaseRows func(tx *sqlx.Tx, rows []*dequeueRebaseRow) error

	// processFinalTx processes a finalized transaction log
	processFinalTx func(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, parentTxs []*TxType) (*TxType, error)

	// processRecentTx processes a recent (non-finalized) transaction log
	processRecentTx func(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, fork *exectx.ForkWithClients, parentTxs []*TxType) (*TxType, error)

	// persistTxs persists processed transactions to the database
	persistTxs func(tx *sqlx.Tx, txs []*TxType) error
}

// dequeueRebaseRow is a persisted request tx reference used by the one-time dequeue rebase.
type dequeueRebaseRow struct {
	blockRoot    []byte
	blockNumber  uint64
	blockIndex   uint64
	forkId       uint64
	dequeueBlock uint64
}

// contractIndexerState represents the current state of the contract indexer
type contractIndexerState struct {
	FinalBlock    uint64                                       `json:"final_block"`
	FinalQueueLen uint64                                       `json:"final_queue"`
	ForkStates    map[beacon.ForkKey]*contractIndexerForkState `json:"fork_states"`

	// QueueActivationBlock is the finalized el block number where request dequeuing started
	// (0 = not resolved yet). Set once by the dequeue rebase for contracts with a
	// queueActivationBlock resolver.
	QueueActivationBlock uint64 `json:"activation_block,omitempty"`
}

// contractIndexerForkState represents the state of the contract indexer for a specific unfinalized fork
type contractIndexerForkState struct {
	Block    uint64 `json:"b"`
	QueueLen uint64 `json:"q"`
}

// newContractIndexer creates a new contract indexer with the given options
func newContractIndexer[TxType any](indexer *exectx.IndexerCtx, logger logrus.FieldLogger, options *contractIndexerOptions[TxType]) *contractIndexer[TxType] {
	ci := &contractIndexer[TxType]{
		indexer: indexer,
		logger:  logger,
		options: options,
	}

	return ci
}

// loadState loads the contract indexer state from the database
func (ci *contractIndexer[_]) loadState() {
	syncState := contractIndexerState{}
	db.GetExplorerState(ci.indexer.Ctx, ci.options.stateKey, &syncState)
	ci.state = &syncState

	if ci.state.ForkStates == nil {
		ci.state.ForkStates = make(map[beacon.ForkKey]*contractIndexerForkState)
	}

	if ci.state.FinalBlock == 0 {
		ci.state.FinalBlock = ci.options.deployBlock
	}
}

// persistState saves the current contract indexer state to the database
func (ci *contractIndexer[_]) persistState(tx *sqlx.Tx) error {
	finalizedBlockNumber := ci.getFinalizedBlockNumber()
	for forkId, forkState := range ci.state.ForkStates {
		if forkState.Block < finalizedBlockNumber {
			delete(ci.state.ForkStates, forkId)
		}
	}

	err := db.SetExplorerState(ci.indexer.Ctx, tx, ci.options.stateKey, ci.state)
	if err != nil {
		return fmt.Errorf("error while updating contract indexer state: %v", err)
	}

	return nil
}

// runContractIndexer is the main entry point for running the contract indexer
// It processes finalized and recent block ranges in order
func (ci *contractIndexer[_]) runContractIndexer() error {
	if ci.state == nil {
		ci.loadState()
	}

	// rebase pre-activation dequeue blocks first, so the transaction matcher never sees the
	// activation block range with unassigned (0) dequeue blocks
	err := ci.runDequeueRebase()
	if err != nil {
		return fmt.Errorf("error while rebasing dequeue blocks: %w", err)
	}

	finalizedEpoch, _ := ci.indexer.ChainState.GetFinalizedCheckpoint()
	if finalizedEpoch > 0 {
		finalizedBlockNumber := ci.getFinalizedBlockNumber()

		if finalizedBlockNumber == 0 {
			return fmt.Errorf("finalized block not found in cache or db")
		}

		if finalizedBlockNumber < ci.state.FinalBlock {
			return fmt.Errorf("finalized block number (%v) smaller than index state (%v)", finalizedBlockNumber, ci.state.FinalBlock)
		}

		if finalizedBlockNumber > ci.state.FinalBlock {
			err := ci.processFinalizedBlocks(finalizedBlockNumber)
			if err != nil {
				return err
			}
		}
	}

	ci.processRecentBlocks()

	return nil
}

// getFinalizedBlockNumber retrieves the latest finalized el block number
func (ci *contractIndexer[_]) getFinalizedBlockNumber() uint64 {
	var finalizedBlockNumber uint64

	_, finalizedRoot := ci.indexer.ChainState.GetFinalizedCheckpoint()
	if finalizedBlock := ci.indexer.BeaconIndexer.GetBlockByRoot(finalizedRoot); finalizedBlock != nil {
		if indexVals := finalizedBlock.GetBlockIndex(ci.indexer.Ctx); indexVals != nil {
			finalizedBlockNumber = indexVals.ExecutionNumber
		}
	}

	if finalizedBlockNumber == 0 {
		// load from db
		if finalizedBlock := db.GetSlotByRoot(ci.indexer.Ctx, finalizedRoot[:]); finalizedBlock != nil && finalizedBlock.EthBlockNumber != nil {
			finalizedBlockNumber = *finalizedBlock.EthBlockNumber
		}
	}

	return finalizedBlockNumber
}

// getQueueActivationBlock returns the first el block number where request dequeuing is active
// for the given fork (nil = finalized canonical view). Contracts without an activation
// resolver dequeue since deployment.
func (ci *contractIndexer[_]) getQueueActivationBlock(fork *exectx.ForkWithClients) (uint64, bool) {
	if ci.options.queueActivationBlock == nil {
		return 0, true
	}

	if ci.state.QueueActivationBlock != 0 {
		return ci.state.QueueActivationBlock, true
	}

	return ci.options.queueActivationBlock(fork)
}

// applyQueueDequeues applies the requests dequeued in blocks [fromBlock, toBlock] (inclusive)
// to the queue length. Dequeuing only happens from the activation block onwards, so earlier
// blocks in the range dequeue nothing; while the activation block is unknown the queue only grows.
func (ci *contractIndexer[_]) applyQueueDequeues(queueLength, fromBlock, toBlock, activationBlock uint64, activationKnown bool) uint64 {
	if ci.options.dequeueRate == 0 || !activationKnown {
		return queueLength
	}

	if fromBlock < activationBlock {
		fromBlock = activationBlock
	}

	if toBlock < fromBlock {
		return queueLength
	}

	dequeuedRequests := (toBlock - fromBlock + 1) * ci.options.dequeueRate
	if dequeuedRequests > queueLength {
		return 0
	}

	return queueLength - dequeuedRequests
}

// calculateDequeueBlock returns the el block number where a request logged in logBlock leaves
// the contract queue, given the queue length at the start of logBlock. While the activation
// block is not known yet it returns 0 (not determinable - assigned by the dequeue rebase once
// the first post-activation block is finalized).
func (ci *contractIndexer[_]) calculateDequeueBlock(logBlock, queueLength, activationBlock uint64, activationKnown bool) uint64 {
	if ci.options.dequeueRate == 0 {
		return logBlock
	}

	if !activationKnown {
		return 0
	}

	dequeueBase := logBlock
	if dequeueBase < activationBlock {
		dequeueBase = activationBlock
	}

	return dequeueBase + (queueLength / ci.options.dequeueRate)
}

// computeRebasedDequeueBlocks replays the given request tx rows (in queue order) through the
// queue with dequeuing starting at the activation block. Finalized rows (fork id 0) get their
// exact dequeue blocks; non-finalized rows with a stale pre-activation dequeue block are placed
// behind the finalized backlog (matching prefers finalized txs, but they must leave the pending
// window eventually). It returns the rows whose dequeue block changed and the queue length
// remaining after the last finalized block.
func (ci *contractIndexer[_]) computeRebasedDequeueBlocks(rows []*dequeueRebaseRow, activationBlock, finalBlock uint64) ([]*dequeueRebaseRow, uint64) {
	updates := make([]*dequeueRebaseRow, 0, len(rows))
	strandedRows := make([]*dequeueRebaseRow, 0)
	queueLength := uint64(0)
	queueBlock := uint64(0)
	finalizedCount := uint64(0)

	for _, row := range rows {
		if row.forkId != 0 {
			if row.dequeueBlock < activationBlock {
				strandedRows = append(strandedRows, row)
			}

			continue
		}

		if row.blockNumber > queueBlock {
			queueLength = ci.applyQueueDequeues(queueLength, queueBlock, row.blockNumber-1, activationBlock, true)
			queueBlock = row.blockNumber
		}

		dequeueBlock := ci.calculateDequeueBlock(row.blockNumber, queueLength, activationBlock, true)
		queueLength++
		finalizedCount++

		if dequeueBlock != row.dequeueBlock {
			row.dequeueBlock = dequeueBlock
			updates = append(updates, row)
		}
	}

	for idx, row := range strandedRows {
		row.dequeueBlock = activationBlock + ((finalizedCount + uint64(idx)) / ci.options.dequeueRate)
		updates = append(updates, row)
	}

	// preserve the queue state up to and including the last finalized block
	if finalBlock >= queueBlock {
		queueLength = ci.applyQueueDequeues(queueLength, queueBlock, finalBlock, activationBlock, true)
	}

	return updates, queueLength
}

// runDequeueRebase reassigns the dequeue blocks of already-persisted request txs once the queue
// activation block is final. Requests enqueued before the activation fork are stored with
// dequeue block 0, as block numbers shift with missed slots and may differ between forks until
// the boundary is finalized. Once the first post-activation block is finalized, the finalized
// rows are replayed through the queue to assign their real dequeue blocks and to reseed the
// tracked queue length. Runs once; the resolved activation block is kept in the indexer state.
func (ci *contractIndexer[_]) runDequeueRebase() error {
	if ci.options.queueActivationBlock == nil || ci.options.dequeueRate == 0 {
		return nil
	}

	if ci.state.QueueActivationBlock != 0 {
		return nil
	}

	activationBlock, activationKnown := ci.options.queueActivationBlock(nil)
	if !activationKnown {
		return nil
	}

	// include stale rows up to the activation block even if the finalized crawl is behind
	maxBlockNumber := ci.state.FinalBlock
	if activationBlock > 0 && activationBlock-1 > maxBlockNumber {
		maxBlockNumber = activationBlock - 1
	}

	rows := ci.options.loadRebaseRows(maxBlockNumber)
	updates, queueLength := ci.computeRebasedDequeueBlocks(rows, activationBlock, ci.state.FinalBlock)

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		if len(updates) > 0 {
			err := ci.options.persistRebaseRows(tx, updates)
			if err != nil {
				return fmt.Errorf("error while persisting rebased dequeue blocks: %w", err)
			}
		}

		ci.state.QueueActivationBlock = activationBlock
		ci.state.FinalQueueLen = queueLength

		return ci.persistState(tx)
	})
	if err != nil {
		return err
	}

	ci.logger.Infof("queue activation block %v resolved, rebased dequeue blocks of %v request txs (%v queued)", activationBlock, len(updates), queueLength)

	return nil
}

// loadFilteredLogs fetches filtered logs from the execution client
func (ci *contractIndexer[_]) loadFilteredLogs(ctx context.Context, client *execution.Client, query ethereum.FilterQuery) ([]types.Log, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	return client.GetRPCClient().GetEthClient().FilterLogs(ctx, query)
}

// loadTransactionByHash fetches a transaction by its hash from the execution client
func (ci *contractIndexer[_]) loadTransactionByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Transaction, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tx, _, err := client.GetRPCClient().GetEthClient().TransactionByHash(ctx, hash)
	return tx, err
}

// txRecipient returns the transaction recipient. Contract-creation transactions
// have no recipient (To is nil); for those the request reached the system contract
// via an internal call, so the emitting contract address is the effective target.
func txRecipient(tx *types.Transaction, log *types.Log) common.Address {
	if to := tx.To(); to != nil {
		return *to
	}
	return log.Address
}

// loadHeaderByHash fetches a block header by its hash from the execution client
func (ci *contractIndexer[_]) loadHeaderByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return client.GetRPCClient().GetHeaderByHash(ctx, hash)
}

// processFinalizedBlocks processes contract events from finalized block ranges
// it fetches logs in batches and calls the provided processFinalTx function to process each log
func (ci *contractIndexer[TxType]) processFinalizedBlocks(finalizedBlockNumber uint64) error {
	clients := ci.indexer.GetFinalizedClients(execution.AnyClient)
	if len(clients) == 0 {
		return fmt.Errorf("no ready execution client found")
	}

	ctx, cancel := context.WithCancel(ci.indexer.Ctx)
	defer cancel()

	activationBlock, activationKnown := ci.getQueueActivationBlock(nil)

	retryCount := 0

	// process blocks in range until the finalized block is reached
	for ci.state.FinalBlock < finalizedBlockNumber {
		client := clients[retryCount%len(clients)]

		batchSize := uint64(ci.options.batchSize)
		if retryCount > 0 {
			// reduce batch size on retries to avoid response limit errors for block ranges with many logs
			batchSize /= uint64(math.Pow(2, float64(retryCount)))
			if batchSize < 10 {
				batchSize = 10
			}
		}

		toBlock := ci.state.FinalBlock + uint64(ci.options.batchSize)
		if toBlock > finalizedBlockNumber {
			toBlock = finalizedBlockNumber
		}

		// fetch logs from the execution client
		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(0).SetUint64(ci.state.FinalBlock + 1),
			ToBlock:   big.NewInt(0).SetUint64(toBlock),
			Addresses: []common.Address{
				ci.options.contractAddress(),
			},
			// Match any topic. A nil Topics serializes to "topics": null, which some
			// execution clients (e.g. ethrex) reject as a missing parameter; a non-nil
			// empty slice serializes to "topics": [] and is accepted everywhere.
			Topics: [][]common.Hash{},
		}

		logs, err := ci.loadFilteredLogs(ctx, client, query)
		if err != nil {
			if retryCount < 3 {
				retryCount++
				continue
			}

			return fmt.Errorf("error fetching contract logs: %v", err)
		}

		ci.logger.Debugf("received contract logs for block %v - %v: %v events", ci.state.FinalBlock, toBlock, len(logs))

		retryCount = 0

		// parse logs and load tx/block details
		var txHash, txHeaderHash []byte
		var txDetails *types.Transaction
		var txBlockHeader *types.Header

		requestTxs := []*TxType{}
		queueBlock := ci.state.FinalBlock + 1
		queueLength := ci.state.FinalQueueLen

		for idx := range logs {
			log := &logs[idx]

			// load transaction if not already loaded
			if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
				txDetails, err = ci.loadTransactionByHash(ctx, client, log.TxHash)
				if err != nil {
					return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
				}

				txHash = log.TxHash[:]
			}

			// load block header if not already loaded
			if txBlockHeader == nil || !bytes.Equal(txHeaderHash, log.BlockHash[:]) {
				txBlockHeader, err = ci.loadHeaderByHash(ctx, client, log.BlockHash)
				if err != nil {
					return fmt.Errorf("could not load block details (%v): %v", log.BlockHash, err)
				}

				txHeaderHash = log.BlockHash[:]
			}

			// get transaction sender
			chainId := txDetails.ChainId()
			if chainId != nil && chainId.Cmp(big.NewInt(0)) == 0 {
				chainId = nil
			}
			txFrom, err := types.Sender(types.LatestSignerForChainID(chainId), txDetails)
			if err != nil {
				return fmt.Errorf("could not decode tx sender (%v): %v", log.TxHash, err)
			}

			// process queue decrease for past blocks
			if queueBlock > log.BlockNumber {
				ci.logger.Warnf("contract log for block %v received after block %v", log.BlockNumber, queueBlock)
				return nil
			} else if ci.options.dequeueRate > 0 && queueBlock < log.BlockNumber {
				// apply the requests dequeued since the last processed log
				queueLength = ci.applyQueueDequeues(queueLength, queueBlock, log.BlockNumber-1, activationBlock, activationKnown)
				queueBlock = log.BlockNumber
			}

			// calculate the dequeue block number for the current log
			dequeueBlock := ci.calculateDequeueBlock(log.BlockNumber, queueLength, activationBlock, activationKnown)
			if ci.options.dequeueRate > 0 {
				queueLength++
			}

			// process the log and get the corresponding transaction
			requestTx, err := ci.options.processFinalTx(log, txDetails, txBlockHeader, txFrom, dequeueBlock, requestTxs)
			if err != nil {
				continue
			}

			if requestTx == nil {
				continue
			}

			requestTxs = append(requestTxs, requestTx)
		}

		// apply the requests dequeued up to and including the last block in the range,
		// so the persisted queue length reflects the state after the whole range
		queueLength = ci.applyQueueDequeues(queueLength, queueBlock, toBlock, activationBlock, activationKnown)

		if len(requestTxs) > 0 {
			ci.logger.Infof("crawled transactions for block %v - %v: %v events", ci.state.FinalBlock, toBlock, len(requestTxs))
		}

		// persist the processed transactions and update the indexer state
		err = ci.persistFinalizedRequestTxs(toBlock, queueLength, requestTxs)
		if err != nil {
			return fmt.Errorf("could not persist indexed transactions: %v", err)
		}

		// cooldown to avoid rate limiting from external archive nodes
		time.Sleep(1 * time.Second)
	}
	return nil
}

// processRecentBlocks processes contract events from recent (non-finalized) blocks across all forks
func (ci *contractIndexer[_]) processRecentBlocks() error {
	headForks := ci.indexer.GetForksWithClients(execution.AnyClient)
	for _, headFork := range headForks {
		err := ci.processRecentBlocksForFork(headFork)
		if err != nil {
			if headFork.Canonical {
				ci.logger.Errorf("could not process recent events from canonical fork %v: %v", headFork.ForkId, err)
			} else {
				ci.logger.Warnf("could not process recent events from fork %v: %v", headFork.ForkId, err)
			}
		}
	}
	return nil
}

// processRecentBlocksForFork processes contract events from recent blocks for a specific fork
func (ci *contractIndexer[TxType]) processRecentBlocksForFork(headFork *exectx.ForkWithClients) error {
	// get the head el block number for the fork
	elHeadBlock := ci.indexer.BeaconIndexer.GetCanonicalHead(&headFork.ForkId)
	if elHeadBlock == nil {
		return fmt.Errorf("head block not found")
	}

	elHeadBlockIndex := elHeadBlock.GetBlockIndex(ci.indexer.Ctx)
	if elHeadBlockIndex == nil {
		return fmt.Errorf("head block index not found")
	}

	elHeadBlockNumber := elHeadBlockIndex.ExecutionNumber
	if elHeadBlockNumber > 0 {
		elHeadBlockNumber--
	}

	startBlockNumber := ci.state.FinalBlock + 1
	queueLength := ci.state.FinalQueueLen

	// get last processed block for this fork
	if forkState := ci.state.ForkStates[headFork.ForkId]; forkState != nil && forkState.Block <= elHeadBlockNumber {
		if forkState.Block == elHeadBlockNumber {
			return nil // already processed
		}

		startBlockNumber = forkState.Block + 1
		queueLength = forkState.QueueLen
	} else {
		// seems we haven't seen this fork before, check if we can continue from a parent fork
		for parentForkId := range ci.indexer.BeaconIndexer.GetParentForkIds(headFork.ForkId) {
			if parentForkState := ci.state.ForkStates[beacon.ForkKey(parentForkId)]; parentForkState != nil && parentForkState.Block <= elHeadBlockNumber {
				startBlockNumber = parentForkState.Block + 1
				queueLength = parentForkState.QueueLen
			}
		}
	}

	var resError error
	var ctxCancel context.CancelFunc
	defer func() {
		if ctxCancel != nil {
			ctxCancel()
		}
	}()

	// the activation block may differ between forks until the boundary is finalized, so it is
	// resolved along the processed fork; rows written with an unfinalized activation block are
	// re-crawled (and the pre-activation backlog rebased) by the finalization routine
	activationBlock, activationKnown := ci.getQueueActivationBlock(headFork)

	queueBlock := startBlockNumber

	// process blocks in range until the head el block is reached
	for startBlockNumber <= elHeadBlockNumber {
		var toBlock uint64
		var logs []types.Log
		var reqError error
		var txHash, txHeaderHash []byte
		var txDetails *types.Transaction
		var txBlockHeader *types.Header

		requestTxs := []*TxType{}

		for retryCount := 0; retryCount < 3; retryCount++ {
			client := headFork.Clients[retryCount%len(headFork.Clients)]

			batchSize := uint64(ci.options.batchSize)
			if retryCount > 0 {
				// reduce batch size on retries to avoid response limit errors for block ranges with many logs
				batchSize /= uint64(math.Pow(2, float64(retryCount)))
				if batchSize < 10 {
					batchSize = 10
				}
			}

			toBlock = startBlockNumber + uint64(ci.options.batchSize)
			if toBlock > elHeadBlockNumber {
				toBlock = elHeadBlockNumber
			}

			if ctxCancel != nil {
				ctxCancel()
			}
			ctx, cancel := context.WithTimeout(ci.indexer.Ctx, 600*time.Second)
			ctxCancel = cancel

			// fetch logs from the execution client
			query := ethereum.FilterQuery{
				FromBlock: big.NewInt(0).SetUint64(startBlockNumber),
				ToBlock:   big.NewInt(0).SetUint64(toBlock),
				Addresses: []common.Address{
					ci.options.contractAddress(),
				},
				// Match any topic. A nil Topics serializes to "topics": null, which some
				// execution clients (e.g. ethrex) reject as a missing parameter; a non-nil
				// empty slice serializes to "topics": [] and is accepted everywhere.
				Topics: [][]common.Hash{},
			}

			logs, reqError = ci.loadFilteredLogs(ctx, client, query)
			if reqError != nil {
				ci.logger.Warnf("error fetching contract logs for fork %v (%v-%v): %v", headFork.ForkId, startBlockNumber, toBlock, reqError)
				continue
			}

			for idx := range logs {
				var err error

				log := &logs[idx]

				// load transaction if not already loaded
				if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
					txDetails, err = ci.loadTransactionByHash(ctx, client, log.TxHash)
					if err != nil {
						return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
					}

					txHash = log.TxHash[:]
				}

				// load block header if not already loaded
				if txBlockHeader == nil || !bytes.Equal(txHeaderHash, log.BlockHash[:]) {
					txBlockHeader, err = ci.loadHeaderByHash(ctx, client, log.BlockHash)
					if err != nil {
						return fmt.Errorf("could not load block details (%v): %v", log.BlockHash, err)
					}

					txHeaderHash = log.BlockHash[:]
				}

				// get transaction sender
				chainId := txDetails.ChainId()
				if chainId != nil && chainId.Cmp(big.NewInt(0)) == 0 {
					chainId = nil
				}
				txFrom, err := types.Sender(types.LatestSignerForChainID(chainId), txDetails)
				if err != nil {
					return fmt.Errorf("could not decode tx sender (%v): %v", log.TxHash, err)
				}

				// process queue decrease for past blocks
				if queueBlock > log.BlockNumber {
					ci.logger.Warnf("contract log for block %v received after block %v", log.BlockNumber, queueBlock)
					return nil
				} else if ci.options.dequeueRate > 0 && queueBlock < log.BlockNumber {
					// apply the requests dequeued since the last processed log
					queueLength = ci.applyQueueDequeues(queueLength, queueBlock, log.BlockNumber-1, activationBlock, activationKnown)
					queueBlock = log.BlockNumber
				}

				// calculate the dequeue block number for the current log
				dequeueBlock := ci.calculateDequeueBlock(log.BlockNumber, queueLength, activationBlock, activationKnown)
				if ci.options.dequeueRate > 0 {
					queueLength++
				}

				// process the log and get the corresponding transaction
				requestTx, err := ci.options.processRecentTx(log, txDetails, txBlockHeader, txFrom, dequeueBlock, headFork, requestTxs)
				if err != nil {
					continue
				}

				if requestTx == nil {
					continue
				}

				requestTxs = append(requestTxs, requestTx)
			}

			// apply the requests dequeued up to and including the last block in the range,
			// so the persisted queue length reflects the state after the whole range
			queueLength = ci.applyQueueDequeues(queueLength, queueBlock, toBlock, activationBlock, activationKnown)
			queueBlock = toBlock + 1

			if len(requestTxs) > 0 {
				ci.logger.Infof("crawled recent contract logs for fork %v (%v-%v): %v events", headFork.ForkId, startBlockNumber, toBlock, len(requestTxs))
			}

			// persist the processed transactions and update the indexer state
			err := ci.persistRecentRequestTxs(headFork.ForkId, toBlock, queueLength, requestTxs)
			if err != nil {
				return fmt.Errorf("could not persist contract logs: %v", err)
			}

			// cooldown to avoid rate limiting from external archive nodes
			time.Sleep(1 * time.Second)

			break
		}

		if reqError != nil {
			return fmt.Errorf("error fetching contract logs for fork %v (%v-%v): %v", headFork.ForkId, startBlockNumber, toBlock, reqError)
		}

		startBlockNumber = toBlock + 1
	}

	return resError
}

// persistFinalizedRequestTxs persists processed finalized transactions and the indexer state to the database
func (ci *contractIndexer[TxType]) persistFinalizedRequestTxs(finalBlockNumber, finalQueueLen uint64, requests []*TxType) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		if len(requests) > 0 {
			err := ci.options.persistTxs(tx, requests)
			if err != nil {
				return fmt.Errorf("error while persisting contract logs: %v", err)
			}
		}

		ci.state.FinalBlock = finalBlockNumber
		ci.state.FinalQueueLen = finalQueueLen

		return ci.persistState(tx)
	})
}

// persistRecentRequestTxs persists processed recent transactions and the indexer state to the database
func (ci *contractIndexer[TxType]) persistRecentRequestTxs(forkId beacon.ForkKey, finalBlockNumber, finalQueueLen uint64, requests []*TxType) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		if len(requests) > 0 {
			err := ci.options.persistTxs(tx, requests)
			if err != nil {
				return fmt.Errorf("error while persisting contract logs: %v", err)
			}
		}

		ci.state.ForkStates[forkId] = &contractIndexerForkState{
			Block:    finalBlockNumber,
			QueueLen: finalQueueLen,
		}

		return ci.persistState(tx)
	})
}
