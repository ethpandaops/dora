package system_contracts

import (
	"fmt"
	"math/big"
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

// BuilderDepositIndexer indexes the EIP-8282 builder deposit system contract.
type BuilderDepositIndexer struct {
	indexerCtx *execution.IndexerCtx
	logger     logrus.FieldLogger
	indexer    *contractIndexer[dbtypes.BuilderDepositTx]
	matcher    *transactionMatcher[builderDepositMatch]
}

type builderDepositMatch struct {
	slotRoot  []byte
	slotIndex uint64
	txHash    []byte
}

// NewBuilderDepositIndexer creates a new builder deposit contract indexer.
func NewBuilderDepositIndexer(indexer *execution.IndexerCtx) *BuilderDepositIndexer {
	batchSize := utils.Config.ExecutionApi.LogBatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	bi := &BuilderDepositIndexer{
		indexerCtx: indexer,
		logger:     indexer.Logger.WithField("indexer", "builder_deposits"),
	}

	specs := indexer.ChainState.GetSpecs()

	bi.indexer = newContractIndexer(
		indexer,
		indexer.Logger.WithField("contract-indexer", "builder_deposits"),
		&contractIndexerOptions[dbtypes.BuilderDepositTx]{
			stateKey:        "indexer.builderdepositindexer",
			batchSize:       batchSize,
			contractAddress: bi.indexerCtx.GetSystemContractAddress(rpc.BuilderDepositRequestContract),
			deployBlock:     uint64(utils.Config.ExecutionApi.GloasDeployBlock),
			dequeueRate:     specs.MaxBuilderDepositRequestsPerPayload,

			processFinalTx:  bi.processFinalTx,
			processRecentTx: bi.processRecentTx,
			persistTxs:      bi.persistBuilderDepositTxs,
		},
	)

	bi.matcher = newTransactionMatcher(
		indexer,
		indexer.Logger.WithField("contract-matcher", "builder_deposits"),
		&transactionMatcherOptions[builderDepositMatch]{
			stateKey:    "indexer.builderdepositmatcher",
			deployBlock: uint64(utils.Config.ExecutionApi.GloasDeployBlock),
			timeLimit:   2 * time.Second,

			matchBlockRange: bi.matchBlockRange,
			persistMatches:  bi.persistMatches,
		},
	)

	go bi.runBuilderDepositIndexerLoop()

	return bi
}

// GetMatcherHeight returns the last processed el block number from the transaction matcher.
func (bi *BuilderDepositIndexer) GetMatcherHeight() uint64 {
	return bi.matcher.GetMatcherHeight()
}

// runBuilderDepositIndexerLoop is the main loop for the builder deposit indexer.
func (bi *BuilderDepositIndexer) runBuilderDepositIndexerLoop() {
	defer utils.HandleSubroutinePanic("BuilderDepositIndexer.runBuilderDepositIndexerLoop", bi.runBuilderDepositIndexerLoop)

	for {
		time.Sleep(30 * time.Second)
		bi.logger.Debugf("run builder deposit indexer logic")

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

// processFinalTx parses a finalized builder deposit log into a request tx.
func (bi *BuilderDepositIndexer) processFinalTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, _ []*dbtypes.BuilderDepositTx) (*dbtypes.BuilderDepositTx, error) {
	requestTx := bi.parseRequestLog(log)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid builder deposit log")
	}

	txTo := *tx.To()

	requestTx.BlockTime = header.Time
	requestTx.TxSender = txFrom[:]
	requestTx.TxTarget = txTo[:]
	requestTx.DequeueBlock = dequeueBlock

	return requestTx, nil
}

// processRecentTx parses a recent (unfinalized) builder deposit log into a request tx.
func (bi *BuilderDepositIndexer) processRecentTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, fork *execution.ForkWithClients, _ []*dbtypes.BuilderDepositTx) (*dbtypes.BuilderDepositTx, error) {
	requestTx := bi.parseRequestLog(log)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid builder deposit log")
	}

	txTo := *tx.To()

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

// parseRequestLog parses a builder deposit log into a request tx.
func (bi *BuilderDepositIndexer) parseRequestLog(log *types.Log) *dbtypes.BuilderDepositTx {
	// data layout (BuilderDepositRequest, no sender prefix):
	// 0-48:    pubkey (48 bytes)
	// 48-80:   withdrawal credentials (32 bytes)
	// 80-88:   amount (8 bytes, big-endian)
	// 88-184:  signature (96 bytes)
	if len(log.Data) < 184 {
		bi.logger.Warnf("invalid builder deposit log data length: %v", len(log.Data))
		return nil
	}

	pubkey := log.Data[0:48]
	withdrawalCredentials := log.Data[48:80]
	amount := big.NewInt(0).SetBytes(log.Data[80:88]).Uint64()
	signature := log.Data[88:184]

	requestTx := &dbtypes.BuilderDepositTx{
		BlockNumber:           log.BlockNumber,
		BlockIndex:            uint64(log.Index),
		BlockRoot:             log.BlockHash[:],
		PublicKey:             pubkey,
		WithdrawalCredentials: withdrawalCredentials,
		Amount:                amount,
		Signature:             signature,
		TxHash:                log.TxHash[:],
	}

	return requestTx
}

// persistBuilderDepositTxs persists builder deposit request txs to the database.
func (bi *BuilderDepositIndexer) persistBuilderDepositTxs(tx *sqlx.Tx, requests []*dbtypes.BuilderDepositTx) error {
	requestCount := len(requests)
	for requestIdx := 0; requestIdx < requestCount; requestIdx += 500 {
		endIdx := requestIdx + 500
		if endIdx > requestCount {
			endIdx = requestCount
		}

		err := db.InsertBuilderDepositTxs(bi.indexerCtx.Ctx, tx, requests[requestIdx:endIdx])
		if err != nil {
			return fmt.Errorf("error while inserting builder deposit txs: %v", err)
		}
	}

	return nil
}

// matchBlockRange matches builder deposit requests (CL) with their EL request txs by dequeue block.
func (bi *BuilderDepositIndexer) matchBlockRange(fromBlock uint64, toBlock uint64) ([]*builderDepositMatch, error) {
	requestMatches := []*builderDepositMatch{}

	dequeueDepositTxs := db.GetBuilderDepositTxsByDequeueRange(bi.indexerCtx.Ctx, fromBlock, toBlock)
	if len(dequeueDepositTxs) > 0 {
		firstBlock := dequeueDepositTxs[0].DequeueBlock
		lastBlock := dequeueDepositTxs[len(dequeueDepositTxs)-1].DequeueBlock

		for _, builderDeposit := range db.GetBuilderDepositsByElBlockRange(bi.indexerCtx.Ctx, firstBlock, lastBlock) {
			if len(builderDeposit.TxHash) > 0 {
				continue
			}

			parentForkIds := bi.indexerCtx.BeaconIndexer.GetParentForkIds(beacon.ForkKey(builderDeposit.ForkId))
			isParentFork := func(forkId uint64) bool {
				if forkId == builderDeposit.ForkId {
					return true
				}
				for _, parentForkId := range parentForkIds {
					if uint64(parentForkId) == forkId {
						return true
					}
				}
				return false
			}

			matchingTxs := []*dbtypes.BuilderDepositTx{}
			for _, tx := range dequeueDepositTxs {
				if tx.DequeueBlock == builderDeposit.BlockNumber && isParentFork(tx.ForkId) {
					matchingTxs = append(matchingTxs, tx)
				}
			}

			if len(matchingTxs) == 0 {
				for _, tx := range dequeueDepositTxs {
					if tx.DequeueBlock == builderDeposit.BlockNumber {
						matchingTxs = append(matchingTxs, tx)
					}
				}
			}

			if len(matchingTxs) < int(builderDeposit.SlotIndex)+1 {
				continue
			}

			txHash := matchingTxs[builderDeposit.SlotIndex].TxHash
			bi.logger.Debugf("Matched builder deposit %d:%v with tx 0x%x", builderDeposit.SlotNumber, builderDeposit.SlotIndex, txHash)

			requestMatches = append(requestMatches, &builderDepositMatch{
				slotRoot:  builderDeposit.SlotRoot,
				slotIndex: builderDeposit.SlotIndex,
				txHash:    txHash,
			})
		}
	}

	return requestMatches, nil
}

// persistMatches persists builder deposit tx-hash matches to the database.
func (bi *BuilderDepositIndexer) persistMatches(tx *sqlx.Tx, matches []*builderDepositMatch) error {
	for _, match := range matches {
		err := db.UpdateBuilderDepositTxHash(bi.indexerCtx.Ctx, tx, match.slotRoot, match.slotIndex, match.txHash)
		if err != nil {
			return err
		}
	}

	return nil
}
