package indexer

import (
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
)

type BlockInfo struct {
	Root        []byte
	Header      *rpctypes.SignedBeaconBlockHeader
	cachedBlock *indexerCacheBlock
}

func getBlockInfoFromCachedBlock(cachedBlock *indexerCacheBlock) *BlockInfo {
	cachedBlock.mutex.RLock()
	defer cachedBlock.mutex.RUnlock()
	return &BlockInfo{
		Root:        cachedBlock.root,
		Header:      cachedBlock.header,
		cachedBlock: cachedBlock,
	}
}

func (block *BlockInfo) GetBlockBody() *rpctypes.SignedBeaconBlock {
	return block.cachedBlock.getBlockBody()
}

func (block *BlockInfo) GetOrphaned() bool {
	return false
}
