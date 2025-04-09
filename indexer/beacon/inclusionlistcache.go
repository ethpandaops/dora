package beacon

import (
	"runtime/debug"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// inclusionListCache is a cache for storing inclusion lists.
type inclusionListCache struct {
	indexer          *Indexer
	cacheMutex       sync.RWMutex
	inclusionListMap map[phase0.Slot][]*v1.SignedInclusionList
}

// newInclusionListCache creates a new instance of inclusionListCache.
func newInclusionListCache(indexer *Indexer) *inclusionListCache {
	cache := &inclusionListCache{
		indexer:          indexer,
		inclusionListMap: map[phase0.Slot][]*v1.SignedInclusionList{},
	}

	go cache.cleanupLoop()

	return cache
}

// addInclusionList adds the given inclusion list to the cache.
func (cache *inclusionListCache) addInclusionList(inclusionList *v1.SignedInclusionList) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for _, inclusionListCached := range cache.inclusionListMap[inclusionList.Message.Slot] {
		if inclusionListCached.Message.ValidatorIndex != inclusionList.Message.ValidatorIndex {
			continue
		}

		if inclusionListCached.Signature == inclusionList.Signature {
			// This is a duplicated event possibly coming from different clients.
			return
		}

		// This is an equivocation. We cache all of an equivocator's inclusion lists to display them in the explorer.
		break
	}

	cache.inclusionListMap[inclusionList.Message.Slot] = append(cache.inclusionListMap[inclusionList.Message.Slot], inclusionList)
}

// getInclusionListsBySlot returns the cached inclusion lists with the given slot.
func (cache *inclusionListCache) getInclusionListsBySlot(slot phase0.Slot) []*v1.SignedInclusionList {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	inclusionLists := make([]*v1.SignedInclusionList, len(cache.inclusionListMap[slot]))
	if len(inclusionLists) > 0 {
		copy(inclusionLists, cache.inclusionListMap[slot])
	}

	return inclusionLists
}

// cleanupLoop is a loop that cleans up the cache.
func (cache *inclusionListCache) cleanupLoop() {
	defer func() {
		if err := recover(); err != nil {
			cache.indexer.logger.WithError(err.(error)).Errorf("uncaught panic in indexer.beacon.inclusionListCache.cleanupLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go cache.cleanupLoop()
		}
	}()

	for {
		time.Sleep(30 * time.Minute)
		cache.cleanupCache()
	}
}

// cleanupCache cleans up the cache.
func (cache *inclusionListCache) cleanupCache() {
	chainState := cache.indexer.consensusPool.GetChainState()
	cutOffEpoch := chainState.CurrentEpoch() - phase0.Epoch(cache.indexer.activityHistoryLength)

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	deleted := 0
	for slot, inclusionLists := range cache.inclusionListMap {
		if chainState.EpochOfSlot(slot) < cutOffEpoch {
			deleted += len(inclusionLists)
			delete(cache.inclusionListMap, slot)
			continue
		}
	}

	cache.indexer.logger.Infof("cleaned up inclusion list cache, deleted %d entries", deleted)
}
