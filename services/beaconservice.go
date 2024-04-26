package services

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common/lru"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer"
	"github.com/ethpandaops/dora/rpc"
	"github.com/ethpandaops/dora/utils"
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
	assignmentsCache    *lru.Cache[uint64, *rpc.EpochAssignments]
}

var GlobalBeaconService *BeaconService

// StartBeaconService is used to start the global beaconchain service
func StartBeaconService() error {
	if GlobalBeaconService != nil {
		return nil
	}

	validatorNames := &ValidatorNames{}
	loadingChan := validatorNames.LoadValidatorNames()
	<-loadingChan

	indexer, err := indexer.NewIndexer()
	if err != nil {
		return err
	}

	for idx, endpoint := range utils.Config.BeaconApi.Endpoints {
		indexer.AddClient(uint16(idx), &endpoint)
	}

	GlobalBeaconService = &BeaconService{
		indexer:          indexer,
		validatorNames:   validatorNames,
		assignmentsCache: lru.NewCache[uint64, *rpc.EpochAssignments](10),
	}
	return nil
}

func (bs *BeaconService) GetIndexer() *indexer.Indexer {
	return bs.indexer
}

func (bs *BeaconService) GetClients() []*indexer.IndexerClient {
	return bs.indexer.GetClients()
}

func (bs *BeaconService) GetHeadForks(readyOnly bool) []*indexer.HeadFork {
	return bs.indexer.GetHeadForks(readyOnly)
}

func (bs *BeaconService) GetValidatorName(index uint64) string {
	return bs.validatorNames.GetValidatorName(index)
}

func (bs *BeaconService) GetValidatorNamesCount() uint64 {
	return bs.validatorNames.GetValidatorNamesCount()
}

func (bs *BeaconService) GetCachedValidatorSet() map[phase0.ValidatorIndex]*v1.Validator {
	return bs.indexer.GetCachedValidatorSet()
}

func (bs *BeaconService) GetFinalizedEpoch() (int64, []byte) {
	finalizedEpoch, finalizedRoot, _, _ := bs.indexer.GetFinalizationCheckpoints()
	return finalizedEpoch, finalizedRoot
}

func (bs *BeaconService) GetCachedEpochStats(epoch uint64) *indexer.EpochStats {
	return bs.indexer.GetCachedEpochStats(epoch)
}

func (bs *BeaconService) GetGenesis() (*v1.Genesis, error) {
	return bs.indexer.GetCachedGenesis(), nil
}

type CombinedBlockResponse struct {
	Root     []byte
	Header   *phase0.SignedBeaconBlockHeader
	Block    *spec.VersionedSignedBeaconBlock
	Orphaned bool
}

func (bs *BeaconService) GetSlotDetailsByBlockroot(blockroot []byte) (*CombinedBlockResponse, error) {
	var result *CombinedBlockResponse
	if blockInfo := bs.indexer.GetCachedBlock(blockroot); blockInfo != nil {
		result = &CombinedBlockResponse{
			Root:     blockInfo.Root,
			Header:   blockInfo.GetHeader(),
			Block:    blockInfo.GetBlockBody(),
			Orphaned: !blockInfo.IsCanonical(bs.indexer, nil),
		}
	} else {
		var skipClients []*indexer.IndexerClient = nil

		var header *v1.BeaconBlockHeader
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

		var block *spec.VersionedSignedBeaconBlock
		for retry := 0; retry < 3; retry++ {
			client := bs.indexer.GetReadyClient(false, header.Root[:], skipClients)
			block, err = client.GetRpcClient().GetBlockBodyByBlockroot(header.Root[:])
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
		result = &CombinedBlockResponse{
			Root:     header.Root[:],
			Header:   header.Header,
			Block:    block,
			Orphaned: !header.Canonical,
		}
	}

	return result, nil
}

func (bs *BeaconService) GetSlotDetailsBySlot(slot uint64) (*CombinedBlockResponse, error) {
	var result *CombinedBlockResponse
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
		result = &CombinedBlockResponse{
			Root:     cachedBlock.Root,
			Header:   cachedBlock.GetHeader(),
			Block:    cachedBlock.GetBlockBody(),
			Orphaned: !cachedBlock.IsCanonical(bs.indexer, nil),
		}
	} else {
		var skipClients []*indexer.IndexerClient = nil

		var header *v1.BeaconBlockHeader
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

		var block *spec.VersionedSignedBeaconBlock
		for retry := 0; retry < 3; retry++ {
			client := bs.indexer.GetReadyClient(false, header.Root[:], skipClients)
			block, err = client.GetRpcClient().GetBlockBodyByBlockroot(header.Root[:])
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

		result = &CombinedBlockResponse{
			Root:     header.Root[:],
			Header:   header.Header,
			Block:    block,
			Orphaned: !header.Canonical,
		}
	}

	return result, nil
}

func (bs *BeaconService) GetBlobSidecarsByBlockRoot(blockroot []byte) ([]*deneb.BlobSidecar, error) {
	return bs.indexer.GetRpcClient(true, blockroot).GetBlobSidecarsByBlockroot(blockroot)
}

func (bs *BeaconService) GetOrphanedBlock(blockroot []byte) *CombinedBlockResponse {
	orphanedBlock := db.GetOrphanedBlock(blockroot)
	if orphanedBlock == nil {
		return nil
	}

	header := &phase0.SignedBeaconBlockHeader{}
	if orphanedBlock.HeaderVer != 1 {
		logrus.Warnf("failed unmarshal orphaned block header from db: unknown version")
		return nil
	}
	err := header.UnmarshalSSZ(orphanedBlock.HeaderSSZ)
	if err != nil {
		logrus.Warnf("failed unmarshal orphaned block header from db: %v", err)
		return nil
	}
	body, err := indexer.UnmarshalVersionedSignedBeaconBlockSSZ(orphanedBlock.BlockVer, orphanedBlock.BlockSSZ)
	if err != nil {
		logrus.Warnf("Error parsing unfinalized block body from db: %v", err)
		return nil
	}

	return &CombinedBlockResponse{
		Root:     orphanedBlock.Root,
		Header:   header,
		Block:    body,
		Orphaned: true,
	}
}

func (bs *BeaconService) GetEpochAssignments(epoch uint64) (*rpc.EpochAssignments, error) {
	finalizedEpoch, _ := bs.GetFinalizedEpoch()

	if int64(epoch) > finalizedEpoch {
		epochStats := bs.indexer.GetCachedEpochStats(epoch)
		if epochStats != nil {
			epochAssignments := &rpc.EpochAssignments{
				DependendRoot:       phase0.Root(epochStats.DependentRoot),
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

	var epoch uint64
	for epochIdx := int64(firstEpoch); epochIdx >= int64(lastEpoch); epochIdx-- {
		epoch = uint64(epochIdx)
		for idx := uint64(0); idx < utils.Config.Chain.Config.SlotsPerEpoch; idx++ {
			proposerAssignments[(epoch*utils.Config.Chain.Config.SlotsPerEpoch)+idx] = math.MaxInt64
		}
	}

	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	idxMinEpoch := finalizedEpoch + 1
	idxHeadEpoch := utils.EpochOfSlot(bs.indexer.GetHighestSlot())
	if firstEpoch > idxHeadEpoch {
		firstEpoch = idxHeadEpoch
	}

	if firstEpoch >= uint64(idxMinEpoch) {
		firstMissingEpoch := int64(-1)
		for epochIdx := int64(firstEpoch); epochIdx >= idxMinEpoch && epochIdx >= int64(lastEpoch); epochIdx-- {
			epoch = uint64(epochIdx)
			epochStats := bs.indexer.GetCachedEpochStats(epoch)

			if epochStats != nil {
				synchronizedEpochs[epoch] = true
				proposers := epochStats.TryGetProposerAssignments()
				for slot, vidx := range proposers {
					proposerAssignments[slot] = vidx
				}
				continue
			}

			if firstMissingEpoch == -1 {
				firstMissingEpoch = int64(epoch)
			}
		}
		if epoch <= lastEpoch && firstMissingEpoch == -1 {
			return
		}
		if firstMissingEpoch == -1 {
			firstEpoch = uint64(idxMinEpoch)
		} else {
			firstEpoch = uint64(firstMissingEpoch)
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
			if resEpoch == nil {
				resEpoch = db.GetUnfinalizedEpoch(epoch)
			}
		}
		if resEpoch == nil {
			resEpoch = &dbtypes.Epoch{
				Epoch: epoch,
			}
		}

		resEpochs[resIdx] = resEpoch
		resIdx++
	}

	return resEpochs
}

func (bs *BeaconService) GetDbBlocks(firstSlot uint64, limit int32, withMissing bool, withOrphaned bool) []*dbtypes.Slot {
	resBlocks := make([]*dbtypes.Slot, limit)
	resIdx := 0

	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	idxMinSlot := (finalizedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	idxHeadSlot := bs.indexer.GetHighestSlot()
	if firstSlot > idxHeadSlot {
		firstSlot = idxHeadSlot
	}

	var proposerAssignments map[uint64]uint64
	proposerAssignmentsEpoch := int64(-1)

	slot := firstSlot
	if idxMinSlot >= 0 && firstSlot >= uint64(idxMinSlot) {
		for slotIdx := int64(slot); slotIdx >= idxMinSlot && resIdx < int(limit); slotIdx-- {
			slot = uint64(slotIdx)

			blocks := bs.indexer.GetCachedBlocks(slot)
			if len(blocks) > 0 {
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

			if withMissing {
				epoch := utils.EpochOfSlot(slot)
				if int64(epoch) != proposerAssignmentsEpoch {
					epochStats := bs.indexer.GetCachedEpochStats(epoch)
					if epochStats != nil {
						proposerAssignments = epochStats.TryGetProposerAssignments()
					}
					proposerAssignmentsEpoch = int64(epoch)
				}

				hasCanonicalProposer := false
				canonicalProposer := uint64(math.MaxInt64)

				if len(blocks) > 0 {
					if proposerAssignments == nil {
						hasCanonicalProposer = true
					} else {
						canonicalProposer = proposerAssignments[slot]
						for bidx := 0; bidx < len(blocks) && resIdx < int(limit); bidx++ {
							block := blocks[bidx]
							header := block.GetHeader()
							if header == nil {
								continue
							}

							if uint64(header.Message.ProposerIndex) == canonicalProposer {
								hasCanonicalProposer = true
								break
							}
						}
					}
				}
				if !hasCanonicalProposer {
					resBlocks[resIdx] = &dbtypes.Slot{
						SlotHeader: dbtypes.SlotHeader{
							Slot:     slot,
							Proposer: canonicalProposer,
							Status:   dbtypes.Missing,
						},
					}
					resIdx++
				}
			}
		}
		if slot > 0 {
			slot--
		}
	}

	if resIdx < int(limit) {
		dbBlocks := db.GetSlots(slot, uint32(limit-int32(resIdx)), withMissing, withOrphaned)
		for _, dbBlock := range dbBlocks {

			if withMissing {
				for ; slot > dbBlock.Slot+1; slot-- {
					resBlocks[resIdx] = &dbtypes.Slot{
						SlotHeader: dbtypes.SlotHeader{
							Slot:     slot,
							Proposer: uint64(math.MaxInt64),
							Status:   dbtypes.Missing,
						},
						SyncParticipation: -1,
					}
					resIdx++

					if resIdx >= int(limit) {
						break
					}
				}
			}

			if resIdx >= int(limit) {
				break
			}

			if dbBlock.Block != nil {
				resBlocks[resIdx] = dbBlock.Block
			} else {
				resBlocks[resIdx] = &dbtypes.Slot{
					SlotHeader: dbtypes.SlotHeader{
						Slot:     dbBlock.Slot,
						Proposer: dbBlock.Proposer,
						Status:   dbtypes.Missing,
					},
				}
			}
			resIdx++
			slot = dbBlock.Slot
		}
	}

	return resBlocks
}

func (bs *BeaconService) GetDbBlocksForSlots(firstSlot uint64, slotLimit uint32, withMissing bool, withOrphaned bool) []*dbtypes.Slot {
	resBlocks := make([]*dbtypes.Slot, 0)

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

	var proposerAssignments map[uint64]uint64
	proposerAssignmentsEpoch := int64(-1)

	slot := firstSlot
	if idxMinSlot >= 0 && firstSlot >= uint64(idxMinSlot) {
		for slotIdx := int64(slot); slotIdx >= int64(idxMinSlot) && slotIdx >= int64(lastSlot); slotIdx-- {
			slot = uint64(slotIdx)
			blocks := bs.indexer.GetCachedBlocks(slot)
			if len(blocks) > 0 {
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

			if withMissing {
				epoch := utils.EpochOfSlot(slot)
				if int64(epoch) != proposerAssignmentsEpoch {
					epochStats := bs.indexer.GetCachedEpochStats(epoch)
					if epochStats != nil {
						proposerAssignments = epochStats.TryGetProposerAssignments()
					}
					proposerAssignmentsEpoch = int64(epoch)
				}

				hasCanonicalProposer := false
				canonicalProposer := uint64(math.MaxInt64)

				if len(blocks) > 0 {
					if proposerAssignments == nil {
						hasCanonicalProposer = true
					} else {
						canonicalProposer = proposerAssignments[slot]
						for bidx := 0; bidx < len(blocks); bidx++ {
							block := blocks[bidx]
							header := block.GetHeader()
							if header == nil {
								continue
							}

							if uint64(header.Message.ProposerIndex) == canonicalProposer {
								hasCanonicalProposer = true
								break
							}
						}
					}
				}
				if !hasCanonicalProposer {
					resBlocks = append(resBlocks, &dbtypes.Slot{
						SlotHeader: dbtypes.SlotHeader{
							Slot:     slot,
							Proposer: canonicalProposer,
							Status:   dbtypes.Missing,
						},
					})
				}
			}
		}
		if slot > 0 {
			slot--
		}
	}

	if slot > lastSlot {
		dbBlocks := db.GetSlotsRange(slot, lastSlot, withMissing, withOrphaned)

		for _, dbBlock := range dbBlocks {
			if withMissing {
				for ; slot > dbBlock.Slot+1; slot-- {
					resBlocks = append(resBlocks, &dbtypes.Slot{
						SlotHeader: dbtypes.SlotHeader{
							Slot:     slot,
							Proposer: uint64(math.MaxInt64),
							Status:   dbtypes.Missing,
						},
						SyncParticipation: -1,
					})
				}
			}

			if dbBlock.Block != nil {
				resBlocks = append(resBlocks, dbBlock.Block)
			} else {
				resBlocks = append(resBlocks, &dbtypes.Slot{
					SlotHeader: dbtypes.SlotHeader{
						Slot:     dbBlock.Slot,
						Proposer: dbBlock.Proposer,
						Status:   dbtypes.Missing,
					},
				})
			}
			slot = dbBlock.Slot
		}

		if withMissing {
			for ; slot > lastSlot; slot-- {
				resBlocks = append(resBlocks, &dbtypes.Slot{
					SlotHeader: dbtypes.SlotHeader{
						Slot:     slot,
						Proposer: uint64(math.MaxInt64),
						Status:   dbtypes.Missing,
					},
					SyncParticipation: -1,
				})
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

func (bs *BeaconService) GetDbBlocksByFilter(filter *dbtypes.BlockFilter, pageIdx uint64, pageSize uint32) []*dbtypes.AssignedSlot {
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
					graffitiBytes, _ := block.GetBlockBody().Graffiti()
					blockGraffiti := string(graffitiBytes[:])
					if !strings.Contains(blockGraffiti, filter.Graffiti) {
						continue
					}
				}
				proposer := uint64(block.GetHeader().Message.ProposerIndex)
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
					proposer: uint64(block.GetHeader().Message.ProposerIndex),
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
		var storedProposerAssignments []*dbtypes.SlotAssignment

		for epochIdx := int64(idxHeadEpoch); epochIdx >= int64(idxMinEpoch); epochIdx-- {
			epoch := uint64(epochIdx)
			var proposerAssignments map[uint64]uint64
			epochStats := bs.indexer.GetCachedEpochStats(epoch)
			if epochStats != nil {
				proposerAssignments = epochStats.GetProposerAssignments()
			} else {
				if storedProposerAssignments == nil {
					// get all unfinalized proposer assignments from db
					firstSlot := idxHeadEpoch * utils.Config.Chain.Config.SlotsPerEpoch
					lastSlot := idxMinEpoch * utils.Config.Chain.Config.SlotsPerEpoch
					storedProposerAssignments = db.GetSlotAssignmentsForSlots(firstSlot, lastSlot)
				}
				proposerAssignments = map[uint64]uint64{}
				firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
				lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
				for slot := firstSlot; slot <= lastSlot; slot++ {
					proposerAssignments[slot] = math.MaxInt64
				}
				for _, dbProposerAssignment := range storedProposerAssignments {
					if dbProposerAssignment.Slot >= firstSlot && dbProposerAssignment.Slot <= lastSlot {
						proposerAssignments[dbProposerAssignment.Slot] = dbProposerAssignment.Proposer
					}
				}
			}

			for slot, assigned := range proposerAssignments {
				if proposedMap[slot] {
					continue
				}
				if filter.WithMissing == 2 && slot > idxHeadSlot {
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
	resBlocks := make([]*dbtypes.AssignedSlot, 0)
	resIdx := 0

	cachedStart := pageIdx * uint64(pageSize)
	cachedEnd := cachedStart + uint64(pageSize)
	if cachedEnd+1 < cachedMatchesLen {
		cachedEnd++
	}

	if cachedPages > 0 && pageIdx < cachedPages {
		for _, block := range cachedMatches[cachedStart:cachedEnd] {
			assignedBlock := dbtypes.AssignedSlot{
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
			assignedBlock := dbtypes.AssignedSlot{
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
	var dbBlocks []*dbtypes.AssignedSlot
	if dbPage == 0 {
		dbBlocks = db.GetFilteredSlots(filter, dbMinSlot, 0, uint32(dbCacheOffset)+1)
	} else {
		dbBlocks = db.GetFilteredSlots(filter, dbMinSlot, (dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize+1)
	}
	resBlocks = append(resBlocks, dbBlocks...)

	return resBlocks
}

func (bs *BeaconService) GetDbBlocksByParentRoot(parentRoot []byte) []*dbtypes.Slot {
	parentBlock := bs.indexer.GetCachedBlock(parentRoot)
	cachedMatches := bs.indexer.GetCachedBlocksByParentRoot(parentRoot)
	resBlocks := make([]*dbtypes.Slot, len(cachedMatches))
	for idx, block := range cachedMatches {
		resBlocks[idx] = bs.indexer.BuildLiveBlock(block)
	}
	if parentBlock == nil {
		resBlocks = append(resBlocks, db.GetSlotsByParentRoot(parentRoot)...)
	}
	return resBlocks
}

func (bs *BeaconService) CheckBlockOrphanedStatus(blockRoot []byte) bool {
	cachedBlock := bs.indexer.GetCachedBlock(blockRoot)
	if cachedBlock != nil {
		return !cachedBlock.IsCanonical(bs.indexer, nil)
	}
	dbRefs := db.GetBlockStatus([][]byte{blockRoot})
	return len(dbRefs) > 0 && dbRefs[0].Status
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
