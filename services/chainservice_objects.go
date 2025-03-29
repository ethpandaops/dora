package services

import (
	"bytes"
	"slices"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
)

func (bs *ChainService) GetIncludedDepositsByFilter(filter *dbtypes.DepositFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.Deposit, uint64) {
	chainState := bs.consensusPool.GetChainState()
	finalizedBlock, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()

	// load most recent objects from indexer cache
	cachedMatches := make([]*dbtypes.Deposit, 0)
	canonicalForkIds := bs.GetCanonicalForkKeys()

	var epochStats *beacon.EpochStats
	var depositIndex *uint64

	for slot := idxMinSlot; slot < currentSlot; slot++ {
		blocks := bs.beaconIndexer.GetBlocksBySlot(slot)
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]

				isCanonical := slices.Contains(canonicalForkIds, block.GetForkId())
				if filter.WithOrphaned != 1 {
					if filter.WithOrphaned == 0 && !isCanonical {
						continue
					}
					if filter.WithOrphaned == 2 && isCanonical {
						continue
					}
				}

				epoch := chainState.EpochOfSlot(slot)
				if epochStats == nil || epochStats.GetEpoch() != epoch {
					epochStats = bs.beaconIndexer.GetEpochStats(epoch, nil)

					if epochStats != nil {
						if epochStatsValues := epochStats.GetValues(false); epochStatsValues != nil {
							depositIndexVal := epochStatsValues.FirstDepositIndex
							depositIndex = &depositIndexVal
						} else {
							depositIndex = nil
						}
					} else {
						depositIndex = nil
					}
				}

				deposits := block.GetDbDeposits(bs.beaconIndexer, depositIndex, isCanonical)
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
		_, dbCount, err = db.GetDepositsFilteredLegacy(0, 1, uint64(finalizedBlock), filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetDepositsFilteredLegacy(0, uint32(dbCacheOffset), uint64(finalizedBlock), filter)
	} else {
		dbObjects, dbCount, err = db.GetDepositsFilteredLegacy((dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, uint64(finalizedBlock), filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetIncludedDepositsByFilter error: %v", err)
	} else {
		for idx, dbObject := range dbObjects {
			if dbObject.SlotNumber > uint64(finalizedBlock) {
				isCanonical := slices.Contains(canonicalForkIds, beacon.ForkKey(dbObject.ForkId))
				dbObjects[idx].Orphaned = !isCanonical
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
	chainState := bs.consensusPool.GetChainState()
	finalizedBlock, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()
	canonicalForkIds := bs.GetCanonicalForkKeys()

	// load most recent objects from indexer cache
	cachedMatches := make([]*dbtypes.VoluntaryExit, 0)
	for slotIdx := int64(currentSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
		slot := uint64(slotIdx)
		blocks := bs.beaconIndexer.GetBlocksBySlot(phase0.Slot(slot))
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				isCanonical := slices.Contains(canonicalForkIds, block.GetForkId())
				if filter.WithOrphaned != 1 {
					if filter.WithOrphaned == 0 && !isCanonical {
						continue
					}
					if filter.WithOrphaned == 2 && isCanonical {
						continue
					}
				}
				if filter.MinSlot > 0 && slot < filter.MinSlot {
					continue
				}
				if filter.MaxSlot > 0 && slot > filter.MaxSlot {
					continue
				}

				voluntaryExits := block.GetDbVoluntaryExits(bs.beaconIndexer, isCanonical)
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
		_, dbCount, err = db.GetVoluntaryExitsFiltered(0, 1, uint64(finalizedBlock), filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetVoluntaryExitsFiltered(0, uint32(dbCacheOffset), uint64(finalizedBlock), filter)
	} else {
		dbObjects, dbCount, err = db.GetVoluntaryExitsFiltered((dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, uint64(finalizedBlock), filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetVoluntaryExitsByFilter error: %v", err)
	} else {
		for idx, dbObject := range dbObjects {
			if dbObject.SlotNumber > uint64(finalizedBlock) {
				isCanonical := slices.Contains(canonicalForkIds, beacon.ForkKey(dbObject.ForkId))
				dbObjects[idx].Orphaned = !isCanonical
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
	chainState := bs.consensusPool.GetChainState()
	finalizedBlock, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()
	canonicalForkIds := bs.GetCanonicalForkKeys()

	// load most recent objects from indexer cache
	cachedMatches := make([]*dbtypes.Slashing, 0)
	for slotIdx := int64(currentSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
		slot := uint64(slotIdx)
		blocks := bs.beaconIndexer.GetBlocksBySlot(phase0.Slot(slot))
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				isCanonical := slices.Contains(canonicalForkIds, block.GetForkId())
				if filter.WithOrphaned != 1 {
					if filter.WithOrphaned == 0 && !isCanonical {
						continue
					}
					if filter.WithOrphaned == 2 && isCanonical {
						continue
					}
				}
				if filter.MinSlot > 0 && slot < filter.MinSlot {
					continue
				}
				if filter.MaxSlot > 0 && slot > filter.MaxSlot {
					continue
				}

				slashings := block.GetDbSlashings(bs.beaconIndexer, isCanonical)
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
		_, dbCount, err = db.GetSlashingsFiltered(0, 1, uint64(finalizedBlock), filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetSlashingsFiltered(0, uint32(dbCacheOffset), uint64(finalizedBlock), filter)
	} else {
		dbObjects, dbCount, err = db.GetSlashingsFiltered((dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, uint64(finalizedBlock), filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetSlashingsByFilter error: %v", err)
	} else {
		for idx, dbObject := range dbObjects {
			if dbObject.SlotNumber > uint64(finalizedBlock) {
				isCanonical := slices.Contains(canonicalForkIds, beacon.ForkKey(dbObject.ForkId))
				dbObjects[idx].Orphaned = !isCanonical
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
