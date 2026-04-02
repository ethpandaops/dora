package services

import (
	"context"
	"slices"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
)

func (bs *ChainService) GetVoluntaryExitsByFilter(ctx context.Context, filter *dbtypes.VoluntaryExitFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.VoluntaryExit, uint64) {
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
	if cachedPages > pageIdx {
		dbPage = 0
	}

	dbCacheOffset := uint64(pageSize) - (cachedMatchesLen % uint64(pageSize))

	var dbObjects []*dbtypes.VoluntaryExit
	var dbCount uint64
	var err error

	if resIdx >= int(pageSize) {
		// all results from cache, just get result count from db
		_, dbCount, err = db.GetVoluntaryExitsFiltered(ctx, 0, 1, uint64(finalizedBlock), filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetVoluntaryExitsFiltered(ctx, 0, uint32(dbCacheOffset), uint64(finalizedBlock), filter)
	} else {
		dbObjects, dbCount, err = db.GetVoluntaryExitsFiltered(ctx, (dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, uint64(finalizedBlock), filter)
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

func (bs *ChainService) GetSlashingsByFilter(ctx context.Context, filter *dbtypes.SlashingFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.Slashing, uint64) {
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
		_, dbCount, err = db.GetSlashingsFiltered(ctx, 0, 1, uint64(finalizedBlock), filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetSlashingsFiltered(ctx, 0, uint32(dbCacheOffset), uint64(finalizedBlock), filter)
	} else {
		dbObjects, dbCount, err = db.GetSlashingsFiltered(ctx, (dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, uint64(finalizedBlock), filter)
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

func (bs *ChainService) GetWithdrawalsByFilter(ctx context.Context, filter *dbtypes.WithdrawalFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.Withdrawal, uint64) {
	chainState := bs.consensusPool.GetChainState()
	finalizedBlock, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()
	canonicalForkIds := bs.GetCanonicalForkKeys()

	// Pre-fetch fee recipient entries from DB for the unpruned slot range
	minBlockUid := uint64(idxMinSlot) << 16
	maxBlockUid := uint64(currentSlot+1) << 16
	feeRecipientType := uint8(dbtypes.WithdrawalTypeFeeRecipient)
	dbFeeEntries, _ := db.GetWithdrawalsByBlockUidRange(ctx, minBlockUid, maxBlockUid, &feeRecipientType)
	feeByBlockUid := make(map[uint64]*dbtypes.Withdrawal, len(dbFeeEntries))
	for _, entry := range dbFeeEntries {
		feeByBlockUid[entry.BlockUid] = entry
	}

	// Load most recent objects from indexer cache
	cachedMatches := make([]*dbtypes.Withdrawal, 0)
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

				withdrawals := block.GetDbWithdrawals(bs.beaconIndexer, isCanonical)

				// Merge fee recipient entry from DB if available
				if feeEntry, ok := feeByBlockUid[block.BlockUID]; ok {
					feeEntry.Orphaned = !isCanonical
					withdrawals = append(withdrawals, feeEntry)
				}

				for idx, withdrawal := range withdrawals {
					if filter.Validator != nil && (withdrawal.Validator == nil || *withdrawal.Validator != *filter.Validator) {
						continue
					}
					if filter.AccountID != nil && (withdrawal.AccountID == nil || *withdrawal.AccountID != *filter.AccountID) {
						continue
					}
					if filter.Type != nil && withdrawal.Type != *filter.Type {
						continue
					}
					if filter.MinAmount != nil && withdrawal.Amount < *filter.MinAmount {
						continue
					}
					if filter.MaxAmount != nil && withdrawal.Amount > *filter.MaxAmount {
						continue
					}
					if filter.ValidatorName != "" && withdrawal.Validator != nil {
						validatorName := bs.validatorNames.GetValidatorName(*withdrawal.Validator)
						if !strings.Contains(validatorName, filter.ValidatorName) {
							continue
						}
					}

					cachedMatches = append(cachedMatches, withdrawals[idx])
				}
			}
		}
	}

	cachedMatchesLen := uint64(len(cachedMatches))
	cachedPages := cachedMatchesLen / uint64(pageSize)
	resObjs := make([]*dbtypes.Withdrawal, 0)
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

	// Load older objects from db
	dbPage := pageIdx - cachedPages
	if cachedPages > pageIdx {
		dbPage = 0
	}

	dbCacheOffset := uint64(pageSize) - (cachedMatchesLen % uint64(pageSize))

	var dbObjects []*dbtypes.Withdrawal
	var dbCount uint64
	var err error

	if resIdx >= int(pageSize) {
		_, dbCount, err = db.GetWithdrawalsFiltered(ctx, 0, 1, uint64(finalizedBlock), filter)
	} else if dbPage == 0 {
		dbObjects, dbCount, err = db.GetWithdrawalsFiltered(ctx, 0, uint32(dbCacheOffset), uint64(finalizedBlock), filter)
	} else {
		dbObjects, dbCount, err = db.GetWithdrawalsFiltered(ctx, (dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, uint64(finalizedBlock), filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetWithdrawalsByFilter error: %v", err)
	} else {
		for idx, dbObject := range dbObjects {
			if dbObject.BlockUid>>16 > uint64(finalizedBlock) {
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
