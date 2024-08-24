package beacon

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
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

type newForkInfo struct {
	fork        *Fork
	updateRoots [][]byte
}

// processBlock processes a block and detects new forks if any.
// It persists the new forks to the database, updates any subsequent blocks building on top of the given block and returns the fork ID.
func (cache *forkCache) processBlock(block *Block) error {
	cache.forkProcessLock.Lock()
	defer cache.forkProcessLock.Unlock()

	parentRoot := block.GetParentRoot()
	if parentRoot == nil {
		return fmt.Errorf("parent root not found for block %v", block.Slot)
	}

	chainState := cache.indexer.consensusPool.GetChainState()

	// get fork id from parent block
	parentForkId := ForkKey(1)
	parentSlot := phase0.Slot(0)
	parentIsProcessed := false
	parentIsFinalized := false

	parentBlock := cache.indexer.blockCache.getBlockByRoot(*parentRoot)
	if parentBlock == nil {
		blockHead := db.GetBlockHeadByRoot((*parentRoot)[:])
		if blockHead != nil {
			parentForkId = ForkKey(blockHead.ForkId)
			parentSlot = phase0.Slot(blockHead.Slot)
			parentIsProcessed = true
			parentIsFinalized = parentSlot < chainState.GetFinalizedSlot()
		}
	} else if parentBlock.fokChecked {
		parentForkId = parentBlock.forkId
		parentSlot = parentBlock.Slot
		parentIsProcessed = true
		parentIsFinalized = parentBlock.Slot < chainState.GetFinalizedSlot()
	}

	// check if this block introduces a new fork, it does so if:
	// 1. the parent is known & processed and has 1 more child block besides this one
	// 2. the current block has 2 or more child blocks (multiple forks possible)
	newForks := []*newForkInfo{}
	currentForkId := parentForkId

	// check scenario 1
	if parentIsProcessed {
		otherChildren := []*Block{}
		for _, child := range cache.indexer.blockCache.getBlocksByParentRoot(*parentRoot) {
			if child == block {
				continue
			}

			otherChildren = append(otherChildren, child)
		}

		if len(otherChildren) > 0 {
			logbuf := strings.Builder{}

			// parent already has a children, so this block introduces a new fork
			if cache.getForkByLeaf(block.Root) != nil {
				cache.indexer.logger.Warnf("fork already exists for leaf %v [%v] (processing %v, scenario 1)", block.Slot, block.Root.String(), block.Slot)
			} else {
				cache.lastForkId++
				fork := newFork(cache.lastForkId, parentSlot, *parentRoot, block, parentForkId)
				cache.addFork(fork)

				currentForkId = fork.forkId
				newFork := &newForkInfo{
					fork: fork,
				}
				newForks = append(newForks, newFork)

				fmt.Fprintf(&logbuf, ", head1: %v [%v, ? upd]", block.Slot, block.Root.String())
			}

			if !parentIsFinalized && len(otherChildren) == 1 {
				// parent is not finalized and it's the first fork based on this parent
				// so we need to create another fork for the other chain and update the fork ids of the blocks

				if cache.getForkByLeaf(block.Root) != nil {
					cache.indexer.logger.Warnf("fork already exists for leaf %v [%v] (processing %v, scenario 1)", block.Slot, block.Root.String(), block.Slot)
				} else {

					cache.lastForkId++
					otherFork := newFork(cache.lastForkId, parentSlot, *parentRoot, otherChildren[0], parentForkId)
					cache.addFork(otherFork)

					newFork := &newForkInfo{
						fork:        otherFork,
						updateRoots: cache.updateForkBlocks(otherChildren[0], otherFork.forkId, false),
					}
					newForks = append(newForks, newFork)

					fmt.Fprintf(&logbuf, ", head2: %v [%v, %v upd]", newFork.fork.leafSlot, newFork.fork.leafRoot.String(), len(newFork.updateRoots))
				}
			}

			if logbuf.Len() > 0 {
				cache.indexer.logger.Infof("new fork leaf detected (base %v [%v]%v)", parentSlot, parentRoot.String(), logbuf.String())
			}
		}
	}

	// check scenario 2
	childBlocks := make([]*Block, 0)
	for _, child := range cache.indexer.blockCache.getBlocksByParentRoot(block.Root) {
		if !child.fokChecked {
			continue
		}

		childBlocks = append(childBlocks, child)
	}

	if len(childBlocks) > 1 {
		// one or more forks detected
		logbuf := strings.Builder{}
		for idx, child := range childBlocks {
			if cache.getForkByLeaf(child.Root) != nil {
				cache.indexer.logger.Warnf("fork already exists for leaf %v [%v] (processing %v, scenario 2)", child.Slot, child.Root.String(), block.Slot)
			} else {
				cache.lastForkId++
				fork := newFork(cache.lastForkId, block.Slot, block.Root, child, currentForkId)
				cache.addFork(fork)

				newFork := &newForkInfo{
					fork:        fork,
					updateRoots: cache.updateForkBlocks(child, fork.forkId, false),
				}
				newForks = append(newForks, newFork)

				fmt.Fprintf(&logbuf, ", head%v: %v [%v, %v upd]", idx+1, newFork.fork.leafSlot, newFork.fork.leafRoot.String(), len(newFork.updateRoots))
			}
		}

		if logbuf.Len() > 0 {
			cache.indexer.logger.Infof("new child forks detected (base %v [%v]%v)", block.Slot, block.Root.String(), logbuf.String())
		}
	}

	// update fork ids of all blocks building on top of this block
	updatedBlocks := cache.updateForkBlocks(block, currentForkId, true)

	// set detected fork id to the block
	block.forkId = currentForkId
	block.fokChecked = true

	// update fork head block if needed
	fork := cache.getForkById(currentForkId)
	if fork != nil {
		lastBlock := block
		if len(updatedBlocks) > 0 {
			lastBlock = cache.indexer.blockCache.getBlockByRoot(phase0.Root(updatedBlocks[len(updatedBlocks)-1]))
		}
		if lastBlock != nil && (fork.headBlock == nil || lastBlock.Slot > fork.headBlock.Slot) {
			fork.headBlock = lastBlock
		}
	}

	// persist new forks and updated blocks to the database
	if len(newForks) > 0 || len(updatedBlocks) > 0 {
		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			for _, newFork := range newForks {
				err := db.InsertFork(newFork.fork.toDbFork(), tx)
				if err != nil {
					return err
				}

				if len(newFork.updateRoots) > 0 {
					err = db.UpdateUnfinalizedBlockForkId(newFork.updateRoots, uint64(newFork.fork.forkId), tx)
					if err != nil {
						return err
					}
				}
			}

			if len(updatedBlocks) > 0 {
				err := db.UpdateUnfinalizedBlockForkId(updatedBlocks, uint64(currentForkId), tx)
				if err != nil {
					return err
				}

				cache.indexer.logger.Infof("updated %v blocks to fork %v", len(updatedBlocks), currentForkId)
			}

			err := cache.updateForkState(tx)
			if err != nil {
				return fmt.Errorf("error while updating fork state: %v", err)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// updateForkBlocks updates the blocks building on top of the given block in the fork and returns the updated block roots.
func (cache *forkCache) updateForkBlocks(startBlock *Block, forkId ForkKey, skipStartBlock bool) [][]byte {
	blockRoots := [][]byte{}

	if !skipStartBlock {
		blockRoots = append(blockRoots, startBlock.Root[:])
	}

	for {
		nextBlocks := cache.indexer.blockCache.getBlocksByParentRoot(startBlock.Root)
		if len(nextBlocks) != 1 {
			break
		}

		nextBlock := nextBlocks[0]
		if !nextBlock.fokChecked {
			break
		}

		if nextBlock.forkId == forkId {
			break
		}

		nextBlock.forkId = forkId
		blockRoots = append(blockRoots, nextBlock.Root[:])

		startBlock = nextBlock
	}

	return blockRoots
}
