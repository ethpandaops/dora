package beacon

import (
	"bytes"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
)

// blockCache is a cache for storing blocks.
type blockCache struct {
	indexer     *Indexer
	cacheMutex  sync.RWMutex
	highestSlot int64
	lowestSlot  int64
	slotMap     map[phase0.Slot][]*Block
	rootMap     map[phase0.Root]*Block
}

// newBlockCache creates a new instance of blockCache.
func newBlockCache(indexer *Indexer) *blockCache {
	return &blockCache{
		indexer: indexer,
		slotMap: map[phase0.Slot][]*Block{},
		rootMap: map[phase0.Root]*Block{},
	}
}

// createOrGetBlock creates a new block with the given root and slot, or returns an existing block if it already exists.
// It returns the created block and a boolean indicating whether the block was newly created or not.
func (cache *blockCache) createOrGetBlock(root phase0.Root, slot phase0.Slot) (*Block, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if cache.rootMap[root] != nil {
		return cache.rootMap[root], false
	}

	cacheBlock := newBlock(cache.indexer.dynSsz, root, slot)
	cache.rootMap[root] = cacheBlock

	if cache.slotMap[slot] == nil {
		cache.slotMap[slot] = []*Block{cacheBlock}
	} else {
		cache.slotMap[slot] = append(cache.slotMap[slot], cacheBlock)
	}

	if int64(slot) > cache.highestSlot {
		cache.highestSlot = int64(slot)
	}

	if cache.lowestSlot < 0 || int64(slot) < cache.lowestSlot {
		cache.lowestSlot = int64(slot)
	}

	return cacheBlock, true
}

// getBlockByRoot returns the cached block with the given root.
func (cache *blockCache) getBlockByRoot(root phase0.Root) *Block {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	return cache.rootMap[root]
}

// getPruningBlocks returns the blocks that can be pruned based on the given finalized slot.
func (cache *blockCache) getPruningBlocks(minInMemorySlot phase0.Slot) []*Block {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	blocks := []*Block{}
	for slot, slotBlocks := range cache.slotMap {
		if slot >= minInMemorySlot {
			continue
		}

		for _, block := range slotBlocks {
			if block.block == nil {
				continue
			}

			blocks = append(blocks, block)
		}
	}

	return blocks
}

// isCanonicalBlock checks if the block with the given blockRoot is a canonical block with respect to the block with the given head.
func (cache *blockCache) isCanonicalBlock(blockRoot phase0.Root, head phase0.Root) bool {
	res, _ := cache.getCanonicalDistance(blockRoot, head)
	return res
}

// getCanonicalDistance returns the canonical distance between the block with the given blockRoot and the block with the given head.
// It returns a boolean indicating whether the block with blockRoot is a canonical block, and the distance between the two blocks.
func (cache *blockCache) getCanonicalDistance(blockRoot phase0.Root, head phase0.Root) (bool, uint64) {
	block := cache.getBlockByRoot(blockRoot)
	if block == nil {
		return false, 0
	}

	canonicalBlock := cache.getBlockByRoot(head)
	if canonicalBlock == nil {
		return false, 0
	}

	var distance uint64 = 0
	if bytes.Equal(canonicalBlock.Root[:], blockRoot[:]) {
		return true, distance
	}

	for canonicalBlock != nil {
		if canonicalBlock.Slot < block.Slot {
			return false, 0
		}

		parentRoot := canonicalBlock.GetParentRoot()
		if parentRoot == nil {
			return false, 0
		}

		distance++
		if bytes.Equal(parentRoot[:], blockRoot[:]) {
			return true, distance
		}

		canonicalBlock = cache.getBlockByRoot(*parentRoot)
		if canonicalBlock == nil {
			return false, 0
		}
	}

	return false, 0
}

// getDependentBlock returns the dependent block of the given block based on the chain state.
func (cache *blockCache) getDependentBlock(chainState *consensus.ChainState, block *Block) *Block {
	if block.dependentRoot != nil {
		return cache.getBlockByRoot(*block.dependentRoot)
	}

	parentRoot := block.GetParentRoot()
	blockEpoch := chainState.EpochOfSlot(block.Slot)

	for {
		if parentRoot == nil {
			break
		}

		parentBlock := cache.getBlockByRoot(*parentRoot)
		if parentBlock == nil {
			blockHead := db.GetBlockHeadByRoot((*parentRoot)[:])
			if blockHead == nil {
				break
			}

			parentBlock = newBlock(cache.indexer.dynSsz, phase0.Root(blockHead.Root), phase0.Slot(blockHead.Slot))
			parentBlock.isInFinalizedDb = true
			parentRootVal := phase0.Root(blockHead.ParentRoot)
			parentBlock.parentRoot = &parentRootVal
		}

		if chainState.EpochOfSlot(parentBlock.Slot) < blockEpoch {
			block.dependentRoot = &parentBlock.Root
			return parentBlock
		}

		parentRoot = parentBlock.GetParentRoot()
	}

	return nil
}
