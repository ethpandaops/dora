package services

import (
	"bytes"
	"context"
	"fmt"
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

// ResolveValidatorName returns the display name for the deposit's validator, resolved
// at the deposit's inclusion slot (or the deposit transaction's EL block time while the
// deposit is not included yet).
func (ccr *CombinedDepositRequest) ResolveValidatorName(bs *ChainService, index uint64) string {
	if ccr.Request != nil {
		return bs.GetValidatorNameAt(index, phase0.Slot(ccr.Request.SlotNumber))
	}
	if ccr.Transaction != nil {
		return bs.GetValidatorNameAtTime(index, int64(ccr.Transaction.BlockTime))
	}
	return bs.GetValidatorName(index)
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
				// 0x01 = ETH1, 0x02 = compounding, 0xB0 = builder deposit
				if wdcreds[0] != 0x01 && wdcreds[0] != 0x02 && wdcreds[0] != 0xB0 {
					continue
				}

				if !bytes.Equal(wdcreds[12:], txFilter.WithdrawalAddress) {
					continue
				}
			}

			if len(txFilter.WithdrawalCredTypes) > 0 {
				wdcreds := depositWithTx.WithdrawalCredentials
				if len(wdcreds) == 0 || !slices.Contains(txFilter.WithdrawalCredTypes, wdcreds[0]) {
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
	// Postponed marks a deposit that process_pending_deposits reorders to the back of
	// the queue (its validator is exiting), so it no longer follows the queue's
	// slot<->index order and is resolved by slot rather than by position. Its
	// EpochEstimate is left unset as it does not flow through the normal churn queue.
	Postponed bool
}

type IndexedDepositQueue struct {
	Queue           []*IndexedDepositQueueEntry
	TotalNew        uint64
	TotalGwei       phase0.Gwei
	QueueEstimation phase0.Epoch

	// LastIncludedDepositIndex is the EL index of the most recent deposit included on chain as of
	// the queue snapshot (nil if unknown). Deposits with a higher index were included after the
	// snapshot (e.g. in the current epoch) and are not yet reflected in the queue; deposits with a
	// lower index that are absent from the queue have already been applied.
	LastIncludedDepositIndex *uint64

	// churn-simulation residual, captured after estimating every queued deposit, so a
	// hypothetical deposit appended to the tail can be projected (see EstimateAppendedDepositEpoch).
	churnEpoch                 phase0.Epoch
	churnBalance               phase0.Gwei
	churnEpochCount            uint64
	activationExitChurnLimit   phase0.Gwei
	maxPendingDepositsPerEpoch uint64
	totalActiveBalance         phase0.Gwei
}

// EstimateAppendedDepositEpoch projects the epoch in which a deposit of the given amount would be
// processed by process_pending_deposits if it were appended to the tail of the queue right now. It
// continues the same churn simulation used for the existing entries from its residual state. It
// returns 0 (unknown) when the churn parameters are unavailable (e.g. no active balance yet).
func (q *IndexedDepositQueue) EstimateAppendedDepositEpoch(amount phase0.Gwei) phase0.Epoch {
	if q.totalActiveBalance == 0 || q.activationExitChurnLimit == 0 {
		return 0
	}

	queueEpoch := q.churnEpoch
	queueBalance := q.churnBalance

	if q.maxPendingDepositsPerEpoch > 0 && q.churnEpochCount >= q.maxPendingDepositsPerEpoch {
		queueEpoch++
		queueBalance = q.activationExitChurnLimit
	}
	for queueBalance < amount {
		queueEpoch++
		queueBalance += q.activationExitChurnLimit
	}

	return queueEpoch
}

func (bs *ChainService) GetIndexedDepositQueue(ctx context.Context, headBlock *beacon.Block) *IndexedDepositQueue {
	if headBlock == nil {
		return nil
	}

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
	// The anchor (most recent included deposit) is resolved unconditionally — even for an empty
	// queue — so callers can tell already-applied deposits from deposits included after the snapshot.
	lastIncludedDeposit := bs.getRecentIncludedDeposits(ctx, queueBlockRoot)
	if lastIncludedDeposit != nil && lastIncludedDeposit.Index != nil {
		anchorIndex := *lastIncludedDeposit.Index
		indexedQueue.LastIncludedDepositIndex = &anchorIndex
	}

	// The churn-simulation residual below is still populated for an empty queue (so the
	// safety estimate for a newly appended deposit works), hence no early return here.
	if len(queue) > 0 {
		// Assign EL deposit indexes by position, flagging postponed (reordered) entries
		// that must instead be resolved by slot.
		indexes, postponed := resolveQueueDepositIndexes(queue, lastIncludedDeposit)
		for idx := range indexedQueue.Queue {
			indexedQueue.Queue[idx].DepositIndex = indexes[idx]
		}

		// Resolve postponed entries by slot (one batched query) and flag them for rendering.
		bs.resolvePostponedDepositIndexes(ctx, queue, indexedQueue.Queue, postponed)
		for idx := range indexedQueue.Queue {
			if postponed[idx] {
				indexedQueue.Queue[idx].Postponed = true
			}
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
	hasChurnDeposit := false

	for idx, queueEntry := range indexedQueue.Queue {
		queueEntry.QueuePos = uint64(idx)

		if queueEntry.Postponed {
			// A postponed deposit is processed at the later of when the churn queue reaches
			// its position (queueEpoch — postponed deposits sit at the back) and when its
			// validator becomes withdrawable. If the queue would only reach it after the
			// validator is already withdrawable, it is processed like a normal queued deposit
			// and is no longer flagged postponed; otherwise the exit is the bottleneck and it
			// keeps waiting. Postponed deposits are applied without consuming churn.
			exitEpoch := bs.postponedDepositEpoch(queueEntry.PendingDeposit.Pubkey)
			switch {
			case exitEpoch == 0:
				// validator/exit unknown: keep flagged, no estimate
			case exitEpoch > queueEpoch:
				queueEntry.EpochEstimate = exitEpoch
			default:
				queueEntry.EpochEstimate = queueEpoch
				queueEntry.Postponed = false
				hasChurnDeposit = true
			}
		} else if totalActiveBalance > 0 {
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
			hasChurnDeposit = true
		}

		indexedQueue.TotalGwei += queueEntry.PendingDeposit.Amount
	}

	// Only deposits flowing through the churn queue contribute to the overall processing
	// estimate; an empty or all-postponed queue leaves QueueEstimation at 0 (rendered as
	// "--").
	if hasChurnDeposit {
		indexedQueue.QueueEstimation = queueEpoch
	}

	// Retain the churn-simulation residual so a hypothetical deposit appended to the tail can be
	// projected (EstimateAppendedDepositEpoch) — used by the pre-Gloas builder onboarding safety check.
	indexedQueue.churnEpoch = queueEpoch
	indexedQueue.churnBalance = queueBalance
	indexedQueue.churnEpochCount = currentEpochCount
	indexedQueue.activationExitChurnLimit = activationExitChurnLimit
	indexedQueue.maxPendingDepositsPerEpoch = maxPendingDepositsPerEpoch
	indexedQueue.totalActiveBalance = totalActiveBalance

	return indexedQueue
}

// postponedDepositEpoch estimates when a postponed deposit will be applied: the epoch its
// validator becomes withdrawable. Returns 0 (unknown) if the validator can't be resolved or
// has no concrete withdrawable epoch yet.
func (bs *ChainService) postponedDepositEpoch(pubkey phase0.BLSPubKey) phase0.Epoch {
	validatorIdx, found := bs.beaconIndexer.GetValidatorIndexByPubkey(pubkey)
	if !found {
		return 0
	}
	validator := bs.GetValidatorByIndex(validatorIdx, false)
	if validator == nil || validator.Validator == nil {
		return 0
	}
	if validator.Validator.WithdrawableEpoch >= beacon.FarFutureEpoch {
		return 0
	}
	return validator.Validator.WithdrawableEpoch
}

// resolveQueueDepositIndexes assigns an EL deposit index to each queue entry by position
// and flags the postponed (reordered) entries. It returns, per entry, the resolved index
// (nil where it must be resolved by slot) and whether the entry is postponed.
//
// pending_deposits comes verbatim from the beacon state but carries no EL deposit index.
// Regular deposits sit in the queue in strict slot<->index order, so their indexes are
// derived by anchoring the tail to the last included deposit and counting backwards.
//
// process_pending_deposits moves deposits of an exiting validator to the back of the
// queue, breaking that order. Such postponed entries surface as a slot that dips below
// the running maximum; they are excluded from the backward count (left nil) and resolved
// individually by slot afterwards. Detection is purely slot-based (independent of the
// possibly-delayed validator set) so the assigned index is always correct.
//
// 0x01->0x02 compounding-switch deposits are synthesized in the queue with no EL deposit
// at all; they are skipped entirely. Every other queue entry maps to a real EL deposit in
// contiguous index order (post-Gloas builder deposits arrive via the separate builder
// deposit contract and never appear in the regular deposit stream), so each non-postponed
// entry simply takes the next index below the anchor.
func resolveQueueDepositIndexes(queue []*electra.PendingDeposit, anchor *dbtypes.Deposit) (indexes []*uint64, postponed []bool) {
	indexes = make([]*uint64, len(queue))
	postponed = make([]bool, len(queue))

	// Forward pass: flag entries whose slot dips below the running maximum.
	prevRegularSlot := phase0.Slot(0)
	for idx, deposit := range queue {
		if isSyntheticPendingDeposit(deposit) {
			continue
		}
		if deposit.Slot < prevRegularSlot {
			postponed[idx] = true
			continue
		}
		prevRegularSlot = deposit.Slot
	}

	// Backward pass: assign indexes to the regular (in-order) entries.
	tailRegularIdx := -1
	if anchor != nil && anchor.Index != nil {
		candidate := int64(*anchor.Index)
		for idx := len(queue) - 1; idx >= 0; idx-- {
			deposit := queue[idx]
			if isSyntheticPendingDeposit(deposit) || postponed[idx] {
				continue
			}
			if candidate < 0 {
				break // ran out of indexes; leave earlier entries unindexed
			}
			if tailRegularIdx < 0 {
				tailRegularIdx = idx
			}
			depositIndexCopy := uint64(candidate)
			indexes[idx] = &depositIndexCopy
			candidate--
		}
	}

	// Anchor verification. The tail-most regular entry must align with the anchor's slot.
	// If it doesn't (a degenerate queue whose most recent deposits are all postponed or
	// builder deposits, so the anchor is not the real tail), the positional assignment
	// cannot be trusted and every non-synthetic entry is resolved by slot instead. This
	// recovers the "queue is nothing but postponed deposits" case, which has no slot dip.
	anchorVerified := tailRegularIdx >= 0 && anchor != nil &&
		queue[tailRegularIdx].Slot == phase0.Slot(anchor.SlotNumber)
	if !anchorVerified {
		for idx, deposit := range queue {
			if isSyntheticPendingDeposit(deposit) {
				continue
			}
			postponed[idx] = true
			indexes[idx] = nil
		}
	}

	return indexes, postponed
}

// resolvePostponedDepositIndexes fills in the EL deposit index of queue entries flagged
// as postponed. These were reordered out of the queue's slot<->index sequence, so they
// can't be aligned by position; instead they are looked up in a single batched query by
// their slot (which equals the beacon state's PendingDeposit.slot) and matched on
// (slot, pubkey, amount, withdrawal_credentials). Byte-identical deposits sharing a slot
// are consumed in slot_index order, matching their queue order. Entries with no DB match
// are left without an index rather than wrongly assigned.
func (bs *ChainService) resolvePostponedDepositIndexes(ctx context.Context, queue []*electra.PendingDeposit, entries []*IndexedDepositQueueEntry, postponed []bool) {
	slotSet := make(map[uint64]struct{})
	for idx, isPostponed := range postponed {
		if isPostponed {
			slotSet[uint64(queue[idx].Slot)] = struct{}{}
		}
	}
	if len(slotSet) == 0 {
		return
	}

	slots := make([]uint64, 0, len(slotSet))
	for slot := range slotSet {
		slots = append(slots, slot)
	}

	rows, err := db.GetDepositRequestsBySlots(ctx, slots, bs.GetCanonicalForkIds())
	if err != nil {
		logrus.Warnf("ChainService.resolvePostponedDepositIndexes error: %v", err)
		return
	}

	depositKey := func(slot, amount uint64, pubkey, wdcreds []byte) string {
		return fmt.Sprintf("%d-%x-%d-%x", slot, pubkey, amount, wdcreds)
	}

	// Rows arrive ordered by (slot_number, slot_index), so identical deposits keep their
	// inclusion order within each key.
	indexesByKey := make(map[string][]uint64, len(rows))
	for _, row := range rows {
		if row.Index == nil {
			continue
		}
		key := depositKey(row.SlotNumber, row.Amount, row.PublicKey, row.WithdrawalCredentials)
		indexesByKey[key] = append(indexesByKey[key], *row.Index)
	}

	for idx, isPostponed := range postponed {
		if !isPostponed {
			continue
		}
		deposit := queue[idx]
		key := depositKey(uint64(deposit.Slot), uint64(deposit.Amount), deposit.Pubkey[:], deposit.WithdrawalCredentials)
		indexes := indexesByKey[key]
		if len(indexes) == 0 {
			continue
		}
		depositIndexCopy := indexes[0]
		indexesByKey[key] = indexes[1:]
		entries[idx].DepositIndex = &depositIndexCopy
	}
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

// getRecentIncludedDeposits returns the most recent included deposit — the anchor used to
// align EL deposit indexes to the pending_deposits queue.
//
// Post-Gloas (EIP-8282) builder deposits arrive via the dedicated builder deposit contract
// and never appear in the regular deposit stream, so every included regular deposit (any
// credential type, including 0xB0) enters the pending_deposits queue and is a valid anchor;
// the EL deposit index sequence stays contiguous with the queue.
func (bs *ChainService) getRecentIncludedDeposits(ctx context.Context, headRoot phase0.Root) *dbtypes.Deposit {
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

			// Every included regular deposit enters the queue and is a valid anchor; the
			// most recent one wins.
			for _, deposit := range deposits {
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

	return lastQueued
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
