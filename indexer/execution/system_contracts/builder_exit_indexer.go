package system_contracts

import (
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/indexer/execution"
	"github.com/ethpandaops/dora/utils"
)

// BuilderExitIndexer indexes the EIP-8282 builder exit system contract.
type BuilderExitIndexer struct {
	indexerCtx *execution.IndexerCtx
	logger     logrus.FieldLogger
	indexer    *contractIndexer[dbtypes.BuilderExitTx]
	matcher    *transactionMatcher[builderExitMatch]
}

type builderExitMatch struct {
	slotRoot  []byte
	slotIndex uint64
	txHash    []byte
}

// NewBuilderExitIndexer creates a new builder exit contract indexer.
func NewBuilderExitIndexer(indexer *execution.IndexerCtx) *BuilderExitIndexer {
	batchSize := utils.Config.ExecutionApi.LogBatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	bi := &BuilderExitIndexer{
		indexerCtx: indexer,
		logger:     indexer.Logger.WithField("indexer", "builder_exits"),
	}

	specs := indexer.ChainState.GetSpecs()

	bi.indexer = newContractIndexer(
		indexer,
		indexer.Logger.WithField("contract-indexer", "builder_exits"),
		&contractIndexerOptions[dbtypes.BuilderExitTx]{
			stateKey:  "indexer.builderexitindexer",
			batchSize: batchSize,
			contractAddress: func() common.Address {
				return bi.indexerCtx.GetSystemContractAddress(rpc.BuilderExitRequestContract)
			},
			deployBlock: uint64(utils.Config.ExecutionApi.GloasDeployBlock),
			dequeueRate: specs.MaxBuilderExitRequestsPerPayload,

			processFinalTx:  bi.processFinalTx,
			processRecentTx: bi.processRecentTx,
			persistTxs:      bi.persistBuilderExitTxs,
		},
	)

	bi.matcher = newTransactionMatcher(
		indexer,
		indexer.Logger.WithField("contract-matcher", "builder_exits"),
		&transactionMatcherOptions[builderExitMatch]{
			stateKey:    "indexer.builderexitmatcher",
			deployBlock: uint64(utils.Config.ExecutionApi.GloasDeployBlock),
			timeLimit:   2 * time.Second,

			matchBlockRange: bi.matchBlockRange,
			persistMatches:  bi.persistMatches,
		},
	)

	go bi.runBuilderExitIndexerLoop()

	return bi
}

// GetMatcherHeight returns the last processed el block number from the transaction matcher.
func (bi *BuilderExitIndexer) GetMatcherHeight() uint64 {
	return bi.matcher.GetMatcherHeight()
}

// runBuilderExitIndexerLoop is the main loop for the builder exit indexer.
func (bi *BuilderExitIndexer) runBuilderExitIndexerLoop() {
	defer utils.HandleSubroutinePanic("BuilderExitIndexer.runBuilderExitIndexerLoop", bi.runBuilderExitIndexerLoop)

	for {
		time.Sleep(30 * time.Second)
		bi.logger.Debugf("run builder exit indexer logic")

		err := bi.indexer.runContractIndexer()
		if err != nil {
			bi.logger.Errorf("indexer error: %v", err)
		}

		err = bi.matcher.runTransactionMatcher(bi.indexer.state.FinalBlock)
		if err != nil {
			bi.logger.Errorf("matcher error: %v", err)
		}
	}
}

// processFinalTx parses a finalized builder exit log into a request tx.
func (bi *BuilderExitIndexer) processFinalTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, _ []*dbtypes.BuilderExitTx) (*dbtypes.BuilderExitTx, error) {
	requestTx := bi.parseRequestLog(log)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid builder exit log")
	}

	txTo := txRecipient(tx, log)

	requestTx.BlockTime = header.Time
	requestTx.TxSender = txFrom[:]
	requestTx.TxTarget = txTo[:]
	requestTx.DequeueBlock = dequeueBlock

	return requestTx, nil
}

// processRecentTx parses a recent (unfinalized) builder exit log into a request tx.
func (bi *BuilderExitIndexer) processRecentTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, fork *execution.ForkWithClients, _ []*dbtypes.BuilderExitTx) (*dbtypes.BuilderExitTx, error) {
	requestTx := bi.parseRequestLog(log)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid builder exit log")
	}

	txTo := txRecipient(tx, log)

	requestTx.BlockTime = header.Time
	requestTx.TxSender = txFrom[:]
	requestTx.TxTarget = txTo[:]
	requestTx.DequeueBlock = dequeueBlock

	clBlock := bi.indexerCtx.BeaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(log.BlockHash))
	if len(clBlock) > 0 {
		requestTx.ForkId = uint64(clBlock[0].GetForkId())
	} else {
		requestTx.ForkId = uint64(fork.ForkId)
	}

	return requestTx, nil
}

// parseRequestLog parses a builder exit log into a request tx.
func (bi *BuilderExitIndexer) parseRequestLog(log *types.Log) *dbtypes.BuilderExitTx {
	// data layout (BuilderExitRequest):
	// 0-20:  source address (20 bytes)
	// 20-68: builder pubkey (48 bytes)
	if len(log.Data) < 68 {
		bi.logger.Warnf("invalid builder exit log data length: %v", len(log.Data))
		return nil
	}

	sourceAddr := log.Data[0:20]
	pubkey := log.Data[20:68]

	requestTx := &dbtypes.BuilderExitTx{
		BlockNumber:   log.BlockNumber,
		BlockIndex:    uint64(log.Index),
		BlockRoot:     log.BlockHash[:],
		SourceAddress: sourceAddr,
		PublicKey:     pubkey,
		TxHash:        log.TxHash[:],
	}

	return requestTx
}

// persistBuilderExitTxs persists builder exit request txs to the database.
func (bi *BuilderExitIndexer) persistBuilderExitTxs(tx *sqlx.Tx, requests []*dbtypes.BuilderExitTx) error {
	requestCount := len(requests)
	for requestIdx := 0; requestIdx < requestCount; requestIdx += 500 {
		endIdx := requestIdx + 500
		if endIdx > requestCount {
			endIdx = requestCount
		}

		err := db.InsertBuilderExitTxs(bi.indexerCtx.Ctx, tx, requests[requestIdx:endIdx])
		if err != nil {
			return fmt.Errorf("error while inserting builder exit txs: %v", err)
		}
	}

	return nil
}

// matchBlockRange matches builder exit requests (CL) with their EL request txs by dequeue block.
func (bi *BuilderExitIndexer) matchBlockRange(fromBlock uint64, toBlock uint64) ([]*builderExitMatch, error) {
	requestMatches := []*builderExitMatch{}

	dequeueExitTxs := db.GetBuilderExitTxsByDequeueRange(bi.indexerCtx.Ctx, fromBlock, toBlock)
	if len(dequeueExitTxs) > 0 {
		firstBlock := dequeueExitTxs[0].DequeueBlock
		lastBlock := dequeueExitTxs[len(dequeueExitTxs)-1].DequeueBlock

		for _, builderExit := range db.GetBuilderExitsByElBlockRange(bi.indexerCtx.Ctx, firstBlock, lastBlock) {
			if len(builderExit.TxHash) > 0 {
				continue
			}

			parentForkIds := bi.indexerCtx.BeaconIndexer.GetParentForkIds(beacon.ForkKey(builderExit.ForkId))
			isParentFork := func(forkId uint64) bool {
				if forkId == builderExit.ForkId {
					return true
				}
				for _, parentForkId := range parentForkIds {
					if uint64(parentForkId) == forkId {
						return true
					}
				}
				return false
			}

			matchingTxs := []*dbtypes.BuilderExitTx{}
			for _, tx := range dequeueExitTxs {
				if tx.DequeueBlock == builderExit.BlockNumber && isParentFork(tx.ForkId) {
					matchingTxs = append(matchingTxs, tx)
				}
			}

			if len(matchingTxs) == 0 {
				for _, tx := range dequeueExitTxs {
					if tx.DequeueBlock == builderExit.BlockNumber {
						matchingTxs = append(matchingTxs, tx)
					}
				}
			}

			if len(matchingTxs) < int(builderExit.SlotIndex)+1 {
				continue
			}

			txHash := matchingTxs[builderExit.SlotIndex].TxHash
			bi.logger.Debugf("Matched builder exit %d:%v with tx 0x%x", builderExit.SlotNumber, builderExit.SlotIndex, txHash)

			requestMatches = append(requestMatches, &builderExitMatch{
				slotRoot:  builderExit.SlotRoot,
				slotIndex: builderExit.SlotIndex,
				txHash:    txHash,
			})
		}
	}

	return requestMatches, nil
}

// persistMatches persists builder exit tx-hash matches to the database.
func (bi *BuilderExitIndexer) persistMatches(tx *sqlx.Tx, matches []*builderExitMatch) error {
	for _, match := range matches {
		err := db.UpdateBuilderExitTxHash(bi.indexerCtx.Ctx, tx, match.slotRoot, match.slotIndex, match.txHash)
		if err != nil {
			return err
		}
	}

	return nil
}
