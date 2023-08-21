package indexer

import (
	"sync"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
)

type indexerCacheBlock struct {
	root   []byte
	slot   uint64
	mutex  sync.RWMutex
	seenBy uint64
	isInDb bool
	header *rpctypes.SignedBeaconBlockHeader
	block  *rpctypes.SignedBeaconBlock
}

func (cache *indexerCache) getCachedBlock(root []byte) *indexerCacheBlock {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()
	rootKey := string(root)
	cacheBlock := cache.rootMap[rootKey]
	return cacheBlock
}

func (cache *indexerCache) createOrGetCachedBlock(root []byte, slot uint64) (*indexerCacheBlock, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	rootKey := string(root)
	if cache.rootMap[rootKey] != nil {
		return cache.rootMap[rootKey], false
	}
	cacheBlock := &indexerCacheBlock{
		root: root,
		slot: slot,
	}
	cache.rootMap[rootKey] = cacheBlock
	if cache.slotMap[slot] == nil {
		cache.slotMap[slot] = []*indexerCacheBlock{cacheBlock}
	} else {
		cache.slotMap[slot] = append(cache.slotMap[slot], cacheBlock)
	}
	if int64(slot) > cache.highestSlot {
		cache.highestSlot = int64(slot)
	}
	if int64(slot) > cache.lowestSlot {
		cache.lowestSlot = int64(slot)
	}
	return cacheBlock, true
}

func (block *indexerCacheBlock) getParentRoot() []byte {
	block.mutex.RLock()
	defer block.mutex.RUnlock()
	if block.header != nil {
		return block.header.Message.ParentRoot
	}
	return nil
}

func (block *indexerCacheBlock) getBlockBody() *rpctypes.SignedBeaconBlock {
	block.mutex.RLock()
	defer block.mutex.RUnlock()
	if block.block != nil {
		return block.block
	}
	if !block.isInDb {
		return nil
	}
	// TODO: load from DB
	return nil
}
