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

func (ds *contractIndexer[_]) loadState() {
	syncState := contractIndexerState{}
	db.GetExplorerState(ds.options.indexerKey, &syncState)
	ds.state = &syncState

	if ds.state.ForkStates == nil {
		ds.state.ForkStates = make(map[beacon.ForkKey]*contractIndexerForkState)
	}

	if ds.state.FinalBlock == 0 {
		ds.state.FinalBlock = ds.options.deployBlock
	}
}

func (ds *contractIndexer[_]) persistState(tx *sqlx.Tx) error {
	finalizedBlockNumber := ds.getFinalizedBlockNumber()
	for forkId, forkState := range ds.state.ForkStates {
		if forkState.Block < finalizedBlockNumber {
			delete(ds.state.ForkStates, forkId)
		}
	}

	err := db.SetExplorerState(ds.options.indexerKey, ds.state, tx)
	if err != nil {
		return fmt.Errorf("error while updating contract indexer state: %v", err)
	}

	return nil
}

// runConsolidationIndexer runs the consolidation indexer logic.
// It fetches consolidation logs from finalized and recent blocks.
func (ds *contractIndexer[_]) runContractIndexer() error {
	if ds.state == nil {
		ds.loadState()
	}

	finalizedEpoch, _ := ds.indexer.chainState.GetFinalizedCheckpoint()
	if finalizedEpoch > 0 {
		finalizedBlockNumber := ds.getFinalizedBlockNumber()

		if finalizedBlockNumber == 0 {
			return fmt.Errorf("finalized block not found in cache or db")
		}

		if finalizedBlockNumber < ds.state.FinalBlock {
			return fmt.Errorf("finalized block number (%v) smaller than index state (%v)", finalizedBlockNumber, ds.state.FinalBlock)
		}

		if finalizedBlockNumber > ds.state.FinalBlock {
			err := ds.processFinalizedBlocks(finalizedBlockNumber)
			if err != nil {
				return err
			}
		}
	}

	ds.processRecentBlocks()

	return nil
}

func (ds *contractIndexer[_]) getFinalizedBlockNumber() uint64 {
	var finalizedBlockNumber uint64

	_, finalizedRoot := ds.indexer.chainState.GetFinalizedCheckpoint()
	if finalizedBlock := ds.indexer.beaconIndexer.GetBlockByRoot(finalizedRoot); finalizedBlock != nil {
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

func (ds *contractIndexer[_]) loadFilteredLogs(ctx context.Context, client *execution.Client, query ethereum.FilterQuery) ([]types.Log, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	return client.GetRPCClient().GetEthClient().FilterLogs(ctx, query)
}

func (ds *contractIndexer[_]) loadTransactionByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Transaction, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tx, _, err := client.GetRPCClient().GetEthClient().TransactionByHash(ctx, hash)
	return tx, err
}

func (ds *contractIndexer[_]) loadHeaderByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return client.GetRPCClient().GetHeaderByHash(ctx, hash)
}

func (ds *contractIndexer[TxType]) processFinalizedBlocks(finalizedBlockNumber uint64) error {
	clients := ds.indexer.getFinalizedClients(execution.AnyClient)
	if len(clients) == 0 {
		return fmt.Errorf("no ready execution client found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	retryCount := 0

	for ds.state.FinalBlock < finalizedBlockNumber {
		client := clients[retryCount%len(clients)]

		batchSize := uint64(ds.options.batchSize)
		if retryCount > 0 {
			batchSize /= uint64(math.Pow(2, float64(retryCount)))
			if batchSize < 10 {
				batchSize = 10
			}
		}

		toBlock := ds.state.FinalBlock + uint64(ds.options.batchSize)
		if toBlock > finalizedBlockNumber {
			toBlock = finalizedBlockNumber
		}

		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(0).SetUint64(ds.state.FinalBlock + 1),
			ToBlock:   big.NewInt(0).SetUint64(toBlock),
			Addresses: []common.Address{
				ds.options.contractAddress,
			},
		}

		logs, err := ds.loadFilteredLogs(ctx, client, query)
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
		queueBlock := ds.state.FinalBlock
		queueLength := ds.state.FinalQueueLen

		ds.logger.Debugf("received contract logs for block %v - %v: %v events", ds.state.FinalBlock, toBlock, len(logs))

		for idx := range logs {
			log := &logs[idx]

			if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
				txDetails, err = ds.loadTransactionByHash(ctx, client, log.TxHash)
				if err != nil {
					return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
				}

				txHash = log.TxHash[:]
			}

			if txBlockHeader == nil || !bytes.Equal(txHeaderHash, log.BlockHash[:]) {
				txBlockHeader, err = ds.loadHeaderByHash(ctx, client, log.BlockHash)
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
				ds.logger.Warnf("contract log for block %v received after block %v", log.BlockNumber, queueBlock)
				return nil
			} else if queueBlock < log.BlockNumber {
				dequeuedRequests := (log.BlockNumber - queueBlock) * ds.options.dequeueRate
				if dequeuedRequests > queueLength {
					queueLength = 0
				} else {
					queueLength -= dequeuedRequests
				}

				queueBlock = log.BlockNumber
			}

			dequeueBlock := log.BlockNumber + (queueLength / ds.options.dequeueRate)
			queueLength++

			requestTx, err := ds.options.processFinalTx(log, txDetails, txBlockHeader, txFrom, dequeueBlock)
			if err != nil {
				continue
			}

			if requestTx == nil {
				continue
			}

			requestTxs = append(requestTxs, requestTx)
		}

		if queueBlock < toBlock {
			dequeuedRequests := (toBlock - queueBlock) * ds.options.dequeueRate
			if dequeuedRequests > queueLength {
				queueLength = 0
			} else {
				queueLength -= dequeuedRequests
			}

			queueBlock = toBlock
		}

		if len(requestTxs) > 0 {
			ds.logger.Infof("crawled transactions for block %v - %v: %v events", ds.state.FinalBlock, toBlock, len(requestTxs))
		}

		err = ds.persistFinalizedRequestTxs(toBlock, queueLength, requestTxs)
		if err != nil {
			return fmt.Errorf("could not persist indexed transactions: %v", err)
		}

		// cooldown to avoid rate limiting from external archive nodes
		time.Sleep(1 * time.Second)
	}
	return nil
}

func (ds *contractIndexer[_]) processRecentBlocks() error {
	headForks := ds.indexer.getForksWithClients(execution.AnyClient)
	for _, headFork := range headForks {
		err := ds.processRecentBlocksForFork(headFork)
		if err != nil {
			if headFork.canonical {
				ds.logger.Errorf("could not process recent events from canonical fork %v: %v", headFork.forkId, err)
			} else {
				ds.logger.Warnf("could not process recent events from fork %v: %v", headFork.forkId, err)
			}
		}
	}
	return nil
}

func (ds *contractIndexer[TxType]) processRecentBlocksForFork(headFork *forkWithClients) error {
	elHeadBlock := ds.indexer.beaconIndexer.GetCanonicalHead(&headFork.forkId)
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

	startBlockNumber := ds.state.FinalBlock + 1
	startQueueLen := ds.state.FinalQueueLen

	// get last processed block for this fork
	if forkState := ds.state.ForkStates[headFork.forkId]; forkState != nil && forkState.Block <= elHeadBlockNumber {
		if forkState.Block == elHeadBlockNumber {
			return nil // already processed
		}

		startBlockNumber = forkState.Block + 1
		startQueueLen = forkState.QueueLen
	} else {
		for parentForkId := range ds.indexer.beaconIndexer.GetParentForkIds(headFork.forkId) {
			if parentForkState := ds.state.ForkStates[beacon.ForkKey(parentForkId)]; parentForkState != nil && parentForkState.Block <= elHeadBlockNumber {
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

			batchSize := uint64(ds.options.batchSize)
			if retryCount > 0 {
				batchSize /= uint64(math.Pow(2, float64(retryCount)))
				if batchSize < 10 {
					batchSize = 10
				}
			}

			toBlock = startBlockNumber + uint64(ds.options.batchSize)
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
					ds.options.contractAddress,
				},
			}

			logs, reqError = ds.loadFilteredLogs(ctx, client, query)
			if reqError != nil {
				ds.logger.Warnf("error fetching contract logs for fork %v (%v-%v): %v", headFork.forkId, startBlockNumber, toBlock, reqError)
				continue
			}

			for idx := range logs {
				var err error

				log := &logs[idx]

				if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
					txDetails, err = ds.loadTransactionByHash(ctx, client, log.TxHash)
					if err != nil {
						return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
					}

					txHash = log.TxHash[:]
				}

				if txBlockHeader == nil || !bytes.Equal(txHeaderHash, log.BlockHash[:]) {
					txBlockHeader, err = ds.loadHeaderByHash(ctx, client, log.BlockHash)
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
					ds.logger.Warnf("contract log for block %v received after block %v", log.BlockNumber, queueBlock)
					return nil
				} else if queueBlock < log.BlockNumber {
					dequeuedRequests := (log.BlockNumber - queueBlock) * ds.options.dequeueRate
					if dequeuedRequests > startQueueLen {
						startQueueLen = 0
					} else {
						startQueueLen -= dequeuedRequests
					}

					queueBlock = log.BlockNumber
				}

				dequeueBlock := log.BlockNumber + (startQueueLen / ds.options.dequeueRate)
				startQueueLen++

				requestTx, err := ds.options.processRecentTx(log, txDetails, txBlockHeader, txFrom, dequeueBlock, headFork)
				if err != nil {
					continue
				}

				if requestTx == nil {
					continue
				}

				requestTxs = append(requestTxs, requestTx)
			}

			if queueBlock < toBlock {
				dequeuedRequests := (toBlock - queueBlock) * ds.options.dequeueRate
				if dequeuedRequests > startQueueLen {
					startQueueLen = 0
				} else {
					startQueueLen -= dequeuedRequests
				}

				queueBlock = toBlock
			}

			if len(requestTxs) > 0 {
				ds.logger.Infof("crawled recent contract logs for fork %v (%v-%v): %v events", headFork.forkId, startBlockNumber, toBlock, len(requestTxs))

				err := ds.persistRecentRequestTxs(headFork.forkId, queueBlock, startQueueLen, requestTxs)
				if err != nil {
					return fmt.Errorf("could not persist contract logs: %v", err)
				}

				time.Sleep(1 * time.Second)
			}

			break
		}

		if reqError != nil {
			return fmt.Errorf("error fetching contract logs for fork %v (%v-%v): %v", headFork.forkId, startBlockNumber, toBlock, reqError)
		}

		startBlockNumber = toBlock + 1
	}

	return resError
}

func (ds *contractIndexer[TxType]) persistFinalizedRequestTxs(finalBlockNumber, finalQueueLen uint64, requests []*TxType) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		err := ds.options.persistTxs(tx, requests)
		if err != nil {
			return fmt.Errorf("error while persisting contract logs: %v", err)
		}

		ds.state.FinalBlock = finalBlockNumber
		ds.state.FinalQueueLen = finalQueueLen

		return ds.persistState(tx)
	})
}

func (ds *contractIndexer[TxType]) persistRecentRequestTxs(forkId beacon.ForkKey, finalBlockNumber, finalQueueLen uint64, requests []*TxType) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		err := ds.options.persistTxs(tx, requests)
		if err != nil {
			return fmt.Errorf("error while persisting contract logs: %v", err)
		}

		ds.state.ForkStates[forkId] = &contractIndexerForkState{
			Block:    finalBlockNumber,
			QueueLen: finalQueueLen,
		}

		return ds.persistState(tx)
	})
}
