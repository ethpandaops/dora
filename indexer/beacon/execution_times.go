package beacon

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/execution"
)

// ExecutionTime represents execution timing data for a specific client
type ExecutionTime struct {
	ClientType uint8  // client type
	MinTime    uint16 // milliseconds
	MaxTime    uint16 // milliseconds
	AvgTime    uint16 // milliseconds
	Count      uint16 // number of clients
}

// ExecutionTimeData is an interface for execution time data
type ExecutionTimeData interface {
	GetClient() *execution.Client
	GetTime() uint16
}

// ExecutionTimeProvider is an interface for getting execution times from cache
type ExecutionTimeProvider interface {
	GetAndDeleteExecutionTimes(blockHash common.Hash) []ExecutionTimeData
}

// NoOpExecutionTimeProvider is a no-op implementation
type NoOpExecutionTimeProvider struct{}

func (n *NoOpExecutionTimeProvider) GetAndDeleteExecutionTimes(blockHash common.Hash) []ExecutionTimeData {
	return nil
}

// CalculateMinMaxTimesForStorage calculates the overall min/max times from a list of client execution times
func CalculateMinMaxTimesForStorage(times []ExecutionTime) (uint32, uint32) {
	if len(times) == 0 {
		return 0, 0
	}

	var minTime, maxTime uint32
	for _, time := range times {
		if minTime == 0 || uint32(time.MinTime) < minTime {
			minTime = uint32(time.MinTime)
		}
		if uint32(time.MaxTime) > maxTime {
			maxTime = uint32(time.MaxTime)
		}
	}

	return minTime, maxTime
}
