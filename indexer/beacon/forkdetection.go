package beacon

import (
	"fmt"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/db"
)

type newForkInfo struct {
	fork        *Fork
	updateRoots [][]byte
}

type updateForkInfo struct {
	baseRoot []byte
	parent   ForkKey
}

// processBlock processes a block and detects new forks if any.
// It persists the new forks to the database, sets the forkId of the supplied block
// and updates the forkId of all blocks affected by newly detected forks.
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

	// check if this block (c) introduces a new fork, it does so if:
	// 1. the parent (p) is known & processed and has 1 or more child blocks besides this one (c1, c2, ...)
	//  c  c1 c2
	//   \ | /
	//     p
	// 2. the current block (c) has 2 or more child blocks, multiple forks possible (c1, c2, ...)
	//  c1 c2 c3
	//   \ | /
	//     c

	newForks := []*newForkInfo{}
	updateForks := []*updateForkInfo{}
	currentForkId := parentForkId // default to parent fork id

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
				// parent (a) is not finalized and our new detected fork the first fork based on this parent (c)
				// we need to create another fork for the other chain that starts from our fork base (b1, b2, )
				// and update the blocks building on top of it
				//   b2
				//   |
				//   b1  c
				//   | /
				//   a

				if cache.getForkByLeaf(otherChildren[0].Root) != nil {
					cache.indexer.logger.Warnf("fork already exists for leaf %v [%v] (processing %v, scenario 1)", otherChildren[0].Slot, otherChildren[0].Root.String(), block.Slot)
				} else {
					cache.lastForkId++
					otherFork := newFork(cache.lastForkId, parentSlot, *parentRoot, otherChildren[0], parentForkId)
					cache.addFork(otherFork)

					updatedRoots, updatedFork := cache.updateForkBlocks(otherChildren[0], otherFork.forkId, false)
					newFork := &newForkInfo{
						fork:        otherFork,
						updateRoots: updatedRoots,
					}
					newForks = append(newForks, newFork)

					if updatedFork != nil {
						updateForks = append(updateForks, updatedFork)
					}

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
		// multiple blocks building on top of the current one, create a fork for each
		logbuf := strings.Builder{}
		for idx, child := range childBlocks {
			if cache.getForkByLeaf(child.Root) != nil {
				cache.indexer.logger.Warnf("fork already exists for leaf %v [%v] (processing %v, scenario 2)", child.Slot, child.Root.String(), block.Slot)
			} else {
				cache.lastForkId++
				fork := newFork(cache.lastForkId, block.Slot, block.Root, child, currentForkId)
				cache.addFork(fork)

				updatedRoots, updatedFork := cache.updateForkBlocks(child, fork.forkId, false)
				newFork := &newForkInfo{
					fork:        fork,
					updateRoots: updatedRoots,
				}
				newForks = append(newForks, newFork)

				if updatedFork != nil {
					updateForks = append(updateForks, updatedFork)
				}

				fmt.Fprintf(&logbuf, ", head%v: %v [%v, %v upd]", idx+1, newFork.fork.leafSlot, newFork.fork.leafRoot.String(), len(newFork.updateRoots))
			}
		}

		if logbuf.Len() > 0 {
			cache.indexer.logger.Infof("new child forks detected (base %v [%v]%v)", block.Slot, block.Root.String(), logbuf.String())
		}
	}

	// update fork ids of all blocks building on top of the current block
	updatedBlocks, updatedFork := cache.updateForkBlocks(block, currentForkId, true)
	if updatedFork != nil {
		updateForks = append(updateForks, updatedFork)
	}

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
			// helper function to update unfinalized block fork ids in batches
			updateUnfinalizedBlockForkIds := func(updateRoots [][]byte, forkId ForkKey) error {
				batchSize := 1000
				numBatches := (len(updateRoots) + batchSize - 1) / batchSize

				for i := 0; i < numBatches; i++ {
					start := i * batchSize
					end := (i + 1) * batchSize
					if end > len(updateRoots) {
						end = len(updateRoots)
					}

					batchRoots := updateRoots[start:end]

					err := db.UpdateUnfinalizedBlockForkId(batchRoots, uint64(forkId), tx)
					if err != nil {
						return err
					}
				}

				return nil
			}

			// add new forks
			for _, newFork := range newForks {
				err := db.InsertFork(newFork.fork.toDbFork(), tx)
				if err != nil {
					return err
				}

				if len(newFork.updateRoots) > 0 {
					err := updateUnfinalizedBlockForkIds(newFork.updateRoots, newFork.fork.forkId)
					if err != nil {
						return err
					}
				}
			}

			// update blocks building on top of current block
			if len(updatedBlocks) > 0 {
				err := updateUnfinalizedBlockForkIds(updatedBlocks, currentForkId)
				if err != nil {
					return err
				}

				cache.indexer.logger.Infof("updated %v blocks to fork %v", len(updatedBlocks), currentForkId)
			}

			// update parents of forks building on top of current blocks chain segment
			if len(updateForks) > 0 {
				for _, updatedFork := range updateForks {
					err := db.UpdateForkParent(updatedFork.baseRoot, uint64(updatedFork.parent), tx)
					if err != nil {
						return err
					}
				}

				cache.indexer.logger.Infof("updated %v fork parents", len(updateForks))
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
func (cache *forkCache) updateForkBlocks(startBlock *Block, forkId ForkKey, skipStartBlock bool) (blockRoots [][]byte, updatedFork *updateForkInfo) {
	blockRoots = [][]byte{}

	if !skipStartBlock {
		blockRoots = append(blockRoots, startBlock.Root[:])
	}

	for {
		nextBlocks := cache.indexer.blockCache.getBlocksByParentRoot(startBlock.Root)
		if len(nextBlocks) == 0 {
			break
		}

		if len(nextBlocks) > 1 {
			// potential fork ahead, check if the fork is already processed and has correct parent fork id
			if forks := cache.getForkByBase(startBlock.Root); len(forks) > 0 && forks[0].parentFork != forkId {
				for _, fork := range forks {
					fork.parentFork = forkId
				}

				updatedFork = &updateForkInfo{
					baseRoot: startBlock.Root[:],
					parent:   forkId,
				}
			}
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

	return
}
