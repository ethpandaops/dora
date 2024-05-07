package indexer

import (
	"sync"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
)

type CacheBlock struct {
	Root              []byte
	Slot              uint64
	mutex             sync.RWMutex
	seenMap           map[uint16]bool
	isInUnfinalizedDb bool
	isInFinalizedDb   bool
	header            *phase0.SignedBeaconBlockHeader
	block             *spec.VersionedSignedBeaconBlock
	Refs              struct {
		ExecutionHash   []byte
		ExecutionNumber uint64
	}

	dbBlockMutex sync.Mutex
	dbBlockCache *dbtypes.Slot
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
		Root:    root,
		Slot:    slot,
		seenMap: make(map[uint16]bool),
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
	headerSSZ, err := block.header.MarshalSSZ()
	if err != nil {
		logger.Debugf("marshal header ssz failed: %v", err)
		return nil
	}
	blockVer, blockSSZ, err := MarshalVersionedSignedBeaconBlockSSZ(block.GetBlockBody())
	if err != nil {
		logger.Debugf("marshal block ssz failed: %v", err)
		return nil
	}
	return &dbtypes.OrphanedBlock{
		Root:      block.Root,
		HeaderVer: 1,
		HeaderSSZ: headerSSZ,
		BlockVer:  blockVer,
		BlockSSZ:  blockSSZ,
	}
}

func (block *CacheBlock) parseBlockRefs() {
	if block.block == nil {
		return
	}
	blockHash, err := block.block.ExecutionBlockHash()
	if err == nil {
		block.Refs.ExecutionHash = blockHash[:]
	}
	blockNum, err := block.block.ExecutionBlockNumber()
	if err == nil {
		block.Refs.ExecutionNumber = blockNum
	}
}

func (block *CacheBlock) GetParentRoot() []byte {
	block.mutex.RLock()
	defer block.mutex.RUnlock()
	if block.header != nil {
		return block.header.Message.ParentRoot[:]
	}
	return nil
}

func (block *CacheBlock) GetHeader() *phase0.SignedBeaconBlockHeader {
	block.mutex.RLock()
	defer block.mutex.RUnlock()
	return block.header
}

func (block *CacheBlock) GetBlockBody() *spec.VersionedSignedBeaconBlock {
	block.mutex.RLock()
	defer block.mutex.RUnlock()
	if block.block != nil {
		return block.block
	}
	if !block.isInUnfinalizedDb {
		return nil
	}

	logger.Debugf("loading unfinalized block body from db: %v", block.Slot)
	blockData := db.GetUnfinalizedBlock(block.Root)

	blockBody, err := UnmarshalVersionedSignedBeaconBlockSSZ(blockData.BlockVer, blockData.BlockSSZ)
	if err != nil {
		logger.Warnf("error parsing unfinalized block body from db: %v", err)
		return nil
	}
	block.block = blockBody
	block.parseBlockRefs()

	return block.block
}

func (block *CacheBlock) IsCanonical(indexer *Indexer, head []byte) bool {
	if head == nil {
		_, head = indexer.GetCanonicalHead()
	}
	return indexer.indexerCache.isCanonicalBlock(block.Root, head)
}

func (block *CacheBlock) IsReady() bool {
	return block.header != nil && (block.block != nil || block.isInUnfinalizedDb)
}
