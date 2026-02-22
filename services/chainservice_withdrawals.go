package services

import (
	"bytes"
	"context"
	"strings"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/prysmaticlabs/prysm/v5/container/slice"
	"github.com/sirupsen/logrus"
)

type CombinedWithdrawalRequest struct {
	Request             *dbtypes.WithdrawalRequest
	RequestOrphaned     bool
	Transaction         *dbtypes.WithdrawalRequestTx
	TransactionOrphaned bool
}

type CombinedWithdrawalRequestFilter struct {
	Filter  *dbtypes.WithdrawalRequestFilter
	Request uint8 // 0: all, 1: tx only, 2: request only
}

func (cwr *CombinedWithdrawalRequest) SourceAddress() []byte {
	if cwr.Request != nil {
		return cwr.Request.SourceAddress
	}
	if cwr.Transaction != nil {
		return cwr.Transaction.SourceAddress
	}
	return nil
}

func (cwr *CombinedWithdrawalRequest) ValidatorIndex() *uint64 {
	if cwr.Request != nil && cwr.Request.ValidatorIndex != nil {
		return cwr.Request.ValidatorIndex
	}
	if cwr.Transaction != nil && cwr.Transaction.ValidatorIndex != nil {
		return cwr.Transaction.ValidatorIndex
	}
	return nil
}

func (cwr *CombinedWithdrawalRequest) ValidatorPubkey() []byte {
	if cwr.Request != nil && len(cwr.Request.ValidatorPubkey) > 0 {
		return cwr.Request.ValidatorPubkey
	}
	if cwr.Transaction != nil && len(cwr.Transaction.ValidatorPubkey) > 0 {
		return cwr.Transaction.ValidatorPubkey
	}
	return nil
}

func (cwr *CombinedWithdrawalRequest) Amount() uint64 {
	if cwr.Request != nil {
		return db.ConvertInt64ToUint64(cwr.Request.Amount)
	}
	if cwr.Transaction != nil {
		return db.ConvertInt64ToUint64(cwr.Transaction.Amount)
	}
	return 0
}

func (bs *ChainService) GetWithdrawalRequestsByFilter(ctx context.Context, filter *CombinedWithdrawalRequestFilter, pageOffset uint64, pageSize uint32) ([]*CombinedWithdrawalRequest, uint64, uint64) {
	totalPendingTxResults := uint64(0)
	totalReqResults := uint64(0)

	combinedResults := make([]*CombinedWithdrawalRequest, 0)
	canonicalForkIds := bs.GetCanonicalForkIds()

	initiatedFilter := &dbtypes.WithdrawalRequestTxFilter{
		MinDequeue:    bs.GetHighestElBlockNumber(ctx, nil) + 1,
		PublicKey:     filter.Filter.PublicKey,
		MinIndex:      filter.Filter.MinIndex,
		MaxIndex:      filter.Filter.MaxIndex,
		ValidatorName: filter.Filter.ValidatorName,
		WithOrphaned:  filter.Filter.WithOrphaned,
	}

	if filter.Request != 2 {
		dbTransactions, totalDbTransactions, _ := db.GetWithdrawalRequestTxsFiltered(ctx, pageOffset, pageSize, canonicalForkIds, initiatedFilter)
		totalPendingTxResults = totalDbTransactions

		for _, withdrawal := range dbTransactions {
			combinedResults = append(combinedResults, &CombinedWithdrawalRequest{
				Transaction:         withdrawal,
				TransactionOrphaned: !bs.isCanonicalForkId(withdrawal.ForkId, canonicalForkIds),
			})
		}
	}

	if filter.Request != 1 {
		requestTxDetailsFor := [][]byte{}
		dbOperations := []*dbtypes.WithdrawalRequest(nil)
		page2Offset := uint64(0)
		if pageOffset > totalPendingTxResults {
			page2Offset = pageOffset - totalPendingTxResults
		}

		dbOperations, totalReqResults = bs.GetWithdrawalRequestOperationsByFilter(ctx, filter.Filter, page2Offset, pageSize)

		for _, dbOperation := range dbOperations {
			if len(combinedResults) >= int(pageSize) {
				break
			}

			combinedResult := &CombinedWithdrawalRequest{
				Request:         dbOperation,
				RequestOrphaned: !bs.isCanonicalForkId(dbOperation.ForkId, canonicalForkIds),
			}

			if len(dbOperation.TxHash) > 0 {
				requestTxDetailsFor = append(requestTxDetailsFor, dbOperation.TxHash)
			} else if matcherHeight := bs.GetWithdrawalIndexer().GetMatcherHeight(); dbOperation.BlockNumber > matcherHeight {
				// withdrawal request has not been matched with a tx yet, try to find the tx on the fly
				requestTxs := db.GetWithdrawalRequestTxsByDequeueRange(ctx, dbOperation.BlockNumber, dbOperation.BlockNumber)
				if len(requestTxs) > 1 {
					forkIds := bs.GetParentForkIds(beacon.ForkKey(dbOperation.ForkId))
					isParentFork := func(forkId uint64) bool {
						for _, parentForkId := range forkIds {
							if uint64(parentForkId) == forkId {
								return true
							}
						}
						return false
					}

					matchingTxs := []*dbtypes.WithdrawalRequestTx{}
					for _, tx := range requestTxs {
						if isParentFork(tx.ForkId) {
							matchingTxs = append(matchingTxs, tx)
						}
					}

					if len(matchingTxs) >= int(dbOperation.SlotIndex)+1 {
						combinedResult.Transaction = matchingTxs[dbOperation.SlotIndex]
						combinedResult.TransactionOrphaned = !bs.isCanonicalForkId(matchingTxs[dbOperation.SlotIndex].ForkId, canonicalForkIds)
					}

				} else if len(requestTxs) == 1 {
					combinedResult.Transaction = requestTxs[0]
					combinedResult.TransactionOrphaned = !bs.isCanonicalForkId(requestTxs[0].ForkId, canonicalForkIds)
				}
			}

			combinedResults = append(combinedResults, combinedResult)
		}

		// load tx details for withdrawal requests
		if len(requestTxDetailsFor) > 0 {
			for _, txDetails := range db.GetWithdrawalRequestTxsByTxHashes(ctx, requestTxDetailsFor) {
				for _, combinedResult := range combinedResults {
					if combinedResult.Request != nil && bytes.Equal(combinedResult.Request.TxHash, txDetails.TxHash) {
						combinedResult.Transaction = txDetails
						combinedResult.TransactionOrphaned = !bs.isCanonicalForkId(txDetails.ForkId, canonicalForkIds)
					}
				}
			}
		}
	}

	return combinedResults, totalPendingTxResults, totalReqResults
}

func (bs *ChainService) GetWithdrawalRequestOperationsByFilter(ctx context.Context, filter *dbtypes.WithdrawalRequestFilter, pageOffset uint64, pageSize uint32) ([]*dbtypes.WithdrawalRequest, uint64) {
	chainState := bs.consensusPool.GetChainState()
	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()

	canonicalForkIds := bs.GetCanonicalForkIds()

	// load most recent objects from indexer cache
	cachedMatches := make([]*dbtypes.WithdrawalRequest, 0)
	for slotIdx := int64(currentSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
		slot := uint64(slotIdx)
		blocks := bs.beaconIndexer.GetBlocksBySlot(phase0.Slot(slot))
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				isCanonical := bs.isCanonicalForkId(uint64(block.GetForkId()), canonicalForkIds)
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

				withdrawalRequests := block.GetDbWithdrawalRequests(bs.beaconIndexer, isCanonical)
				slice.Reverse(withdrawalRequests) // reverse as other datasources are ordered by descending block index too
				for idx, withdrawalRequest := range withdrawalRequests {
					if filter.MinIndex > 0 && (withdrawalRequest.ValidatorIndex == nil || *withdrawalRequest.ValidatorIndex < filter.MinIndex) {
						continue
					}
					if filter.MaxIndex > 0 && (withdrawalRequest.ValidatorIndex == nil || *withdrawalRequest.ValidatorIndex > filter.MaxIndex) {
						continue
					}
					if len(filter.SourceAddress) > 0 {
						if !bytes.Equal(withdrawalRequest.SourceAddress[:], filter.SourceAddress) {
							continue
						}
					}
					if len(filter.PublicKey) > 0 {
						if !bytes.Equal(withdrawalRequest.ValidatorPubkey[:], filter.PublicKey) {
							continue
						}
					}
					if filter.ValidatorName != "" {
						if withdrawalRequest.ValidatorIndex == nil {
							continue
						}
						validatorName := bs.validatorNames.GetValidatorName(*withdrawalRequest.ValidatorIndex)
						if !strings.Contains(validatorName, filter.ValidatorName) {
							continue
						}
					}

					cachedMatches = append(cachedMatches, withdrawalRequests[idx])
				}
			}
		}
	}

	resObjs := make([]*dbtypes.WithdrawalRequest, 0)
	resIdx := 0

	cachedStart := pageOffset
	cachedEnd := cachedStart + uint64(pageSize)
	cachedMatchesLen := uint64(len(cachedMatches))

	if cachedEnd <= cachedMatchesLen {
		resObjs = append(resObjs, cachedMatches[cachedStart:cachedEnd]...)
		resIdx += int(cachedEnd - cachedStart)
	} else if cachedStart < cachedMatchesLen {
		resObjs = append(resObjs, cachedMatches[cachedStart:]...)
		resIdx += len(cachedMatches) - int(cachedStart)
	}

	// load older objects from db
	var dbObjects []*dbtypes.WithdrawalRequest
	var dbCount uint64
	var err error

	if cachedEnd <= cachedMatchesLen {
		// all results from cache, just get result count from db
		_, dbCount, err = db.GetWithdrawalRequestsFiltered(ctx, 0, 1, canonicalForkIds, filter)
	} else {
		dbSliceStart := uint64(0)
		if cachedStart > cachedMatchesLen {
			dbSliceStart = cachedStart - cachedMatchesLen
		}

		dbSliceLimit := pageSize - uint32(resIdx)
		dbObjects, dbCount, err = db.GetWithdrawalRequestsFiltered(ctx, dbSliceStart, dbSliceLimit, canonicalForkIds, filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetWithdrawalRequestOperationsByFilter error: %v", err)
	} else {
		for idx, dbObject := range dbObjects {
			dbObjects[idx].Orphaned = !bs.isCanonicalForkId(dbObject.ForkId, canonicalForkIds)

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

type WithdrawalQueueEntry struct {
	ValidatorIndex          phase0.ValidatorIndex
	Validator               *v1.Validator
	ValidatorName           string
	WithdrawableEpoch       phase0.Epoch
	Amount                  phase0.Gwei
	EstimatedWithdrawalTime phase0.Slot
}

type WithdrawalQueueFilter struct {
	MinValidatorIndex *uint64
	MaxValidatorIndex *uint64
	ValidatorName     string
	PublicKey         []byte
}

func (bs *ChainService) GetWithdrawalQueueByFilter(ctx context.Context, filter *WithdrawalQueueFilter, pageOffset uint64, pageSize uint32) ([]*WithdrawalQueueEntry, uint64, phase0.Gwei) {
	chainState := bs.consensusPool.GetChainState()
	epochStats, epochStatsEpoch := bs.GetRecentEpochStats(nil)

	if epochStats == nil {
		return []*WithdrawalQueueEntry{}, 0, 0
	}

	var filterIndex *phase0.ValidatorIndex
	if len(filter.PublicKey) > 0 {
		if index, found := bs.beaconIndexer.GetValidatorIndexByPubkey(phase0.BLSPubKey(filter.PublicKey)); found {
			filterIndex = &index
		}
	}

	// First pass - collect all matching entries and validator indices
	queue := []*WithdrawalQueueEntry{}
	validatorIndexesMap := map[phase0.ValidatorIndex]bool{}
	totalAmount := phase0.Gwei(0)

	for _, pendingWithdrawal := range epochStats.PendingWithdrawals {
		// Apply filters
		if filter.MinValidatorIndex != nil && uint64(pendingWithdrawal.ValidatorIndex) < *filter.MinValidatorIndex {
			continue
		}
		if filter.MaxValidatorIndex != nil && uint64(pendingWithdrawal.ValidatorIndex) > *filter.MaxValidatorIndex {
			continue
		}
		if filterIndex != nil && pendingWithdrawal.ValidatorIndex != *filterIndex {
			continue
		}
		if filter.ValidatorName != "" {
			validatorName := bs.validatorNames.GetValidatorName(uint64(pendingWithdrawal.ValidatorIndex))
			if !strings.Contains(validatorName, filter.ValidatorName) {
				continue
			}
		}

		validatorIndexesMap[pendingWithdrawal.ValidatorIndex] = true
		totalAmount += pendingWithdrawal.Amount

		queue = append(queue, &WithdrawalQueueEntry{
			ValidatorIndex:    pendingWithdrawal.ValidatorIndex,
			ValidatorName:     bs.validatorNames.GetValidatorName(uint64(pendingWithdrawal.ValidatorIndex)),
			WithdrawableEpoch: pendingWithdrawal.WithdrawableEpoch,
			Amount:            pendingWithdrawal.Amount,
		})
	}

	validatorIndexes := make([]phase0.ValidatorIndex, 0, len(validatorIndexesMap))
	for index := range validatorIndexesMap {
		validatorIndexes = append(validatorIndexes, index)
	}

	if len(validatorIndexes) == 0 {
		return []*WithdrawalQueueEntry{}, 0, 0
	}

	// Bulk load all validators
	validators, _ := bs.GetFilteredValidatorSet(ctx, &dbtypes.ValidatorFilter{
		Indices: validatorIndexes,
	}, false)

	validatorsMap := map[phase0.ValidatorIndex]*v1.Validator{}
	for idx, validator := range validators {
		validatorsMap[validator.Index] = &validators[idx]
	}

	// Second pass - populate validator data and calculate withdrawal times
	specs := chainState.GetSpecs()
	currentSlot := chainState.EpochToSlot(epochStatsEpoch)
	processableWithdrawals := uint64(0)
	processableSlot := phase0.Slot(0)

	for _, entry := range queue {
		entry.Validator = validatorsMap[entry.ValidatorIndex]

		// Calculate estimated withdrawal time
		minWithdrawalSlot := chainState.EpochToSlot(entry.WithdrawableEpoch)
		if minWithdrawalSlot < currentSlot {
			minWithdrawalSlot = currentSlot
		}
		if processableSlot < minWithdrawalSlot {
			withdrawnCount := uint64(minWithdrawalSlot-processableSlot) * specs.MaxPendingPartialsPerWithdrawalsSweep
			if withdrawnCount >= processableWithdrawals {
				processableWithdrawals = 0
				processableSlot = minWithdrawalSlot
			} else {
				processableWithdrawals -= withdrawnCount
				processableSlot = minWithdrawalSlot
			}
		}

		if entry.Validator != nil && entry.Validator.Status == v1.ValidatorStateActiveOngoing {
			processableWithdrawals++
		}

		entry.EstimatedWithdrawalTime = minWithdrawalSlot + phase0.Slot(processableWithdrawals/specs.MaxPendingPartialsPerWithdrawalsSweep)
	}

	totalCount := uint64(len(queue))

	// Apply pagination
	start := pageOffset
	end := pageOffset + uint64(pageSize)

	if start >= totalCount {
		return []*WithdrawalQueueEntry{}, totalCount, totalAmount
	}

	if end > totalCount {
		end = totalCount
	}

	return queue[start:end], totalCount, totalAmount
}
