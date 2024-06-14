package services

import (
	"math"

	"github.com/attestantio/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/rpc"
	"github.com/ethpandaops/dora/utils"
)

func (bs *ChainService) GetEpochAssignments(epoch uint64) (*rpc.EpochAssignments, error) {
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
	epochAssignments, err = bs.indexer.GetConsensusRpc(true, nil).GetEpochAssignments(epoch, dependentRoot)
	if err != nil {
		return nil, err
	}

	bs.assignmentsCacheMux.Lock()
	bs.assignmentsCache.Add(epoch, epochAssignments)
	bs.assignmentsCacheMux.Unlock()

	return epochAssignments, nil
}

func (bs *ChainService) GetProposerAssignments(firstEpoch uint64, lastEpoch uint64) (proposerAssignments map[uint64]uint64, synchronizedEpochs map[uint64]bool) {
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

func (bs *ChainService) GetDbEpochs(firstEpoch uint64, limit uint32) []*dbtypes.Epoch {
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
