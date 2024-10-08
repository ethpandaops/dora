package execution

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
)

const consolidationContractAddr = "0x"
const consolidationDequeueRate = 1

type ConsolidationIndexer struct {
	indexer         *IndexerCtx
	logger          logrus.FieldLogger
	state           *consolidationIndexerState
	batchSize       int
	contractAddress common.Address
	forkStates      map[beacon.ForkKey]*consolidationIndexerForkState
}

type consolidationIndexerState struct {
	FinalBlock    uint64                                            `json:"final_block"`
	FinalQueueLen uint64                                            `json:"final_queue"`
	ForkStates    map[beacon.ForkKey]*consolidationIndexerForkState `json:"fork_states"`
}

type consolidationIndexerForkState struct {
	Block    uint64 `json:"b"`
	QueueLen uint64 `json:"q"`
}

func NewConsolidationIndexer(indexer *IndexerCtx) *ConsolidationIndexer {
	batchSize := utils.Config.ExecutionApi.DepositLogBatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	ci := &ConsolidationIndexer{
		indexer:         indexer,
		logger:          indexer.logger.WithField("indexer", "deposit"),
		batchSize:       batchSize,
		contractAddress: common.HexToAddress(consolidationContractAddr),
		forkStates:      map[beacon.ForkKey]*consolidationIndexerForkState{},
	}

	go ci.runConsolidationIndexerLoop()

	return ci
}

func (ds *ConsolidationIndexer) runConsolidationIndexerLoop() {
	defer utils.HandleSubroutinePanic("ConsolidationIndexer.runConsolidationIndexerLoop")

	for {
		time.Sleep(60 * time.Second)
		ds.logger.Debugf("run consolidation indexer logic")

		err := ds.runConsolidationIndexer()
		if err != nil {
			ds.logger.Errorf("consolidation indexer error: %v", err)
		}
	}
}

func (ds *ConsolidationIndexer) runConsolidationIndexer() error {
	// get indexer state
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

func (ds *ConsolidationIndexer) loadState() {
	syncState := consolidationIndexerState{}
	db.GetExplorerState("indexer.consolidationstate", &syncState)
	ds.state = &syncState
}

func (ds *ConsolidationIndexer) getFinalizedBlockNumber() uint64 {
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

func (ds *ConsolidationIndexer) loadFilteredLogs(ctx context.Context, client *execution.Client, query ethereum.FilterQuery) ([]types.Log, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	return client.GetRPCClient().GetEthClient().FilterLogs(ctx, query)
}

func (ds *ConsolidationIndexer) loadTransactionByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Transaction, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tx, _, err := client.GetRPCClient().GetEthClient().TransactionByHash(ctx, hash)
	return tx, err
}

func (ds *ConsolidationIndexer) loadHeaderByNumber(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return client.GetRPCClient().GetHeaderByHash(ctx, hash)
}

func (ds *ConsolidationIndexer) processFinalizedBlocks(finalizedBlockNumber uint64) error {
	clients := ds.indexer.getFinalizedClients(execution.AnyClient)
	if len(clients) == 0 {
		return fmt.Errorf("no ready execution client found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	retryCount := 0

	for ds.state.FinalBlock < finalizedBlockNumber {
		client := clients[retryCount%len(clients)]

		batchSize := uint64(ds.batchSize)
		if retryCount > 0 {
			batchSize /= uint64(math.Pow(2, float64(retryCount)))
			if batchSize < 10 {
				batchSize = 10
			}
		}

		toBlock := ds.state.FinalBlock + uint64(ds.batchSize)
		if toBlock > finalizedBlockNumber {
			toBlock = finalizedBlockNumber
		}

		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(0).SetUint64(ds.state.FinalBlock + 1),
			ToBlock:   big.NewInt(0).SetUint64(toBlock),
			Addresses: []common.Address{
				ds.contractAddress,
			},
		}

		logs, err := ds.loadFilteredLogs(ctx, client, query)
		if err != nil {
			if retryCount < 3 {
				retryCount++
				continue
			}

			return fmt.Errorf("error fetching consolidation contract logs: %v", err)
		}

		retryCount = 0

		var txHash []byte
		var txDetails *types.Transaction
		var txBlockHeader *types.Header

		requestTxs := []*dbtypes.ConsolidationRequestTx{}
		queueBlock := ds.state.FinalBlock
		queueLength := ds.state.FinalQueueLen

		ds.logger.Infof("received consolidation log for block %v - %v: %v events", ds.state.FinalBlock, toBlock, len(logs))

		for idx := range logs {
			log := &logs[idx]

			if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
				txDetails, err = ds.loadTransactionByHash(ctx, client, log.TxHash)
				if err != nil {
					return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
				}

				txBlockHeader, err = ds.loadHeaderByNumber(ctx, client, log.BlockHash)
				if err != nil {
					return fmt.Errorf("could not load block details (%v): %v", log.TxHash, err)
				}

				txHash = log.TxHash[:]
			}

			txFrom, err := types.Sender(types.LatestSignerForChainID(txDetails.ChainId()), txDetails)
			if err != nil {
				return fmt.Errorf("could not decode tx sender (%v): %v", log.TxHash, err)
			}
			txTo := *txDetails.To()

			requestTx := ds.parseRequestLog(log, &queueBlock, &queueLength)
			if requestTx == nil {
				continue
			}

			requestTx.BlockTime = txBlockHeader.Time
			requestTx.TxSender = txFrom[:]
			requestTx.TxTarget = txTo[:]

			requestTxs = append(requestTxs, requestTx)
		}

		if queueBlock < toBlock {
			dequeuedRequests := (toBlock - queueBlock) * consolidationDequeueRate
			if dequeuedRequests > queueLength {
				queueLength = 0
			} else {
				queueLength -= dequeuedRequests
			}

			queueBlock = toBlock
		}

		if len(requestTxs) > 0 {
			ds.logger.Infof("crawled consolidation transactions for block %v - %v: %v consolidations", ds.state.FinalBlock, toBlock, len(requestTxs))
		}

		err = ds.persistFinalizedRequestTxs(toBlock, queueLength, requestTxs)
		if err != nil {
			return fmt.Errorf("could not persist consolidation txs: %v", err)
		}

		// cooldown to avoid rate limiting from external archive nodes
		time.Sleep(1 * time.Second)
	}
	return nil
}

func (ds *ConsolidationIndexer) parseRequestLog(log *types.Log, queueBlock, queueLength *uint64) *dbtypes.ConsolidationRequestTx {
	// data layout:
	// 0-20: sender address (20 bytes)
	// 20-68: source pubkey (48 bytes)
	// 68-116: target pubkey (48 bytes)

	if len(log.Data) < 116 {
		ds.logger.Warnf("invalid consolidation log data length: %v", len(log.Data))
		return nil
	}

	senderAddr := log.Data[:20]
	sourcePubkey := log.Data[20:68]
	targetPubkey := log.Data[68:116]

	if *queueBlock > log.BlockNumber {
		ds.logger.Warnf("consolidation request for block %v received after block %v", log.BlockNumber, queueBlock)
		return nil
	} else if *queueBlock < log.BlockNumber {
		dequeuedRequests := (log.BlockNumber - *queueBlock) * consolidationDequeueRate
		if dequeuedRequests > *queueLength {
			*queueLength = 0
		} else {
			*queueLength -= dequeuedRequests
		}

		*queueBlock = log.BlockNumber
	}

	dequeueBlock := log.BlockNumber + (*queueLength / consolidationDequeueRate)
	*queueLength++

	requestTx := &dbtypes.ConsolidationRequestTx{
		BlockNumber:   log.BlockNumber,
		BlockIndex:    uint64(log.Index),
		BlockRoot:     log.BlockHash[:],
		SourceAddress: senderAddr,
		SourcePubkey:  sourcePubkey,
		TargetPubkey:  targetPubkey,
		TxHash:        log.TxHash[:],
		DequeueBlock:  dequeueBlock,
	}

	return requestTx
}

func (ds *ConsolidationIndexer) processRecentBlocks() error {
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

func (ds *ConsolidationIndexer) processRecentBlocksForFork(headFork *forkWithClients) error {
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
	if forkState := ds.forkStates[headFork.forkId]; forkState != nil && forkState.Block <= elHeadBlockNumber {
		if forkState.Block == elHeadBlockNumber {
			return nil // already processed
		}

		startBlockNumber = forkState.Block + 1
		startQueueLen = forkState.QueueLen
	} else {
		for parentForkId := range ds.indexer.beaconIndexer.GetParentForkIds(headFork.forkId) {
			if parentForkState := ds.forkStates[beacon.ForkKey(parentForkId)]; parentForkState != nil && parentForkState.Block <= elHeadBlockNumber {
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
		var txHash []byte
		var txDetails *types.Transaction
		var txBlockHeader *types.Header

		requestTxs := []*dbtypes.ConsolidationRequestTx{}

		for retryCount := 0; retryCount < 3; retryCount++ {
			client := headFork.clients[retryCount%len(headFork.clients)]

			batchSize := uint64(ds.batchSize)
			if retryCount > 0 {
				batchSize /= uint64(math.Pow(2, float64(retryCount)))
				if batchSize < 10 {
					batchSize = 10
				}
			}

			toBlock = startBlockNumber + uint64(ds.batchSize)
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
					ds.contractAddress,
				},
			}

			logs, reqError = ds.loadFilteredLogs(ctx, client, query)
			if reqError != nil {
				ds.logger.Warnf("error fetching consolidation contract logs for fork %v (%v-%v): %v", headFork.forkId, startBlockNumber, toBlock, reqError)
				continue
			}

			for idx := range logs {
				log := &logs[idx]

				if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
					var err error

					txDetails, err = ds.loadTransactionByHash(ctx, client, log.TxHash)
					if err != nil {
						return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
					}

					txBlockHeader, err = ds.loadHeaderByNumber(ctx, client, log.BlockHash)
					if err != nil {
						return fmt.Errorf("could not load block details (%v): %v", log.TxHash, err)
					}

					txHash = log.TxHash[:]
				}

				txFrom, err := types.Sender(types.LatestSignerForChainID(txDetails.ChainId()), txDetails)
				if err != nil {
					return fmt.Errorf("could not decode tx sender (%v): %v", log.TxHash, err)
				}
				txTo := *txDetails.To()

				requestTx := ds.parseRequestLog(log, &queueBlock, &startQueueLen)
				if requestTx == nil {
					continue
				}

				if clBlock := ds.indexer.beaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(log.BlockHash)); len(clBlock) > 0 {
					requestTx.ForkId = uint64(clBlock[0].GetForkId())
				} else {
					requestTx.ForkId = uint64(headFork.forkId)
				}

				requestTx.BlockTime = txBlockHeader.Time
				requestTx.TxSender = txFrom[:]
				requestTx.TxTarget = txTo[:]

				requestTxs = append(requestTxs, requestTx)
			}

			if queueBlock < toBlock {
				dequeuedRequests := (toBlock - queueBlock) * consolidationDequeueRate
				if dequeuedRequests > startQueueLen {
					startQueueLen = 0
				} else {
					startQueueLen -= dequeuedRequests
				}

				queueBlock = toBlock
			}

			if len(requestTxs) > 0 {
				ds.logger.Infof("crawled recent consolidations for fork %v (%v-%v): %v deposits", headFork.forkId, startBlockNumber, toBlock, len(requestTxs))

				err := ds.persistRecentRequestTxs(headFork.forkId, queueBlock, startQueueLen, requestTxs)
				if err != nil {
					return fmt.Errorf("could not persist deposit txs: %v", err)
				}

				time.Sleep(1 * time.Second)
			}

			break
		}

		if reqError != nil {
			return fmt.Errorf("error fetching consolidation contract logs for fork %v (%v-%v): %v", headFork.forkId, startBlockNumber, toBlock, reqError)
		}

		startBlockNumber = toBlock + 1
	}

	return resError
}

func (ds *ConsolidationIndexer) persistFinalizedRequestTxs(finalBlockNumber, finalQueueLen uint64, requests []*dbtypes.ConsolidationRequestTx) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		requestCount := len(requests)
		for requestIdx := 0; requestIdx < requestCount; requestIdx += 500 {
			endIdx := requestIdx + 500
			if endIdx > requestCount {
				endIdx = requestCount
			}

			err := db.InsertConsolidationRequestTxs(requests[requestIdx:endIdx], tx)
			if err != nil {
				return fmt.Errorf("error while inserting consolidation txs: %v", err)
			}
		}

		ds.state.FinalBlock = finalBlockNumber
		ds.state.FinalQueueLen = finalQueueLen

		return ds.persistState(tx)
	})
}

func (ds *ConsolidationIndexer) persistRecentRequestTxs(forkId beacon.ForkKey, finalBlockNumber, finalQueueLen uint64, requests []*dbtypes.ConsolidationRequestTx) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		requestCount := len(requests)
		for requestIdx := 0; requestIdx < requestCount; requestIdx += 500 {
			endIdx := requestIdx + 500
			if endIdx > requestCount {
				endIdx = requestCount
			}

			err := db.InsertConsolidationRequestTxs(requests[requestIdx:endIdx], tx)
			if err != nil {
				return fmt.Errorf("error while inserting consolidation txs: %v", err)
			}
		}

		ds.forkStates[forkId] = &consolidationIndexerForkState{
			Block:    finalBlockNumber,
			QueueLen: finalQueueLen,
		}

		return ds.persistState(tx)
	})
}

func (ds *ConsolidationIndexer) persistState(tx *sqlx.Tx) error {
	finalizedBlockNumber := ds.getFinalizedBlockNumber()
	for forkId, forkState := range ds.forkStates {
		if forkState.Block < finalizedBlockNumber {
			delete(ds.forkStates, forkId)
		}
	}

	ds.state.ForkStates = ds.forkStates

	err := db.SetExplorerState("indexer.consolidationstate", ds.state, tx)
	if err != nil {
		return fmt.Errorf("error while updating deposit state: %v", err)
	}

	return nil
}
