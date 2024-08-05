package services

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

type CombinedBlockResponse struct {
	Root     phase0.Root
	Header   *phase0.SignedBeaconBlockHeader
	Block    *spec.VersionedSignedBeaconBlock
	Orphaned bool
}

func (bs *ChainService) GetBlockBlob(ctx context.Context, blockroot phase0.Root, commitment deneb.KZGCommitment) (*deneb.BlobSidecar, error) {
	client := bs.beaconIndexer.GetReadyClientByBlockRoot(blockroot, true)
	if client == nil {
		client = bs.beaconIndexer.GetReadyClient(true)
	}

	if client == nil {
		return nil, fmt.Errorf("no clients available")
	}

	blobs, err := client.GetClient().GetRPCClient().GetBlobSidecarsByBlockroot(ctx, blockroot[:])
	if err != nil {
		return nil, err
	}

	for _, blob := range blobs {
		if bytes.Equal(blob.KZGCommitment[:], commitment[:]) {
			return blob, nil
		}
	}

	return nil, nil
}

func (bs *ChainService) GetSlotDetailsByBlockroot(ctx context.Context, blockroot phase0.Root) (*CombinedBlockResponse, error) {
	var result *CombinedBlockResponse
	if blockInfo := bs.beaconIndexer.GetBlockByRoot(blockroot); blockInfo != nil {
		result = &CombinedBlockResponse{
			Root:     blockInfo.Root,
			Header:   blockInfo.GetHeader(),
			Block:    blockInfo.GetBlock(),
			Orphaned: !bs.beaconIndexer.IsCanonicalBlock(blockInfo, nil),
		}
	} else if blockInfo, err := bs.beaconIndexer.GetOrphanedBlockByRoot(blockroot); blockInfo != nil || err != nil {
		if err != nil {
			return nil, err
		}
		result = &CombinedBlockResponse{
			Root:     blockInfo.Root,
			Header:   blockInfo.GetHeader(),
			Block:    blockInfo.GetBlock(),
			Orphaned: true,
		}
	} else {
		var header *phase0.SignedBeaconBlockHeader
		var err error
		clients := bs.beaconIndexer.GetReadyClientsByBlockRoot(blockroot, false)
		if len(clients) == 0 {
			clients = bs.beaconIndexer.GetReadyClients(true)
		}
		if len(clients) == 0 {
			return nil, fmt.Errorf("no clients available")
		}

		headRetry := 0
		for ; headRetry < 3; headRetry++ {
			client := clients[headRetry%len(clients)]
			header, err = beacon.LoadBeaconHeader(ctx, client, blockroot)
			if header != nil {
				break
			} else if err != nil {
				log := logrus.WithError(err)
				if client != nil {
					log = log.WithField("client", client.GetClient().GetName())
				}
				log.Warnf("Error loading block header for root 0x%x", blockroot)
			}
		}
		if err != nil || header == nil {
			return nil, err
		}

		var block *spec.VersionedSignedBeaconBlock
		for retry := headRetry; retry < headRetry+3; retry++ {
			client := clients[headRetry%len(clients)]
			block, err = beacon.LoadBeaconBlock(ctx, client, blockroot)
			if block != nil {
				break
			} else if err != nil {
				log := logrus.WithError(err)
				if client != nil {
					log = log.WithField("client", client.GetClient().GetName())
				}
				log.Warnf("Error loading block body for root 0x%x", blockroot)
			}
		}
		if err != nil || block == nil {
			return nil, err
		}
		result = &CombinedBlockResponse{
			Root:     blockroot,
			Header:   header,
			Block:    block,
			Orphaned: false,
		}
	}

	return result, nil
}

func (bs *ChainService) GetSlotDetailsBySlot(ctx context.Context, slot phase0.Slot) (*CombinedBlockResponse, error) {
	var result *CombinedBlockResponse
	if cachedBlocks := bs.beaconIndexer.GetBlocksBySlot(slot); len(cachedBlocks) > 0 {
		var cachedBlock *beacon.Block
		isOrphaned := false
		for _, block := range cachedBlocks {
			if bs.beaconIndexer.IsCanonicalBlock(block, nil) {
				cachedBlock = block
				break
			}
		}
		if cachedBlock == nil {
			cachedBlock = cachedBlocks[0]
			isOrphaned = true
		}
		result = &CombinedBlockResponse{
			Root:     cachedBlock.Root,
			Header:   cachedBlock.GetHeader(),
			Block:    cachedBlock.GetBlock(),
			Orphaned: isOrphaned,
		}
	} else {

		var header *phase0.SignedBeaconBlockHeader
		var blockRoot phase0.Root
		var orphaned bool
		var err error

		clients := bs.beaconIndexer.GetReadyClients(true)
		if len(clients) == 0 {
			return nil, fmt.Errorf("no clients available")
		}

		headRetry := 0
		for ; headRetry < 3; headRetry++ {
			client := clients[headRetry%len(clients)]
			header, blockRoot, orphaned, err = beacon.LoadBeaconHeaderBySlot(ctx, client, slot)
			if header != nil {
				break
			} else if err != nil {
				log := logrus.WithError(err)
				if client != nil {
					log = log.WithField("client", client.GetClient().GetName())
				}
				log.Warnf("Error loading block header for slot %v", slot)
			}
		}
		if err != nil || header == nil {
			return nil, err
		}

		var block *spec.VersionedSignedBeaconBlock
		for retry := headRetry; retry < headRetry+3; retry++ {
			client := clients[headRetry%len(clients)]
			block, err = beacon.LoadBeaconBlock(ctx, client, blockRoot)
			if block != nil {
				break
			} else if err != nil {
				log := logrus.WithError(err)
				if client != nil {
					log = log.WithField("client", client.GetClient().GetName())
				}
				log.Warnf("Error loading block body for slot %v", slot)
			}
		}
		if err != nil || block == nil {
			return nil, err
		}

		result = &CombinedBlockResponse{
			Root:     blockRoot,
			Header:   header,
			Block:    block,
			Orphaned: orphaned,
		}
	}

	return result, nil
}

func (bs *ChainService) GetBlobSidecarsByBlockRoot(ctx context.Context, blockroot []byte) ([]*deneb.BlobSidecar, error) {
	client := bs.beaconIndexer.GetReadyClientByBlockRoot(phase0.Root(blockroot), true)
	if client == nil {
		return nil, fmt.Errorf("no clients available")
	}

	return client.GetClient().GetRPCClient().GetBlobSidecarsByBlockroot(ctx, blockroot)
}

func (bs *ChainService) GetDbBlocks(firstSlot uint64, limit int32, withMissing bool, withOrphaned bool) []*dbtypes.Slot {
	chainState := bs.consensusPool.GetChainState()
	resBlocks := make([]*dbtypes.Slot, limit)
	resIdx := 0

	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	idxHeadSlot := uint64(chainState.CurrentSlot())
	if firstSlot > idxHeadSlot {
		firstSlot = idxHeadSlot
	}

	var proposerAssignments map[phase0.Slot]phase0.ValidatorIndex
	proposerAssignmentsEpoch := phase0.Epoch(math.MaxInt64)

	slot := phase0.Slot(firstSlot)
	if firstSlot >= uint64(idxMinSlot) {
		for slotIdx := int64(slot); slotIdx >= int64(idxMinSlot) && resIdx < int(limit); slotIdx-- {
			slot = phase0.Slot(slotIdx)

			blocks := bs.beaconIndexer.GetBlocksBySlot(slot)
			if len(blocks) > 0 {
				for bidx := 0; bidx < len(blocks) && resIdx < int(limit); bidx++ {
					block := blocks[bidx]
					if !withOrphaned && !bs.beaconIndexer.IsCanonicalBlock(block, nil) {
						continue
					}

					dbBlock := block.GetDbBlock(bs.beaconIndexer)
					if dbBlock != nil {
						resBlocks[resIdx] = dbBlock
						resIdx++
					}
				}
			}

			if withMissing {
				epoch := chainState.EpochOfSlot(slot)
				if epoch != proposerAssignmentsEpoch {
					if epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
						if epochStatsValues := epochStats.GetValues(true); epochStatsValues != nil {
							proposerAssignments = map[phase0.Slot]phase0.ValidatorIndex{}
							for slotIdx, proposer := range epochStatsValues.ProposerDuties {
								slot := chainState.EpochToSlot(epoch) + phase0.Slot(slotIdx)
								proposerAssignments[slot] = proposer
							}
						}
					}
					proposerAssignmentsEpoch = epoch
				}

				hasCanonicalProposer := false
				canonicalProposer := phase0.ValidatorIndex(math.MaxInt64)

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

							if header.Message.ProposerIndex == canonicalProposer {
								hasCanonicalProposer = true
								break
							}
						}
					}
				} else if proposerAssignments != nil {
					canonicalProposer = proposerAssignments[slot]
				}

				if !hasCanonicalProposer {
					resBlocks[resIdx] = &dbtypes.Slot{
						Slot:     uint64(slot),
						Proposer: uint64(canonicalProposer),
						Status:   dbtypes.Missing,
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
		dbBlocks := db.GetSlots(uint64(slot), uint32(limit-int32(resIdx)), withMissing, withOrphaned)
		for _, dbBlock := range dbBlocks {

			if withMissing {
				for ; slot > phase0.Slot(dbBlock.Slot+1); slot-- {
					resBlocks[resIdx] = &dbtypes.Slot{
						Slot:              uint64(slot),
						Proposer:          uint64(math.MaxInt64),
						Status:            dbtypes.Missing,
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
					Slot:     dbBlock.Slot,
					Proposer: dbBlock.Proposer,
					Status:   dbtypes.Missing,
				}
			}
			resIdx++
			slot = phase0.Slot(dbBlock.Slot)
		}
	}

	return resBlocks
}

func (bs *ChainService) GetDbBlocksForSlots(firstSlot uint64, slotLimit uint32, withMissing bool, withOrphaned bool) []*dbtypes.Slot {
	resBlocks := make([]*dbtypes.Slot, 0)

	chainState := bs.consensusPool.GetChainState()
	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)

	var lastSlot uint64
	if firstSlot > uint64(slotLimit) {
		lastSlot = firstSlot - uint64(slotLimit)
	} else {
		lastSlot = 0
	}

	var proposerAssignments map[phase0.Slot]phase0.ValidatorIndex
	proposerAssignmentsEpoch := phase0.Epoch(math.MaxInt64)

	slot := phase0.Slot(firstSlot)
	if firstSlot >= uint64(idxMinSlot) {
		for slotIdx := int64(slot); slotIdx >= int64(idxMinSlot) && slotIdx >= int64(lastSlot); slotIdx-- {
			slot = phase0.Slot(slotIdx)
			blocks := bs.beaconIndexer.GetBlocksBySlot(slot)
			if len(blocks) > 0 {
				for bidx := 0; bidx < len(blocks); bidx++ {
					block := blocks[bidx]
					if !withOrphaned && !bs.beaconIndexer.IsCanonicalBlock(block, nil) {
						continue
					}
					dbBlock := block.GetDbBlock(bs.beaconIndexer)
					if dbBlock != nil {
						resBlocks = append(resBlocks, dbBlock)
					}
				}
			}

			if withMissing {
				epoch := chainState.EpochOfSlot(slot)
				if epoch != proposerAssignmentsEpoch {
					if epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
						if epochStatsValues := epochStats.GetValues(true); epochStatsValues != nil {
							proposerAssignments = map[phase0.Slot]phase0.ValidatorIndex{}
							for slotIdx, proposer := range epochStatsValues.ProposerDuties {
								slot := chainState.EpochToSlot(epoch) + phase0.Slot(slotIdx)
								proposerAssignments[slot] = proposer
							}
						}
					}
					proposerAssignmentsEpoch = epoch
				}

				hasCanonicalProposer := false
				canonicalProposer := phase0.ValidatorIndex(math.MaxInt64)

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

							if header.Message.ProposerIndex == canonicalProposer {
								hasCanonicalProposer = true
								break
							}
						}
					}
				} else if proposerAssignments != nil {
					canonicalProposer = proposerAssignments[slot]
				}

				if !hasCanonicalProposer {
					resBlocks = append(resBlocks, &dbtypes.Slot{
						Slot:     uint64(slot),
						Proposer: uint64(canonicalProposer),
						Status:   dbtypes.Missing,
					})
				}
			}
		}
		if slot > 0 {
			slot--
		}
	}

	if uint64(slot) > lastSlot {
		dbBlocks := db.GetSlotsRange(uint64(slot), lastSlot, withMissing, withOrphaned)

		for _, dbBlock := range dbBlocks {
			if withMissing {
				for ; uint64(slot) > dbBlock.Slot+1; slot-- {
					resBlocks = append(resBlocks, &dbtypes.Slot{
						Slot:              uint64(slot),
						Proposer:          uint64(math.MaxInt64),
						Status:            dbtypes.Missing,
						SyncParticipation: -1,
					})
				}
			}

			if dbBlock.Block != nil {
				resBlocks = append(resBlocks, dbBlock.Block)
			} else {
				resBlocks = append(resBlocks, &dbtypes.Slot{
					Slot:     dbBlock.Slot,
					Proposer: dbBlock.Proposer,
					Status:   dbtypes.Missing,
				})
			}
			slot = phase0.Slot(dbBlock.Slot)
		}

		if withMissing {
			for ; uint64(slot) > lastSlot+1; slot-- {
				resBlocks = append(resBlocks, &dbtypes.Slot{
					Slot:              uint64(slot),
					Proposer:          uint64(math.MaxInt64),
					Status:            dbtypes.Missing,
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
	block    *beacon.Block
}

func (bs *ChainService) GetDbBlocksByFilter(filter *dbtypes.BlockFilter, pageIdx uint64, pageSize uint32) []*dbtypes.AssignedSlot {
	cachedMatches := make([]cachedDbBlock, 0)

	chainState := bs.consensusPool.GetChainState()
	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)

	idxHeadSlot := chainState.CurrentSlot()

	proposedMap := map[phase0.Slot]bool{}
	for slotIdx := int64(idxHeadSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
		slot := phase0.Slot(slotIdx)
		blocks := bs.beaconIndexer.GetBlocksBySlot(slot)
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				blockHeader := block.GetHeader()
				if blockHeader == nil {
					continue
				}
				blockBody := block.GetBlock()
				if blockBody == nil {
					continue
				}
				if filter.WithOrphaned != 1 {
					isOrphaned := !bs.beaconIndexer.IsCanonicalBlock(block, nil)
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
					graffitiBytes, _ := blockBody.Graffiti()
					blockGraffiti := string(graffitiBytes[:])
					if !strings.Contains(blockGraffiti, filter.Graffiti) {
						continue
					}
				}
				if filter.ExtraData != "" {
					executionExtraData := block.GetExecutionExtraData()
					blockExtraData := string(executionExtraData[:])
					if !strings.Contains(blockExtraData, filter.ExtraData) {
						continue
					}
				}
				proposer := uint64(blockHeader.Message.ProposerIndex)
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
					slot:     uint64(block.Slot),
					proposer: uint64(blockHeader.Message.ProposerIndex),
					block:    block,
				})
			}
		}
	}

	if filter.WithMissing != 0 && filter.Graffiti == "" && filter.ExtraData == "" && filter.WithOrphaned != 2 {
		// add missed blocks
		idxHeadEpoch := chainState.EpochOfSlot(idxHeadSlot)
		idxMinEpoch := utils.EpochOfSlot(uint64(idxMinSlot))

		for epochIdx := int64(idxHeadEpoch); epochIdx >= int64(idxMinEpoch); epochIdx-- {
			epoch := phase0.Epoch(epochIdx)
			var proposerAssignments map[phase0.Slot]phase0.ValidatorIndex

			if epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
				if epochStatsValues := epochStats.GetValues(true); epochStatsValues != nil {
					proposerAssignments = map[phase0.Slot]phase0.ValidatorIndex{}
					for slotIdx, proposer := range epochStatsValues.ProposerDuties {
						slot := chainState.EpochToSlot(epoch) + phase0.Slot(slotIdx)
						proposerAssignments[slot] = proposer
					}
				}
			}

			if proposerAssignments == nil {
				proposerAssignments = map[phase0.Slot]phase0.ValidatorIndex{}
				firstSlot := chainState.EpochToSlot(epoch)
				lastSlot := chainState.EpochToSlot(epoch + 1)
				for slot := firstSlot; slot < lastSlot; slot++ {
					proposerAssignments[slot] = math.MaxInt64
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
					if uint64(assigned) != *filter.ProposerIndex {
						continue
					}
				}
				if filter.ProposerName != "" {
					assignedName := bs.validatorNames.GetValidatorName(uint64(assigned))
					if assignedName == "" || !strings.Contains(assignedName, filter.ProposerName) {
						continue
					}
				}

				cachedMatches = append(cachedMatches, cachedDbBlock{
					slot:     uint64(slot),
					proposer: uint64(assigned),
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
				assignedBlock.Block = block.block.GetDbBlock(bs.beaconIndexer)
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
				assignedBlock.Block = block.block.GetDbBlock(bs.beaconIndexer)
			}
			resBlocks = append(resBlocks, &assignedBlock)
			resIdx++
		}
	}
	if resIdx > int(pageSize) {
		return resBlocks
	}

	// load from db
	dbPage := pageIdx - cachedPages
	dbCacheOffset := uint64(pageSize) - (cachedMatchesLen % uint64(pageSize))
	var dbBlocks []*dbtypes.AssignedSlot
	if dbPage == 0 {
		dbBlocks = db.GetFilteredSlots(filter, uint64(idxMinSlot), 0, uint32(dbCacheOffset)+1)
	} else {
		dbBlocks = db.GetFilteredSlots(filter, uint64(idxMinSlot), (dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize+1)
	}
	resBlocks = append(resBlocks, dbBlocks...)

	return resBlocks
}

func (bs *ChainService) GetDbBlocksByParentRoot(parentRoot phase0.Root) []*dbtypes.Slot {
	parentBlock := bs.beaconIndexer.GetBlockByRoot(parentRoot)
	cachedMatches := bs.beaconIndexer.GetBlockByParentRoot(parentRoot)
	resBlocks := make([]*dbtypes.Slot, len(cachedMatches))
	for idx, block := range cachedMatches {
		resBlocks[idx] = block.GetDbBlock(bs.beaconIndexer)
	}
	if parentBlock == nil {
		resBlocks = append(resBlocks, db.GetSlotsByParentRoot(parentRoot[:])...)
	}
	return resBlocks
}

func (bs *ChainService) CheckBlockOrphanedStatus(blockRoot phase0.Root) dbtypes.SlotStatus {
	cachedBlock := bs.beaconIndexer.GetBlockByRoot(blockRoot)
	if cachedBlock != nil {
		if bs.beaconIndexer.IsCanonicalBlock(cachedBlock, nil) {
			return dbtypes.Canonical
		} else {
			return dbtypes.Orphaned
		}
	}
	dbRefs := db.GetSlotStatus([][]byte{blockRoot[:]})
	if len(dbRefs) > 0 {
		return dbRefs[0].Status
	}

	return dbtypes.Missing
}
