package beacon

import (
	"bytes"
	"fmt"
	"sort"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// forkCache is a struct that represents the fork cache in the indexer.
type forkCache struct {
	indexer         *Indexer
	cacheMutex      sync.RWMutex
	forkMap         map[ForkKey]*Fork
	finalizedForkId ForkKey
	lastForkId      ForkKey

	forkProcessLock sync.Mutex
}

// newForkCache creates a new instance of the forkCache struct.
func newForkCache(indexer *Indexer) *forkCache {
	return &forkCache{
		indexer: indexer,
		forkMap: make(map[ForkKey]*Fork),
	}
}

// loadForkState loads the fork state from the database.
func (cache *forkCache) loadForkState() error {
	forkState := dbtypes.IndexerForkState{}
	db.GetExplorerState("indexer.forkstate", &forkState)

	if forkState.ForkId == 0 {
		forkState.ForkId = 1
	}
	if forkState.Finalized == 0 {
		forkState.Finalized = 1
	}

	cache.lastForkId = ForkKey(forkState.ForkId)
	cache.finalizedForkId = ForkKey(forkState.Finalized)

	return nil
}

// updateForkState updates the fork state in the database.
func (cache *forkCache) updateForkState(tx *sqlx.Tx) error {
	err := db.SetExplorerState("indexer.forkstate", &dbtypes.IndexerForkState{
		ForkId:    uint64(cache.lastForkId),
		Finalized: uint64(cache.finalizedForkId),
	}, tx)
	if err != nil {
		return fmt.Errorf("error while updating fork state: %v", err)
	}
	return nil
}

// getForkById retrieves a fork from the cache by its ID.
func (cache *forkCache) getForkById(forkId ForkKey) *Fork {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	return cache.forkMap[forkId]
}

// addFork adds a fork to the cache.
func (cache *forkCache) addFork(fork *Fork) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.forkMap[fork.forkId] = fork
}

func (cache *forkCache) getForkByLeaf(leafRoot phase0.Root) *Fork {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for _, fork := range cache.forkMap {
		if bytes.Equal(fork.leafRoot[:], leafRoot[:]) {
			return fork
		}
	}

	return nil
}

func (cache *forkCache) getForkByBase(baseRoot phase0.Root) []*Fork {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	forks := []*Fork{}
	for _, fork := range cache.forkMap {
		if bytes.Equal(fork.baseRoot[:], baseRoot[:]) {
			forks = append(forks, fork)
		}
	}

	return forks
}

// removeFork removes a fork from the cache.
func (cache *forkCache) removeFork(forkId ForkKey) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	delete(cache.forkMap, forkId)
}

func (cache *forkCache) getParentForkIds(forkId ForkKey) []ForkKey {
	parentForks := []ForkKey{forkId}

	thisFork := cache.getForkById(forkId)
	for thisFork != nil && thisFork.parentFork != 0 {
		parentForks = append(parentForks, thisFork.parentFork)
		thisFork = cache.getForkById(thisFork.parentFork)
	}

	return parentForks
}

// ForkHead represents a fork head with its ID, fork, and block.
type ForkHead struct {
	ForkId ForkKey
	Fork   *Fork
	Block  *Block
}

// getForkHeads returns the fork heads in the cache.
// A head fork is a fork that no other fork is building on top of, so it contains the head of the fork chain.
func (cache *forkCache) getForkHeads() []*ForkHead {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	forkParents := map[ForkKey]bool{}
	for _, fork := range cache.forkMap {
		if fork.parentFork != 0 {
			forkParents[fork.parentFork] = true
		}
	}

	forkHeads := []*ForkHead{}
	if !forkParents[cache.finalizedForkId] {
		canonicalBlocks := cache.indexer.blockCache.getForkBlocks(cache.finalizedForkId)
		sort.Slice(canonicalBlocks, func(i, j int) bool {
			return canonicalBlocks[i].Slot > canonicalBlocks[j].Slot
		})
		if len(canonicalBlocks) > 0 {
			forkHeads = append(forkHeads, &ForkHead{
				ForkId: cache.finalizedForkId,
				Block:  canonicalBlocks[0],
			})
		}
	}

	for forkId, fork := range cache.forkMap {
		if !forkParents[forkId] {
			forkHeads = append(forkHeads, &ForkHead{
				ForkId: forkId,
				Fork:   cache.forkMap[forkId],
				Block:  fork.headBlock,
			})
		}
	}

	return forkHeads
}

// getForksBefore retrieves all forks that happened before the given slot.
func (cache *forkCache) getForksBefore(slot phase0.Slot) []*Fork {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	var forks []*Fork
	for _, fork := range cache.forkMap {
		if fork.baseSlot < slot {
			forks = append(forks, fork)
		}
	}

	return forks
}

// setFinalizedEpoch sets the finalized epoch in the fork cache.
// It removes all forks that happened before the finalized epoch and updates the finalized fork ID.
func (cache *forkCache) setFinalizedEpoch(finalizedSlot phase0.Slot, justifiedRoot phase0.Root) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	closestForkId := ForkKey(0)
	closestDistance := uint64(0)

	for _, fork := range cache.forkMap {
		if fork.leafSlot >= finalizedSlot {
			continue
		}

		isInFork, distance := cache.indexer.blockCache.getCanonicalDistance(fork.leafRoot, justifiedRoot, 0)
		if isInFork && (closestForkId == 0 || distance < closestDistance) {
			closestForkId = fork.forkId
			closestDistance = distance
		}

		delete(cache.forkMap, fork.forkId)
	}

	cache.finalizedForkId = closestForkId

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return cache.updateForkState(tx)
	})
	if err != nil {
		cache.indexer.logger.Errorf("error while updating fork state: %v", err)
	}
}
