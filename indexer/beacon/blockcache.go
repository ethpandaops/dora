package beacon

import (
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
