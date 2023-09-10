package services

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/indexer"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

type BeaconService struct {
	indexer        *indexer.Indexer
	validatorNames *ValidatorNames

	validatorActivityMutex sync.Mutex
	validatorActivityStats struct {
		cacheEpoch uint64
		epochLimit uint64
		activity   map[uint64]uint8
	}

	assignmentsCacheMux sync.Mutex
	assignmentsCache    *lru.Cache[uint64, *rpctypes.EpochAssignments]
}

var GlobalBeaconService *BeaconService

// StartBeaconService is used to start the global beaconchain service
func StartBeaconService() error {
	if GlobalBeaconService != nil {
		return nil
	}

	indexer, err := indexer.NewIndexer()
	if err != nil {
		return err
	}

	for idx, endpoint := range utils.Config.BeaconApi.Endpoints {
		indexer.AddClient(uint8(idx), &endpoint)
	}

	validatorNames := &ValidatorNames{}
	validatorNames.LoadValidatorNames()

	GlobalBeaconService = &BeaconService{
		indexer:          indexer,
		validatorNames:   validatorNames,
		assignmentsCache: lru.NewCache[uint64, *rpctypes.EpochAssignments](10),
	}
	return nil
}

func (bs *BeaconService) GetIndexer() *indexer.Indexer {
	return bs.indexer
}

func (bs *BeaconService) GetClients() []*indexer.IndexerClient {
	return bs.indexer.GetClients()
}

func (bs *BeaconService) GetValidatorName(index uint64) string {
	return bs.validatorNames.GetValidatorName(index)
}

func (bs *BeaconService) GetCachedValidatorSet() *rpctypes.StandardV1StateValidatorsResponse {
	return bs.indexer.GetCachedValidatorSet()
}

func (bs *BeaconService) GetFinalizedEpoch() (int64, []byte) {
	finalizedEpoch, finalizedRoot, _, _ := bs.indexer.GetFinalizationCheckpoints()
	return finalizedEpoch, finalizedRoot
}

func (bs *BeaconService) GetCachedEpochStats(epoch uint64) *indexer.EpochStats {
	return bs.indexer.GetCachedEpochStats(epoch)
}

func (bs *BeaconService) GetGenesis() (*rpctypes.StandardV1GenesisResponse, error) {
	return bs.indexer.GetCachedGenesis(), nil
}

func (bs *BeaconService) GetSlotDetailsByBlockroot(blockroot []byte) (*rpctypes.CombinedBlockResponse, error) {
	var result *rpctypes.CombinedBlockResponse
	if blockInfo := bs.indexer.GetCachedBlock(blockroot); blockInfo != nil {
		result = &rpctypes.CombinedBlockResponse{
			Root:     blockInfo.Root,
			Header:   blockInfo.GetHeader(),
			Block:    blockInfo.GetBlockBody(),
			Orphaned: !blockInfo.IsCanonical(bs.indexer, nil),
		}
	} else {
		var skipClients []*indexer.IndexerClient = nil

		var header *rpctypes.StandardV1BeaconHeaderResponse
		var err error
		for retry := 0; retry < 3; retry++ {
			client := bs.indexer.GetReadyClient(false, blockroot, skipClients)
			header, err = client.GetRpcClient().GetBlockHeaderByBlockroot(blockroot)
			if header != nil {
				break
			} else if err != nil {
				log := logrus.WithError(err)
				if client != nil {
					log = log.WithField("client", client.GetName())
				}
				log.Warnf("Error loading block header for root 0x%x", blockroot)
				skipClients = append(skipClients, client)
			}
		}
		if err != nil || header == nil {
			return nil, err
		}

		var block *rpctypes.StandardV2BeaconBlockResponse
		for retry := 0; retry < 3; retry++ {
			client := bs.indexer.GetReadyClient(false, header.Data.Root, skipClients)
			block, err = client.GetRpcClient().GetBlockBodyByBlockroot(header.Data.Root)
			if block != nil {
				break
			} else if err != nil {
				log := logrus.WithError(err)
				if client != nil {
					log = log.WithField("client", client.GetName())
				}
				log.Warnf("Error loading block body for root 0x%x", blockroot)
				skipClients = append(skipClients, client)
			}
		}
		if err != nil || block == nil {
			return nil, err
		}
		result = &rpctypes.CombinedBlockResponse{
			Root:     header.Data.Root,
			Header:   &header.Data.Header,
			Block:    &block.Data,
			Orphaned: !header.Data.Canonical,
		}
	}

	return result, nil
}

func (bs *BeaconService) GetSlotDetailsBySlot(slot uint64) (*rpctypes.CombinedBlockResponse, error) {
	var result *rpctypes.CombinedBlockResponse
	if cachedBlocks := bs.indexer.GetCachedBlocks(slot); len(cachedBlocks) > 0 {
		var cachedBlock *indexer.CacheBlock
		for _, block := range cachedBlocks {
			if block.IsCanonical(bs.indexer, nil) {
				cachedBlock = block
				break
			}
		}
		if cachedBlock == nil {
			cachedBlock = cachedBlocks[0]
		}
		result = &rpctypes.CombinedBlockResponse{
			Root:     cachedBlock.Root,
			Header:   cachedBlock.GetHeader(),
			Block:    cachedBlock.GetBlockBody(),
			Orphaned: !cachedBlock.IsCanonical(bs.indexer, nil),
		}
	} else {
		var skipClients []*indexer.IndexerClient = nil

		var header *rpctypes.StandardV1BeaconHeaderResponse
		var err error
		for retry := 0; retry < 3; retry++ {
			client := bs.indexer.GetReadyClient(false, nil, skipClients)
			header, err = client.GetRpcClient().GetBlockHeaderBySlot(slot)
			if header != nil {
				break
			} else if err != nil {
				log := logrus.WithError(err)
				if client != nil {
					log = log.WithField("client", client.GetName())
				}
				log.Warnf("Error loading block header for slot %v", slot)
				skipClients = append(skipClients, client)
			}
		}
		if err != nil || header == nil {
			return nil, err
		}

		var block *rpctypes.StandardV2BeaconBlockResponse
		for retry := 0; retry < 3; retry++ {
			client := bs.indexer.GetReadyClient(false, header.Data.Root, skipClients)
			block, err = client.GetRpcClient().GetBlockBodyByBlockroot(header.Data.Root)
			if block != nil {
				break
			} else if err != nil {
				log := logrus.WithError(err)
				if client != nil {
					log = log.WithField("client", client.GetName())
				}
				log.Warnf("Error loading block body for slot %v", slot)
				skipClients = append(skipClients, client)
			}
		}
		if err != nil || block == nil {
			return nil, err
		}

		result = &rpctypes.CombinedBlockResponse{
			Root:     header.Data.Root,
			Header:   &header.Data.Header,
			Block:    &block.Data,
			Orphaned: !header.Data.Canonical,
		}
	}

	return result, nil
}

func (bs *BeaconService) GetBlobSidecarsByBlockRoot(blockroot []byte) (*rpctypes.StandardV1BlobSidecarsResponse, error) {
	return bs.indexer.GetRpcClient(true, blockroot).GetBlobSidecarsByBlockroot(blockroot)
}

func (bs *BeaconService) GetOrphanedBlock(blockroot []byte) *rpctypes.CombinedBlockResponse {
	orphanedBlock := db.GetOrphanedBlock(blockroot)
	if orphanedBlock == nil {
		return nil
	}

	var header rpctypes.SignedBeaconBlockHeader
	err := json.Unmarshal([]byte(orphanedBlock.Header), &header)
	if err != nil {
		logrus.Warnf("Error parsing orphaned block header from db: %v", err)
		return nil
	}
	var block rpctypes.SignedBeaconBlock
	err = json.Unmarshal([]byte(orphanedBlock.Block), &block)
	if err != nil {
		logrus.Warnf("Error parsing orphaned block body from db: %v", err)
		return nil
	}

	return &rpctypes.CombinedBlockResponse{
		Root:     orphanedBlock.Root,
		Header:   &header,
		Block:    &block,
		Orphaned: true,
	}
}

func (bs *BeaconService) GetEpochAssignments(epoch uint64) (*rpctypes.EpochAssignments, error) {
	finalizedEpoch, _ := bs.GetFinalizedEpoch()

	if int64(epoch) > finalizedEpoch {
		epochStats := bs.indexer.GetCachedEpochStats(epoch)
		if epochStats != nil {
			epochAssignments := &rpctypes.EpochAssignments{
				DependendRoot:       epochStats.DependentRoot,
				DependendStateRef:   epochStats.GetDependentStateRef(),
				ProposerAssignments: epochStats.GetProposerAssignments(),
				AttestorAssignments: epochStats.GetAttestorAssignments(),
				SyncAssignments:     epochStats.GetSyncAssignments(),
			}
			return epochAssignments, nil
		} else {
			return nil, nil
		}
	}

	if utils.Config.BeaconApi.SkipFinalAssignments {
		return nil, nil
	}

	bs.assignmentsCacheMux.Lock()
	epochAssignments, found := bs.assignmentsCache.Get(epoch)
	bs.assignmentsCacheMux.Unlock()
	if found {
		return epochAssignments, nil
	}

	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	dependentRoot := db.GetHighestRootBeforeSlot(firstSlot, false)
	var err error
	epochAssignments, err = bs.indexer.GetRpcClient(true, nil).GetEpochAssignments(epoch, dependentRoot)
	if err != nil {
		return nil, err
	}

	bs.assignmentsCacheMux.Lock()
	bs.assignmentsCache.Add(epoch, epochAssignments)
	bs.assignmentsCacheMux.Unlock()

	return epochAssignments, nil
}

func (bs *BeaconService) GetProposerAssignments(firstEpoch uint64, lastEpoch uint64) (proposerAssignments map[uint64]uint64, synchronizedEpochs map[uint64]bool) {
	proposerAssignments = make(map[uint64]uint64)
	synchronizedEpochs = make(map[uint64]bool)

	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	idxMinEpoch := finalizedEpoch + 1
	idxHeadEpoch := utils.EpochOfSlot(bs.indexer.GetHighestSlot())
	if firstEpoch > idxHeadEpoch {
		firstEpoch = idxHeadEpoch
	}

	if firstEpoch >= uint64(idxMinEpoch) {
		var epoch uint64
		for epochIdx := int64(firstEpoch); epochIdx >= int64(idxMinEpoch) && epochIdx >= int64(lastEpoch); epochIdx-- {
			epoch = uint64(epochIdx)
			epochStats := bs.indexer.GetCachedEpochStats(epoch)
			if epochStats != nil {
				synchronizedEpochs[epoch] = true
				for slot, vidx := range epochStats.GetProposerAssignments() {
					proposerAssignments[slot] = vidx
				}
			} else {
				for idx := uint64(0); idx < utils.Config.Chain.Config.SlotsPerEpoch; idx++ {
					proposerAssignments[(epoch*utils.Config.Chain.Config.SlotsPerEpoch)+idx] = math.MaxInt64
				}
			}
		}
		if epoch <= lastEpoch {
			return
		}
		firstEpoch = epoch
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

	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	var idxMinEpoch, idxHeadEpoch uint64
	idxMinEpoch = uint64(finalizedEpoch + 1)
	idxHeadEpoch = utils.EpochOfSlot(bs.indexer.GetHighestSlot())

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

	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	idxMinSlot := (finalizedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	idxHeadSlot := bs.indexer.GetHighestSlot()
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
					if !withOrphaned && !block.IsCanonical(bs.indexer, nil) {
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
		if slot > 0 {
			slot--
		}
	}

	if resIdx < int(limit) {
		dbBlocks := db.GetBlocks(slot, uint32(limit-int32(resIdx)), withOrphaned)
		for _, dbBlock := range dbBlocks {
			resBlocks[resIdx] = dbBlock
			resIdx++
			if resIdx >= int(limit) {
				break
			}
		}
	}

	return resBlocks
}

func (bs *BeaconService) GetDbBlocksForSlots(firstSlot uint64, slotLimit uint32, withOrphaned bool) []*dbtypes.Block {
	resBlocks := make([]*dbtypes.Block, 0)

	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	idxMinSlot := (finalizedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	idxHeadSlot := bs.indexer.GetHighestSlot()
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
					if !withOrphaned && !block.IsCanonical(bs.indexer, nil) {
						continue
					}
					dbBlock := bs.indexer.BuildLiveBlock(block)
					if dbBlock != nil {
						resBlocks = append(resBlocks, dbBlock)
					}
				}
			}
		}
		if slot > 0 {
			slot--
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

type cachedDbBlock struct {
	slot     uint64
	proposer uint64
	block    *indexer.CacheBlock
}

func (bs *BeaconService) GetDbBlocksByFilter(filter *dbtypes.BlockFilter, pageIdx uint64, pageSize uint32) []*dbtypes.AssignedBlock {
	cachedMatches := make([]cachedDbBlock, 0)
	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	idxMinSlot := (finalizedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	idxHeadSlot := bs.indexer.GetHighestSlot()
	proposedMap := map[uint64]bool{}
	for slotIdx := int64(idxHeadSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
		slot := uint64(slotIdx)
		blocks := bs.indexer.GetCachedBlocks(slot)
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				if filter.WithOrphaned != 1 {
					isOrphaned := !block.IsCanonical(bs.indexer, nil)
					if filter.WithOrphaned == 0 && isOrphaned {
						continue
					}
					if filter.WithOrphaned == 2 && !isOrphaned {
						continue
					}
				}
				proposedMap[block.Slot] = true
				if filter.WithMissing == 2 {
					continue
				}

				if filter.Graffiti != "" {
					blockGraffiti := string(block.GetBlockBody().Message.Body.Graffiti)
					if !strings.Contains(blockGraffiti, filter.Graffiti) {
						continue
					}
				}
				proposer := uint64(block.GetBlockBody().Message.ProposerIndex)
				if filter.ProposerIndex != nil {
					if proposer != *filter.ProposerIndex {
						continue
					}
				}
				if filter.ProposerName != "" {
					proposerName := bs.validatorNames.GetValidatorName(proposer)
					if !strings.Contains(proposerName, filter.ProposerName) {
						continue
					}
				}

				cachedMatches = append(cachedMatches, cachedDbBlock{
					slot:     block.Slot,
					proposer: uint64(block.GetBlockBody().Message.ProposerIndex),
					block:    block,
				})
			}
		}
	}

	if filter.WithMissing != 0 && filter.Graffiti == "" && filter.WithOrphaned != 2 {
		// add missed blocks
		idxHeadSlot := bs.indexer.GetHighestSlot()
		idxHeadEpoch := utils.EpochOfSlot(idxHeadSlot)
		idxMinEpoch := utils.EpochOfSlot(uint64(idxMinSlot))
		for epochIdx := int64(idxHeadEpoch); epochIdx >= int64(idxMinEpoch); epochIdx-- {
			epoch := uint64(epochIdx)
			epochStats := bs.indexer.GetCachedEpochStats(epoch)
			if epochStats == nil {
				continue
			}
			for slot, assigned := range epochStats.GetProposerAssignments() {
				if proposedMap[slot] {
					continue
				}

				if filter.ProposerIndex != nil {
					if assigned != *filter.ProposerIndex {
						continue
					}
				}
				if filter.ProposerName != "" {
					assignedName := bs.validatorNames.GetValidatorName(assigned)
					if assignedName == "" || !strings.Contains(assignedName, filter.ProposerName) {
						continue
					}
				}

				cachedMatches = append(cachedMatches, cachedDbBlock{
					slot:     slot,
					proposer: assigned,
					block:    nil,
				})
			}
			sort.Slice(cachedMatches, func(a, b int) bool {
				slotA := cachedMatches[a].slot
				slotB := cachedMatches[b].slot
				return slotA > slotB
			})
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
				Proposer: block.proposer,
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
				Proposer: block.proposer,
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
		dbBlocks = db.GetFilteredBlocks(filter, dbMinSlot, 0, uint32(dbCacheOffset)+1)
	} else {
		dbBlocks = db.GetFilteredBlocks(filter, dbMinSlot, (dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize+1)
	}
	resBlocks = append(resBlocks, dbBlocks...)

	return resBlocks
}

func (bs *BeaconService) GetDbBlocksByParentRoot(parentRoot []byte) []*dbtypes.Block {
	parentBlock := bs.indexer.GetCachedBlock(parentRoot)
	cachedMatches := bs.indexer.GetCachedBlocksByParentRoot(parentRoot)
	resBlocks := make([]*dbtypes.Block, len(cachedMatches))
	for idx, block := range cachedMatches {
		resBlocks[idx] = bs.indexer.BuildLiveBlock(block)
	}
	if parentBlock == nil {
		resBlocks = append(resBlocks, db.GetBlocksByParentRoot(parentRoot)...)
	}
	return resBlocks
}

func (bs *BeaconService) CheckBlockOrphanedStatus(blockRoot []byte) bool {
	cachedBlock := bs.indexer.GetCachedBlock(blockRoot)
	if cachedBlock != nil {
		return !cachedBlock.IsCanonical(bs.indexer, nil)
	}
	dbRefs := db.GetBlockOrphanedRefs([][]byte{blockRoot})
	return len(dbRefs) > 0 && dbRefs[0].Orphaned
}

func (bs *BeaconService) GetValidatorActivity() (map[uint64]uint8, uint64) {
	activityMap := map[uint64]uint8{}
	epochLimit := uint64(3)

	idxHeadSlot := bs.indexer.GetHighestSlot()
	idxHeadEpoch := utils.EpochOfSlot(idxHeadSlot)
	if idxHeadEpoch < 1 {
		return activityMap, 0
	}
	idxHeadEpoch--
	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	var idxMinEpoch uint64
	if finalizedEpoch < 0 {
		idxMinEpoch = 0
	} else {
		idxMinEpoch = uint64(finalizedEpoch + 1)
	}

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
		_, epochVotes := bs.indexer.GetEpochVotes(epoch)
		if epochVotes == nil {
			epochLimit--
		} else {
			for valIdx := range epochVotes.ActivityMap {
				activityMap[valIdx]++
			}
		}
	}

	bs.validatorActivityStats.cacheEpoch = activityEpoch
	bs.validatorActivityStats.epochLimit = epochLimit
	bs.validatorActivityStats.activity = activityMap
	return activityMap, epochLimit
}
