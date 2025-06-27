package snooper

import (
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/execution"
)

// ExecutionTimeCache temporarily stores execution times for blocks that haven't been processed yet
type ExecutionTimeCache struct {
	data    map[common.Hash][]BeaconExecutionTime
	mu      sync.RWMutex
	ttl     time.Duration
	cleanup *time.Ticker
	stopCh  chan struct{}
}

// NewExecutionTimeCache creates a new execution time cache
func NewExecutionTimeCache(ttl time.Duration) *ExecutionTimeCache {
	cache := &ExecutionTimeCache{
		data:   make(map[common.Hash][]BeaconExecutionTime),
		ttl:    ttl,
		stopCh: make(chan struct{}),
	}

	// Start cleanup routine
	cache.cleanup = time.NewTicker(ttl / 2) // Clean up twice per TTL period
	go cache.cleanupRoutine()

	return cache
}

// Set stores an execution time for a specific block hash and client type
func (c *ExecutionTimeCache) Set(blockHash common.Hash, client *execution.Client, execTime time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ms := uint16(execTime.Milliseconds())
	if ms > 60000 {
		ms = 60000 // Cap at 60 seconds
	}

	entry, exists := c.data[blockHash]
	if !exists {
		entry = make([]BeaconExecutionTime, 0)
	}

	entry = append(entry, BeaconExecutionTime{
		Client: client,
		Time:   ms,
		Added:  time.Now(),
	})

	c.data[blockHash] = entry
}

// Get retrieves execution times for a specific block hash
func (c *ExecutionTimeCache) Get(blockHash common.Hash) []BeaconExecutionTime {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.data[blockHash]
	if !exists {
		return nil
	}

	return entry
}

// Delete removes execution times for a specific block hash
func (c *ExecutionTimeCache) Delete(blockHash common.Hash) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.data, blockHash)
}

// GetAndDelete atomically retrieves and removes execution times for a specific block hash
func (c *ExecutionTimeCache) GetAndDelete(blockHash common.Hash) []BeaconExecutionTime {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.data[blockHash]
	if !exists {
		return nil
	}

	delete(c.data, blockHash)
	return entry
}

// GetStats returns cache statistics
func (c *ExecutionTimeCache) GetStats() (int, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := len(c.data)
	expired := 0
	now := time.Now()

	for _, entry := range c.data {
		for _, execTime := range entry {
			if now.Sub(execTime.Added) > c.ttl {
				expired++
			}
		}
	}

	return total, expired
}

// Close stops the cache cleanup routine
func (c *ExecutionTimeCache) Close() {
	close(c.stopCh)
	if c.cleanup != nil {
		c.cleanup.Stop()
	}
}

// cleanupRoutine periodically removes expired entries
func (c *ExecutionTimeCache) cleanupRoutine() {
	for {
		select {
		case <-c.cleanup.C:
			c.performCleanup()
		case <-c.stopCh:
			return
		}
	}
}

// performCleanup removes expired entries from the cache
func (c *ExecutionTimeCache) performCleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	toDelete := make([]common.Hash, 0)

	for hash, entry := range c.data {
		maxTime := time.Time{}
		for _, execTime := range entry {
			if execTime.Added.After(maxTime) {
				maxTime = execTime.Added
			}
		}
		if now.Sub(maxTime) > c.ttl {
			toDelete = append(toDelete, hash)
		}
	}

	for _, hash := range toDelete {
		delete(c.data, hash)
	}
}

// GetClientTypeID maps client names to type IDs
func GetClientTypeID(clientName string) uint8 {
	switch clientName {
	case "geth":
		return 0
	case "nethermind":
		return 1
	case "besu":
		return 2
	case "erigon":
		return 3
	case "reth":
		return 4
	default:
		return 255 // Unknown client type
	}
}

// GetClientTypeName maps client type IDs to names
func GetClientTypeName(clientTypeID uint8) string {
	switch clientTypeID {
	case 0:
		return "geth"
	case 1:
		return "nethermind"
	case 2:
		return "besu"
	case 3:
		return "erigon"
	case 4:
		return "reth"
	default:
		return "unknown"
	}
}
