package snooper

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/clients/execution/snooper"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// SnooperManager manages snooper clients for execution time tracking
type SnooperManager struct {
	logger           logrus.FieldLogger
	indexer          *beacon.Indexer
	cache            *ExecutionTimeCache
	clients          map[uint16]*snooperClientInfo
	mu               sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	reconnectBackoff time.Duration
}

// snooperClientInfo holds information about a snooper client
type snooperClientInfo struct {
	execution            *execution.Client
	snooper              *snooper.Client
	clientID             uint16
	snooperURL           string
	cancel               context.CancelFunc
	module               *snooper.ModuleRegistration
	execTimeSubscription *utils.Subscription[*snooper.WSMessageWithBinary]
	getPayloadHashes     *BlockHashRingBuffer // Ring buffer for engine_getPayloadV* block hashes
}

// NewSnooperManager creates a new snooper manager
func NewSnooperManager(parentCtx context.Context, logger logrus.FieldLogger, indexer *beacon.Indexer) *SnooperManager {
	ctx, cancel := context.WithCancel(parentCtx)

	return &SnooperManager{
		logger:           logger.WithField("component", "snooper-manager"),
		indexer:          indexer,
		cache:            NewExecutionTimeCache(5 * time.Minute), // 5 minute TTL for cached execution times
		clients:          make(map[uint16]*snooperClientInfo),
		ctx:              ctx,
		cancel:           cancel,
		reconnectBackoff: 5 * time.Second,
	}
}

// AddClient adds a new snooper client for an execution client
func (sm *SnooperManager) AddClient(executionClient *execution.Client, snooperURL string) error {
	if snooperURL == "" {
		// No snooper URL configured for this client
		return nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	clientID := executionClient.GetIndex()

	// Check if client already exists
	if _, exists := sm.clients[clientID]; exists {
		return fmt.Errorf("snooper client %d already exists", clientID)
	}

	sm.logger.WithFields(logrus.Fields{
		"client_id":   clientID,
		"snooper_url": snooperURL,
	}).Debug("adding snooper client")

	// Create new snooper client
	config := snooper.ClientConfig{
		URL:    snooperURL,
		Logger: sm.logger,
	}
	snooperClient := snooper.NewClient(config)

	// Create client context
	clientCtx, clientCancel := context.WithCancel(sm.ctx)

	// Register response tracer module
	moduleConfig := snooper.ModuleConfig{
		Type: "response_tracer",
		Name: fmt.Sprintf("dora-execution-time-%d", clientID),
		Config: map[string]interface{}{
			// Extract block information from engine_newPayloadV4 requests
			"request_select":  `{method: .method, blockNumber: .params[0].blockNumber // null, blockHash: .params[0].blockHash // null}`,
			"response_select": `{blockHash: .result.executionPayload.blockHash}`,
			"request_filter": map[string]interface{}{
				"json_query": `((.method | startswith("engine_newPayloadV")) or (.method | startswith("engine_getPayloadV")))`,
			},
		},
	}

	module, err := snooperClient.RegisterModule(moduleConfig)
	if err != nil {
		clientCancel()
		return fmt.Errorf("failed to register module: %w", err)
	}

	clientInfo := &snooperClientInfo{
		execution:            executionClient,
		snooper:              snooperClient,
		clientID:             clientID,
		snooperURL:           snooperURL,
		cancel:               clientCancel,
		module:               module,
		execTimeSubscription: module.Subscribe(10, false),
		getPayloadHashes:     NewBlockHashRingBuffer(5), // Store last 5 block hashes
	}

	sm.clients[clientID] = clientInfo

	// Start the client
	if err := snooperClient.Start(); err != nil {
		clientCancel()
		delete(sm.clients, clientID)
		return fmt.Errorf("failed to start snooper client: %w", err)
	}

	// Start event listener
	sm.wg.Add(1)
	go sm.runClientEventListener(clientCtx, clientInfo)

	return nil
}

// RemoveClient removes a snooper client
func (sm *SnooperManager) RemoveClient(clientID uint16) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	clientInfo, exists := sm.clients[clientID]
	if !exists {
		return
	}

	sm.logger.WithField("client_id", clientID).Info("removing snooper client")

	// Unsubscribe from execution time events
	if clientInfo.execTimeSubscription != nil {
		clientInfo.execTimeSubscription.Unsubscribe()
		clientInfo.execTimeSubscription = nil
	}

	// Unregister module
	if clientInfo.module != nil {
		clientInfo.snooper.UnregisterModule(clientInfo.module)
	}

	// Cancel the client context
	clientInfo.cancel()

	// Stop the client
	if clientInfo.snooper != nil {
		clientInfo.snooper.Stop()
	}

	delete(sm.clients, clientID)
}

// GetCache returns the execution time cache
func (sm *SnooperManager) GetCache() *ExecutionTimeCache {
	return sm.cache
}

// ExecutionTimeEvent represents a block execution time event
type ExecutionTimeEvent struct {
	BlockHash     common.Hash
	BlockNumber   uint64
	ClientID      uint16
	ExecutionTime time.Duration
	Timestamp     time.Time
}

// HandleExecutionTimeEvent processes execution time events from tracer_event messages
func (sm *SnooperManager) HandleExecutionTimeEvent(event *ExecutionTimeEvent) {
	sm.logger.WithFields(logrus.Fields{
		"client_id":      event.ClientID,
		"block_hash":     event.BlockHash.Hex(),
		"block_number":   event.BlockNumber,
		"execution_time": event.ExecutionTime,
	}).Debug("received execution time event")

	// Get client type ID for the client
	clientInfo, exists := sm.clients[event.ClientID]
	if !exists {
		return
	}

	block := sm.indexer.GetBlocksByExecutionBlockHash(phase0.Hash32(event.BlockHash))
	if len(block) == 0 {
		// Store in cache
		sm.cache.Set(event.BlockHash, clientInfo.execution, event.ExecutionTime)
	} else {
		// Update block execution times
		for _, b := range block {
			b.AddExecutionTime(sm.ctx, beacon.ExecutionTime{
				ClientType: clientInfo.execution.GetClientType().Uint8(),
				MinTime:    uint16(event.ExecutionTime.Milliseconds()),
				MaxTime:    uint16(event.ExecutionTime.Milliseconds()),
				AvgTime:    uint16(event.ExecutionTime.Milliseconds()),
				Count:      1,
			})
		}
	}
}

// Close gracefully shuts down the snooper manager
func (sm *SnooperManager) Close() error {
	// Cancel context to stop all clients
	sm.cancel()

	// Remove all clients (this will close them)
	sm.mu.Lock()
	for clientID := range sm.clients {
		if clientInfo := sm.clients[clientID]; clientInfo != nil {
			clientInfo.cancel()
			if clientInfo.snooper != nil {
				clientInfo.snooper.Stop()
			}
		}
	}
	sm.clients = make(map[uint16]*snooperClientInfo)
	sm.mu.Unlock()

	// Wait for all goroutines to finish
	sm.wg.Wait()

	// Close the cache
	sm.cache.Close()

	sm.logger.Info("snooper manager shutdown complete")
	return nil
}

func (sm *SnooperManager) runClientEventListener(ctx context.Context, clientInfo *snooperClientInfo) {
	defer sm.wg.Done()

	// listen to execution time events
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-clientInfo.execTimeSubscription.Channel():
			// Process tracer_event message
			if msg.Method != "tracer_event" {
				continue
			}

			sm.processTracerEvent(msg, clientInfo)
		}
	}
}

// processTracerEvent processes tracer events for both engine_newPayloadV* and engine_getPayloadV* calls
func (sm *SnooperManager) processTracerEvent(msg *snooper.WSMessageWithBinary, clientInfo *snooperClientInfo) {
	tracerData, ok := msg.Data.(map[string]interface{})
	if !ok {
		sm.logger.Debug("invalid tracer event data")
		return
	}

	// Extract request data to determine method type
	requestData, ok := tracerData["request_data"].(map[string]interface{})
	if !ok {
		sm.logger.Debug("missing request_data in tracer event", tracerData)
		return
	}

	method, ok := requestData["method"].(string)
	if !ok {
		sm.logger.Info("missing method in request_data")
		return
	}

	// Handle engine_getPayloadV* calls
	if strings.HasPrefix(method, "engine_getPayloadV") {
		sm.handleGetPayloadEvent(tracerData, clientInfo)
		return
	}

	// Handle engine_newPayloadV* calls
	if strings.HasPrefix(method, "engine_newPayloadV") {
		sm.handleNewPayloadEvent(tracerData, clientInfo)
		return
	}

	sm.logger.WithField("method", method).Debug("unhandled method type in tracer event")
}

// handleGetPayloadEvent processes engine_getPayloadV* tracer events
func (sm *SnooperManager) handleGetPayloadEvent(tracerData map[string]interface{}, clientInfo *snooperClientInfo) {
	// Extract response data to get block hash
	responseData, ok := tracerData["response_data"].(map[string]interface{})
	if !ok {
		sm.logger.Debug("missing response_data in getPayload tracer event")
		return
	}

	blockHashStr, ok := responseData["blockHash"].(string)
	if !ok {
		sm.logger.Debug("missing blockHash in getPayload response_data")
		return
	}

	// Parse block hash
	blockHash := common.HexToHash(blockHashStr)
	if blockHash == (common.Hash{}) {
		sm.logger.WithField("block_hash", blockHashStr).Debug("invalid block hash in getPayload")
		return
	}

	// Add to ring buffer
	clientInfo.getPayloadHashes.Add(blockHash)

	sm.logger.WithFields(logrus.Fields{
		"client_id":   clientInfo.clientID,
		"block_hash":  blockHash.Hex(),
		"buffer_size": clientInfo.getPayloadHashes.Size(),
	}).Debug("stored getPayload block hash")
}

// handleNewPayloadEvent processes engine_newPayloadV* tracer events
func (sm *SnooperManager) handleNewPayloadEvent(tracerData map[string]interface{}, clientInfo *snooperClientInfo) {
	// Extract request data to get block information
	requestData, ok := tracerData["request_data"].(map[string]interface{})
	if !ok {
		sm.logger.Debug("missing request_data in newPayload tracer event")
		return
	}

	// Extract block hash and number
	blockHashStr, ok := requestData["blockHash"].(string)
	if !ok {
		sm.logger.Debug("missing blockHash in newPayload request_data")
		return
	}

	// Parse block hash
	blockHash := common.HexToHash(blockHashStr)
	if blockHash == (common.Hash{}) {
		sm.logger.WithField("block_hash", blockHashStr).Debug("invalid block hash in newPayload")
		return
	}

	// Check if this block hash was from a previous getPayload call
	if clientInfo.getPayloadHashes.Contains(blockHash) {
		sm.logger.WithFields(logrus.Fields{
			"client_id":  clientInfo.clientID,
			"block_hash": blockHash.Hex(),
		}).Debug("ignoring newPayload for block")
		return
	}

	blockNumberStr, ok := requestData["blockNumber"].(string)
	if !ok {
		sm.logger.Debug("missing blockNumber in newPayload request_data")
		return
	}

	// Parse block number (hex string)
	var blockNumber uint64
	if _, err := fmt.Sscanf(blockNumberStr, "0x%x", &blockNumber); err != nil {
		sm.logger.WithField("block_number", blockNumberStr).WithError(err).Debug("failed to parse block number in newPayload")
		return
	}

	// Extract timing information
	duration, ok := tracerData["duration_ms"].(float64)
	if !ok {
		sm.logger.Debug("missing duration_ms in newPayload tracer event")
		return
	}

	// Create execution time event
	event := &ExecutionTimeEvent{
		BlockHash:     blockHash,
		BlockNumber:   blockNumber,
		ClientID:      clientInfo.clientID,
		ExecutionTime: time.Duration(duration) * time.Millisecond,
		Timestamp:     time.Now(),
	}

	sm.logger.WithFields(logrus.Fields{
		"client_id":      event.ClientID,
		"block_hash":     event.BlockHash.Hex(),
		"block_number":   event.BlockNumber,
		"execution_time": event.ExecutionTime,
	}).Debug("processing newPayload execution time event")

	sm.HandleExecutionTimeEvent(event)
}

// HasClients returns true if any snooper clients are configured
func (sm *SnooperManager) HasClients() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.clients) > 0
}
