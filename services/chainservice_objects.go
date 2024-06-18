package services

import (
	"bytes"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer"
	"github.com/ethpandaops/dora/utils"
)

func (bs *ChainService) GetIncludedDepositsByFilter(filter *dbtypes.DepositFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.Deposit, uint64) {
	idxHeadSlot, finalizedEpoch, persistedEpoch, _ := bs.indexer.GetCacheState()
	finalizedBlock := uint64(0)
	if finalizedEpoch > 0 {
		finalizedBlock = (uint64(finalizedEpoch)+1)*utils.Config.Chain.Config.SlotsPerEpoch - 1
	}

	// load most recent objects from indexer cache
	idxMinSlot := (persistedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	cachedMatches := make([]*dbtypes.Deposit, 0)

	var epochStats *indexer.EpochStats
	var depositIndex *uint64

	for slotIdx := idxMinSlot; slotIdx < int64(idxHeadSlot); slotIdx++ {
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

				epoch := utils.EpochOfSlot(slot)
				if epochStats == nil || epochStats.Epoch != epoch {
					epochStats = bs.indexer.GetCachedEpochStats(epoch)

					if epochStats != nil {
						depositIndex = epochStats.GetInitialDepositIndex()
					} else {
						depositIndex = nil
					}
				}

				deposits := indexer.BuildDbDeposits(block, depositIndex)
				for idx, deposit := range deposits {
					if filter.MinIndex > 0 && (deposit.Index == nil || *deposit.Index < filter.MinIndex) {
						continue
					}
					if filter.MaxIndex > 0 && (deposit.Index == nil || *deposit.Index > filter.MaxIndex) {
						continue
					}
					if len(filter.PublicKey) > 0 && !bytes.Equal(deposit.PublicKey, filter.PublicKey) {
						continue
					}
					if filter.MinAmount > 0 && deposit.Amount < filter.MinAmount*utils.GWEI.Uint64() {
						continue
					}
					if filter.MaxAmount > 0 && deposit.Amount > filter.MaxAmount*utils.GWEI.Uint64() {
						continue
					}
					if filter.ValidatorName != "" {
						validatorName := bs.validatorNames.GetValidatorNameByPubkey(deposit.PublicKey)
						if !strings.Contains(validatorName, filter.ValidatorName) {
							continue
						}
					}

					cachedMatches = append(cachedMatches, deposits[idx])
				}
			}
		}
	}

	cachedMatchesLen := uint64(len(cachedMatches))
	cachedPages := cachedMatchesLen / uint64(pageSize)
	resObjs := make([]*dbtypes.Deposit, 0)
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

	var dbObjects []*dbtypes.Deposit
	var dbCount uint64
	var err error

	if resIdx > int(pageSize) {
		// all results from cache, just get result count from db
		_, dbCount, err = db.GetDepositsFiltered(0, 1, finalizedBlock, filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetDepositsFiltered(0, uint32(dbCacheOffset), finalizedBlock, filter)
	} else {
		dbObjects, dbCount, err = db.GetDepositsFiltered((dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, finalizedBlock, filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetIncludedDepositsByFilter error: %v", err)
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
		logrus.Warnf("ChainService.GetVoluntaryExitsByFilter error: %v", err)
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

func (bs *ChainService) GetSlashingsByFilter(filter *dbtypes.SlashingFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.Slashing, uint64) {
	idxHeadSlot, finalizedEpoch, persistedEpoch, _ := bs.indexer.GetCacheState()
	finalizedBlock := uint64(0)
	if finalizedEpoch > 0 {
		finalizedBlock = (uint64(finalizedEpoch)+1)*utils.Config.Chain.Config.SlotsPerEpoch - 1
	}

	// load most recent objects from indexer cache
	idxMinSlot := (persistedEpoch + 1) * int64(utils.Config.Chain.Config.SlotsPerEpoch)
	cachedMatches := make([]*dbtypes.Slashing, 0)
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

				slashings := indexer.BuildDbSlashings(block)
				for idx, slashing := range slashings {
					if filter.MinIndex > 0 && slashing.ValidatorIndex < filter.MinIndex {
						continue
					}
					if filter.MaxIndex > 0 && slashing.ValidatorIndex > filter.MaxIndex {
						continue
					}
					if filter.ValidatorName != "" {
						validatorName := bs.validatorNames.GetValidatorName(slashing.ValidatorIndex)
						if !strings.Contains(validatorName, filter.ValidatorName) {
							continue
						}
					}
					if filter.SlasherName != "" {
						slasherName := bs.validatorNames.GetValidatorName(slashing.SlasherIndex)
						if !strings.Contains(slasherName, filter.SlasherName) {
							continue
						}
					}

					cachedMatches = append(cachedMatches, slashings[idx])
				}
			}
		}
	}

	cachedMatchesLen := uint64(len(cachedMatches))
	cachedPages := cachedMatchesLen / uint64(pageSize)
	resObjs := make([]*dbtypes.Slashing, 0)
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

	var dbObjects []*dbtypes.Slashing
	var dbCount uint64
	var err error

	if resIdx > int(pageSize) {
		// all results from cache, just get result count from db
		_, dbCount, err = db.GetSlashingsFiltered(0, 1, finalizedBlock, filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetSlashingsFiltered(0, uint32(dbCacheOffset), finalizedBlock, filter)
	} else {
		dbObjects, dbCount, err = db.GetSlashingsFiltered((dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, finalizedBlock, filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetSlashingsByFilter error: %v", err)
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
