package services

import (
	"bytes"
	"context"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/prysmaticlabs/prysm/v5/container/slice"
	"github.com/sirupsen/logrus"
)

type CombinedDepositRequest struct {
	Request             *dbtypes.Deposit
	RequestOrphaned     bool
	Transaction         *dbtypes.DepositTx
	TransactionOrphaned bool
	IsQueued            bool
	QueueEntry          *IndexedDepositQueueEntry
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

func (bs *ChainService) GetDepositRequestsByFilter(ctx context.Context, filter *CombinedDepositRequestFilter, pageOffset uint64, pageSize uint32) ([]*CombinedDepositRequest, uint64) {
	combinedResults := make([]*CombinedDepositRequest, 0)
	canonicalForkIds := bs.GetCanonicalForkIds()

	pendingDepositPositions := map[uint64]*IndexedDepositQueueEntry{}

	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	if canonicalHead != nil {
		indexedQueue := bs.GetIndexedDepositQueue(ctx, canonicalHead)
		if indexedQueue != nil {
			for _, queueEntry := range indexedQueue.Queue {
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
		Address:             filter.Filter.Address,
		TargetAddress:       filter.Filter.TargetAddress,
		WithValid:           filter.Filter.WithValid,
		WithdrawalCredTypes: filter.Filter.WithdrawalCredTypes,
	}

	dbOperations, totalReqResults := bs.GetDepositOperationsByFilter(ctx, operationFilter, txFilter, pageOffset, pageSize)

	for _, dbOperation := range dbOperations {
		if len(combinedResults) >= int(pageSize) {
			break
		}

		combinedResult := &CombinedDepositRequest{
			Request:         &dbOperation.Deposit,
			RequestOrphaned: !bs.isCanonicalForkId(dbOperation.ForkId, canonicalForkIds),
		}

		if dbOperation.BlockNumber != nil {
			combinedResult.Transaction = &dbtypes.DepositTx{
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

		if dbOperation.BlockNumber != nil && dbOperation.Index != nil {
			combinedResult.Transaction.Index = *dbOperation.Index
			if queueEntry, ok := pendingDepositPositions[*dbOperation.Index]; ok {
				combinedResult.IsQueued = true
				combinedResult.QueueEntry = queueEntry
			}
		}

		combinedResults = append(combinedResults, combinedResult)
	}

	return combinedResults, totalReqResults
}

func (bs *ChainService) GetDepositOperationsByFilter(ctx context.Context, filter *dbtypes.DepositFilter, txFilter *dbtypes.DepositTxFilter, pageOffset uint64, pageSize uint32) ([]*dbtypes.DepositWithTx, uint64) {
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

		for _, txDetail := range db.GetDepositTxsByIndexes(ctx, detailsForIndex) {
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
				// 0x01 = ETH1, 0x02 = compounding, 0x03 = builder deposit
				if wdcreds[0] != 0x01 && wdcreds[0] != 0x02 && wdcreds[0] != 0x03 {
					continue
				}

				if !bytes.Equal(wdcreds[12:], txFilter.WithdrawalAddress) {
					continue
				}
			}

			filteredMatches = append(filteredMatches, depositWithTx)
		}

		cachedMatches = filteredMatches
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
		_, dbCount, err = db.GetDepositsFiltered(ctx, 0, 1, canonicalForkIds, filter, txFilter)
	} else {
		dbSliceStart := uint64(0)
		if cachedStart > cachedMatchesLen {
			dbSliceStart = cachedStart - cachedMatchesLen
		}

		dbSliceLimit := pageSize - uint32(resIdx)
		dbObjects, dbCount, err = db.GetDepositsFiltered(ctx, dbSliceStart, dbSliceLimit, canonicalForkIds, filter, txFilter)
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

type IndexedDepositQueueEntry struct {
	QueuePos       uint64
	DepositIndex   *uint64
	EpochEstimate  phase0.Epoch
	PendingDeposit *electra.PendingDeposit
}

type IndexedDepositQueue struct {
	Queue           []*IndexedDepositQueueEntry
	TotalNew        uint64
	TotalGwei       phase0.Gwei
	QueueEstimation phase0.Epoch
}

func (bs *ChainService) GetIndexedDepositQueue(ctx context.Context, headBlock *beacon.Block) *IndexedDepositQueue {
	forkId := headBlock.GetForkId()
	queueBlockRoot, queueSlot, queueBalance, queue := bs.beaconIndexer.GetLatestDepositQueueByBlockRoot(headBlock.Root)

	indexedQueue := &IndexedDepositQueue{
		Queue: make([]*IndexedDepositQueueEntry, len(queue)),
	}
	for idx := range queue {
		indexedQueue.Queue[idx] = &IndexedDepositQueueEntry{
			PendingDeposit: queue[idx],
		}
	}
	if len(queue) == 0 {
		return indexedQueue
	}

	// Best-effort deposit-index assignment: the queue itself (pending_deposits) always
	// comes from the beacon state, but the EL deposit indexes are derived by anchoring
	// the tail of the queue to the last included deposit and counting backwards. If the
	// anchor can't be resolved (no included deposit found yet), we still return the full
	// queue — entries are simply left without a DepositIndex instead of hiding everything.
	//
	// Matching is by deposit index AND pubkey: the index sequence is not contiguous in
	// the queue. Post-Gloas builder (0x03) deposits get an EL deposit index but are
	// onboarded as builders and never enter the queue, leaving gaps; and 0x01->0x02
	// compounding-switch deposits are synthesized during state transition with no EL
	// deposit at all. We skip the synthetic ones entirely and, for real ones, skip over
	// index gaps until the pubkey at the candidate index matches the queue entry.
	lastIncludedDeposit, includedPubkeyByIndex := bs.getRecentIncludedDeposits(ctx, queueBlockRoot)
	if lastIncludedDeposit != nil && lastIncludedDeposit.Index != nil {
		candidate := int64(*lastIncludedDeposit.Index)
		for idx := len(queue) - 1; idx >= 0; idx-- {
			deposit := queue[idx]
			if isSyntheticPendingDeposit(deposit) {
				continue // compounding-switch/excess-balance deposit, no EL deposit index
			}
			if candidate < 0 {
				break // ran out of indexes; leave earlier entries unindexed
			}
			// Skip indexes that belong to deposits not present in the queue (builder
			// gaps). A known pubkey that does not match means this index is a gap.
			for candidate >= 0 {
				pubkey, known := includedPubkeyByIndex[uint64(candidate)]
				if known && pubkey != deposit.Pubkey {
					candidate--
					continue
				}
				break // matches, or unknown (beyond cache) -> accept and fall back to contiguous
			}
			if candidate < 0 {
				break
			}
			depositIndexCopy := uint64(candidate)
			indexedQueue.Queue[idx].DepositIndex = &depositIndexCopy
			candidate--
		}
	}

	epochStatsValues, _ := bs.GetRecentEpochStats(&forkId)
	totalActiveBalance := phase0.Gwei(0)
	if epochStatsValues != nil {
		totalActiveBalance = epochStatsValues.ActiveBalance
	}

	queueLen := len(queue)
	newValidators := make(map[phase0.BLSPubKey]interface{})

	var newValidatorsMutex sync.Mutex

	type workItem struct {
		idx   int
		entry *IndexedDepositQueueEntry
	}

	workChan := make(chan workItem, queueLen)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workChan {
				// A pubkey counts as a new validator unless it already maps to a real
				// on-chain validator. A projected validator (derived from this very
				// queue) resolves via GetValidatorIndexByPubkey too, so it must still
				// be counted as new — otherwise TotalNew undercounts.
				idx, found := bs.beaconIndexer.GetValidatorIndexByPubkey(work.entry.PendingDeposit.Pubkey)
				if !found || bs.IsProjectedValidatorIndex(idx) {
					newValidatorsMutex.Lock()
					_, isNew := newValidators[work.entry.PendingDeposit.Pubkey]
					if !isNew {
						newValidators[work.entry.PendingDeposit.Pubkey] = nil
						indexedQueue.TotalNew++
					}
					newValidatorsMutex.Unlock()
				}
			}
		}()
	}

	for idx, entry := range indexedQueue.Queue {
		workChan <- workItem{idx: idx, entry: entry}
	}
	close(workChan)

	wg.Wait()

	chainState := bs.consensusPool.GetChainState()
	specs := chainState.GetSpecs()

	activationExitChurnLimit := phase0.Gwei(chainState.GetActivationExitChurnLimit(uint64(totalActiveBalance)))
	queueEpoch := chainState.EpochOfSlot(queueSlot)

	maxPendingDepositsPerEpoch := specs.MaxPendingDepositsPerEpoch
	if maxPendingDepositsPerEpoch == 0 {
		maxPendingDepositsPerEpoch = 16
	}

	queueEpoch++
	queueBalance += activationExitChurnLimit
	currentEpochCount := uint64(0)

	for idx, queueEntry := range indexedQueue.Queue {
		queueEntry.QueuePos = uint64(idx)

		if totalActiveBalance > 0 {
			if currentEpochCount >= maxPendingDepositsPerEpoch {
				queueEpoch++
				queueBalance = activationExitChurnLimit
				currentEpochCount = 0
			}

			for queueBalance < queueEntry.PendingDeposit.Amount {
				queueEpoch++
				queueBalance += activationExitChurnLimit
				currentEpochCount = 0
			}

			queueEntry.EpochEstimate = queueEpoch
			currentEpochCount++
			queueBalance -= queueEntry.PendingDeposit.Amount
		}

		indexedQueue.TotalGwei += queueEntry.PendingDeposit.Amount
	}

	indexedQueue.QueueEstimation = queueEpoch

	return indexedQueue
}

// isSyntheticPendingDeposit reports whether a pending deposit was synthesized during
// state transition (queue_excess_active_balance, e.g. a 0x01->0x02 compounding switch)
// rather than created from an EL deposit. Such deposits carry the G2 point-at-infinity
// signature (0xc0 followed by zeros) and have no corresponding EL deposit index.
func isSyntheticPendingDeposit(deposit *electra.PendingDeposit) bool {
	if deposit.Signature[0] != 0xc0 {
		return false
	}
	for _, b := range deposit.Signature[1:] {
		if b != 0 {
			return false
		}
	}
	return true
}

// getRecentIncludedDeposits returns the most recent queue-eligible included deposit —
// the anchor used to align EL deposit indexes to the pending_deposits queue — together
// with a map of EL deposit index -> pubkey for the recent included deposits.
//
// The map intentionally includes ALL included deposits, even post-Gloas builder (0x03)
// deposits that are onboarded as builders and never enter the queue, so callers can
// detect the index gaps those deposits leave and match queue entries across them by
// pubkey. The anchor, by contrast, is the last deposit that actually enters the queue
// (builder deposits are excluded only once Gloas is active).
func (bs *ChainService) getRecentIncludedDeposits(ctx context.Context, headRoot phase0.Root) (*dbtypes.Deposit, map[uint64]phase0.BLSPubKey) {
	indexPubkeys := make(map[uint64]phase0.BLSPubKey)

	headBlock := bs.beaconIndexer.GetBlockByRoot(headRoot)
	if headBlock == nil {
		return nil, indexPubkeys
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
	var lastQueued *dbtypes.Deposit
	depositIndexCache := make(map[phase0.Root]uint64)
	for slotIdx := idxMinSlot; slotIdx <= headBlock.Slot; slotIdx++ {
		blocks := bs.beaconIndexer.GetBlocksBySlot(phase0.Slot(uint64(slotIdx)))
		for bidx := 0; bidx < len(blocks); bidx++ {
			block := blocks[bidx]
			isCanonical := slices.Contains(canonicalForkIds, block.GetForkId())
			if !isCanonical {
				continue
			}

			// resolve the first deposit index of this block from its parent epoch stats
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
							if values := epochStats.GetValues(false); values != nil {
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

			// Builder deposits (0x03) enter the queue before Gloas (they become
			// validators) but are onboarded as builders once Gloas is active and then
			// skip the queue, leaving gaps in the index sequence.
			skipBuilderDeposits := chainState.IsEip7732Enabled(chainState.EpochOfSlot(block.Slot))
			for _, deposit := range deposits {
				if deposit.Index != nil {
					indexPubkeys[*deposit.Index] = phase0.BLSPubKey(deposit.PublicKey)
				}
				if skipBuilderDeposits && len(deposit.WithdrawalCredentials) > 0 && deposit.WithdrawalCredentials[0] == 0x03 {
					continue // onboarded as a builder; not in the queue, not a valid anchor
				}
				lastQueued = deposit
			}
		}
	}

	if lastQueued == nil {
		// no included deposit in cache, fall back to the last canonical one in the db
		dbDeposits, _, err := db.GetDepositsFiltered(ctx, 0, 1, canonicalForkIdsUint64, &dbtypes.DepositFilter{
			WithOrphaned: 0,
		}, nil)
		if err != nil {
			logrus.Warnf("ChainService.getRecentIncludedDeposits error: %v", err)
		} else if len(dbDeposits) > 0 {
			lastQueued = &dbDeposits[0].Deposit
		}
	}

	return lastQueued, indexPubkeys
}

type QueuedDepositFilter struct {
	MinIndex          uint64
	MaxIndex          uint64
	NoIndex           bool
	PublicKey         []byte
	WithdrawalAddress []byte
	WithdrawalCreds   []byte
	MinAmount         uint64
	MaxAmount         uint64
}

func (bs *ChainService) GetFilteredQueuedDeposits(ctx context.Context, filter *QueuedDepositFilter) []*IndexedDepositQueueEntry {
	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	if canonicalHead == nil {
		return nil
	}

	queue := bs.GetIndexedDepositQueue(ctx, canonicalHead)
	if queue == nil {
		return nil
	}

	// Filter queue entries based on criteria
	filteredQueue := make([]*IndexedDepositQueueEntry, 0)
	for _, entry := range queue.Queue {
		if filter.MinIndex > 0 && (entry.DepositIndex == nil || *entry.DepositIndex < filter.MinIndex) {
			continue
		}
		if filter.MaxIndex > 0 && (entry.DepositIndex == nil || *entry.DepositIndex > filter.MaxIndex) {
			continue
		}
		if filter.NoIndex && entry.DepositIndex != nil {
			continue
		}
		if len(filter.PublicKey) > 0 && !bytes.Equal(filter.PublicKey, entry.PendingDeposit.Pubkey[:]) {
			continue
		}
		if len(filter.WithdrawalAddress) > 0 {
			wdcreds := entry.PendingDeposit.WithdrawalCredentials[:]
			if wdcreds[0] != 0x01 && wdcreds[0] != 0x02 {
				continue
			}
			if !bytes.Equal(wdcreds[12:], filter.WithdrawalAddress) {
				continue
			}
		}
		if len(filter.WithdrawalCreds) > 0 && !bytes.Equal(entry.PendingDeposit.WithdrawalCredentials[:], filter.WithdrawalCreds) {
			continue
		}
		if filter.MinAmount > 0 && uint64(entry.PendingDeposit.Amount) < filter.MinAmount {
			continue
		}
		if filter.MaxAmount > 0 && uint64(entry.PendingDeposit.Amount) > filter.MaxAmount {
			continue
		}

		filteredQueue = append(filteredQueue, entry)
	}

	return filteredQueue
}
