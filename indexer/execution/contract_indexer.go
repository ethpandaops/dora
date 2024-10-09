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
	"github.com/ethpandaops/dora/utils"
)

type contractIndexer[TxType any] struct {
	indexer *IndexerCtx
	logger  logrus.FieldLogger
	options *contractIndexerOptions[TxType]
	state   *contractIndexerState
}

type contractIndexerOptions[TxType any] struct {
	indexerKey      string
	batchSize       int
	contractAddress common.Address
	deployBlock     uint64
	dequeueRate     uint64

	processFinalTx  func(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64) (*TxType, error)
	processRecentTx func(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, fork *forkWithClients) (*TxType, error)
	persistTxs      func(tx *sqlx.Tx, txs []*TxType) error
}

type contractIndexerState struct {
	FinalBlock    uint64                                       `json:"final_block"`
	FinalQueueLen uint64                                       `json:"final_queue"`
	ForkStates    map[beacon.ForkKey]*contractIndexerForkState `json:"fork_states"`
}

type contractIndexerForkState struct {
	Block    uint64 `json:"b"`
	QueueLen uint64 `json:"q"`
}

func newContractIndexer[TxType any](indexer *IndexerCtx, logger logrus.FieldLogger, options *contractIndexerOptions[TxType]) *contractIndexer[TxType] {
	batchSize := utils.Config.ExecutionApi.DepositLogBatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	ci := &contractIndexer[TxType]{
		indexer: indexer,
		logger:  logger,
		options: options,
	}

	return ci
}

func (ci *contractIndexer[_]) loadState() {
	syncState := contractIndexerState{}
	db.GetExplorerState(ci.options.indexerKey, &syncState)
	ci.state = &syncState

	if ci.state.ForkStates == nil {
		ci.state.ForkStates = make(map[beacon.ForkKey]*contractIndexerForkState)
	}

	if ci.state.FinalBlock == 0 {
		ci.state.FinalBlock = ci.options.deployBlock
	}
}

func (ci *contractIndexer[_]) persistState(tx *sqlx.Tx) error {
	finalizedBlockNumber := ci.getFinalizedBlockNumber()
	for forkId, forkState := range ci.state.ForkStates {
		if forkState.Block < finalizedBlockNumber {
			delete(ci.state.ForkStates, forkId)
		}
	}

	err := db.SetExplorerState(ci.options.indexerKey, ci.state, tx)
	if err != nil {
		return fmt.Errorf("error while updating contract indexer state: %v", err)
	}

	return nil
}

// runConsolidationIndexer runs the consolidation indexer logic.
// It fetches consolidation logs from finalized and recent blocks.
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

func (ci *contractIndexer[_]) loadFilteredLogs(ctx context.Context, client *execution.Client, query ethereum.FilterQuery) ([]types.Log, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	return client.GetRPCClient().GetEthClient().FilterLogs(ctx, query)
}

func (ci *contractIndexer[_]) loadTransactionByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Transaction, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tx, _, err := client.GetRPCClient().GetEthClient().TransactionByHash(ctx, hash)
	return tx, err
}

func (ci *contractIndexer[_]) loadHeaderByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return client.GetRPCClient().GetHeaderByHash(ctx, hash)
}

func (ci *contractIndexer[TxType]) processFinalizedBlocks(finalizedBlockNumber uint64) error {
	clients := ci.indexer.getFinalizedClients(execution.AnyClient)
	if len(clients) == 0 {
		return fmt.Errorf("no ready execution client found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	retryCount := 0

	for ci.state.FinalBlock < finalizedBlockNumber {
		client := clients[retryCount%len(clients)]

		batchSize := uint64(ci.options.batchSize)
		if retryCount > 0 {
			batchSize /= uint64(math.Pow(2, float64(retryCount)))
			if batchSize < 10 {
				batchSize = 10
			}
		}

		toBlock := ci.state.FinalBlock + uint64(ci.options.batchSize)
		if toBlock > finalizedBlockNumber {
			toBlock = finalizedBlockNumber
		}

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

		retryCount = 0

		var txHash, txHeaderHash []byte
		var txDetails *types.Transaction
		var txBlockHeader *types.Header

		requestTxs := []*TxType{}
		queueBlock := ci.state.FinalBlock
		queueLength := ci.state.FinalQueueLen

		ci.logger.Debugf("received contract logs for block %v - %v: %v events", ci.state.FinalBlock, toBlock, len(logs))

		for idx := range logs {
			log := &logs[idx]

			if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
				txDetails, err = ci.loadTransactionByHash(ctx, client, log.TxHash)
				if err != nil {
					return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
				}

				txHash = log.TxHash[:]
			}

			if txBlockHeader == nil || !bytes.Equal(txHeaderHash, log.BlockHash[:]) {
				txBlockHeader, err = ci.loadHeaderByHash(ctx, client, log.BlockHash)
				if err != nil {
					return fmt.Errorf("could not load block details (%v): %v", log.BlockHash, err)
				}

				txHeaderHash = log.BlockHash[:]
			}

			txFrom, err := types.Sender(types.LatestSignerForChainID(txDetails.ChainId()), txDetails)
			if err != nil {
				return fmt.Errorf("could not decode tx sender (%v): %v", log.TxHash, err)
			}

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

			var dequeueBlock uint64
			if ci.options.dequeueRate > 0 {
				dequeueBlock = log.BlockNumber + (queueLength / ci.options.dequeueRate)
				queueLength++
			} else {
				dequeueBlock = log.BlockNumber
			}

			requestTx, err := ci.options.processFinalTx(log, txDetails, txBlockHeader, txFrom, dequeueBlock)
			if err != nil {
				continue
			}

			if requestTx == nil {
				continue
			}

			requestTxs = append(requestTxs, requestTx)
		}

		if ci.options.dequeueRate > 0 && queueBlock < toBlock {
			dequeuedRequests := (toBlock - queueBlock) * ci.options.dequeueRate
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

		err = ci.persistFinalizedRequestTxs(toBlock, queueLength, requestTxs)
		if err != nil {
			return fmt.Errorf("could not persist indexed transactions: %v", err)
		}

		// cooldown to avoid rate limiting from external archive nodes
		time.Sleep(1 * time.Second)
	}
	return nil
}

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

func (ci *contractIndexer[TxType]) processRecentBlocksForFork(headFork *forkWithClients) error {
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
	startQueueLen := ci.state.FinalQueueLen

	// get last processed block for this fork
	if forkState := ci.state.ForkStates[headFork.forkId]; forkState != nil && forkState.Block <= elHeadBlockNumber {
		if forkState.Block == elHeadBlockNumber {
			return nil // already processed
		}

		startBlockNumber = forkState.Block + 1
		startQueueLen = forkState.QueueLen
	} else {
		for parentForkId := range ci.indexer.beaconIndexer.GetParentForkIds(headFork.forkId) {
			if parentForkState := ci.state.ForkStates[beacon.ForkKey(parentForkId)]; parentForkState != nil && parentForkState.Block <= elHeadBlockNumber {
				startBlockNumber = parentForkState.Block + 1
				startQueueLen = parentForkState.QueueLen
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

				if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
					txDetails, err = ci.loadTransactionByHash(ctx, client, log.TxHash)
					if err != nil {
						return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
					}

					txHash = log.TxHash[:]
				}

				if txBlockHeader == nil || !bytes.Equal(txHeaderHash, log.BlockHash[:]) {
					txBlockHeader, err = ci.loadHeaderByHash(ctx, client, log.BlockHash)
					if err != nil {
						return fmt.Errorf("could not load block details (%v): %v", log.BlockHash, err)
					}

					txHeaderHash = log.BlockHash[:]
				}

				txFrom, err := types.Sender(types.LatestSignerForChainID(txDetails.ChainId()), txDetails)
				if err != nil {
					return fmt.Errorf("could not decode tx sender (%v): %v", log.TxHash, err)
				}

				if queueBlock > log.BlockNumber {
					ci.logger.Warnf("contract log for block %v received after block %v", log.BlockNumber, queueBlock)
					return nil
				} else if ci.options.dequeueRate > 0 && queueBlock < log.BlockNumber {
					dequeuedRequests := (log.BlockNumber - queueBlock) * ci.options.dequeueRate
					if dequeuedRequests > startQueueLen {
						startQueueLen = 0
					} else {
						startQueueLen -= dequeuedRequests
					}

					queueBlock = log.BlockNumber
				}

				var dequeueBlock uint64
				if ci.options.dequeueRate > 0 {
					dequeueBlock = log.BlockNumber + (startQueueLen / ci.options.dequeueRate)
					startQueueLen++
				} else {
					dequeueBlock = log.BlockNumber
				}

				requestTx, err := ci.options.processRecentTx(log, txDetails, txBlockHeader, txFrom, dequeueBlock, headFork)
				if err != nil {
					continue
				}

				if requestTx == nil {
					continue
				}

				requestTxs = append(requestTxs, requestTx)
			}

			if queueBlock < toBlock {
				dequeuedRequests := (toBlock - queueBlock) * ci.options.dequeueRate
				if dequeuedRequests > startQueueLen {
					startQueueLen = 0
				} else {
					startQueueLen -= dequeuedRequests
				}

				queueBlock = toBlock
			}

			if len(requestTxs) > 0 {
				ci.logger.Infof("crawled recent contract logs for fork %v (%v-%v): %v events", headFork.forkId, startBlockNumber, toBlock, len(requestTxs))
			}

			err := ci.persistRecentRequestTxs(headFork.forkId, queueBlock, startQueueLen, requestTxs)
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
