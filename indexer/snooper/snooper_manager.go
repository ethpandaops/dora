package snooper

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
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
	execTimeSubscription *utils.Subscription[*snooper.ExecutionTimeEvent]
}

// NewSnooperManager creates a new snooper manager
func NewSnooperManager(logger logrus.FieldLogger, indexer *beacon.Indexer) *SnooperManager {
	ctx, cancel := context.WithCancel(context.Background())

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
	snooperClient := snooper.NewClient(snooperURL, clientID, sm.logger)

	// Create client context
	clientCtx, clientCancel := context.WithCancel(sm.ctx)

	clientInfo := &snooperClientInfo{
		execution:            executionClient,
		snooper:              snooperClient,
		clientID:             clientID,
		snooperURL:           snooperURL,
		cancel:               clientCancel,
		execTimeSubscription: snooperClient.SubscribeExecutionTimeEvent(10, false),
	}

	sm.clients[clientID] = clientInfo

	// Start the client in a goroutine with reconnection logic
	sm.wg.Add(1)
	go sm.runClientWithReconnection(clientCtx, clientInfo)
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

	// Cancel the client context
	clientInfo.cancel()

	// Close the client
	if clientInfo.snooper != nil {
		clientInfo.snooper.Close()
	}

	delete(sm.clients, clientID)
}

// GetCache returns the execution time cache
func (sm *SnooperManager) GetCache() *ExecutionTimeCache {
	return sm.cache
}

// HandleExecutionTimeEvent implements the snooper.ExecutionTimeEventHandler interface
func (sm *SnooperManager) HandleExecutionTimeEvent(event *snooper.ExecutionTimeEvent) {
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
			b.AddExecutionTime(beacon.ExecutionTime{
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
				clientInfo.snooper.Close()
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

// runClientWithReconnection runs a snooper client with automatic reconnection
func (sm *SnooperManager) runClientWithReconnection(ctx context.Context, clientInfo *snooperClientInfo) {
	defer sm.wg.Done()

	logger := sm.logger.WithField("client_id", clientInfo.clientID)
	backoffDuration := sm.reconnectBackoff
	maxBackoff := 5 * time.Minute

	for {
		select {
		case <-ctx.Done():
			logger.Debug("client context cancelled, stopping reconnection loop")
			return
		default:
		}

		logger.Debug("attempting to connect snooper client")

		// Try to connect
		err := clientInfo.snooper.Connect()
		if err != nil {
			logger.WithError(err).WithField("backoff", backoffDuration).Warn("failed to connect snooper client, retrying")

			// Wait before retrying
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoffDuration):
			}

			// Exponential backoff with jitter
			backoffDuration = time.Duration(float64(backoffDuration) * 1.5)
			if backoffDuration > maxBackoff {
				backoffDuration = maxBackoff
			}
			continue
		}

		// Reset backoff on successful connection
		backoffDuration = sm.reconnectBackoff
		logger.Debug("snooper client connected successfully")

		// Run the client
		err = clientInfo.snooper.Run()
		if err != nil && err != context.Canceled {
			logger.WithError(err).Warn("Snooper client disconnected")
		}

		// Check if we should stop
		select {
		case <-ctx.Done():
			logger.Debug("client context cancelled, stopping")
			return
		default:
			logger.Debug("snooper client disconnected, will attempt reconnection")
		}
	}
}

func (sm *SnooperManager) runClientEventListener(ctx context.Context, clientInfo *snooperClientInfo) {
	// listen to execution time events
	for {
		select {
		case <-ctx.Done():
			return
		case blockEvent := <-clientInfo.execTimeSubscription.Channel():
			sm.HandleExecutionTimeEvent(blockEvent)
		}
	}
}

// HasClients returns true if any snooper clients are configured
func (sm *SnooperManager) HasClients() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.clients) > 0
}
