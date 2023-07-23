package services

import (
	"fmt"

	"github.com/pk910/light-beaconchain-explorer/cache"
	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/indexer"
	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type BeaconService struct {
	rpcClient   *rpc.BeaconClient
	tieredCache *cache.TieredCache
	indexer     *indexer.Indexer
}

var GlobalBeaconService *BeaconService

// StartBeaconService is used to start the global beaconchain service
func StartBeaconService() error {
	if GlobalBeaconService != nil {
		return nil
	}

	rpcClient, err := rpc.NewBeaconClient(utils.Config.BeaconApi.Endpoint, utils.Config.BeaconApi.AssignmentsCacheSize)
	if err != nil {
		return err
	}

	cachePrefix := fmt.Sprintf("%srpc-", utils.Config.BeaconApi.RedisCachePrefix)
	tieredCache, err := cache.NewTieredCache(utils.Config.BeaconApi.LocalCacheSize, utils.Config.BeaconApi.RedisCacheAddr, cachePrefix)
	if err != nil {
		return err
	}

	inMemoryEpochs := utils.Config.BeaconApi.InMemoryEpochs
	if inMemoryEpochs < 2 {
		inMemoryEpochs = 2
	}
	epochProcessingDelay := utils.Config.BeaconApi.EpochProcessingDelay
	if epochProcessingDelay < 2 {
		epochProcessingDelay = 2
	} else if epochProcessingDelay > inMemoryEpochs {
		inMemoryEpochs = epochProcessingDelay
	}
	prepopulateEpochs := utils.Config.BeaconApi.InMemoryEpochs
	if prepopulateEpochs > inMemoryEpochs {
		prepopulateEpochs = inMemoryEpochs
	}
	indexer, err := indexer.NewIndexer(rpcClient, prepopulateEpochs, inMemoryEpochs, epochProcessingDelay, !utils.Config.BeaconApi.DisableIndexWriter)
	if err != nil {
		return err
	}
	err = indexer.Start()
	if err != nil {
		return err
	}

	GlobalBeaconService = &BeaconService{
		rpcClient:   rpcClient,
		tieredCache: tieredCache,
		indexer:     indexer,
	}
	return nil
}

func (bs *BeaconService) GetFinalizedBlockHead() (*rpctypes.StandardV1BeaconHeaderResponse, error) {
	return bs.rpcClient.GetFinalizedBlockHead()
}

func (bs *BeaconService) GetSlotDetailsByBlockroot(blockroot []byte) (*rpctypes.CombinedBlockResponse, error) {
	header, err := bs.rpcClient.GetBlockHeaderByBlockroot(blockroot)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, nil
	}
	block, err := bs.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return nil, err
	}
	return &rpctypes.CombinedBlockResponse{
		Header: header,
		Block:  block,
	}, nil
}

func (bs *BeaconService) GetSlotDetailsBySlot(slot uint64) (*rpctypes.CombinedBlockResponse, error) {
	header, err := bs.rpcClient.GetBlockHeaderBySlot(slot)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, nil
	}
	block, err := bs.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return nil, err
	}
	return &rpctypes.CombinedBlockResponse{
		Header: header,
		Block:  block,
	}, nil
}

func (bs *BeaconService) GetEpochAssignments(epoch uint64) (*rpctypes.EpochAssignments, error) {
	lowestIndexerEpoch := utils.EpochOfSlot(bs.indexer.GetLowestCachedSlot())
	if epoch >= lowestIndexerEpoch {
		epochStats := bs.indexer.GetCachedEpochStats(epoch)
		if epochStats != nil {
			return epochStats.Assignments, nil
		} else {
			return nil, nil
		}
	}

	return bs.rpcClient.GetEpochAssignments(epoch)
}

func (bs *BeaconService) GetDbEpochs(firstEpoch uint64, limit uint32) []*dbtypes.Epoch {
	resEpochs := make([]*dbtypes.Epoch, limit)
	resIdx := 0

	dbEpochs := db.GetEpochs(firstEpoch, limit)
	dbIdx := 0
	dbCnt := len(dbEpochs)

	idxMinEpoch := utils.EpochOfSlot(bs.indexer.GetLowestCachedSlot())
	idxHeadEpoch := utils.EpochOfSlot(bs.indexer.GetHeadSlot())

	lastEpoch := firstEpoch - uint64(limit)
	if lastEpoch < 0 {
		lastEpoch = 0
	}
	for epoch := firstEpoch; epoch >= lastEpoch && resIdx < int(limit); epoch-- {
		var resEpoch *dbtypes.Epoch
		if dbIdx < dbCnt && dbEpochs[dbIdx].Epoch == epoch {
			resEpoch = dbEpochs[dbIdx]
			dbIdx++
		}
		if epoch >= idxMinEpoch && epoch <= idxHeadEpoch {
			resEpoch = bs.indexer.BuildLiveEpoch(epoch)
		}
		if resEpoch != nil {
			resEpochs[resIdx] = resEpoch
			resIdx++
		}
	}

	return resEpochs
}

func (bs *BeaconService) GetDbBlocks(firstSlot uint64, limit int32, withOrphaned bool) []*dbtypes.Block {
	resBlocks := make([]*dbtypes.Block, limit)
	resIdx := 0

	idxMinSlot := bs.indexer.GetLowestCachedSlot()
	idxHeadSlot := bs.indexer.GetHeadSlot()
	if firstSlot > idxHeadSlot {
		firstSlot = idxHeadSlot
	}

	slot := firstSlot
	if firstSlot >= idxMinSlot {
		for ; slot >= idxMinSlot && resIdx < int(limit); slot-- {
			blocks := bs.indexer.GetCachedBlocks(slot)
			if blocks != nil {
				for bidx := 0; bidx < len(blocks) && resIdx < int(limit); bidx++ {
					block := blocks[bidx]
					if block.Orphaned && !withOrphaned {
						continue
					}
					dbBlock := bs.indexer.BuildLiveBlock(block)
					if dbBlock != nil {
						resBlocks[resIdx] = dbBlock
						resIdx++
					}
				}
			}
		}
	}

	if resIdx < int(limit) {
		dbBlocks := db.GetBlocks(slot, uint32(limit-int32(resIdx)), withOrphaned)
		if dbBlocks != nil {
			for idx := 0; idx < len(dbBlocks) && resIdx < int(limit); idx++ {
				resBlocks[resIdx] = dbBlocks[idx]
				resIdx++
			}
		}
	}

	return resBlocks
}
