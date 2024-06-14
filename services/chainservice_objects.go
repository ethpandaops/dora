package services

import (
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer"
	"github.com/ethpandaops/dora/utils"
)

func (bs *ChainService) GetVoluntaryExitsByFilter(filter *dbtypes.VoluntaryExitFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.VoluntaryExit, uint64) {
	idxHeadSlot, finalizedEpoch, persistedEpoch, _ := bs.indexer.GetCacheState()
	finalizedBlock := uint64(0)
	if finalizedEpoch > 0 {
		finalizedBlock = (uint64(finalizedEpoch)+1)*utils.Config.Chain.Config.SlotsPerEpoch - 1
	}

	// load most recent objects from indexer cache
	idxMinSlot := (persistedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	cachedMatches := make([]*dbtypes.VoluntaryExit, 0)
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
				if filter.MinSlot > 0 && slot < filter.MinSlot {
					continue
				}
				if filter.MaxSlot > 0 && slot > filter.MaxSlot {
					continue
				}

				voluntaryExits := indexer.BuildDbVoluntaryExits(block)
				for idx, voluntaryExit := range voluntaryExits {
					if filter.MinIndex > 0 && voluntaryExit.ValidatorIndex < filter.MinIndex {
						continue
					}
					if filter.MaxIndex > 0 && voluntaryExit.ValidatorIndex > filter.MaxIndex {
						continue
					}
					if filter.ValidatorName != "" {
						validatorName := bs.validatorNames.GetValidatorName(voluntaryExit.ValidatorIndex)
						if !strings.Contains(validatorName, filter.ValidatorName) {
							continue
						}
						continue
					}

					cachedMatches = append(cachedMatches, voluntaryExits[idx])
				}
			}
		}
	}

	cachedMatchesLen := uint64(len(cachedMatches))
	cachedPages := cachedMatchesLen / uint64(pageSize)
	resObjs := make([]*dbtypes.VoluntaryExit, 0)
	resIdx := 0

	cachedStart := pageIdx * uint64(pageSize)
	cachedEnd := cachedStart + uint64(pageSize)

	if cachedPages > 0 && pageIdx < cachedPages {
		resObjs = append(resObjs, cachedMatches[cachedStart:cachedEnd]...)
		resIdx += int(cachedEnd - cachedStart)
	} else if pageIdx == cachedPages {
		resObjs = append(resObjs, cachedMatches[cachedStart:]...)
		resIdx += len(cachedMatches) - int(cachedStart)
	}

	// load older objects from db
	dbPage := pageIdx - cachedPages
	dbCacheOffset := uint64(pageSize) - (cachedMatchesLen % uint64(pageSize))

	var dbObjects []*dbtypes.VoluntaryExit
	var dbCount uint64
	var err error

	if resIdx > int(pageSize) {
		// all results from cache, just get result count from db
		_, dbCount, err = db.GetVoluntaryExitsFiltered(0, 1, finalizedBlock, filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetVoluntaryExitsFiltered(0, uint32(dbCacheOffset), finalizedBlock, filter)
	} else {
		dbObjects, dbCount, err = db.GetVoluntaryExitsFiltered((dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, finalizedBlock, filter)
	}

	if err != nil {
		logrus.Warnf("GetVoluntaryExitsByFilter error: %v", err)
	} else {
		for idx, dbObject := range dbObjects {
			if dbObject.SlotNumber > finalizedBlock {
				blockStatus := bs.CheckBlockOrphanedStatus(dbObject.SlotRoot)
				dbObjects[idx].Orphaned = blockStatus == dbtypes.Orphaned
			}

			if filter.WithOrphaned != 1 {
				if filter.WithOrphaned == 0 && dbObjects[idx].Orphaned {
					continue
				}
				if filter.WithOrphaned == 2 && !dbObjects[idx].Orphaned {
					continue
				}
			}

			resObjs = append(resObjs, dbObjects[idx])
		}
	}

	return resObjs, cachedMatchesLen + dbCount
}
