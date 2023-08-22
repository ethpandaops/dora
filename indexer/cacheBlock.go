package indexer

import (
	"encoding/json"
	"sync"

	"github.com/pk910/light-beaconchain-explorer/dbtypes"
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

func (cache *indexerCache) removeCachedBlock(cachedBlock *indexerCacheBlock) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	logger.Debugf("Remove cached block: %v (0x%x)", cachedBlock.slot, cachedBlock.root)

	rootKey := string(cachedBlock.root)
	delete(cache.rootMap, rootKey)

	slotBlocks := cache.slotMap[cachedBlock.slot]
	if slotBlocks != nil {
		var idx uint64
		len := uint64(len(slotBlocks))
		for idx = 0; idx < len; idx++ {
			if slotBlocks[idx] == cachedBlock {
				break
			}
		}
		if idx < len {
			if len == 1 {
				delete(cache.slotMap, cachedBlock.slot)
			} else {
				if idx < len-1 {
					cache.slotMap[cachedBlock.slot][idx] = cache.slotMap[cachedBlock.slot][len-1]
				}
				cache.slotMap[cachedBlock.slot] = cache.slotMap[cachedBlock.slot][0 : len-1]
			}
		}
	}
}

func (cache *indexerCache) resetLowestSlot() {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	var lowestSlot int64 = -1
	for slot := range cache.slotMap {
		if lowestSlot == -1 || int64(slot) < lowestSlot {
			lowestSlot = int64(slot)
		}
	}
	if lowestSlot != cache.lowestSlot {
		logger.Debugf("Reset lowest cached slot: %v", lowestSlot)
		cache.lowestSlot = lowestSlot
	}
}

func (block *indexerCacheBlock) buildOrphanedBlock() *dbtypes.OrphanedBlock {
	headerJson, err := json.Marshal(block.header)
	if err != nil {
		return nil
	}
	blockJson, err := json.Marshal(block.getBlockBody())
	if err != nil {
		return nil
	}
	return &dbtypes.OrphanedBlock{
		Root:   block.root,
		Header: string(headerJson),
		Block:  string(blockJson),
	}
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
