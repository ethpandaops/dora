package snooper

import (
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/execution"
	beacon "github.com/ethpandaops/dora/indexer/beacon"
)

// ExecutionTimeProviderImpl implements the ExecutionTimeProvider interface
type ExecutionTimeProviderImpl struct {
	cache *ExecutionTimeCache
}

// NewExecutionTimeProvider creates a new execution time provider
func NewExecutionTimeProvider(cache *ExecutionTimeCache) *ExecutionTimeProviderImpl {
	return &ExecutionTimeProviderImpl{
		cache: cache,
	}
}

// BeaconExecutionTime represents execution timing data compatible with beacon package
type BeaconExecutionTime struct {
	Client *execution.Client // snooper client
	Time   uint16            // milliseconds
	Added  time.Time         // when this was recorded
}

// Implement ExecutionTimeData interface
func (e *BeaconExecutionTime) GetClient() *execution.Client { return e.Client }
func (e *BeaconExecutionTime) GetTime() uint16              { return e.Time }

// GetAndDeleteExecutionTimes implements the ExecutionTimeProvider interface
func (p *ExecutionTimeProviderImpl) GetAndDeleteExecutionTimes(blockHash common.Hash) []beacon.ExecutionTimeData {
	if p.cache == nil {
		return nil
	}

	execTimes := p.cache.GetAndDelete(blockHash)
	if execTimes == nil {
		return nil
	}

	// Convert to beacon package compatible format
	result := make([]beacon.ExecutionTimeData, len(execTimes))
	for i, cachedTime := range execTimes {
		execTime := &BeaconExecutionTime{
			Client: cachedTime.Client,
			Time:   cachedTime.Time,
		}
		result[i] = execTime
	}

	return result
}
