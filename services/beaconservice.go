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
	prepopulateEpochs := utils.Config.BeaconApi.PrepopulateEpochs
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

func (bs *BeaconService) GetLowestCachedSlot() int64 {
	return bs.indexer.GetLowestCachedSlot()
}

func (bs *BeaconService) GetCachedEpochStats(epoch uint64) *indexer.EpochStats {
	return bs.indexer.GetCachedEpochStats(epoch)
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
	idxMinSlot := bs.indexer.GetLowestCachedSlot()
	if idxMinSlot >= 0 && epoch >= utils.EpochOfSlot(uint64(idxMinSlot)) {
		epochStats := bs.indexer.GetCachedEpochStats(epoch)
		if epochStats != nil {
			return epochStats.Assignments, nil
		} else {
			return nil, nil
		}
	}

	return bs.rpcClient.GetEpochAssignments(epoch)
}

func (bs *BeaconService) GetProposerAssignments(firstEpoch uint64, lastEpoch uint64) (proposerAssignments map[uint64]uint64, synchronizedEpochs map[uint64]bool) {
	proposerAssignments = make(map[uint64]uint64)
	synchronizedEpochs = make(map[uint64]bool)

	idxMinSlot := bs.indexer.GetLowestCachedSlot()
	if idxMinSlot >= 0 {
		idxMinEpoch := utils.EpochOfSlot(uint64(idxMinSlot))
		idxHeadEpoch := bs.indexer.GetHeadSlot()
		if firstEpoch > idxHeadEpoch {
			firstEpoch = idxHeadEpoch
		}

		if firstEpoch >= uint64(idxMinEpoch) {
			var epoch uint64
			for epochIdx := int64(firstEpoch); epochIdx >= int64(idxMinEpoch) && epochIdx >= int64(lastEpoch); epochIdx-- {
				epoch = uint64(epochIdx)

				epochStats := bs.indexer.GetCachedEpochStats(epoch)
				if epochStats != nil && epochStats.Assignments != nil {
					synchronizedEpochs[epoch] = true
					for slot, vidx := range epochStats.Assignments.ProposerAssignments {
						proposerAssignments[slot] = vidx
					}
				}
			}
			if epoch <= lastEpoch {
				return
			}
			firstEpoch = epoch
		}
	}

	// load from db
	firstSlot := (firstEpoch * utils.Config.Chain.Config.SlotsPerEpoch) + (utils.Config.Chain.Config.SlotsPerEpoch - 1)
	lastSlot := lastEpoch * utils.Config.Chain.Config.SlotsPerEpoch
	dbSlotAssignments := db.GetSlotAssignmentsForSlots(firstSlot, lastSlot)
	for _, dbSlotAssignment := range dbSlotAssignments {
		synchronizedEpochs[utils.EpochOfSlot(dbSlotAssignment.Slot)] = true
		proposerAssignments[dbSlotAssignment.Slot] = dbSlotAssignment.Proposer
	}
	return
}

func (bs *BeaconService) GetDbEpochs(firstEpoch uint64, limit uint32) []*dbtypes.Epoch {
	resEpochs := make([]*dbtypes.Epoch, limit)
	resIdx := 0

	dbEpochs := db.GetEpochs(firstEpoch, limit)
	dbIdx := 0
	dbCnt := len(dbEpochs)

	idxMinSlot := bs.indexer.GetLowestCachedSlot()
	var idxMinEpoch, idxHeadEpoch uint64
	if idxMinSlot >= 0 {
		idxMinEpoch = utils.EpochOfSlot(uint64(idxMinSlot))
		idxHeadEpoch = utils.EpochOfSlot(bs.indexer.GetHeadSlot())
	}

	lastEpoch := firstEpoch - uint64(limit)
	if lastEpoch < 0 {
		lastEpoch = 0
	}
	for epochIdx := int64(firstEpoch); epochIdx >= int64(lastEpoch) && resIdx < int(limit); epochIdx-- {
		epoch := uint64(epochIdx)
		var resEpoch *dbtypes.Epoch
		if dbIdx < dbCnt && dbEpochs[dbIdx].Epoch == epoch {
			resEpoch = dbEpochs[dbIdx]
			dbIdx++
		}
		if idxMinSlot >= 0 && epoch >= idxMinEpoch && epoch <= idxHeadEpoch {
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
	if idxMinSlot >= 0 && firstSlot >= uint64(idxMinSlot) {
		for slotIdx := int64(slot); slotIdx >= idxMinSlot && resIdx < int(limit); slotIdx-- {
			slot = uint64(slotIdx)
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

func (bs *BeaconService) GetDbBlocksForSlots(firstSlot uint64, slotLimit uint32, withOrphaned bool) []*dbtypes.Block {
	resBlocks := make([]*dbtypes.Block, 0)

	idxMinSlot := bs.indexer.GetLowestCachedSlot()
	idxHeadSlot := bs.indexer.GetHeadSlot()
	if firstSlot > idxHeadSlot {
		firstSlot = idxHeadSlot
	}
	var lastSlot uint64
	if firstSlot > uint64(slotLimit) {
		lastSlot = firstSlot - uint64(slotLimit)
	} else {
		lastSlot = 0
	}

	slot := firstSlot
	if idxMinSlot >= 0 && firstSlot >= uint64(idxMinSlot) {
		for slotIdx := int64(slot); slotIdx >= int64(idxMinSlot) && slotIdx >= int64(lastSlot); slotIdx-- {
			slot = uint64(slotIdx)
			blocks := bs.indexer.GetCachedBlocks(slot)
			if blocks != nil {
				for bidx := 0; bidx < len(blocks); bidx++ {
					block := blocks[bidx]
					if block.Orphaned && !withOrphaned {
						continue
					}
					dbBlock := bs.indexer.BuildLiveBlock(block)
					if dbBlock != nil {
						resBlocks = append(resBlocks, dbBlock)
					}
				}
			}
		}
	}

	if slot > lastSlot {
		dbBlocks := db.GetBlocksForSlots(slot, lastSlot, withOrphaned)
		if dbBlocks != nil {
			for idx := 0; idx < len(dbBlocks); idx++ {
				resBlocks = append(resBlocks, dbBlocks[idx])
			}
		}
	}

	return resBlocks
}
