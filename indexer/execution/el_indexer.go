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
	"github.com/ethpandaops/dora/services"
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

	// Event channels for CL block notifications
	blockChan           chan *CLBlockEvent
	finalizationChan    chan *CLFinalizationEvent
	catchupTicker       *time.Ticker
}

// CLBlockEvent represents a beacon chain block event
type CLBlockEvent struct {
	Slot           uint64
	BlockHash      []byte // CL block hash
	ELBlockNumber  uint64
	ELBlockHash    []byte
	ForkId         uint64
	Timestamp      uint64
}

// CLFinalizationEvent represents a finalization event
type CLFinalizationEvent struct {
	Epoch              uint64
	FinalizedSlot      uint64
	FinalizedELBlock   uint64
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
		blockChan:         make(chan *CLBlockEvent, 100),
		finalizationChan:  make(chan *CLFinalizationEvent, 10),
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

	// Start catchup routine for missed blocks
	ei.startCatchupRoutine()

	// Start event processing loops
	ei.isRunning = true
	go ei.runBlockEventLoop()
	go ei.runFinalizationEventLoop()

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

	if ei.catchupTicker != nil {
		ei.catchupTicker.Stop()
	}

	ei.isRunning = false
}

// OnCLBlockProcessed is called when the CL indexer processes a new block
func (ei *ElIndexer) OnCLBlockProcessed(slot uint64, blockHash []byte, elBlockNumber uint64, elBlockHash []byte, forkId uint64, timestamp uint64) {
	if !ei.enabled || !ei.isRunning {
		return
	}

	event := &CLBlockEvent{
		Slot:          slot,
		BlockHash:     blockHash,
		ELBlockNumber: elBlockNumber,
		ELBlockHash:   elBlockHash,
		ForkId:        forkId,
		Timestamp:     timestamp,
	}

	select {
	case ei.blockChan <- event:
	case <-ei.stopChan:
		return
	default:
		ei.logger.Warn("Block event channel full, dropping event")
	}
}

// OnCLFinalization is called when the CL chain finalizes an epoch
func (ei *ElIndexer) OnCLFinalization(epoch, finalizedSlot, finalizedELBlock uint64) {
	if !ei.enabled || !ei.isRunning {
		return
	}

	event := &CLFinalizationEvent{
		Epoch:            epoch,
		FinalizedSlot:    finalizedSlot,
		FinalizedELBlock: finalizedELBlock,
	}

	select {
	case ei.finalizationChan <- event:
	case <-ei.stopChan:
		return
	default:
		ei.logger.Warn("Finalization event channel full, dropping event")
	}
}

// runBlockEventLoop processes CL block events
func (ei *ElIndexer) runBlockEventLoop() {
	defer utils.HandleSubroutinePanic("ElIndexer.runBlockEventLoop", ei.runBlockEventLoop)

	for {
		select {
		case event := <-ei.blockChan:
			if err := ei.processCLBlockEvent(event); err != nil {
				ei.logger.WithError(err).WithField("el_block", event.ELBlockNumber).Error("Error processing CL block event")
			}
		case <-ei.stopChan:
			return
		}
	}
}

// runFinalizationEventLoop processes finalization events
func (ei *ElIndexer) runFinalizationEventLoop() {
	defer utils.HandleSubroutinePanic("ElIndexer.runFinalizationEventLoop", ei.runFinalizationEventLoop)

	for {
		select {
		case event := <-ei.finalizationChan:
			if err := ei.processFinalizationEvent(event); err != nil {
				ei.logger.WithError(err).Error("Error processing finalization event")
			}
		case <-ei.stopChan:
			return
		}
	}
}

// processCLBlockEvent processes a CL block event and indexes the corresponding EL block
func (ei *ElIndexer) processCLBlockEvent(event *CLBlockEvent) error {
	ei.processingMutex.Lock()
	defer ei.processingMutex.Unlock()

	if event.ELBlockNumber == 0 || event.ELBlockHash == nil {
		// No EL block in this CL block
		return nil
	}

	// Check if we've already indexed this block with this fork_id
	existing, err := db.GetElBlock(event.ELBlockHash)
	if err == nil && existing != nil && existing.ForkId == event.ForkId {
		// Already indexed with correct fork_id
		return nil
	}

	ei.logger.WithFields(logrus.Fields{
		"el_block": event.ELBlockNumber,
		"fork_id":  event.ForkId,
		"cl_slot":  event.Slot,
	}).Debug("Processing EL block from CL event")

	// Fetch and index the EL block
	return ei.processBlock(event.ELBlockNumber, event.ELBlockHash, event.ForkId, event.Timestamp)
}

// processFinalizationEvent updates fork_id to 0 for finalized canonical blocks
func (ei *ElIndexer) processFinalizationEvent(event *CLFinalizationEvent) error {
	ei.logger.WithFields(logrus.Fields{
		"epoch":               event.Epoch,
		"finalized_el_block":  event.FinalizedELBlock,
	}).Info("Processing finalization event")

	// Get canonical fork IDs from beacon service
	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()

	// Update all canonical blocks up to finalized block to have fork_id = 0
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.UpdateElBlocksForkIdForFinalized(event.FinalizedELBlock, canonicalForkIds, tx)
	})
}

// processBlock processes a single execution layer block
func (ei *ElIndexer) processBlock(blockNum uint64, blockHash []byte, forkId uint64, timestamp uint64) error {
	client := ei.indexerCtx.executionPool.GetReadyEndpoint(nil)
	if client == nil {
		ei.logger.Warn("No ready execution client available")
		return nil
	}

	ctx := context.Background()

	// Fetch block by hash to ensure we get the right fork
	block, err := client.GetRPC().BlockByHash(ctx, common.BytesToHash(blockHash))
	if err != nil {
		return err
	}

	// Verify block number matches
	if block.NumberU64() != blockNum {
		ei.logger.WithFields(logrus.Fields{
			"expected": blockNum,
			"actual":   block.NumberU64(),
		}).Warn("Block number mismatch")
		return nil
	}

	// Process all transactions
	for txIndex, tx := range block.Transactions() {
		if err := ei.processTransaction(tx, block, uint(txIndex), forkId); err != nil {
			ei.logger.WithError(err).WithField("tx", tx.Hash().Hex()).Error("Error processing transaction")
			// Continue with other transactions
		}
	}

	// Save block to DB
	if err := ei.saveBlock(block, forkId); err != nil {
		return err
	}

	// Update indexer state for this fork
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		state := &dbtypes.ElIndexerState{
			ForkId:               forkId,
			LastIndexedBlock:     blockNum,
			LastIndexedTimestamp: block.Time(),
		}
		return db.SetElIndexerState(state, tx)
	})
}

// processTransaction processes a single transaction
func (ei *ElIndexer) processTransaction(tx *types.Transaction, block *types.Block, txIndex uint, forkId uint64) error {
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
	dbTx := ei.buildTransactionData(tx, receipt, block, from, txIndex, forkId)

	// Save transaction
	if err := db.RunDBTransaction(func(txDb *sqlx.Tx) error {
		return db.InsertElTransactions([]*dbtypes.ElTransaction{dbTx}, txDb)
	}); err != nil {
		return err
	}

	// Update address metadata
	var toAddr []byte
	if tx.To() != nil {
		toAddr = tx.To().Bytes()
	}
	if err := ei.updateAddressMetadata(from.Bytes(), toAddr, block.NumberU64(), block.Time(), forkId); err != nil {
		ei.logger.WithError(err).Error("Error updating address metadata")
	}

	// Process internal transactions if enabled
	if ei.trackInternalTxs && ei.traceIndexer != nil {
		if err := ei.traceIndexer.ProcessTransaction(tx.Hash(), block.NumberU64(), forkId); err != nil {
			ei.logger.WithError(err).Error("Error processing internal transactions")
		}
	}

	// Process events/logs
	if err := ei.processLogs(receipt.Logs, tx.Hash(), block, forkId); err != nil {
		ei.logger.WithError(err).Error("Error processing logs")
	}

	// Process token transfers if enabled
	if ei.trackTokens && ei.tokenIndexer != nil {
		if err := ei.tokenIndexer.ProcessLogs(receipt.Logs, tx.Hash(), block, forkId); err != nil {
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

	// Queue ENS resolution
	if ei.ensIndexer != nil {
		ei.ensIndexer.QueueResolve(from, block.NumberU64())
		if tx.To() != nil {
			ei.ensIndexer.QueueResolve(*tx.To(), block.NumberU64())
		}
	}

	return nil
}

// buildTransactionData builds a transaction database entry
func (ei *ElIndexer) buildTransactionData(tx *types.Transaction, receipt *types.Receipt, block *types.Block, from common.Address, txIndex uint, forkId uint64) *dbtypes.ElTransaction {
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
		TxType:           tx.Type(),
		Status:           uint8(receipt.Status),
		ContractAddress:  contractAddr,
		LogsCount:        uint(len(receipt.Logs)),
		Orphaned:         false,
		ForkId:           forkId,
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
func (ei *ElIndexer) saveBlock(block *types.Block, forkId uint64) error {
	var baseFee *uint64
	if block.BaseFee() != nil {
		baseFeeVal := block.BaseFee().Uint64()
		baseFee = &baseFeeVal
	}

	difficulty := block.Difficulty().Bytes()
	if difficulty == nil {
		difficulty = []byte{0}
	}

	// Calculate total fees
	totalFees := big.NewInt(0)
	// This would require summing all transaction fees - skip for now

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
		TotalFees:        totalFees.Bytes(),
		Orphaned:         false,
		ForkId:           forkId,
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertElBlock(dbBlock, tx)
	})
}

// processLogs processes transaction logs/events
func (ei *ElIndexer) processLogs(logs []*types.Log, txHash common.Hash, block *types.Block, forkId uint64) error {
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
			ForkId:          forkId,
		}
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertElEvents(dbEvents, tx)
	})
}

// updateAddressMetadata updates address metadata
func (ei *ElIndexer) updateAddressMetadata(from, to []byte, blockNumber, timestamp, forkId uint64) error {
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		// Update from address
		fromAddr := &dbtypes.ElAddress{
			Address:       from,
			FirstSeenBlock: &blockNumber,
			FirstSeenTime: &timestamp,
			LastSeenBlock: &blockNumber,
			LastSeenTime:  &timestamp,
			TxCount:       1,
			OutTxCount:    1,
			Balance:       []byte{0},
		}
		if err := db.UpsertElAddress(fromAddr, tx); err != nil {
			return err
		}

		// Update to address if present
		if to != nil && len(to) > 0 {
			toAddr := &dbtypes.ElAddress{
				Address:       to,
				FirstSeenBlock: &blockNumber,
				FirstSeenTime: &timestamp,
				LastSeenBlock: &blockNumber,
				LastSeenTime:  &timestamp,
				TxCount:       1,
				InTxCount:     1,
				Balance:       []byte{0},
			}
			if err := db.UpsertElAddress(toAddr, tx); err != nil {
				return err
			}
		}

		return nil
	})
}

// startCatchupRoutine starts a routine to catch up on missed blocks
func (ei *ElIndexer) startCatchupRoutine() {
	ei.catchupTicker = time.NewTicker(5 * time.Minute)

	go func() {
		defer utils.HandleSubroutinePanic("ElIndexer.catchupRoutine", func() {})

		for {
			select {
			case <-ei.catchupTicker.C:
				if err := ei.performCatchup(); err != nil {
					ei.logger.WithError(err).Error("Error during catchup")
				}
			case <-ei.stopChan:
				return
			}
		}
	}()
}

// performCatchup catches up on blocks that were missed or not synced yet
func (ei *ElIndexer) performCatchup() error {
	// Get CL blocks that have EL block info but haven't been indexed yet
	// This would require querying the CL database for blocks with EL info
	// and checking if we have them in the EL database
	ei.logger.Debug("Performing catchup check")

	// TODO: Implement catchup logic by querying CL database
	// For now, this is a placeholder
	return nil
}

// startCleanupRoutine starts the data cleanup routine
func (ei *ElIndexer) startCleanupRoutine() {
	interval := utils.Config.Indexer.ElCleanupInterval
	if interval == 0 {
		interval = 1 * time.Hour
	}

	ei.cleanupTicker = time.NewTicker(interval)

	go func() {
		defer utils.HandleSubroutinePanic("ElIndexer.cleanupRoutine", func() {})

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
		"cutoff":         time.Unix(int64(cutoffTimestamp), 0),
		"retention_days": *ei.dataRetentionDays,
	}).Info("Performing data cleanup")

	if err := db.CleanupElData(cutoffTimestamp); err != nil {
		ei.logger.WithError(err).Error("Failed to cleanup old EL data")
	} else {
		ei.logger.Info("Data cleanup completed successfully")
	}
}
