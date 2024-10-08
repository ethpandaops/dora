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

const withdrawalContractAddr = "0x"
const withdrawalDequeueRate = 1

type WithdrawalIndexer struct {
	indexer         *IndexerCtx
	logger          logrus.FieldLogger
	state           *withdrawalIndexerState
	batchSize       int
	contractAddress common.Address
	forkStates      map[beacon.ForkKey]*withdrawalIndexerForkState
}

type withdrawalIndexerState struct {
	FinalBlock    uint64                                         `json:"final_block"`
	FinalQueueLen uint64                                         `json:"final_queue"`
	ForkStates    map[beacon.ForkKey]*withdrawalIndexerForkState `json:"fork_states"`
}

type withdrawalIndexerForkState struct {
	Block    uint64 `json:"b"`
	QueueLen uint64 `json:"q"`
}

func NewWithdrawalIndexer(indexer *IndexerCtx) *WithdrawalIndexer {
	batchSize := utils.Config.ExecutionApi.DepositLogBatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	ci := &WithdrawalIndexer{
		indexer:         indexer,
		logger:          indexer.logger.WithField("indexer", "withdrawals"),
		batchSize:       batchSize,
		contractAddress: common.HexToAddress(withdrawalContractAddr),
		forkStates:      map[beacon.ForkKey]*withdrawalIndexerForkState{},
	}

	go ci.runWithdrawalIndexerLoop()

	return ci
}

func (ds *WithdrawalIndexer) runWithdrawalIndexerLoop() {
	defer utils.HandleSubroutinePanic("WithdrawalIndexer.runWithdrawalIndexerLoop")

	for {
		time.Sleep(30 * time.Second)
		ds.logger.Debugf("run withdrawal indexer logic")

		err := ds.runWithdrawalIndexer()
		if err != nil {
			ds.logger.Errorf("withdrawal indexer error: %v", err)
		}
	}
}

func (ds *WithdrawalIndexer) runWithdrawalIndexer() error {
	// get indexer state
	if ds.state == nil {
		ds.loadState()
	}

	specs := ds.indexer.chainState.GetSpecs()
	if ds.indexer.chainState.CurrentEpoch() < phase0.Epoch(*specs.ElectraForkEpoch) {
		// skip consolidation indexer before Electra fork
		return nil
	}

	if ds.state.FinalBlock == 0 {
		// start from electra fork block
		electraSlot := ds.indexer.chainState.EpochToSlot(phase0.Epoch(*specs.ElectraForkEpoch))
		dbSlotRoot := db.GetFirstRootAfterSlot(uint64(electraSlot), false)
		if dbSlotRoot == nil {
			dbSlot := db.GetSlotByRoot(dbSlotRoot)
			ds.state.FinalBlock = *dbSlot.EthBlockNumber
		}
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

func (ds *WithdrawalIndexer) loadState() {
	syncState := withdrawalIndexerState{}
	db.GetExplorerState("indexer.withdrawalstate", &syncState)
	ds.state = &syncState
}

func (ds *WithdrawalIndexer) getFinalizedBlockNumber() uint64 {
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

func (ds *WithdrawalIndexer) loadFilteredLogs(ctx context.Context, client *execution.Client, query ethereum.FilterQuery) ([]types.Log, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	return client.GetRPCClient().GetEthClient().FilterLogs(ctx, query)
}

func (ds *WithdrawalIndexer) loadTransactionByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Transaction, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tx, _, err := client.GetRPCClient().GetEthClient().TransactionByHash(ctx, hash)
	return tx, err
}

func (ds *WithdrawalIndexer) loadHeaderByHash(ctx context.Context, client *execution.Client, hash common.Hash) (*types.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return client.GetRPCClient().GetHeaderByHash(ctx, hash)
}

func (ds *WithdrawalIndexer) processFinalizedBlocks(finalizedBlockNumber uint64) error {
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

			return fmt.Errorf("error fetching withdrawal contract logs: %v", err)
		}

		retryCount = 0

		var txHash []byte
		var txDetails *types.Transaction
		var txBlockHeader *types.Header

		requestTxs := []*dbtypes.WithdrawalRequestTx{}
		queueBlock := ds.state.FinalBlock
		queueLength := ds.state.FinalQueueLen

		ds.logger.Infof("received withdrawal log for block %v - %v: %v events", ds.state.FinalBlock, toBlock, len(logs))

		for idx := range logs {
			log := &logs[idx]

			if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
				txDetails, err = ds.loadTransactionByHash(ctx, client, log.TxHash)
				if err != nil {
					return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
				}

				txBlockHeader, err = ds.loadHeaderByHash(ctx, client, log.BlockHash)
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
			dequeuedRequests := (toBlock - queueBlock) * withdrawalDequeueRate
			if dequeuedRequests > queueLength {
				queueLength = 0
			} else {
				queueLength -= dequeuedRequests
			}

			queueBlock = toBlock
		}

		if len(requestTxs) > 0 {
			ds.logger.Infof("crawled withdrawal transactions for block %v - %v: %v withdrawals", ds.state.FinalBlock, toBlock, len(requestTxs))
		}

		err = ds.persistFinalizedRequestTxs(toBlock, queueLength, requestTxs)
		if err != nil {
			return fmt.Errorf("could not persist withdrawal txs: %v", err)
		}

		// cooldown to avoid rate limiting from external archive nodes
		time.Sleep(1 * time.Second)
	}
	return nil
}

func (ds *WithdrawalIndexer) parseRequestLog(log *types.Log, queueBlock, queueLength *uint64) *dbtypes.WithdrawalRequestTx {
	// data layout:
	// 0-20: sender address (20 bytes)
	// 20-68: validator pubkey (48 bytes)
	// 68-76: amount (8 bytes)

	if len(log.Data) < 76 {
		ds.logger.Warnf("invalid withdrawal log data length: %v", len(log.Data))
		return nil
	}

	senderAddr := log.Data[:20]
	sourcePubkey := log.Data[20:68]
	amount := big.NewInt(0).SetBytes(log.Data[68:76]).Uint64()

	if *queueBlock > log.BlockNumber {
		ds.logger.Warnf("withdrawal request for block %v received after block %v", log.BlockNumber, queueBlock)
		return nil
	} else if *queueBlock < log.BlockNumber {
		dequeuedRequests := (log.BlockNumber - *queueBlock) * withdrawalDequeueRate
		if dequeuedRequests > *queueLength {
			*queueLength = 0
		} else {
			*queueLength -= dequeuedRequests
		}

		*queueBlock = log.BlockNumber
	}

	dequeueBlock := log.BlockNumber + (*queueLength / withdrawalDequeueRate)
	*queueLength++

	requestTx := &dbtypes.WithdrawalRequestTx{
		BlockNumber:     log.BlockNumber,
		BlockIndex:      uint64(log.Index),
		BlockRoot:       log.BlockHash[:],
		SourceAddress:   senderAddr,
		ValidatorPubkey: sourcePubkey,
		Amount:          amount,
		TxHash:          log.TxHash[:],
		DequeueBlock:    dequeueBlock,
	}

	return requestTx
}

func (ds *WithdrawalIndexer) processRecentBlocks() error {
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

func (ds *WithdrawalIndexer) processRecentBlocksForFork(headFork *forkWithClients) error {
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
		var clBlock []*beacon.Block

		requestTxs := []*dbtypes.WithdrawalRequestTx{}

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
				ds.logger.Warnf("error fetching withdrawal contract logs for fork %v (%v-%v): %v", headFork.forkId, startBlockNumber, toBlock, reqError)
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

					clBlock = ds.indexer.beaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(log.BlockHash))

					txHash = log.TxHash[:]
				}

				var blockTime uint64
				if len(clBlock) > 0 {
					blockTime = uint64(ds.indexer.chainState.SlotToTime(clBlock[0].Slot).Unix())
				} else if txBlockHeader == nil || txBlockHeader.Hash() != log.BlockHash {
					var err error

					txBlockHeader, err = ds.loadHeaderByHash(ctx, client, log.BlockHash)
					if err != nil {
						return fmt.Errorf("could not load block details (%v): %v", log.TxHash, err)
					}

					blockTime = txBlockHeader.Time
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

				if len(clBlock) > 0 {
					requestTx.ForkId = uint64(clBlock[0].GetForkId())
				} else {
					requestTx.ForkId = uint64(headFork.forkId)
				}

				requestTx.BlockTime = blockTime
				requestTx.TxSender = txFrom[:]
				requestTx.TxTarget = txTo[:]

				requestTxs = append(requestTxs, requestTx)
			}

			if queueBlock < toBlock {
				dequeuedRequests := (toBlock - queueBlock) * withdrawalDequeueRate
				if dequeuedRequests > startQueueLen {
					startQueueLen = 0
				} else {
					startQueueLen -= dequeuedRequests
				}

				queueBlock = toBlock
			}

			if len(requestTxs) > 0 {
				ds.logger.Infof("crawled recent withdrawals for fork %v (%v-%v): %v withdrawals", headFork.forkId, startBlockNumber, toBlock, len(requestTxs))

				err := ds.persistRecentRequestTxs(headFork.forkId, queueBlock, startQueueLen, requestTxs)
				if err != nil {
					return fmt.Errorf("could not persist withdrawal tx: %v", err)
				}

				time.Sleep(1 * time.Second)
			}

			break
		}

		if reqError != nil {
			return fmt.Errorf("error fetching withdrawal contract logs for fork %v (%v-%v): %v", headFork.forkId, startBlockNumber, toBlock, reqError)
		}

		startBlockNumber = toBlock + 1
	}

	return resError
}

func (ds *WithdrawalIndexer) persistFinalizedRequestTxs(finalBlockNumber, finalQueueLen uint64, requests []*dbtypes.WithdrawalRequestTx) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		requestCount := len(requests)
		for requestIdx := 0; requestIdx < requestCount; requestIdx += 500 {
			endIdx := requestIdx + 500
			if endIdx > requestCount {
				endIdx = requestCount
			}

			err := db.InsertWithdrawalRequestTxs(requests[requestIdx:endIdx], tx)
			if err != nil {
				return fmt.Errorf("error while inserting withdrawal txs: %v", err)
			}
		}

		ds.state.FinalBlock = finalBlockNumber
		ds.state.FinalQueueLen = finalQueueLen

		return ds.persistState(tx)
	})
}

func (ds *WithdrawalIndexer) persistRecentRequestTxs(forkId beacon.ForkKey, finalBlockNumber, finalQueueLen uint64, requests []*dbtypes.WithdrawalRequestTx) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		requestCount := len(requests)
		for requestIdx := 0; requestIdx < requestCount; requestIdx += 500 {
			endIdx := requestIdx + 500
			if endIdx > requestCount {
				endIdx = requestCount
			}

			err := db.InsertWithdrawalRequestTxs(requests[requestIdx:endIdx], tx)
			if err != nil {
				return fmt.Errorf("error while inserting withdrawal txs: %v", err)
			}
		}

		ds.forkStates[forkId] = &withdrawalIndexerForkState{
			Block:    finalBlockNumber,
			QueueLen: finalQueueLen,
		}

		return ds.persistState(tx)
	})
}

func (ds *WithdrawalIndexer) persistState(tx *sqlx.Tx) error {
	finalizedBlockNumber := ds.getFinalizedBlockNumber()
	for forkId, forkState := range ds.forkStates {
		if forkState.Block < finalizedBlockNumber {
			delete(ds.forkStates, forkId)
		}
	}

	ds.state.ForkStates = ds.forkStates

	err := db.SetExplorerState("indexer.withdrawalstate", ds.state, tx)
	if err != nil {
		return fmt.Errorf("error while updating withdrawal tx indexer state: %v", err)
	}

	return nil
}
