package services

import (
	"strings"
	"sync"
	"time"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/indexer"
	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type BeaconService struct {
	rpcClient      *rpc.BeaconClient
	indexer        *indexer.Indexer
	validatorNames *ValidatorNames

	validatorActivityMutex sync.Mutex
	validatorActivityStats struct {
		cacheEpoch uint64
		epochLimit uint64
		activity   map[uint64]uint8
	}
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

	indexer, err := indexer.NewIndexer(rpcClient)
	if err != nil {
		return err
	}
	err = indexer.Start()
	if err != nil {
		return err
	}

	validatorNames := &ValidatorNames{}
	if utils.Config.Frontend.ValidatorNamesYaml != "" {
		validatorNames.LoadFromYaml(utils.Config.Frontend.ValidatorNamesYaml)
	}
	if utils.Config.Frontend.ValidatorNamesInventory != "" {
		validatorNames.LoadFromRangesApi(utils.Config.Frontend.ValidatorNamesInventory)
	}

	GlobalBeaconService = &BeaconService{
		rpcClient:      rpcClient,
		indexer:        indexer,
		validatorNames: validatorNames,
	}
	return nil
}

func (bs *BeaconService) GetValidatorName(index uint64) string {
	return bs.validatorNames.GetValidatorName(index)
}

func (bs *BeaconService) GetCachedValidatorSet() *rpctypes.StandardV1StateValidatorsResponse {
	return bs.indexer.GetCachedValidatorSet()
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

func (bs *BeaconService) GetGenesis() (*rpctypes.StandardV1GenesisResponse, error) {
	return bs.rpcClient.GetGenesis()
}

func (bs *BeaconService) GetSlotDetailsByBlockroot(blockroot []byte, withBlobs bool) (*rpctypes.CombinedBlockResponse, error) {
	var result *rpctypes.CombinedBlockResponse
	if blockInfo := bs.indexer.GetCachedBlock(blockroot); blockInfo != nil {
		result = &rpctypes.CombinedBlockResponse{
			Header:   blockInfo.Header,
			Block:    blockInfo.Block,
			Orphaned: blockInfo.Orphaned,
		}
	} else {
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
		result = &rpctypes.CombinedBlockResponse{
			Header:   header,
			Block:    block,
			Orphaned: !header.Data.Canonical,
		}
	}

	if result.Block.Data.Message.Body.BlobKzgCommitments != nil && withBlobs && utils.EpochOfSlot(uint64(result.Header.Data.Header.Message.Slot)) >= utils.Config.Chain.Config.DenebForkEpoch {
		blobs, _ := bs.rpcClient.GetBlobSidecarsByBlockroot(result.Header.Data.Root)
		if blobs != nil {
			result.Blobs = blobs
		}
	}
	return result, nil
}

func (bs *BeaconService) GetSlotDetailsBySlot(slot uint64, withBlobs bool) (*rpctypes.CombinedBlockResponse, error) {
	var result *rpctypes.CombinedBlockResponse
	if cachedBlocks := bs.indexer.GetCachedBlocks(slot); cachedBlocks != nil {
		var cachedBlock *indexer.BlockInfo
		for _, block := range cachedBlocks {
			if !block.Orphaned {
				cachedBlock = block
				break
			}
		}
		if cachedBlock == nil {
			cachedBlock = cachedBlocks[0]
		}
		result = &rpctypes.CombinedBlockResponse{
			Header:   cachedBlock.Header,
			Block:    cachedBlock.Block,
			Orphaned: cachedBlock.Orphaned,
		}
	} else {
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
		result = &rpctypes.CombinedBlockResponse{
			Header:   header,
			Block:    block,
			Orphaned: !header.Data.Canonical,
		}
	}

	if result.Block.Data.Message.Body.BlobKzgCommitments != nil && withBlobs && utils.EpochOfSlot(uint64(result.Header.Data.Header.Message.Slot)) >= utils.Config.Chain.Config.DenebForkEpoch {
		blobs, _ := bs.rpcClient.GetBlobSidecarsByBlockroot(result.Header.Data.Root)
		if blobs != nil {
			result.Blobs = blobs
		}
	}
	return result, nil
}

func (bs *BeaconService) GetBlobSidecarsByBlockRoot(blockroot []byte) (*rpctypes.StandardV1BlobSidecarsResponse, error) {
	return bs.rpcClient.GetBlobSidecarsByBlockroot(blockroot)
}

func (bs *BeaconService) GetOrphanedBlock(blockroot []byte) *rpctypes.CombinedBlockResponse {
	orphanedBlock := db.GetOrphanedBlock(blockroot)
	if orphanedBlock == nil {
		return nil
	}
	blockInfo := indexer.ParseOrphanedBlock(orphanedBlock)
	if blockInfo == nil {
		return nil
	}

	return &rpctypes.CombinedBlockResponse{
		Header:   blockInfo.Header,
		Block:    blockInfo.Block,
		Blobs:    nil,
		Orphaned: true,
	}
}

func (bs *BeaconService) GetCachedBlockByBlockroot(blockroot []byte) *rpctypes.CombinedBlockResponse {
	blockInfo := bs.indexer.GetCachedBlock(blockroot)
	if blockInfo == nil {
		return nil
	}
	return &rpctypes.CombinedBlockResponse{
		Header:   blockInfo.Header,
		Block:    blockInfo.Block,
		Orphaned: blockInfo.Orphaned,
	}
}

func (bs *BeaconService) GetCachedBlockByStateroot(stateroot []byte) *rpctypes.CombinedBlockResponse {
	blockInfo := bs.indexer.GetCachedBlockByStateroot(stateroot)
	if blockInfo == nil {
		return nil
	}
	return &rpctypes.CombinedBlockResponse{
		Header:   blockInfo.Header,
		Block:    blockInfo.Block,
		Orphaned: blockInfo.Orphaned,
	}
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

	lastEpoch := int64(firstEpoch) - int64(limit)
	if lastEpoch < 0 {
		lastEpoch = 0
	}
	for epochIdx := int64(firstEpoch); epochIdx >= lastEpoch && resIdx < int(limit); epochIdx-- {
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
		slot--
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
		slot--
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

func (bs *BeaconService) GetDbBlocksByGraffiti(graffiti string, pageIdx uint64, pageSize uint32, withOrphaned bool) []*dbtypes.Block {
	cachedMatches := make([]*indexer.BlockInfo, 0)
	idxMinSlot := bs.indexer.GetLowestCachedSlot()
	idxHeadSlot := bs.indexer.GetHeadSlot()
	if idxMinSlot >= 0 {
		for slotIdx := int64(idxHeadSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
			slot := uint64(slotIdx)
			blocks := bs.indexer.GetCachedBlocks(slot)
			if blocks != nil {
				for bidx := 0; bidx < len(blocks); bidx++ {
					block := blocks[bidx]
					if block.Orphaned && !withOrphaned {
						continue
					}
					blockGraffiti := string(block.Block.Data.Message.Body.Graffiti)
					if !strings.Contains(blockGraffiti, graffiti) {
						continue
					}
					cachedMatches = append(cachedMatches, block)
				}
			}
		}
	}

	cachedMatchesLen := uint64(len(cachedMatches))
	cachedPages := cachedMatchesLen / uint64(pageSize)
	resBlocks := make([]*dbtypes.Block, 0)
	resIdx := 0

	cachedStart := pageIdx * uint64(pageSize)
	cachedEnd := cachedStart + uint64(pageSize)
	if cachedEnd+1 < cachedMatchesLen {
		cachedEnd++
	}

	if cachedPages > 0 && pageIdx < cachedPages {
		for _, block := range cachedMatches[cachedStart:cachedEnd] {
			resBlocks = append(resBlocks, bs.indexer.BuildLiveBlock(block))
			resIdx++
		}
	} else if pageIdx == cachedPages {
		start := pageIdx * uint64(pageSize)
		for _, block := range cachedMatches[start:] {
			resBlocks = append(resBlocks, bs.indexer.BuildLiveBlock(block))
			resIdx++
		}
	}
	if resIdx > int(pageSize) {
		return resBlocks
	}

	// load from db
	var dbMinSlot uint64
	if idxMinSlot < 0 {
		dbMinSlot = utils.TimeToSlot(uint64(time.Now().Unix()))
	} else {
		dbMinSlot = uint64(idxMinSlot)
	}

	dbPage := pageIdx - cachedPages
	dbCacheOffset := uint64(pageSize) - (cachedMatchesLen % uint64(pageSize))
	var dbBlocks []*dbtypes.Block
	if dbPage == 0 {
		dbBlocks = db.GetBlocksWithGraffiti(graffiti, dbMinSlot, 0, uint32(dbCacheOffset)+1, withOrphaned)
	} else {
		dbBlocks = db.GetBlocksWithGraffiti(graffiti, dbMinSlot, (dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize+1, withOrphaned)
	}
	if dbBlocks != nil {
		for _, dbBlock := range dbBlocks {
			resBlocks = append(resBlocks, dbBlock)
		}
	}

	return resBlocks
}

func (bs *BeaconService) GetDbBlocksByProposer(proposer uint64, pageIdx uint64, pageSize uint32, withMissing bool, withOrphaned bool) []*dbtypes.AssignedBlock {
	cachedMatches := make([]struct {
		slot  uint64
		block *indexer.BlockInfo
	}, 0)
	idxMinSlot := bs.indexer.GetLowestCachedSlot()
	idxHeadSlot := bs.indexer.GetHeadSlot()
	if idxMinSlot >= 0 {
		idxHeadEpoch := utils.EpochOfSlot(idxHeadSlot)
		idxMinEpoch := utils.EpochOfSlot(uint64(idxMinSlot))
		for epochIdx := int64(idxHeadEpoch); epochIdx >= int64(idxMinEpoch); epochIdx-- {
			epoch := uint64(epochIdx)
			epochStats := bs.indexer.GetCachedEpochStats(epoch)
			if epochStats == nil || epochStats.Assignments == nil {
				continue
			}

			for slot, assigned := range epochStats.Assignments.ProposerAssignments {
				if assigned != proposer {
					continue
				}
				blocks := bs.indexer.GetCachedBlocks(slot)
				haveBlock := false
				if blocks != nil {
					for bidx := 0; bidx < len(blocks); bidx++ {
						block := blocks[bidx]
						if block.Orphaned && !withOrphaned {
							continue
						}
						if uint64(block.Block.Data.Message.ProposerIndex) != proposer {
							continue
						}
						cachedMatches = append(cachedMatches, struct {
							slot  uint64
							block *indexer.BlockInfo
						}{
							slot:  slot,
							block: block,
						})
						haveBlock = true
					}
				}
				if !haveBlock && withMissing {
					cachedMatches = append(cachedMatches, struct {
						slot  uint64
						block *indexer.BlockInfo
					}{
						slot:  slot,
						block: nil,
					})
				}
			}

		}
	}

	cachedMatchesLen := uint64(len(cachedMatches))
	cachedPages := cachedMatchesLen / uint64(pageSize)
	resBlocks := make([]*dbtypes.AssignedBlock, 0)
	resIdx := 0

	cachedStart := pageIdx * uint64(pageSize)
	cachedEnd := cachedStart + uint64(pageSize)
	if cachedEnd+1 < cachedMatchesLen {
		cachedEnd++
	}

	if cachedPages > 0 && pageIdx < cachedPages {
		for _, block := range cachedMatches[cachedStart:cachedEnd] {
			assignedBlock := dbtypes.AssignedBlock{
				Slot:     block.slot,
				Proposer: proposer,
			}
			if block.block != nil {
				assignedBlock.Block = bs.indexer.BuildLiveBlock(block.block)
			}
			resBlocks = append(resBlocks, &assignedBlock)

			resIdx++
		}
	} else if pageIdx == cachedPages {
		start := pageIdx * uint64(pageSize)
		for _, block := range cachedMatches[start:] {
			assignedBlock := dbtypes.AssignedBlock{
				Slot:     block.slot,
				Proposer: proposer,
			}
			if block.block != nil {
				assignedBlock.Block = bs.indexer.BuildLiveBlock(block.block)
			}
			resBlocks = append(resBlocks, &assignedBlock)
			resIdx++
		}
	}
	if resIdx > int(pageSize) {
		return resBlocks
	}

	// load from db
	var dbMinSlot uint64
	if idxMinSlot < 0 {
		dbMinSlot = utils.TimeToSlot(uint64(time.Now().Unix()))
	} else {
		dbMinSlot = uint64(idxMinSlot)
	}

	dbPage := pageIdx - cachedPages
	dbCacheOffset := uint64(pageSize) - (cachedMatchesLen % uint64(pageSize))
	var dbBlocks []*dbtypes.AssignedBlock
	if dbPage == 0 {
		dbBlocks = db.GetAssignedBlocks(proposer, dbMinSlot, 0, uint32(dbCacheOffset)+1, withOrphaned)
	} else {
		dbBlocks = db.GetAssignedBlocks(proposer, dbMinSlot, (dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize+1, withOrphaned)
	}
	if dbBlocks != nil {
		for _, dbBlock := range dbBlocks {
			resBlocks = append(resBlocks, dbBlock)
		}
	}

	return resBlocks
}

func (bs *BeaconService) GetValidatorActivity() (map[uint64]uint8, uint64) {
	activityMap := map[uint64]uint8{}
	epochLimit := uint64(3)

	idxHeadSlot := bs.indexer.GetHeadSlot()
	idxHeadEpoch := utils.EpochOfSlot(idxHeadSlot)
	if idxHeadEpoch < 1 {
		return activityMap, 0
	}
	idxHeadEpoch--
	idxMinSlot := bs.indexer.GetLowestCachedSlot()
	if idxMinSlot < 0 {
		return activityMap, 0
	}
	idxMinEpoch := utils.EpochOfSlot(uint64(idxMinSlot))

	activityEpoch := utils.EpochOfSlot(idxHeadSlot - 1)
	bs.validatorActivityMutex.Lock()
	defer bs.validatorActivityMutex.Unlock()
	if bs.validatorActivityStats.activity != nil && bs.validatorActivityStats.cacheEpoch == activityEpoch {
		return bs.validatorActivityStats.activity, bs.validatorActivityStats.epochLimit
	}

	actualEpochCount := idxHeadEpoch - idxMinEpoch + 1
	if actualEpochCount > epochLimit {
		idxMinEpoch = idxHeadEpoch - epochLimit + 1
	} else if actualEpochCount < epochLimit {
		epochLimit = actualEpochCount
	}

	for epochIdx := int64(idxHeadEpoch); epochIdx >= int64(idxMinEpoch); epochIdx-- {
		epoch := uint64(epochIdx)
		epochVotes := bs.indexer.GetEpochVotes(epoch)
		for valIdx := range epochVotes.ActivityMap {
			activityMap[valIdx]++
		}
	}

	bs.validatorActivityStats.cacheEpoch = activityEpoch
	bs.validatorActivityStats.epochLimit = epochLimit
	bs.validatorActivityStats.activity = activityMap
	return activityMap, epochLimit
}
