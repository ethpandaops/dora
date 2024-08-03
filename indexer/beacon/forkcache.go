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
		forkState.ForkId = 1
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

// removeFork removes a fork from the cache.
func (cache *forkCache) removeFork(forkId ForkKey) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	delete(cache.forkMap, forkId)
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
		if fork.baseSlot >= finalizedSlot {
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

// checkForkDistance checks the distance between two blocks in a fork and returns the base block and distances.
// If the fork happened before the latest finalized slot, only the side of the fork that does not include the finalized block gets returned.
func (cache *forkCache) checkForkDistance(block1 *Block, block2 *Block, parentsMap map[phase0.Root]bool) (baseBlock *Block, block1Distance uint64, leafBlock1 *Block, block2Distance uint64, leafBlock2 *Block) {
	finalizedSlot := cache.indexer.consensusPool.GetChainState().GetFinalizedSlot()
	_, finalizedRoot := cache.indexer.consensusPool.GetChainState().GetFinalizedCheckpoint()
	leafBlock1 = block1
	leafBlock2 = block2

	var block1IsFinalized, block2IsFinalized bool

	for {
		parentsMap[block1.Root] = true
		parentsMap[block2.Root] = true

		if bytes.Equal(block1.Root[:], block2.Root[:]) {
			baseBlock = block1
			return
		}

		if !block1IsFinalized && bytes.Equal(block1.Root[:], finalizedRoot[:]) {
			block1IsFinalized = true
		}

		if !block2IsFinalized && bytes.Equal(block2.Root[:], finalizedRoot[:]) {
			block2IsFinalized = true
		}

		if block1.Slot <= finalizedSlot && block2.Slot <= finalizedSlot {
			if block1IsFinalized {
				baseBlock = block2
				leafBlock1 = nil
				return
			}
			if block2IsFinalized {
				baseBlock = block1
				leafBlock2 = nil
				return
			}

			break
		}

		block1Slot := block1.Slot
		block2Slot := block2.Slot

		if block1Slot <= block2Slot && !block2IsFinalized {
			leafBlock2 = block2
			parentRoot := block2.GetParentRoot()
			if parentRoot == nil {
				break
			}

			block2 = cache.indexer.blockCache.getBlockByRoot(*parentRoot)
			if block2 == nil {
				dbBlockHead := db.GetBlockHeadByRoot(parentRoot[:])
				if dbBlockHead != nil {
					block2 = newBlock(cache.indexer.dynSsz, phase0.Root(dbBlockHead.Root), phase0.Slot(dbBlockHead.Slot))
					block2.isInFinalizedDb = true
					block2.parentRoot = (*phase0.Root)(dbBlockHead.ParentRoot)
				} else {
					break
				}
			}

			block2Distance++
		}

		if block2Slot <= block1Slot && !block1IsFinalized {
			leafBlock1 = block1
			parentRoot := block1.GetParentRoot()
			if parentRoot == nil {
				break
			}

			block1 = cache.indexer.blockCache.getBlockByRoot(*parentRoot)
			if block1 == nil {
				dbBlockHead := db.GetBlockHeadByRoot(parentRoot[:])
				if dbBlockHead != nil {
					block1 = newBlock(cache.indexer.dynSsz, phase0.Root(dbBlockHead.Root), phase0.Slot(dbBlockHead.Slot))
					block1.isInFinalizedDb = true
					block1.parentRoot = (*phase0.Root)(dbBlockHead.ParentRoot)
				} else {
					break
				}
			}

			block1Distance++
		}
	}

	return nil, 0, nil, 0, nil
}

// processBlock processes a block and detects new forks if any.
// It persists the new forks to the database, updates any subsequent blocks building on top of the given block and returns the fork ID.
func (cache *forkCache) processBlock(block *Block) (ForkKey, error) {
	cache.forkProcessLock.Lock()
	defer cache.forkProcessLock.Unlock()

	parentForkId := ForkKey(1)
	// get fork id from parent block
	parentRoot := block.GetParentRoot()
	if parentRoot != nil {
		parentBlock := cache.indexer.blockCache.getBlockByRoot(*parentRoot)
		if parentBlock == nil {
			blockHead := db.GetBlockHeadByRoot((*parentRoot)[:])
			if blockHead != nil {
				parentForkId = ForkKey(blockHead.ForkId)
			}
		} else {
			parentForkId = parentBlock.forkId
		}
	}

	forkBlocks := cache.indexer.blockCache.getForkBlocks(parentForkId)
	sort.Slice(forkBlocks, func(i, j int) bool {
		return forkBlocks[i].Slot > forkBlocks[j].Slot
	})

	var fork1, fork2 *Fork
	var fork1Roots, fork2Roots [][]byte
	currentForkId := parentForkId

	parentsMap := map[phase0.Root]bool{}
	for _, forkBlock := range forkBlocks {
		if forkBlock == block || parentsMap[forkBlock.Root] {
			continue
		}

		baseBlock, distance1, leaf1, distance2, leaf2 := cache.checkForkDistance(block, forkBlock, parentsMap)
		if baseBlock != nil && distance1 > 0 && distance2 > 0 {
			// new fork detected

			if leaf1 != nil {
				cache.lastForkId++
				fork1 = newFork(cache.lastForkId, baseBlock, leaf1, parentForkId)
				cache.addFork(fork1)
				fork1Roots = cache.updateNewForkBlocks(fork1, forkBlocks, block)
			}

			if leaf2 != nil {
				cache.lastForkId++
				fork2 = newFork(cache.lastForkId, baseBlock, leaf2, parentForkId)
				cache.addFork(fork2)
				fork2Roots = cache.updateNewForkBlocks(fork2, forkBlocks, nil)
			}

			if parentForkId > 0 {
				parentFork := cache.getForkById(parentForkId)
				if parentFork != nil {
					parentFork.headBlock = baseBlock
				}
			}

			logbuf := strings.Builder{}
			fmt.Fprintf(&logbuf, "new fork detected (base %v [%v]", baseBlock.Slot, baseBlock.Root.String())
			if leaf1 != nil {
				fmt.Fprintf(&logbuf, ", head1: %v [%v]", leaf1.Slot, leaf1.Root.String())
			}
			if leaf2 != nil {
				fmt.Fprintf(&logbuf, ", head2: %v [%v]", leaf2.Slot, leaf2.Root.String())
			}
			fmt.Fprintf(&logbuf, ")")
			cache.indexer.logger.Infof(logbuf.String())

			if fork1 != nil {
				currentForkId = fork1.forkId
			}

			break
		}
	}

	updatedBlocks := [][]byte{}
	if currentForkId != 0 {
		// apply fork id to all blocks building on top of this block
		nextBlock := block

		for {
			nextBlocks := cache.indexer.blockCache.getBlocksByParentRoot(nextBlock.Root)
			if len(nextBlocks) > 1 {
				// sub-fork detected, but that's probably already handled
				// TODO (low prio): check if the sub-fork really exists?
				break
			}

			if len(nextBlocks) == 0 {
				break
			}

			nextBlock = nextBlocks[0]

			if nextBlock.forkId == currentForkId {
				continue
			}

			nextBlock.forkId = currentForkId
			updatedBlocks = append(updatedBlocks, nextBlock.Root[:])
		}

		fork := cache.getForkById(currentForkId)
		if fork != nil && (fork.headBlock == nil || fork.headBlock.Slot < nextBlock.Slot) {
			fork.headBlock = nextBlock
		}
	}

	if fork1 != nil || fork2 != nil || len(updatedBlocks) > 0 {
		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			if fork1 != nil {
				err := db.InsertFork(fork1.toDbFork(), tx)
				if err != nil {
					return err
				}

				err = db.UpdateUnfinalizedBlockForkId(fork1Roots, uint64(fork1.forkId), tx)
				if err != nil {
					return err
				}

				cache.indexer.logger.Infof("fork %v created (base %v [%v], head %v [%v], updated blocks: %v)", fork1.forkId, fork1.baseSlot, fork1.baseRoot.String(), fork1.leafSlot, fork1.leafRoot.String(), len(fork1Roots))
			}

			if fork2 != nil {
				err := db.InsertFork(fork2.toDbFork(), tx)
				if err != nil {
					return err
				}

				err = db.UpdateUnfinalizedBlockForkId(fork2Roots, uint64(fork2.forkId), tx)
				if err != nil {
					return err
				}

				cache.indexer.logger.Infof("fork %v created (base %v [%v], head %v [%v], updated blocks: %v)", fork2.forkId, fork2.baseSlot, fork2.baseRoot.String(), fork2.leafSlot, fork2.leafRoot.String(), len(fork2Roots))
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
			return currentForkId, err
		}
	}

	return currentForkId, nil
}

// updateNewForkBlocks updates the fork blocks with the given fork. returns the roots of the updated blocks.
func (cache *forkCache) updateNewForkBlocks(fork *Fork, blocks []*Block, ignoreBlock *Block) [][]byte {
	updatedRoots := [][]byte{}

	for _, block := range blocks {
		if block.Slot <= fork.baseSlot {
			return updatedRoots
		}

		if block == ignoreBlock {
			continue
		}

		isInFork, _ := cache.indexer.blockCache.getCanonicalDistance(fork.leafRoot, block.Root, 0)
		if !isInFork {
			continue
		}

		block.forkId = fork.forkId
		updatedRoots = append(updatedRoots, block.Root[:])
	}

	return updatedRoots
}
