package execution

import (
	"fmt"
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

const consolidationContractAddr = "0x00b42dbF2194e931E80326D950320f7d9Dbeac02"
const consolidationDequeueRate = 1

type ConsolidationIndexer struct {
	indexerCtx *IndexerCtx
	logger     logrus.FieldLogger
	indexer    *contractIndexer[dbtypes.ConsolidationRequestTx]
	matcher    *ConsolidationMatcher
}

func NewConsolidationIndexer(indexer *IndexerCtx) *ConsolidationIndexer {
	batchSize := utils.Config.ExecutionApi.DepositLogBatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	ci := &ConsolidationIndexer{
		indexerCtx: indexer,
		logger:     indexer.logger.WithField("indexer", "consolidation"),
	}

	ci.indexer = newContractIndexer[dbtypes.ConsolidationRequestTx](
		indexer,
		ci.logger.WithField("routine", "crawler"),
		&contractIndexerOptions[dbtypes.ConsolidationRequestTx]{
			indexerKey:      "indexer.consolidationindexer",
			batchSize:       batchSize,
			contractAddress: common.HexToAddress(consolidationContractAddr),
			deployBlock:     uint64(utils.Config.ExecutionApi.ElectraDeployBlock),
			dequeueRate:     consolidationDequeueRate,

			processFinalTx:  ci.processFinalTx,
			processRecentTx: ci.processRecentTx,
			persistTxs:      ci.persistConsolidationTxs,
		},
	)

	ci.matcher = NewConsolidationMatcher(indexer, ci)

	go ci.runConsolidationIndexerLoop()

	return ci
}

func (ds *ConsolidationIndexer) runConsolidationIndexerLoop() {
	defer utils.HandleSubroutinePanic("ConsolidationIndexer.runConsolidationIndexerLoop")

	for {
		time.Sleep(30 * time.Second)
		ds.logger.Debugf("run consolidation indexer logic")

		err := ds.indexer.runContractIndexer()
		if err != nil {
			ds.logger.Errorf("indexer error: %v", err)
		}

		err = ds.matcher.runConsolidationMatcher()
		if err != nil {
			ds.logger.Errorf("matcher error: %v", err)
		}
	}
}

func (ds *ConsolidationIndexer) processFinalTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64) (*dbtypes.ConsolidationRequestTx, error) {
	requestTx := ds.parseRequestLog(log)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid consolidation log")
	}

	txTo := *tx.To()

	requestTx.BlockTime = header.Time
	requestTx.TxSender = txFrom[:]
	requestTx.TxTarget = txTo[:]
	requestTx.DequeueBlock = dequeueBlock

	return requestTx, nil
}

func (ds *ConsolidationIndexer) processRecentTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, fork *forkWithClients) (*dbtypes.ConsolidationRequestTx, error) {
	requestTx := ds.parseRequestLog(log)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid consolidation log")
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

func (ds *ConsolidationIndexer) parseRequestLog(log *types.Log) *dbtypes.ConsolidationRequestTx {
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

	requestTx := &dbtypes.ConsolidationRequestTx{
		BlockNumber:   log.BlockNumber,
		BlockIndex:    uint64(log.Index),
		BlockRoot:     log.BlockHash[:],
		SourceAddress: senderAddr,
		SourcePubkey:  sourcePubkey,
		TargetPubkey:  targetPubkey,
		TxHash:        log.TxHash[:],
	}

	return requestTx
}

func (ds *ConsolidationIndexer) persistConsolidationTxs(tx *sqlx.Tx, requests []*dbtypes.ConsolidationRequestTx) error {
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

	return nil
}
