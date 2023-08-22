package indexer

import (
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
)

type BlockInfo struct {
	Root        []byte
	Header      *rpctypes.SignedBeaconBlockHeader
	Orphaned    bool
	cachedBlock *indexerCacheBlock
}

func (cache *indexerCache) getBlockInfoFromCachedBlock(cachedBlock *indexerCacheBlock, headRoot []byte) *BlockInfo {
	if headRoot == nil {
		_, headRoot = cache.indexer.GetCanonicalHead()
	}
	orphaned := !cache.isCanonicalBlock(cachedBlock.root, headRoot)

	cachedBlock.mutex.RLock()
	defer cachedBlock.mutex.RUnlock()
	return &BlockInfo{
		Root:        cachedBlock.root,
		Header:      cachedBlock.header,
		Orphaned:    orphaned,
		cachedBlock: cachedBlock,
	}
}

func (block *BlockInfo) GetBlockBody() *rpctypes.SignedBeaconBlock {
	return block.cachedBlock.getBlockBody()
}
