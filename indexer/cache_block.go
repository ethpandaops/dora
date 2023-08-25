package indexer

import (
	"encoding/json"
	"sync"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
)

type CacheBlock struct {
	Root   []byte
	Slot   uint64
	mutex  sync.RWMutex
	seenBy uint64
	isInDb bool
	header *rpctypes.SignedBeaconBlockHeader
	block  *rpctypes.SignedBeaconBlock

	dbBlockMutex sync.Mutex
	dbBlockCache *dbtypes.Block
}

func (cache *indexerCache) getCachedBlock(root []byte) *CacheBlock {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()
	return cache.rootMap[string(root)]
}

func (cache *indexerCache) createOrGetCachedBlock(root []byte, slot uint64) (*CacheBlock, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	rootKey := string(root)
	if cache.rootMap[rootKey] != nil {
		return cache.rootMap[rootKey], false
	}
	cacheBlock := &CacheBlock{
		Root: root,
		Slot: slot,
	}
	cache.rootMap[rootKey] = cacheBlock
	if cache.slotMap[slot] == nil {
		cache.slotMap[slot] = []*CacheBlock{cacheBlock}
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

func (cache *indexerCache) removeCachedBlock(cachedBlock *CacheBlock) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()
	logger.Debugf("Remove cached block: %v (0x%x)", cachedBlock.Slot, cachedBlock.Root)

	rootKey := string(cachedBlock.Root)
	delete(cache.rootMap, rootKey)

	slotBlocks := cache.slotMap[cachedBlock.Slot]
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
				delete(cache.slotMap, cachedBlock.Slot)
			} else {
				if idx < len-1 {
					cache.slotMap[cachedBlock.Slot][idx] = cache.slotMap[cachedBlock.Slot][len-1]
				}
				cache.slotMap[cachedBlock.Slot] = cache.slotMap[cachedBlock.Slot][0 : len-1]
			}
		}
	}
}

func (block *CacheBlock) buildOrphanedBlock() *dbtypes.OrphanedBlock {
	headerJson, err := json.Marshal(block.header)
	if err != nil {
		return nil
	}
	blockJson, err := json.Marshal(block.GetBlockBody())
	if err != nil {
		return nil
	}
	return &dbtypes.OrphanedBlock{
		Root:   block.Root,
		Header: string(headerJson),
		Block:  string(blockJson),
	}
}

func (block *CacheBlock) GetParentRoot() []byte {
	block.mutex.RLock()
	defer block.mutex.RUnlock()
	if block.header != nil {
		return block.header.Message.ParentRoot
	}
	return nil
}

func (block *CacheBlock) GetHeader() *rpctypes.SignedBeaconBlockHeader {
	block.mutex.RLock()
	defer block.mutex.RUnlock()
	return block.header
}

func (block *CacheBlock) GetBlockBody() *rpctypes.SignedBeaconBlock {
	block.mutex.RLock()
	defer block.mutex.RUnlock()
	if block.block != nil {
		return block.block
	}
	if !block.isInDb {
		return nil
	}

	logger.Debugf("loading unfinalized block body from db: %v", block.Slot)
	blockData := db.GetUnfinalizedBlock(block.Root)
	var blockBody rpctypes.SignedBeaconBlock
	err := json.Unmarshal([]byte(blockData.Block), &blockBody)
	if err != nil {
		logger.Warnf("error parsing unfinalized block body from db: %v", err)
		return nil
	}
	block.block = &blockBody

	return block.block
}

func (block *CacheBlock) IsCanonical(indexer *Indexer, head []byte) bool {
	if head == nil {
		_, head = indexer.GetCanonicalHead()
	}
	return indexer.indexerCache.isCanonicalBlock(block.Root, head)
}

func (block *CacheBlock) IsReady() bool {
	return block.header != nil && (block.block != nil || block.isInDb)
}
