package execution

import (
	"fmt"
	"math/big"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
)

const withdrawalContractAddr = "0x00A3ca265EBcb825B45F985A16CEFB49958cE017"
const withdrawalDequeueRate = 2

type WithdrawalIndexer struct {
	indexerCtx *IndexerCtx
	logger     logrus.FieldLogger
	indexer    *contractIndexer[dbtypes.WithdrawalRequestTx]
	matcher    *WithdrawalMatcher
}

func NewWithdrawalIndexer(indexer *IndexerCtx) *WithdrawalIndexer {
	batchSize := utils.Config.ExecutionApi.DepositLogBatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	ci := &WithdrawalIndexer{
		indexerCtx: indexer,
		logger:     indexer.logger.WithField("indexer", "withdrawal"),
	}

	ci.indexer = newContractIndexer[dbtypes.WithdrawalRequestTx](
		indexer,
		ci.logger.WithField("routine", "crawler"),
		&contractIndexerOptions[dbtypes.WithdrawalRequestTx]{
			indexerKey:      "indexer.withdrawalindexer",
			batchSize:       batchSize,
			contractAddress: common.HexToAddress(withdrawalContractAddr),
			deployBlock:     uint64(utils.Config.ExecutionApi.ElectraDeployBlock),
			dequeueRate:     withdrawalDequeueRate,

			processFinalTx:  ci.processFinalTx,
			processRecentTx: ci.processRecentTx,
			persistTxs:      ci.persistWithdrawalTxs,
		},
	)

	ci.matcher = NewWithdrawalMatcher(indexer, ci)

	go ci.runWithdrawalIndexerLoop()

	return ci
}

func (ds *WithdrawalIndexer) runWithdrawalIndexerLoop() {
	defer utils.HandleSubroutinePanic("WithdrawalIndexer.runWithdrawalIndexerLoop")

	for {
		time.Sleep(30 * time.Second)
		ds.logger.Debugf("run withdrawal indexer logic")

		err := ds.indexer.runContractIndexer()
		if err != nil {
			ds.logger.Errorf("indexer error: %v", err)
		}

		err = ds.matcher.runWithdrawalMatcher()
		if err != nil {
			ds.logger.Errorf("matcher error: %v", err)
		}
	}
}

func (ds *WithdrawalIndexer) processFinalTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64) (*dbtypes.WithdrawalRequestTx, error) {
	requestTx := ds.parseRequestLog(log)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid withdrawal log")
	}

	txTo := *tx.To()

	requestTx.BlockTime = header.Time
	requestTx.TxSender = txFrom[:]
	requestTx.TxTarget = txTo[:]
	requestTx.DequeueBlock = dequeueBlock

	return requestTx, nil
}

func (ds *WithdrawalIndexer) processRecentTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, fork *forkWithClients) (*dbtypes.WithdrawalRequestTx, error) {
	requestTx := ds.parseRequestLog(log)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid withdrawal log")
	}

	txTo := *tx.To()

	requestTx.BlockTime = header.Time
	requestTx.TxSender = txFrom[:]
	requestTx.TxTarget = txTo[:]
	requestTx.DequeueBlock = dequeueBlock

	clBlock := ds.indexerCtx.beaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(log.BlockHash))
	if len(clBlock) > 0 {
		requestTx.ForkId = uint64(clBlock[0].GetForkId())
	} else {
		requestTx.ForkId = uint64(fork.forkId)
	}

	return requestTx, nil
}

func (ds *WithdrawalIndexer) parseRequestLog(log *types.Log) *dbtypes.WithdrawalRequestTx {
	// data layout:
	// 0-20: sender address (20 bytes)
	// 20-68: validator pubkey (48 bytes)
	// 68-76: amount (8 bytes)

	if len(log.Data) < 76 {
		ds.logger.Warnf("invalid withdrawal log data length: %v", len(log.Data))
		return nil
	}

	senderAddr := log.Data[:20]
	validatorPubkey := log.Data[20:68]
	amount := big.NewInt(0).SetBytes(log.Data[68:76]).Uint64()

	requestTx := &dbtypes.WithdrawalRequestTx{
		BlockNumber:     log.BlockNumber,
		BlockIndex:      uint64(log.Index),
		BlockRoot:       log.BlockHash[:],
		SourceAddress:   senderAddr,
		ValidatorPubkey: validatorPubkey,
		Amount:          amount,
		TxHash:          log.TxHash[:],
	}

	return requestTx
}

func (ds *WithdrawalIndexer) persistWithdrawalTxs(tx *sqlx.Tx, requests []*dbtypes.WithdrawalRequestTx) error {
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

	return nil
}
