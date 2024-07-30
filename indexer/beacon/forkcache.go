package beacon

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/jmoiron/sqlx"
)

type forkCache struct {
	indexer    *Indexer
	cacheMutex sync.RWMutex
	forkMap    map[ForkKey]*Fork

	forkProcessLock sync.Mutex
}

func newForkCache(indexer *Indexer) *forkCache {
	return &forkCache{
		indexer: indexer,
		forkMap: make(map[ForkKey]*Fork),
	}
}

func (cache *forkCache) getForkById(forkId ForkKey) *Fork {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	return cache.forkMap[forkId]
}

func (cache *forkCache) addFork(fork *Fork) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.forkMap[fork.forkId] = fork
}

func (cache *forkCache) getClosestFork(block *Block) *Fork {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	var closestFork *Fork
	var closestDistance uint64

	for _, fork := range cache.forkMap {
		isInFork, distance := cache.indexer.blockCache.getCanonicalDistance(fork.leafRoot, block.Root, closestDistance)
		if !isInFork {
			continue
		}

		if closestFork == nil || distance < closestDistance {
			closestFork = fork
			closestDistance = distance
		}
	}

	return closestFork
}

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

func (cache *forkCache) processBlock(block *Block) (ForkKey, error) {
	cache.forkProcessLock.Lock()
	defer cache.forkProcessLock.Unlock()

	parentFork := cache.getClosestFork(block)

	var parentForkId ForkKey
	if parentFork != nil {
		parentForkId = parentFork.forkId
	}

	forkBlocks := cache.indexer.blockCache.getForkBlocks(parentForkId)
	sort.Slice(forkBlocks, func(i, j int) bool {
		return forkBlocks[i].Slot > forkBlocks[j].Slot
	})

	parentsMap := map[phase0.Root]bool{}
	for _, forkBlock := range forkBlocks {
		if forkBlock == block || parentsMap[forkBlock.Root] {
			continue
		}

		baseBlock, distance1, leaf1, distance2, leaf2 := cache.checkForkDistance(block, forkBlock, parentsMap)
		if baseBlock != nil && distance1 > uint64(cache.indexer.minForkDistance) && distance2 > uint64(cache.indexer.minForkDistance) {
			// new fork detected
			var fork1, fork2 *Fork
			var fork1Roots, fork2Roots [][]byte

			if leaf1 != nil {
				fork1 = newFork(baseBlock, leaf1, parentFork)
				cache.addFork(fork1)
				fork1Roots = cache.updateNewForkBlocks(fork1, forkBlocks)
			}

			if leaf2 != nil {
				fork2 = newFork(baseBlock, leaf2, parentFork)
				cache.addFork(fork2)
				fork2Roots = cache.updateNewForkBlocks(fork2, forkBlocks)
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

			err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
				err := db.InsertFork(fork1.toDbFork(), tx)
				if err != nil {
					return err
				}

				err = db.UpdateUnfinalizedBlockForkId(fork1Roots, uint64(fork1.forkId), tx)
				if err != nil {
					return err
				}

				err = db.InsertFork(fork2.toDbFork(), tx)
				if err != nil {
					return err
				}

				err = db.UpdateUnfinalizedBlockForkId(fork2Roots, uint64(fork2.forkId), tx)
				if err != nil {
					return err
				}

				return nil
			})

			return fork1.forkId, err
		}
	}

	return parentForkId, nil
}

func (cache *forkCache) updateNewForkBlocks(fork *Fork, blocks []*Block) [][]byte {
	updatedRoots := [][]byte{}

	for _, block := range blocks {
		if block.Slot <= fork.baseSlot {
			return nil
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
