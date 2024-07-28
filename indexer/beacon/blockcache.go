package beacon

import (
	"bytes"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type blockCache struct {
	cacheMutex  sync.RWMutex
	highestSlot int64
	lowestSlot  int64
	slotMap     map[phase0.Slot][]*Block
	rootMap     map[phase0.Root]*Block
}

func newBlockCache() *blockCache {
	return &blockCache{
		slotMap: map[phase0.Slot][]*Block{},
		rootMap: map[phase0.Root]*Block{},
	}
}

func (cache *blockCache) createOrGetBlock(root phase0.Root, slot phase0.Slot) (*Block, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if cache.rootMap[root] != nil {
		return cache.rootMap[root], false
	}

	cacheBlock := newBlock(root, slot)
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

func (cache *blockCache) getBlockByRoot(root phase0.Root) *Block {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	return cache.rootMap[root]
}

func (cache *blockCache) isCanonicalBlock(blockRoot phase0.Root, head phase0.Root) bool {
	res, _ := cache.getCanonicalDistance(blockRoot, head)
	return res
}

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
