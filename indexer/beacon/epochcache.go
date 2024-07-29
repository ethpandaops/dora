package beacon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
)

type epochStatsKey [32 + 8]byte

func getEpochStatsKey(epoch phase0.Epoch, dependentRoot phase0.Root) epochStatsKey {
	var key epochStatsKey

	copy(key[0:], dependentRoot[:])
	binary.LittleEndian.PutUint64(key[32:], uint64(epoch))

	return key
}

type epochCache struct {
	indexer     *Indexer
	cacheMutex  sync.RWMutex
	statsMap    map[epochStatsKey]*EpochStats
	stateMap    map[phase0.Root]*EpochState
	loadingChan chan bool
}

func newEpochCache(indexer *Indexer) *epochCache {
	cache := &epochCache{
		indexer:     indexer,
		statsMap:    map[epochStatsKey]*EpochStats{},
		stateMap:    map[phase0.Root]*EpochState{},
		loadingChan: make(chan bool, indexer.maxParallelStateCalls),
	}
	go cache.startLoaderLoop()

	return cache
}

func (cache *epochCache) createOrGetEpochStats(epoch phase0.Epoch, dependentRoot phase0.Root) (*EpochStats, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	statsKey := getEpochStatsKey(epoch, dependentRoot)

	if cache.statsMap[statsKey] != nil {
		return cache.statsMap[statsKey], false
	}

	fmt.Printf("request stats %v %v\n", epoch, dependentRoot.String())

	epochState := cache.stateMap[dependentRoot]
	if epochState == nil {
		epochState = newEpochState(dependentRoot)
		cache.stateMap[dependentRoot] = epochState
	}

	epochStats := newEpochStats(epoch, dependentRoot, epochState)
	cache.statsMap[statsKey] = epochStats

	return epochStats, true
}

func (cache *epochCache) getPendingEpochStats() []*EpochStats {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	pendingStats := make([]*EpochStats, 0)
	for _, stats := range cache.statsMap {
		if stats.dependentState.loadingStatus == 0 {
			pendingStats = append(pendingStats, stats)
		}
	}

	sort.Slice(pendingStats, func(a, b int) bool {
		reqCountA := len(pendingStats[a].requestedBy)
		reqCountB := len(pendingStats[b].requestedBy)

		if reqCountA != reqCountB {
			return reqCountA > reqCountB
		}

		return pendingStats[a].epoch > pendingStats[b].epoch
	})

	return pendingStats
}

func (cache *epochCache) removeEpochStats(epochStats *EpochStats) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	statsKey := getEpochStatsKey(epochStats.epoch, epochStats.dependentRoot)

	if cache.statsMap[statsKey] == nil {
		return
	}

	delete(cache.statsMap, statsKey)

	foundOtherStats := false
	for _, stats := range cache.statsMap {
		if bytes.Equal(stats.dependentRoot[:], epochStats.dependentRoot[:]) {
			foundOtherStats = true
			break
		}
	}

	if !foundOtherStats {
		epochStats.dependentState.dispose()
		delete(cache.stateMap, epochStats.dependentRoot)
	}
}

func (cache *epochCache) startLoaderLoop() {
	defer func() {
		if err := recover(); err != nil {
			cache.indexer.logger.WithError(err.(error)).Errorf("uncaught panic in indexer.beacon.epochCache.startLoaderLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go cache.startLoaderLoop()
		}
	}()

	for {
		cache.runLoaderLoop()
		time.Sleep(2 * time.Second)
	}
}

func (cache *epochCache) runLoaderLoop() {
	// load next epoch stats
	pendingStats := cache.getPendingEpochStats()
	if len(pendingStats) == 0 {
		return
	}

	if cache.indexer.maxParallelStateCalls > 0 {
		cache.loadingChan <- true
	}

	go cache.loadEpochStats(pendingStats[0])
}

func (cache *epochCache) loadEpochStats(epochStats *EpochStats) {
	defer func() {
		if cache.indexer.maxParallelStateCalls > 0 {
			<-cache.loadingChan
		}
	}()

	clients := epochStats.getRequestedBy()
	sort.Slice(clients, func(a, b int) bool {
		cliA := clients[a]
		cliB := clients[b]
		if cliA.skipValidators != cliB.skipValidators {
			if cliA.skipValidators {
				return true
			} else {
				return false
			}
		}

		onlineA := cliA.client.GetStatus() == consensus.ClientStatusOnline || cliA.client.GetStatus() == consensus.ClientStatusOptimistic
		onlineB := cliB.client.GetStatus() == consensus.ClientStatusOnline || cliB.client.GetStatus() == consensus.ClientStatusOptimistic
		if onlineA != onlineB {
			if onlineA {
				return false
			} else {
				return true
			}
		}

		hasBlockA := cache.indexer.blockCache.isCanonicalBlock(epochStats.dependentRoot, cliA.headRoot)
		hasBlockB := cache.indexer.blockCache.isCanonicalBlock(epochStats.dependentRoot, cliB.headRoot)
		if hasBlockA != hasBlockB {
			if hasBlockA {
				return false
			} else {
				return true
			}
		}

		if cliA.archive != cliB.archive {
			if cliA.archive {
				return false
			} else {
				return true
			}
		}

		if cliA.priority != cliB.priority {
			if cliA.priority > cliB.priority {
				return false
			} else {
				return true
			}
		}

		return false
	})

	for _, client := range clients {
		err := epochStats.dependentState.loadState(client)
		if err != nil {
			client.logger.Warnf("failed loading epoch %v stats (dep: %v): %v", epochStats.epoch, epochStats.dependentRoot.String(), err)
		} else {
			break
		}
	}

	if epochStats.dependentState.loadingStatus != 2 {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for _, stats := range cache.statsMap {
		if stats.dependentState == epochStats.dependentState {
			stats.processState(cache.indexer)
		}
	}
}
