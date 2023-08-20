package indexer

import (
	"bytes"
	"sync"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
)

type EpochStats struct {
	epoch         uint64
	dependendRoot []byte
	dutiesInDb    bool

	assignmentsMutex  sync.RWMutex
	assignmentsFailed bool
	assignments       *rpctypes.EpochAssignments

	proposerAssignments map[uint64]uint64
	attestorAssignments map[string][]uint64
	syncAssignments     []uint64
}

func (cache *indexerCache) getEpochStats(epoch uint64, dependendRoot []byte) *EpochStats {
	cache.epochStatsMutex.RLock()
	defer cache.epochStatsMutex.RUnlock()
	if cache.epochStatsMap[epoch] != nil {
		for _, epochStats := range cache.epochStatsMap[epoch] {
			if bytes.Equal(epochStats.dependendRoot, dependendRoot) {
				return epochStats
			}
		}
	}
	return nil
}

func (cache *indexerCache) createOrGetEpochStats(epoch uint64, dependendRoot []byte, client *indexerClient) *EpochStats {
	cache.epochStatsMutex.Lock()
	defer cache.epochStatsMutex.Unlock()
	if cache.epochStatsMap[epoch] == nil {
		cache.epochStatsMap[epoch] = make([]*EpochStats, 0)
	} else {
		for _, epochStats := range cache.epochStatsMap[epoch] {
			if bytes.Equal(epochStats.dependendRoot, dependendRoot) {
				return epochStats
			}
		}
	}
	epochStats := &EpochStats{
		epoch:         epoch,
		dependendRoot: dependendRoot,
	}
	cache.epochStatsMap[epoch] = append(cache.epochStatsMap[epoch], epochStats)
	return epochStats
}
