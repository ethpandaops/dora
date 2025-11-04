package execution

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
)

// ElIndexer is the main execution layer indexer
type ElIndexer struct {
	indexerCtx *IndexerCtx
	logger     logrus.FieldLogger
	enabled    bool

	// Configuration
	startBlock        uint64
	dataRetentionDays *uint64
	trackInternalTxs  bool
	trackTokens       bool

	// State
	currentBlock       uint64
	lastFinalizedBlock uint64
	isRunning          bool

	// Sub-indexers
	traceIndexer   *TraceIndexer
	tokenIndexer   *TokenIndexer
	balanceIndexer *BalanceIndexer
	ensIndexer     *ENSIndexer

	// Processing
	processingMutex sync.Mutex
	cleanupTicker   *time.Ticker
	stopChan        chan struct{}
}

// NewElIndexer creates a new execution layer indexer
func NewElIndexer(indexerCtx *IndexerCtx) *ElIndexer {
	ei := &ElIndexer{
		indexerCtx:        indexerCtx,
		logger:            indexerCtx.logger.WithField("component", "el-indexer"),
		enabled:           utils.Config.Indexer.ExecutionIndexer,
		startBlock:        utils.Config.Indexer.ElStartBlock,
		dataRetentionDays: utils.Config.Indexer.ElDataRetentionDays,
		trackInternalTxs:  utils.Config.Indexer.ElTrackInternalTxs,
		trackTokens:       utils.Config.Indexer.ElTrackTokens,
		stopChan:          make(chan struct{}),
	}

	indexerCtx.elIndexer = ei

	return ei
}

// IsEnabled returns whether the indexer is enabled
func (ei *ElIndexer) IsEnabled() bool {
	return ei.enabled
}

// Start starts the execution layer indexer
func (ei *ElIndexer) Start() error {
	if !ei.enabled {
		ei.logger.Info("Execution layer indexer is disabled")
		return nil
	}

	ei.logger.Info("Starting execution layer indexer")

	// Initialize sub-indexers
	if ei.trackInternalTxs {
		ei.logger.Info("Enabling internal transaction tracking")
		ei.traceIndexer = NewTraceIndexer(ei.indexerCtx, ei.logger.WithField("sub", "trace"))
	}

	if ei.trackTokens {
		ei.logger.Info("Enabling token tracking")
		ei.tokenIndexer = NewTokenIndexer(ei.indexerCtx, ei.logger.WithField("sub", "token"))
	}

	ei.balanceIndexer = NewBalanceIndexer(ei.indexerCtx, ei.logger.WithField("sub", "balance"))
	ei.ensIndexer = NewENSIndexer(ei.indexerCtx, ei.logger.WithField("sub", "ens"))

	// Start cleanup routine if data retention is configured
	if ei.dataRetentionDays != nil {
		ei.logger.WithField("retention_days", *ei.dataRetentionDays).Info("Data retention configured")
		ei.startCleanupRoutine()
	}

	// Start main indexer loop
	ei.isRunning = true
	go ei.runIndexerLoop()

	return nil
}

// Stop stops the execution layer indexer
func (ei *ElIndexer) Stop() {
	if !ei.enabled || !ei.isRunning {
		return
	}

	ei.logger.Info("Stopping execution layer indexer")
	close(ei.stopChan)

	if ei.cleanupTicker != nil {
		ei.cleanupTicker.Stop()
	}

	ei.isRunning = false
}

// runIndexerLoop is the main indexing loop
func (ei *ElIndexer) runIndexerLoop() {
	defer utils.HandleSubroutinePanic("ElIndexer.runIndexerLoop", ei.runIndexerLoop)

	ticker := time.NewTicker(12 * time.Second) // Poll every 12 seconds (2 slots)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := ei.processNewBlocks(); err != nil {
				ei.logger.WithError(err).Error("Error processing new blocks")
			}
		case <-ei.stopChan:
			return
		}
	}
}

// processNewBlocks processes new execution layer blocks
func (ei *ElIndexer) processNewBlocks() error {
	ei.processingMutex.Lock()
	defer ei.processingMutex.Unlock()

	// Get the latest block from execution client
	client := ei.indexerCtx.executionPool.GetReadyEndpoint(nil)
	if client == nil {
		return nil // No ready client
	}

	ctx := context.Background()
	latestBlockNum, err := client.GetRPC().BlockNumber(ctx)
	if err != nil {
		return err
	}

	// Get last indexed block from DB
	state, err := db.GetElIndexerState(0) // Fork ID 0 for canonical chain
	if err != nil {
		// First time indexing
		state = &dbtypes.ElIndexerState{
			ForkId:               0,
			LastIndexedBlock:     ei.startBlock - 1,
			LastIndexedTimestamp: 0,
		}
	}

	// Index blocks in batches
	fromBlock := state.LastIndexedBlock + 1
	toBlock := latestBlockNum

	// Limit batch size to avoid overload
	if toBlock-fromBlock > 100 {
		toBlock = fromBlock + 100
	}

	if fromBlock > toBlock {
		return nil // Already up to date
	}

	ei.logger.WithFields(logrus.Fields{
		"from": fromBlock,
		"to":   toBlock,
	}).Info("Indexing execution layer blocks")

	for blockNum := fromBlock; blockNum <= toBlock; blockNum++ {
		if err := ei.processBlock(blockNum); err != nil {
			ei.logger.WithError(err).WithField("block", blockNum).Error("Error processing block")
			return err
		}
	}

	return nil
}

// processBlock processes a single execution layer block
func (ei *ElIndexer) processBlock(blockNum uint64) error {
	client := ei.indexerCtx.executionPool.GetReadyEndpoint(nil)
	if client == nil {
		return nil
	}

	ctx := context.Background()

	// Fetch block
	block, err := client.GetRPC().BlockByNumber(ctx, big.NewInt(int64(blockNum)))
	if err != nil {
		return err
	}

	// Process all transactions
	for txIndex, tx := range block.Transactions() {
		if err := ei.processTransaction(tx, block, uint(txIndex)); err != nil {
			ei.logger.WithError(err).WithField("tx", tx.Hash().Hex()).Error("Error processing transaction")
			// Continue with other transactions
		}
	}

	// Save block to DB
	if err := ei.saveBlock(block); err != nil {
		return err
	}

	// Update indexer state
	return db.RunDBTransaction(func(tx interface{}) error {
		state := &dbtypes.ElIndexerState{
			ForkId:               0,
			LastIndexedBlock:     blockNum,
			LastIndexedTimestamp: block.Time(),
		}
		return db.SetElIndexerState(state, tx.(*db.WriterDataBase).Tx)
	})
}

// processTransaction processes a single transaction
func (ei *ElIndexer) processTransaction(tx *types.Transaction, block *types.Block, txIndex uint) error {
	client := ei.indexerCtx.executionPool.GetReadyEndpoint(nil)
	if client == nil {
		return nil
	}

	ctx := context.Background()

	// Get transaction receipt for status, logs, gas used
	receipt, err := client.GetRPC().TransactionReceipt(ctx, tx.Hash())
	if err != nil {
		return err
	}

	// Get transaction sender
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil {
		return err
	}

	// Build transaction data
	dbTx := ei.buildTransactionData(tx, receipt, block, from, txIndex)

	// Save transaction
	if err := db.RunDBTransaction(func(txDb interface{}) error {
		return db.InsertElTransactions([]*dbtypes.ElTransaction{dbTx}, txDb.(*db.WriterDataBase).Tx)
	}); err != nil {
		return err
	}

	// Update address metadata
	if err := ei.updateAddressMetadata(from.Bytes(), tx.To().Bytes(), block.NumberU64(), block.Time()); err != nil {
		ei.logger.WithError(err).Error("Error updating address metadata")
	}

	// Process internal transactions if enabled
	if ei.trackInternalTxs && ei.traceIndexer != nil {
		if err := ei.traceIndexer.ProcessTransaction(tx.Hash(), block.NumberU64()); err != nil {
			ei.logger.WithError(err).Error("Error processing internal transactions")
		}
	}

	// Process events/logs
	if err := ei.processLogs(receipt.Logs, tx.Hash(), block); err != nil {
		ei.logger.WithError(err).Error("Error processing logs")
	}

	// Process token transfers if enabled
	if ei.trackTokens && ei.tokenIndexer != nil {
		if err := ei.tokenIndexer.ProcessLogs(receipt.Logs, tx.Hash(), block); err != nil {
			ei.logger.WithError(err).Error("Error processing token transfers")
		}
	}

	// Queue balance update
	if ei.balanceIndexer != nil {
		ei.balanceIndexer.QueueUpdate(from, block.NumberU64())
		if tx.To() != nil {
			ei.balanceIndexer.QueueUpdate(*tx.To(), block.NumberU64())
		}
	}

	return nil
}

// buildTransactionData builds a transaction database entry
func (ei *ElIndexer) buildTransactionData(tx *types.Transaction, receipt *types.Receipt, block *types.Block, from common.Address, txIndex uint) *dbtypes.ElTransaction {
	var toAddr []byte
	if tx.To() != nil {
		toAddr = tx.To().Bytes()
	}

	var methodId []byte
	if len(tx.Data()) >= 4 {
		methodId = tx.Data()[0:4]
	}

	var contractAddr []byte
	if receipt.ContractAddress != (common.Address{}) {
		contractAddr = receipt.ContractAddress.Bytes()
	}

	value := tx.Value().Bytes()
	if value == nil {
		value = []byte{0}
	}

	dbTx := &dbtypes.ElTransaction{
		Hash:             tx.Hash().Bytes(),
		BlockNumber:      block.NumberU64(),
		BlockHash:        block.Hash().Bytes(),
		BlockTimestamp:   block.Time(),
		TransactionIndex: txIndex,
		FromAddress:      from.Bytes(),
		ToAddress:        toAddr,
		Value:            value,
		Nonce:            tx.Nonce(),
		GasLimit:         tx.Gas(),
		GasUsed:          receipt.GasUsed,
		InputData:        tx.Data(),
		MethodId:         methodId,
		TransactionType:  tx.Type(),
		Status:           uint8(receipt.Status),
		ContractAddress:  contractAddr,
		LogsCount:        uint(len(receipt.Logs)),
		Orphaned:         false,
		ForkId:           0,
	}

	// Handle EIP-1559 fields
	if tx.Type() == types.DynamicFeeTxType {
		if tx.GasFeeCap() != nil {
			maxFee := tx.GasFeeCap().Uint64()
			dbTx.MaxFeePerGas = &maxFee
		}
		if tx.GasTipCap() != nil {
			maxPriority := tx.GasTipCap().Uint64()
			dbTx.MaxPriorityFeePerGas = &maxPriority
		}
		if receipt.EffectiveGasPrice != nil {
			effectiveGasPrice := receipt.EffectiveGasPrice.Uint64()
			dbTx.EffectiveGasPrice = &effectiveGasPrice
		}
	} else {
		if tx.GasPrice() != nil {
			gasPrice := tx.GasPrice().Uint64()
			dbTx.GasPrice = &gasPrice
			dbTx.EffectiveGasPrice = &gasPrice
		}
	}

	return dbTx
}

// saveBlock saves a block to the database
func (ei *ElIndexer) saveBlock(block *types.Block) error {
	var baseFee *uint64
	if block.BaseFee() != nil {
		baseFeeVal := block.BaseFee().Uint64()
		baseFee = &baseFeeVal
	}

	difficulty := block.Difficulty().Bytes()
	if difficulty == nil {
		difficulty = []byte{0}
	}

	dbBlock := &dbtypes.ElBlock{
		Number:           block.NumberU64(),
		Hash:             block.Hash().Bytes(),
		ParentHash:       block.ParentHash().Bytes(),
		Timestamp:        block.Time(),
		FeeRecipient:     block.Coinbase().Bytes(),
		StateRoot:        block.Root().Bytes(),
		ReceiptsRoot:     block.ReceiptHash().Bytes(),
		LogsBloom:        block.Bloom().Bytes(),
		Difficulty:       difficulty,
		TotalDifficulty:  []byte{0}, // Would need to calculate
		Size:             block.Size(),
		GasUsed:          block.GasUsed(),
		GasLimit:         block.GasLimit(),
		BaseFeePerGas:    baseFee,
		ExtraData:        block.Extra(),
		Nonce:            block.Nonce[:],
		TransactionCount: uint(len(block.Transactions())),
		Orphaned:         false,
		ForkId:           0,
	}

	return db.RunDBTransaction(func(tx interface{}) error {
		return db.InsertElBlock(dbBlock, tx.(*db.WriterDataBase).Tx)
	})
}

// processLogs processes transaction logs/events
func (ei *ElIndexer) processLogs(logs []*types.Log, txHash common.Hash, block *types.Block) error {
	if len(logs) == 0 {
		return nil
	}

	dbEvents := make([]*dbtypes.ElEvent, len(logs))
	for i, log := range logs {
		var topic0, topic1, topic2, topic3 []byte
		if len(log.Topics) > 0 {
			topic0 = log.Topics[0].Bytes()
		}
		if len(log.Topics) > 1 {
			topic1 = log.Topics[1].Bytes()
		}
		if len(log.Topics) > 2 {
			topic2 = log.Topics[2].Bytes()
		}
		if len(log.Topics) > 3 {
			topic3 = log.Topics[3].Bytes()
		}

		// Try to decode event name
		var eventName *string
		if topic0 != nil {
			if sig, err := db.GetElEventSignature(topic0); err == nil {
				eventName = &sig.Name
			}
		}

		dbEvents[i] = &dbtypes.ElEvent{
			TransactionHash: txHash.Bytes(),
			BlockNumber:     block.NumberU64(),
			BlockTimestamp:  block.Time(),
			LogIndex:        uint(log.Index),
			Address:         log.Address.Bytes(),
			Topic0:          topic0,
			Topic1:          topic1,
			Topic2:          topic2,
			Topic3:          topic3,
			Data:            log.Data,
			EventName:       eventName,
		}
	}

	return db.RunDBTransaction(func(tx interface{}) error {
		return db.InsertElEvents(dbEvents, tx.(*db.WriterDataBase).Tx)
	})
}

// updateAddressMetadata updates address metadata
func (ei *ElIndexer) updateAddressMetadata(from, to []byte, blockNumber, timestamp uint64) error {
	return db.RunDBTransaction(func(tx interface{}) error {
		// Update from address
		fromAddr := &dbtypes.ElAddress{
			Address:            from,
			FirstSeenBlock:     blockNumber,
			FirstSeenTimestamp: timestamp,
			LastSeenBlock:      blockNumber,
			LastSeenTimestamp:  timestamp,
			TxCount:            1,
			OutTxCount:         1,
			Balance:            []byte{0},
		}
		if err := db.UpsertElAddress(fromAddr, tx.(*db.WriterDataBase).Tx); err != nil {
			return err
		}

		// Update to address if present
		if to != nil && len(to) > 0 {
			toAddr := &dbtypes.ElAddress{
				Address:            to,
				FirstSeenBlock:     blockNumber,
				FirstSeenTimestamp: timestamp,
				LastSeenBlock:      blockNumber,
				LastSeenTimestamp:  timestamp,
				TxCount:            1,
				InTxCount:          1,
				Balance:            []byte{0},
			}
			if err := db.UpsertElAddress(toAddr, tx.(*db.WriterDataBase).Tx); err != nil {
				return err
			}
		}

		return nil
	})
}

// startCleanupRoutine starts the data cleanup routine
func (ei *ElIndexer) startCleanupRoutine() {
	interval := utils.Config.Indexer.ElCleanupInterval
	if interval == 0 {
		interval = 1 * time.Hour
	}

	ei.cleanupTicker = time.NewTicker(interval)

	go func() {
		for {
			select {
			case <-ei.cleanupTicker.C:
				ei.performCleanup()
			case <-ei.stopChan:
				return
			}
		}
	}()
}

// performCleanup performs data cleanup based on retention policy
func (ei *ElIndexer) performCleanup() {
	if ei.dataRetentionDays == nil {
		return
	}

	cutoffTimestamp := uint64(time.Now().AddDate(0, 0, -int(*ei.dataRetentionDays)).Unix())

	ei.logger.WithFields(logrus.Fields{
		"cutoff":        time.Unix(int64(cutoffTimestamp), 0),
		"retention_days": *ei.dataRetentionDays,
	}).Info("Performing data cleanup")

	if err := db.CleanupElData(cutoffTimestamp); err != nil {
		ei.logger.WithError(err).Error("Failed to cleanup old EL data")
	} else {
		ei.logger.Info("Data cleanup completed successfully")
	}
}
