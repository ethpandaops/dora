package services

import (
	"bytes"
	"context"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/prysmaticlabs/prysm/v5/container/slice"
	"github.com/sirupsen/logrus"
)

// CombinedBuilderDeposit pairs a consensus-layer builder deposit request with its
// matching execution-layer request tx (either may be nil when only one side is known).
type CombinedBuilderDeposit struct {
	Request             *dbtypes.BuilderDeposit
	RequestOrphaned     bool
	Transaction         *dbtypes.BuilderDepositTx
	TransactionOrphaned bool
}

// CombinedBuilderExit pairs a consensus-layer builder exit request with its
// matching execution-layer request tx (either may be nil when only one side is known).
type CombinedBuilderExit struct {
	Request             *dbtypes.BuilderExit
	RequestOrphaned     bool
	Transaction         *dbtypes.BuilderExitTx
	TransactionOrphaned bool
}

// GetBuilderDepositsByFilter returns builder deposit requests merged from the
// pending EL request txs (not yet dequeued) and the included CL requests
// (cache + DB), each paired with its matching request tx.
func (bs *ChainService) GetBuilderDepositsByFilter(ctx context.Context, filter *dbtypes.BuilderDepositFilter, pageOffset uint64, pageSize uint32) ([]*CombinedBuilderDeposit, uint64, uint64) {
	totalPendingTxResults := uint64(0)
	totalReqResults := uint64(0)

	combinedResults := make([]*CombinedBuilderDeposit, 0)
	canonicalForkIds := bs.GetCanonicalForkIds()

	// pending EL request txs that have not been dequeued (and therefore not included) yet
	initiatedFilter := &dbtypes.BuilderDepositTxFilter{
		MinDequeue: bs.GetHighestElBlockNumber(ctx, nil) + 1,
		PublicKey:  filter.PublicKey,
		MinIndex:   filter.MinIndex,
		MaxIndex:   filter.MaxIndex,
		MinAmount:  filter.MinAmount,
		MaxAmount:  filter.MaxAmount,
	}

	dbTransactions, totalDbTransactions, _ := db.GetBuilderDepositTxsFiltered(ctx, pageOffset, pageSize, initiatedFilter)
	totalPendingTxResults = totalDbTransactions

	for _, deposit := range dbTransactions {
		combinedResults = append(combinedResults, &CombinedBuilderDeposit{
			Transaction:         deposit,
			TransactionOrphaned: !bs.isCanonicalForkId(deposit.ForkId, canonicalForkIds),
		})
	}

	requestTxDetailsFor := [][]byte{}
	page2Offset := uint64(0)
	if pageOffset > totalPendingTxResults {
		page2Offset = pageOffset - totalPendingTxResults
	}

	dbOperations, totalReqResults := bs.getBuilderDepositOperationsByFilter(ctx, filter, page2Offset, pageSize)

	for _, dbOperation := range dbOperations {
		if len(combinedResults) >= int(pageSize) {
			break
		}

		combinedResult := &CombinedBuilderDeposit{
			Request:         dbOperation,
			RequestOrphaned: !bs.isCanonicalForkId(dbOperation.ForkId, canonicalForkIds),
		}

		if len(dbOperation.TxHash) > 0 {
			requestTxDetailsFor = append(requestTxDetailsFor, dbOperation.TxHash)
		} else if matcherHeight := bs.GetBuilderDepositIndexer().GetMatcherHeight(); dbOperation.BlockNumber > matcherHeight {
			combinedResult.Transaction, combinedResult.TransactionOrphaned = bs.matchBuilderDepositTxOnTheFly(ctx, dbOperation, canonicalForkIds)
		}

		combinedResults = append(combinedResults, combinedResult)
	}

	if len(requestTxDetailsFor) > 0 {
		for _, txDetails := range db.GetBuilderDepositTxsByTxHashes(ctx, requestTxDetailsFor) {
			for _, combinedResult := range combinedResults {
				if combinedResult.Request != nil && bytes.Equal(combinedResult.Request.TxHash, txDetails.TxHash) {
					combinedResult.Transaction = txDetails
					combinedResult.TransactionOrphaned = !bs.isCanonicalForkId(txDetails.ForkId, canonicalForkIds)
				}
			}
		}
	}

	return combinedResults, totalPendingTxResults, totalReqResults
}

func (bs *ChainService) matchBuilderDepositTxOnTheFly(ctx context.Context, dbOperation *dbtypes.BuilderDeposit, canonicalForkIds []uint64) (*dbtypes.BuilderDepositTx, bool) {
	requestTxs := db.GetBuilderDepositTxsByDequeueRange(ctx, dbOperation.BlockNumber, dbOperation.BlockNumber)
	if len(requestTxs) == 1 {
		return requestTxs[0], !bs.isCanonicalForkId(requestTxs[0].ForkId, canonicalForkIds)
	}
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

		matchingTxs := []*dbtypes.BuilderDepositTx{}
		for _, tx := range requestTxs {
			if isParentFork(tx.ForkId) {
				matchingTxs = append(matchingTxs, tx)
			}
		}

		if len(matchingTxs) >= int(dbOperation.SlotIndex)+1 {
			tx := matchingTxs[dbOperation.SlotIndex]
			return tx, !bs.isCanonicalForkId(tx.ForkId, canonicalForkIds)
		}
	}

	return nil, false
}

func (bs *ChainService) getBuilderDepositOperationsByFilter(ctx context.Context, filter *dbtypes.BuilderDepositFilter, pageOffset uint64, pageSize uint32) ([]*dbtypes.BuilderDeposit, uint64) {
	chainState := bs.consensusPool.GetChainState()
	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()

	canonicalForkIds := bs.GetCanonicalForkIds()

	// load most recent objects from indexer cache
	cachedMatches := make([]*dbtypes.BuilderDeposit, 0)
	for slotIdx := int64(currentSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
		slot := uint64(slotIdx)
		blocks := bs.beaconIndexer.GetBlocksBySlot(phase0.Slot(slot))
		for _, block := range blocks {
			isCanonical := bs.isCanonicalForkId(uint64(block.GetForkId()), canonicalForkIds)
			if !includeByOrphanedFilter(filter.WithOrphaned, isCanonical) {
				continue
			}
			if filter.MinSlot > 0 && slot < filter.MinSlot {
				continue
			}
			if filter.MaxSlot > 0 && slot > filter.MaxSlot {
				continue
			}

			deposits := block.GetDbBuilderDeposits(bs.beaconIndexer, isCanonical)
			slice.Reverse(deposits) // reverse as other datasources are ordered by descending block index too
			for idx, deposit := range deposits {
				if !builderDepositMatchesFilter(deposit, filter) {
					continue
				}
				cachedMatches = append(cachedMatches, deposits[idx])
			}
		}
	}

	resObjs, cachedMatchesLen, resIdx := paginateCachedBuilderDeposits(cachedMatches, pageOffset, pageSize)

	var dbObjects []*dbtypes.BuilderDeposit
	var dbCount uint64
	var err error

	cachedEnd := pageOffset + uint64(pageSize)
	if cachedEnd <= cachedMatchesLen {
		_, dbCount, err = db.GetBuilderDepositsFiltered(ctx, 0, 1, canonicalForkIds, filter)
	} else {
		dbSliceStart := uint64(0)
		if pageOffset > cachedMatchesLen {
			dbSliceStart = pageOffset - cachedMatchesLen
		}
		dbSliceLimit := pageSize - uint32(resIdx)
		dbObjects, dbCount, err = db.GetBuilderDepositsFiltered(ctx, dbSliceStart, dbSliceLimit, canonicalForkIds, filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.getBuilderDepositOperationsByFilter error: %v", err)
	} else {
		for idx, dbObject := range dbObjects {
			dbObjects[idx].Orphaned = !bs.isCanonicalForkId(dbObject.ForkId, canonicalForkIds)
			if !includeByOrphanedFilter(filter.WithOrphaned, !dbObjects[idx].Orphaned) {
				continue
			}
			resObjs = append(resObjs, dbObjects[idx])
		}
	}

	return resObjs, cachedMatchesLen + dbCount
}

// GetBuilderExitsByFilter returns builder exit requests merged from the pending EL
// request txs (not yet dequeued) and the included CL requests (cache + DB), each
// paired with its matching request tx.
func (bs *ChainService) GetBuilderExitsByFilter(ctx context.Context, filter *dbtypes.BuilderExitFilter, pageOffset uint64, pageSize uint32) ([]*CombinedBuilderExit, uint64, uint64) {
	totalPendingTxResults := uint64(0)

	combinedResults := make([]*CombinedBuilderExit, 0)
	canonicalForkIds := bs.GetCanonicalForkIds()

	initiatedFilter := &dbtypes.BuilderExitTxFilter{
		MinDequeue:    bs.GetHighestElBlockNumber(ctx, nil) + 1,
		PublicKey:     filter.PublicKey,
		SourceAddress: filter.SourceAddress,
		MinIndex:      filter.MinIndex,
		MaxIndex:      filter.MaxIndex,
	}

	dbTransactions, totalDbTransactions, _ := db.GetBuilderExitTxsFiltered(ctx, pageOffset, pageSize, initiatedFilter)
	totalPendingTxResults = totalDbTransactions

	for _, exit := range dbTransactions {
		combinedResults = append(combinedResults, &CombinedBuilderExit{
			Transaction:         exit,
			TransactionOrphaned: !bs.isCanonicalForkId(exit.ForkId, canonicalForkIds),
		})
	}

	requestTxDetailsFor := [][]byte{}
	page2Offset := uint64(0)
	if pageOffset > totalPendingTxResults {
		page2Offset = pageOffset - totalPendingTxResults
	}

	dbOperations, totalReqResults := bs.getBuilderExitOperationsByFilter(ctx, filter, page2Offset, pageSize)

	for _, dbOperation := range dbOperations {
		if len(combinedResults) >= int(pageSize) {
			break
		}

		combinedResult := &CombinedBuilderExit{
			Request:         dbOperation,
			RequestOrphaned: !bs.isCanonicalForkId(dbOperation.ForkId, canonicalForkIds),
		}

		if len(dbOperation.TxHash) > 0 {
			requestTxDetailsFor = append(requestTxDetailsFor, dbOperation.TxHash)
		} else if matcherHeight := bs.GetBuilderExitIndexer().GetMatcherHeight(); dbOperation.BlockNumber > matcherHeight {
			combinedResult.Transaction, combinedResult.TransactionOrphaned = bs.matchBuilderExitTxOnTheFly(ctx, dbOperation, canonicalForkIds)
		}

		combinedResults = append(combinedResults, combinedResult)
	}

	if len(requestTxDetailsFor) > 0 {
		for _, txDetails := range db.GetBuilderExitTxsByTxHashes(ctx, requestTxDetailsFor) {
			for _, combinedResult := range combinedResults {
				if combinedResult.Request != nil && bytes.Equal(combinedResult.Request.TxHash, txDetails.TxHash) {
					combinedResult.Transaction = txDetails
					combinedResult.TransactionOrphaned = !bs.isCanonicalForkId(txDetails.ForkId, canonicalForkIds)
				}
			}
		}
	}

	return combinedResults, totalPendingTxResults, totalReqResults
}

func (bs *ChainService) matchBuilderExitTxOnTheFly(ctx context.Context, dbOperation *dbtypes.BuilderExit, canonicalForkIds []uint64) (*dbtypes.BuilderExitTx, bool) {
	requestTxs := db.GetBuilderExitTxsByDequeueRange(ctx, dbOperation.BlockNumber, dbOperation.BlockNumber)
	if len(requestTxs) == 1 {
		return requestTxs[0], !bs.isCanonicalForkId(requestTxs[0].ForkId, canonicalForkIds)
	}
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

		matchingTxs := []*dbtypes.BuilderExitTx{}
		for _, tx := range requestTxs {
			if isParentFork(tx.ForkId) {
				matchingTxs = append(matchingTxs, tx)
			}
		}

		if len(matchingTxs) >= int(dbOperation.SlotIndex)+1 {
			tx := matchingTxs[dbOperation.SlotIndex]
			return tx, !bs.isCanonicalForkId(tx.ForkId, canonicalForkIds)
		}
	}

	return nil, false
}

func (bs *ChainService) getBuilderExitOperationsByFilter(ctx context.Context, filter *dbtypes.BuilderExitFilter, pageOffset uint64, pageSize uint32) ([]*dbtypes.BuilderExit, uint64) {
	chainState := bs.consensusPool.GetChainState()
	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()

	canonicalForkIds := bs.GetCanonicalForkIds()

	cachedMatches := make([]*dbtypes.BuilderExit, 0)
	for slotIdx := int64(currentSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
		slot := uint64(slotIdx)
		blocks := bs.beaconIndexer.GetBlocksBySlot(phase0.Slot(slot))
		for _, block := range blocks {
			isCanonical := bs.isCanonicalForkId(uint64(block.GetForkId()), canonicalForkIds)
			if !includeByOrphanedFilter(filter.WithOrphaned, isCanonical) {
				continue
			}
			if filter.MinSlot > 0 && slot < filter.MinSlot {
				continue
			}
			if filter.MaxSlot > 0 && slot > filter.MaxSlot {
				continue
			}

			exits := block.GetDbBuilderExits(bs.beaconIndexer, isCanonical)
			slice.Reverse(exits)
			for idx, exit := range exits {
				if !builderExitMatchesFilter(exit, filter) {
					continue
				}
				cachedMatches = append(cachedMatches, exits[idx])
			}
		}
	}

	resObjs, cachedMatchesLen, resIdx := paginateCachedBuilderExits(cachedMatches, pageOffset, pageSize)

	var dbObjects []*dbtypes.BuilderExit
	var dbCount uint64
	var err error

	cachedEnd := pageOffset + uint64(pageSize)
	if cachedEnd <= cachedMatchesLen {
		_, dbCount, err = db.GetBuilderExitsFiltered(ctx, 0, 1, canonicalForkIds, filter)
	} else {
		dbSliceStart := uint64(0)
		if pageOffset > cachedMatchesLen {
			dbSliceStart = pageOffset - cachedMatchesLen
		}
		dbSliceLimit := pageSize - uint32(resIdx)
		dbObjects, dbCount, err = db.GetBuilderExitsFiltered(ctx, dbSliceStart, dbSliceLimit, canonicalForkIds, filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.getBuilderExitOperationsByFilter error: %v", err)
	} else {
		for idx, dbObject := range dbObjects {
			dbObjects[idx].Orphaned = !bs.isCanonicalForkId(dbObject.ForkId, canonicalForkIds)
			if !includeByOrphanedFilter(filter.WithOrphaned, !dbObjects[idx].Orphaned) {
				continue
			}
			resObjs = append(resObjs, dbObjects[idx])
		}
	}

	return resObjs, cachedMatchesLen + dbCount
}

// includeByOrphanedFilter reports whether an object should be included given the
// WithOrphaned filter mode (0: canonical only, 1: all, 2: orphaned only).
func includeByOrphanedFilter(withOrphaned uint8, isCanonical bool) bool {
	switch withOrphaned {
	case 0:
		return isCanonical
	case 2:
		return !isCanonical
	default:
		return true
	}
}

func builderDepositMatchesFilter(deposit *dbtypes.BuilderDeposit, filter *dbtypes.BuilderDepositFilter) bool {
	if len(filter.PublicKey) > 0 && !bytes.Equal(deposit.PublicKey, filter.PublicKey) {
		return false
	}
	if filter.MinIndex > 0 && (deposit.BuilderIndex == nil || *deposit.BuilderIndex < filter.MinIndex) {
		return false
	}
	if filter.MaxIndex > 0 && (deposit.BuilderIndex == nil || *deposit.BuilderIndex > filter.MaxIndex) {
		return false
	}
	if filter.MinAmount != nil && deposit.Amount < *filter.MinAmount {
		return false
	}
	if filter.MaxAmount != nil && deposit.Amount > *filter.MaxAmount {
		return false
	}
	return true
}

func builderExitMatchesFilter(exit *dbtypes.BuilderExit, filter *dbtypes.BuilderExitFilter) bool {
	if len(filter.PublicKey) > 0 && !bytes.Equal(exit.PublicKey, filter.PublicKey) {
		return false
	}
	if len(filter.SourceAddress) > 0 && !bytes.Equal(exit.SourceAddress, filter.SourceAddress) {
		return false
	}
	if filter.MinIndex > 0 && (exit.BuilderIndex == nil || *exit.BuilderIndex < filter.MinIndex) {
		return false
	}
	if filter.MaxIndex > 0 && (exit.BuilderIndex == nil || *exit.BuilderIndex > filter.MaxIndex) {
		return false
	}
	return true
}

func paginateCachedBuilderDeposits(cachedMatches []*dbtypes.BuilderDeposit, pageOffset uint64, pageSize uint32) ([]*dbtypes.BuilderDeposit, uint64, int) {
	resObjs := make([]*dbtypes.BuilderDeposit, 0)
	resIdx := 0
	cachedMatchesLen := uint64(len(cachedMatches))
	cachedEnd := pageOffset + uint64(pageSize)

	if cachedEnd <= cachedMatchesLen {
		resObjs = append(resObjs, cachedMatches[pageOffset:cachedEnd]...)
		resIdx += int(cachedEnd - pageOffset)
	} else if pageOffset < cachedMatchesLen {
		resObjs = append(resObjs, cachedMatches[pageOffset:]...)
		resIdx += len(cachedMatches) - int(pageOffset)
	}

	return resObjs, cachedMatchesLen, resIdx
}

func paginateCachedBuilderExits(cachedMatches []*dbtypes.BuilderExit, pageOffset uint64, pageSize uint32) ([]*dbtypes.BuilderExit, uint64, int) {
	resObjs := make([]*dbtypes.BuilderExit, 0)
	resIdx := 0
	cachedMatchesLen := uint64(len(cachedMatches))
	cachedEnd := pageOffset + uint64(pageSize)

	if cachedEnd <= cachedMatchesLen {
		resObjs = append(resObjs, cachedMatches[pageOffset:cachedEnd]...)
		resIdx += int(cachedEnd - pageOffset)
	} else if pageOffset < cachedMatchesLen {
		resObjs = append(resObjs, cachedMatches[pageOffset:]...)
		resIdx += len(cachedMatches) - int(pageOffset)
	}

	return resObjs, cachedMatchesLen, resIdx
}
