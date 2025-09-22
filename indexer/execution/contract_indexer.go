package execution

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
)

// contractIndexer handles the indexing of contract events for a specific system contract
// it crawls logs in order and tracks the queue length to precalculate the dequeue block number where the request will be sent to the beacon chain
type contractIndexer[TxType any] struct {
	indexer *IndexerCtx
	logger  logrus.FieldLogger
	options *contractIndexerOptions[TxType]
	state   *contractIndexerState
}

// contractIndexerOptions defines the configuration for the contract indexer
type contractIndexerOptions[TxType any] struct {
	stateKey        string         // key to identify the indexer state in the database
	batchSize       int            // number of logs to fetch per request
	contractAddress common.Address // address of the contract to index
	deployBlock     uint64         // block number from where to start crawling logs
	dequeueRate     uint64         // number of logs to dequeue per block, 0 for no queue

	// processFinalTx processes a finalized transaction log
	processFinalTx func(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, parentTxs []*TxType) (*TxType, error)

	// processRecentTx processes a recent (non-finalized) transaction log
	processRecentTx func(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, fork *forkWithClients, parentTxs []*TxType) (*TxType, error)

	// persistTxs persists processed transactions to the database
	persistTxs func(tx *sqlx.Tx, txs []*TxType) error
}

// contractIndexerState represents the current state of the contract indexer
type contractIndexerState struct {
	FinalBlock    uint64                                       `json:"final_block"`
	FinalQueueLen uint64                                       `json:"final_queue"`
	ForkStates    map[beacon.ForkKey]*contractIndexerForkState `json:"fork_states"`
}

// contractIndexerForkState represents the state of the contract indexer for a specific unfinalized fork
type contractIndexerForkState struct {
	Block    uint64 `json:"b"`
	QueueLen uint64 `json:"q"`
}

// newContractIndexer creates a new contract indexer with the given options
func newContractIndexer[TxType any](indexer *IndexerCtx, logger logrus.FieldLogger, options *contractIndexerOptions[TxType]) *contractIndexer[TxType] {
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
	db.GetExplorerState(ci.options.stateKey, &syncState)
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

	err := db.SetExplorerState(ci.options.stateKey, ci.state, tx)
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

	finalizedEpoch, _ := ci.indexer.chainState.GetFinalizedCheckpoint()
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

	_, finalizedRoot := ci.indexer.chainState.GetFinalizedCheckpoint()
	if finalizedBlock := ci.indexer.beaconIndexer.GetBlockByRoot(finalizedRoot); finalizedBlock != nil {
		if indexVals := finalizedBlock.GetBlockIndex(); indexVals != nil {
			finalizedBlockNumber = indexVals.ExecutionNumber
		}
	}

	if finalizedBlockNumber == 0 {
		// load from db
		if finalizedBlock := db.GetSlotByRoot(finalizedRoot[:]); finalizedBlock != nil && finalizedBlock.EthBlockNumber != nil {
			finalizedBlockNumber = *finalizedBlock.EthBlockNumber
		}
	}

	return finalizedBlockNumber
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

// loadHeaderByHash fetches a block header by its hash from the execution client
func (ci *contractIndexer[_]) loadHeaderByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return client.GetRPCClient().GetHeaderByHash(ctx, hash)
}

// processFinalizedBlocks processes contract events from finalized block ranges
// it fetches logs in batches and calls the provided processFinalTx function to process each log
func (ci *contractIndexer[TxType]) processFinalizedBlocks(finalizedBlockNumber uint64) error {
	clients := ci.indexer.getFinalizedClients(execution.AnyClient)
	if len(clients) == 0 {
		return fmt.Errorf("no ready execution client found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
				ci.options.contractAddress,
			},
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
				// calculate how many requests were dequeued since the last processed log
				dequeuedRequests := (log.BlockNumber - queueBlock) * ci.options.dequeueRate
				if dequeuedRequests > queueLength {
					queueLength = 0
				} else {
					queueLength -= dequeuedRequests
				}

				queueBlock = log.BlockNumber
			}

			// calculate the dequeue block number for the current log
			var dequeueBlock uint64
			if ci.options.dequeueRate > 0 {
				dequeueBlock = log.BlockNumber + (queueLength / ci.options.dequeueRate)
				queueLength++
			} else {
				dequeueBlock = log.BlockNumber
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

		// calculate how many requests were dequeued at the end of the current block range
		if ci.options.dequeueRate > 0 {
			// we need to add 1 to the block range as we want to preserve the queue state after the last block in the range
			dequeuedRequests := (toBlock - queueBlock + 1) * ci.options.dequeueRate
			if dequeuedRequests > queueLength {
				queueLength = 0
			} else {
				queueLength -= dequeuedRequests
			}

			queueBlock = toBlock
		}

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
	headForks := ci.indexer.getForksWithClients(execution.AnyClient)
	for _, headFork := range headForks {
		err := ci.processRecentBlocksForFork(headFork)
		if err != nil {
			if headFork.canonical {
				ci.logger.Errorf("could not process recent events from canonical fork %v: %v", headFork.forkId, err)
			} else {
				ci.logger.Warnf("could not process recent events from fork %v: %v", headFork.forkId, err)
			}
		}
	}
	return nil
}

// processRecentBlocksForFork processes contract events from recent blocks for a specific fork
func (ci *contractIndexer[TxType]) processRecentBlocksForFork(headFork *forkWithClients) error {
	// get the head el block number for the fork
	elHeadBlock := ci.indexer.beaconIndexer.GetCanonicalHead(&headFork.forkId)
	if elHeadBlock == nil {
		return fmt.Errorf("head block not found")
	}

	elHeadBlockIndex := elHeadBlock.GetBlockIndex()
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
	if forkState := ci.state.ForkStates[headFork.forkId]; forkState != nil && forkState.Block <= elHeadBlockNumber {
		if forkState.Block == elHeadBlockNumber {
			return nil // already processed
		}

		startBlockNumber = forkState.Block + 1
		queueLength = forkState.QueueLen
	} else {
		// seems we haven't seen this fork before, check if we can continue from a parent fork
		for parentForkId := range ci.indexer.beaconIndexer.GetParentForkIds(headFork.forkId) {
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
			client := headFork.clients[retryCount%len(headFork.clients)]

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
			ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
			ctxCancel = cancel

			// fetch logs from the execution client
			query := ethereum.FilterQuery{
				FromBlock: big.NewInt(0).SetUint64(startBlockNumber),
				ToBlock:   big.NewInt(0).SetUint64(toBlock),
				Addresses: []common.Address{
					ci.options.contractAddress,
				},
			}

			logs, reqError = ci.loadFilteredLogs(ctx, client, query)
			if reqError != nil {
				ci.logger.Warnf("error fetching contract logs for fork %v (%v-%v): %v", headFork.forkId, startBlockNumber, toBlock, reqError)
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
					dequeuedRequests := (log.BlockNumber - queueBlock) * ci.options.dequeueRate
					if dequeuedRequests > queueLength {
						queueLength = 0
					} else {
						queueLength -= dequeuedRequests
					}

					queueBlock = log.BlockNumber
				}

				// calculate the dequeue block number for the current log
				var dequeueBlock uint64
				if ci.options.dequeueRate > 0 {
					dequeueBlock = log.BlockNumber + (queueLength / ci.options.dequeueRate)
					queueLength++
				} else {
					dequeueBlock = log.BlockNumber
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

			// calculate how many requests were dequeued at the end of the current block range
			if ci.options.dequeueRate > 0 {
				dequeuedRequests := (toBlock - queueBlock + 1) * ci.options.dequeueRate
				if dequeuedRequests > queueLength {
					queueLength = 0
				} else {
					queueLength -= dequeuedRequests
				}
			}

			queueBlock = toBlock

			if len(requestTxs) > 0 {
				ci.logger.Infof("crawled recent contract logs for fork %v (%v-%v): %v events", headFork.forkId, startBlockNumber, toBlock, len(requestTxs))
			}

			// persist the processed transactions and update the indexer state
			err := ci.persistRecentRequestTxs(headFork.forkId, queueBlock, queueLength, requestTxs)
			if err != nil {
				return fmt.Errorf("could not persist contract logs: %v", err)
			}

			// cooldown to avoid rate limiting from external archive nodes
			time.Sleep(1 * time.Second)

			break
		}

		if reqError != nil {
			return fmt.Errorf("error fetching contract logs for fork %v (%v-%v): %v", headFork.forkId, startBlockNumber, toBlock, reqError)
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
