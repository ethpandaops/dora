package services

import (
	"bytes"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/prysmaticlabs/prysm/v5/container/slice"
	"github.com/sirupsen/logrus"
)

type CombinedDepositRequest struct {
	Request             *dbtypes.Deposit
	RequestOrphaned     bool
	Transaction         *dbtypes.DepositTx
	TransactionOrphaned bool
	IsQueued            bool
	QueueEntry          *IndexedDepositQueue
}

type CombinedDepositRequestFilter struct {
	Filter *dbtypes.DepositTxFilter
}

func (ccr *CombinedDepositRequest) SourceAddress() []byte {
	if ccr.Transaction != nil {
		return ccr.Transaction.TxSender
	}
	return nil
}

func (ccr *CombinedDepositRequest) DepositIndex() uint64 {
	if ccr.Request != nil && ccr.Request.Index != nil {
		return *ccr.Request.Index
	}
	if ccr.Transaction != nil {
		return ccr.Transaction.Index
	}
	return math.MaxUint64
}

func (ccr *CombinedDepositRequest) PublicKey() []byte {
	if ccr.Request != nil {
		return ccr.Request.PublicKey
	}
	if ccr.Transaction != nil {
		return ccr.Transaction.PublicKey
	}
	return nil
}

func (ccr *CombinedDepositRequest) WithdrawalCredentials() []byte {
	if ccr.Request != nil {
		return ccr.Request.WithdrawalCredentials
	}
	if ccr.Transaction != nil {
		return ccr.Transaction.WithdrawalCredentials
	}
	return nil
}

func (ccr *CombinedDepositRequest) Amount() uint64 {
	if ccr.Request != nil {
		return ccr.Request.Amount
	}
	if ccr.Transaction != nil {
		return ccr.Transaction.Amount
	}
	return 0
}

func (bs *ChainService) GetDepositRequestsByFilter(filter *CombinedDepositRequestFilter, pageOffset uint64, pageSize uint32) ([]*CombinedDepositRequest, uint64) {
	totalReqResults := uint64(0)

	combinedResults := make([]*CombinedDepositRequest, 0)
	canonicalForkIds := bs.GetCanonicalForkIds()

	pendingDepositPositions := map[uint64]*IndexedDepositQueue{}

	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	if canonicalHead != nil {
		indexedDepositQueue := bs.GetIndexedDepositQueue(canonicalHead)
		if indexedDepositQueue != nil {
			for _, queueEntry := range indexedDepositQueue {
				depositIndex := queueEntry.DepositIndex
				if depositIndex != nil {
					pendingDepositPositions[*depositIndex] = queueEntry
				}
			}
		}
	}

	/*
		if filter.Request != 2 {
			dbTransactions, totalDbTransactions, _ := db.GetDepositTxsFiltered(pageOffset, pageSize, canonicalForkIds, filter.Filter)
			totalPendingTxResults = totalDbTransactions

			for _, consolidation := range dbTransactions {
				combinedResults = append(combinedResults, &CombinedDepositRequest{
					Transaction:         consolidation,
					TransactionOrphaned: !bs.isCanonicalForkId(consolidation.ForkId, canonicalForkIds),
				})
			}
		}
	*/

	dbOperations := []*dbtypes.DepositWithTx{}

	operationFilter := &dbtypes.DepositFilter{
		MinIndex:      filter.Filter.MinIndex,
		MaxIndex:      filter.Filter.MaxIndex,
		PublicKey:     filter.Filter.PublicKey,
		ValidatorName: filter.Filter.ValidatorName,
		MinAmount:     filter.Filter.MinAmount,
		MaxAmount:     filter.Filter.MaxAmount,
		WithOrphaned:  filter.Filter.WithOrphaned,
	}

	txFilter := &dbtypes.DepositTxFilter{
		Address:       filter.Filter.Address,
		TargetAddress: filter.Filter.TargetAddress,
		WithValid:     filter.Filter.WithValid,
	}

	dbOperations, totalReqResults = bs.GetDepositOperationsByFilter(operationFilter, txFilter, pageOffset, pageSize)

	for _, dbOperation := range dbOperations {
		if len(combinedResults) >= int(pageSize) {
			break
		}

		combinedResult := &CombinedDepositRequest{
			Request:         &dbOperation.Deposit,
			RequestOrphaned: !bs.isCanonicalForkId(dbOperation.ForkId, canonicalForkIds),
		}

		if queueEntry, ok := pendingDepositPositions[*dbOperation.Index]; ok {
			combinedResult.IsQueued = true
			combinedResult.QueueEntry = queueEntry
		}

		if dbOperation.BlockNumber != nil {
			combinedResult.Transaction = &dbtypes.DepositTx{
				Index:                 *dbOperation.Index,
				BlockNumber:           *dbOperation.BlockNumber,
				BlockTime:             *dbOperation.BlockTime,
				BlockRoot:             dbOperation.BlockRoot,
				PublicKey:             dbOperation.PublicKey,
				WithdrawalCredentials: dbOperation.WithdrawalCredentials,
				Amount:                dbOperation.Amount,
				ForkId:                dbOperation.ForkId,
				ValidSignature:        *dbOperation.ValidSignature,
				TxHash:                dbOperation.TxHash,
				TxSender:              dbOperation.TxSender,
				TxTarget:              dbOperation.TxTarget,
			}
			combinedResult.TransactionOrphaned = combinedResult.RequestOrphaned
		}

		combinedResults = append(combinedResults, combinedResult)
	}

	return combinedResults, totalReqResults
}

func (bs *ChainService) GetDepositOperationsByFilter(filter *dbtypes.DepositFilter, txFilter *dbtypes.DepositTxFilter, pageOffset uint64, pageSize uint32) ([]*dbtypes.DepositWithTx, uint64) {
	chainState := bs.consensusPool.GetChainState()
	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()

	canonicalForkIds := bs.GetCanonicalForkIds()

	// load most recent objects from indexer cache
	cachedMatches := make([]*dbtypes.DepositWithTx, 0)
	depositIndexCache := make(map[phase0.Root]uint64)
	for slotIdx := idxMinSlot; slotIdx <= currentSlot; slotIdx++ {
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

				// get deposit index
				var firstDepositIndex uint64
				var hasFirstDepositIndex bool

				parentRoot := block.GetParentRoot()
				if parentRoot != nil {
					firstDepositIndex, hasFirstDepositIndex = depositIndexCache[*parentRoot]
					if !hasFirstDepositIndex {
						epochStats := bs.beaconIndexer.GetEpochStatsByBlockRoot(chainState.EpochOfSlot(block.Slot), *parentRoot)
						if epochStats != nil {
							epochStatsParentRoot := epochStats.GetDependentRoot()
							if bytes.Equal(epochStatsParentRoot[:], parentRoot[:]) {
								values := epochStats.GetValues(false)
								if values != nil {
									firstDepositIndex = values.FirstDepositIndex
								}
							}
						}
					}
				}

				var deposits []*dbtypes.Deposit
				if hasFirstDepositIndex {
					depositIndex := firstDepositIndex
					deposits = block.GetDbDeposits(bs.beaconIndexer, &depositIndex, isCanonical)
					depositIndexCache[block.Root] = depositIndex
				} else {
					deposits = block.GetDbDeposits(bs.beaconIndexer, nil, isCanonical)
				}

				for _, deposit := range deposits {
					if filter.MinIndex > 0 && (deposit.Index == nil || *deposit.Index < filter.MinIndex) {
						continue
					}
					if filter.MaxIndex > 0 && (deposit.Index == nil || *deposit.Index > filter.MaxIndex) {
						continue
					}
					if len(filter.PublicKey) > 0 {
						if !bytes.Equal(deposit.PublicKey[:], filter.PublicKey) {
							continue
						}
					}
					if filter.ValidatorName != "" {
						validatorIndex, validatorFound := bs.beaconIndexer.GetValidatorIndexByPubkey(phase0.BLSPubKey(deposit.PublicKey))
						if !validatorFound {
							continue
						}
						validatorName := bs.validatorNames.GetValidatorName(uint64(validatorIndex))
						if !strings.Contains(validatorName, filter.ValidatorName) {
							continue
						}
					}

					if filter.MinAmount > 0 && deposit.Amount < filter.MinAmount {
						continue
					}
					if filter.MaxAmount > 0 && deposit.Amount > filter.MaxAmount {
						continue
					}

					depositWithTx := &dbtypes.DepositWithTx{
						Deposit: *deposit,
					}

					cachedMatches = append(cachedMatches, depositWithTx)
				}
			}
		}
	}

	if txFilter != nil {
		detailsForIndex := make([]uint64, 0)
		for _, depositWithTx := range cachedMatches {
			if depositWithTx.Index != nil {
				detailsForIndex = append(detailsForIndex, *depositWithTx.Index)
			}
		}

		for _, txDetail := range db.GetDepositTxsByIndexes(detailsForIndex) {
			for _, depositWithTx := range cachedMatches {
				if depositWithTx.Index != nil && *depositWithTx.Index == txDetail.Index && bytes.Equal(depositWithTx.PublicKey[:], txDetail.PublicKey[:]) {
					depositWithTx.BlockNumber = &txDetail.BlockNumber
					depositWithTx.BlockTime = &txDetail.BlockTime
					depositWithTx.BlockRoot = txDetail.BlockRoot
					depositWithTx.ValidSignature = &txDetail.ValidSignature
					depositWithTx.TxHash = txDetail.TxHash
					depositWithTx.TxSender = txDetail.TxSender
					depositWithTx.TxTarget = txDetail.TxTarget
				}
			}
		}

		filteredMatches := make([]*dbtypes.DepositWithTx, 0, len(cachedMatches))
		for _, depositWithTx := range cachedMatches {
			if txFilter.WithValid == 0 && (depositWithTx.ValidSignature == nil || (*depositWithTx.ValidSignature != 1 && *depositWithTx.ValidSignature != 2)) {
				continue
			} else if txFilter.WithValid == 2 && (depositWithTx.ValidSignature == nil || (*depositWithTx.ValidSignature != 0)) {
				continue
			}

			if len(txFilter.Address) > 0 && (len(depositWithTx.TxSender) == 0 || !bytes.Equal(depositWithTx.TxSender[:], txFilter.Address)) {
				continue
			}

			if len(txFilter.TargetAddress) > 0 && (len(depositWithTx.TxTarget) == 0 || !bytes.Equal(depositWithTx.TxTarget[:], txFilter.TargetAddress)) {
				continue
			}

			if len(txFilter.WithdrawalAddress) > 0 {
				wdcreds := depositWithTx.WithdrawalCredentials
				if wdcreds[0] != 0x01 && wdcreds[0] != 0x02 {
					continue
				}

				if !bytes.Equal(wdcreds[12:], txFilter.WithdrawalAddress) {
					continue
				}
			}

			filteredMatches = append(filteredMatches, depositWithTx)
		}
	}

	slice.Reverse(cachedMatches) // reverse as other datasources are ordered by descending block index too

	resObjs := make([]*dbtypes.DepositWithTx, 0)
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
	var dbObjects []*dbtypes.DepositWithTx
	var dbCount uint64
	var err error

	if cachedEnd <= cachedMatchesLen {
		// all results from cache, just get result count from db
		_, dbCount, err = db.GetDepositsFiltered(0, 1, canonicalForkIds, filter, txFilter)
	} else {
		dbSliceStart := uint64(0)
		if cachedStart > cachedMatchesLen {
			dbSliceStart = cachedStart - cachedMatchesLen
		}

		dbSliceLimit := pageSize - uint32(resIdx)
		dbObjects, dbCount, err = db.GetDepositsFiltered(dbSliceStart, dbSliceLimit, canonicalForkIds, filter, txFilter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetDepositOperationsByFilter error: %v", err)
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

type IndexedDepositQueue struct {
	QueuePos       uint64
	DepositIndex   *uint64
	TimeEstimate   time.Time
	PendingDeposit *electra.PendingDeposit
}

func (bs *ChainService) GetIndexedDepositQueue(headBlock *beacon.Block) []*IndexedDepositQueue {
	queueBlockRoot, queue := bs.beaconIndexer.GetLatestDepositQueueByBlockRoot(headBlock.Root)
	lastIncludedDeposit := bs.getLastIncludedDeposit(queueBlockRoot)
	if lastIncludedDeposit == nil || lastIncludedDeposit.Index == nil {
		return nil
	}

	chainState := bs.consensusPool.GetChainState()
	epochStats := bs.beaconIndexer.GetEpochStatsByBlockRoot(chainState.EpochOfSlot(headBlock.Slot), headBlock.Root)
	totalActiveBalance := phase0.Gwei(0)
	if epochStats != nil {
		values := epochStats.GetValues(false)
		if values != nil {
			totalActiveBalance = values.EffectiveBalance
		}
	}

	queueLen := len(queue)
	indexedQueue := make([]*IndexedDepositQueue, queueLen)
	depositIndex := *lastIncludedDeposit.Index

	for idx := queueLen - 1; idx >= 0; idx-- {
		deposit := queue[idx]
		queueEntry := &IndexedDepositQueue{
			PendingDeposit: deposit,
		}

		if deposit.Slot > 0 {
			if depositIndex == 0 {
				// something is bad, return nil
				return nil
			}

			depositIndexCopy := uint64(depositIndex)
			queueEntry.DepositIndex = &depositIndexCopy
			depositIndex--
		}

		indexedQueue[idx] = queueEntry
	}

	totalDepositAmount := phase0.Gwei(0)
	activationExitChurnLimit := chainState.GetActivationExitChurnLimit(uint64(totalActiveBalance))
	currentEpoch := chainState.CurrentEpoch()

	for idx, queueEntry := range indexedQueue {
		indexedQueue[idx].QueuePos = uint64(idx)

		if totalActiveBalance > 0 {
			estQueueEpochDuration := phase0.Epoch(uint64(totalDepositAmount) / activationExitChurnLimit)
			indexedQueue[idx].TimeEstimate = chainState.EpochToTime(currentEpoch + estQueueEpochDuration)
		}

		totalDepositAmount += phase0.Gwei(queueEntry.PendingDeposit.Amount)
	}

	if !bytes.Equal(indexedQueue[len(indexedQueue)-1].PendingDeposit.Pubkey[:], lastIncludedDeposit.PublicKey[:]) {
		// something is bad, return nil
		return nil
	}

	return indexedQueue
}

func (bs *ChainService) getLastIncludedDeposit(headRoot phase0.Root) *dbtypes.Deposit {
	headBlock := bs.beaconIndexer.GetBlockByRoot(headRoot)
	if headBlock == nil {
		return nil
	}

	canonicalForkIds := bs.beaconIndexer.GetParentForkIds(headBlock.GetForkId())
	canonicalForkIdsUint64 := make([]uint64, len(canonicalForkIds))
	for idx, forkId := range canonicalForkIds {
		canonicalForkIdsUint64[idx] = uint64(forkId)
	}

	chainState := bs.consensusPool.GetChainState()
	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)

	// load most recent objects from indexer cache
	var lastDeposits []*dbtypes.Deposit
	depositIndexCache := make(map[phase0.Root]uint64)
	for slotIdx := idxMinSlot; slotIdx <= headBlock.Slot; slotIdx++ {
		slot := uint64(slotIdx)
		blocks := bs.beaconIndexer.GetBlocksBySlot(phase0.Slot(slot))
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				isCanonical := slices.Contains(canonicalForkIds, block.GetForkId())
				if !isCanonical {
					continue
				}

				// get deposit index
				var firstDepositIndex uint64
				var hasFirstDepositIndex bool

				parentRoot := block.GetParentRoot()
				if parentRoot != nil {
					firstDepositIndex, hasFirstDepositIndex = depositIndexCache[*parentRoot]
					if !hasFirstDepositIndex {
						epochStats := bs.beaconIndexer.GetEpochStatsByBlockRoot(chainState.EpochOfSlot(block.Slot), *parentRoot)
						if epochStats != nil {
							epochStatsParentRoot := epochStats.GetDependentRoot()
							if bytes.Equal(epochStatsParentRoot[:], parentRoot[:]) {
								values := epochStats.GetValues(false)
								if values != nil {
									hasFirstDepositIndex = true
									firstDepositIndex = values.FirstDepositIndex
								}
							}
						}
					}
				}

				var deposits []*dbtypes.Deposit
				if hasFirstDepositIndex {
					depositIndex := firstDepositIndex
					deposits = block.GetDbDeposits(bs.beaconIndexer, &depositIndex, isCanonical)
					depositIndexCache[block.Root] = depositIndex
				} else {
					deposits = block.GetDbDeposits(bs.beaconIndexer, nil, isCanonical)
				}

				if len(deposits) > 0 {
					lastDeposits = deposits
				}
			}
		}
	}

	if len(lastDeposits) > 0 {
		lastDeposit := lastDeposits[len(lastDeposits)-1]
		return lastDeposit
	} else {
		// get last deposit from db
		dbDeposits, _, err := db.GetDepositsFiltered(0, 1, canonicalForkIdsUint64, &dbtypes.DepositFilter{
			WithOrphaned: 0,
		}, nil)
		if err != nil {
			logrus.Warnf("ChainService.getLastIncludedDeposit error: %v", err)
		} else {
			return &dbDeposits[0].Deposit
		}
	}

	return nil
}
