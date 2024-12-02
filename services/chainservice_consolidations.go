package services

import (
	"bytes"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/sirupsen/logrus"
)

type CombinedConsolidationRequest struct {
	Request             *dbtypes.ConsolidationRequest
	RequestOrphaned     bool
	Transaction         *dbtypes.ConsolidationRequestTx
	TransactionOrphaned bool
}

type CombinedConsolidationRequestFilter struct {
	Filter  *dbtypes.ConsolidationRequestFilter
	Request uint8 // 0: all, 1: tx only, 2: request only
}

func (ccr *CombinedConsolidationRequest) SourceAddress() []byte {
	if ccr.Request != nil {
		return ccr.Request.SourceAddress
	}
	if ccr.Transaction != nil {
		return ccr.Transaction.SourceAddress
	}
	return nil
}

func (ccr *CombinedConsolidationRequest) SourceIndex() *uint64 {
	if ccr.Request != nil && ccr.Request.SourceIndex != nil {
		return ccr.Request.SourceIndex
	}
	if ccr.Transaction != nil && ccr.Transaction.SourceIndex != nil {
		return ccr.Transaction.SourceIndex
	}
	return nil
}

func (ccr *CombinedConsolidationRequest) SourcePubkey() []byte {
	if ccr.Request != nil {
		return ccr.Request.SourcePubkey
	}
	if ccr.Transaction != nil {
		return ccr.Transaction.SourcePubkey
	}
	return nil
}

func (ccr *CombinedConsolidationRequest) TargetIndex() *uint64 {
	if ccr.Request != nil && ccr.Request.TargetIndex != nil {
		return ccr.Request.TargetIndex
	}
	if ccr.Transaction != nil && ccr.Transaction.TargetIndex != nil {
		return ccr.Transaction.TargetIndex
	}
	return nil
}

func (ccr *CombinedConsolidationRequest) TargetPubkey() []byte {
	if ccr.Request != nil {
		return ccr.Request.TargetPubkey
	}
	if ccr.Transaction != nil {
		return ccr.Transaction.TargetPubkey
	}
	return nil
}

func (bs *ChainService) GetConsolidationRequestsByFilter(filter *CombinedConsolidationRequestFilter, pageIdx uint64, pageSize uint32) ([]*CombinedConsolidationRequest, uint64) {
	totalResults := uint64(0)
	combinedResults := make([]*CombinedConsolidationRequest, 0)
	canonicalForkIds := bs.GetCanonicalForkIds()

	initiatedFilter := &dbtypes.ConsolidationRequestTxFilter{
		PublicKey:        filter.Filter.PublicKey,
		SourceAddress:    filter.Filter.SourceAddress,
		MinSrcIndex:      filter.Filter.MinSrcIndex,
		MaxSrcIndex:      filter.Filter.MaxSrcIndex,
		SrcValidatorName: filter.Filter.SrcValidatorName,
		MinTgtIndex:      filter.Filter.MinTgtIndex,
		MaxTgtIndex:      filter.Filter.MaxTgtIndex,
		TgtValidatorName: filter.Filter.TgtValidatorName,
		WithOrphaned:     filter.Filter.WithOrphaned,
	}

	var dbOperations []*dbtypes.ConsolidationRequest

	if filter.Request != 1 {
		dbOperations, totalResults = bs.GetConsolidationRequestOperationsByFilter(filter.Filter, pageIdx, pageSize)
		if len(dbOperations) > 0 {
			initiatedFilter.MinDequeue = dbOperations[0].BlockNumber + 1
		}
	}

	if filter.Request != 2 {
		dbTransactions, totalDbTransactions, _ := db.GetConsolidationRequestTxsFiltered(0, 20, canonicalForkIds, initiatedFilter)
		totalResults += totalDbTransactions

		for _, consolidation := range dbTransactions {
			combinedResults = append(combinedResults, &CombinedConsolidationRequest{
				Transaction:         consolidation,
				TransactionOrphaned: !bs.isCanonicalForkId(consolidation.ForkId, canonicalForkIds),
			})
		}
	}

	if filter.Request != 1 {
		requestTxDetailsFor := [][]byte{}

		for _, dbOperation := range dbOperations {
			combinedResult := &CombinedConsolidationRequest{
				Request:         dbOperation,
				RequestOrphaned: !bs.isCanonicalForkId(dbOperation.ForkId, canonicalForkIds),
			}

			if len(dbOperation.TxHash) > 0 {
				requestTxDetailsFor = append(requestTxDetailsFor, dbOperation.TxHash)
			} else if matcherHeight := bs.GetConsolidationIndexer().GetMatcherHeight(); dbOperation.BlockNumber > matcherHeight {
				// consolidation request has not been matched with a tx yet, try to find the tx on the fly
				requestTxs := db.GetConsolidationRequestTxsByDequeueRange(dbOperation.BlockNumber, dbOperation.BlockNumber)
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

					matchingTxs := []*dbtypes.ConsolidationRequestTx{}
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

		// load tx details for consolidation requests
		if len(requestTxDetailsFor) > 0 {
			for _, txDetails := range db.GetConsolidationRequestTxsByTxHashes(requestTxDetailsFor) {
				for _, combinedResult := range combinedResults {
					if combinedResult.Request != nil && bytes.Equal(combinedResult.Request.TxHash, txDetails.TxHash) {
						combinedResult.Transaction = txDetails
						combinedResult.TransactionOrphaned = !bs.isCanonicalForkId(txDetails.ForkId, canonicalForkIds)
					}
				}
			}
		}
	}

	return combinedResults, totalResults
}

func (bs *ChainService) GetConsolidationRequestOperationsByFilter(filter *dbtypes.ConsolidationRequestFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.ConsolidationRequest, uint64) {
	chainState := bs.consensusPool.GetChainState()
	_, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	idxMinSlot := chainState.EpochToSlot(prunedEpoch)
	currentSlot := chainState.CurrentSlot()

	canonicalForkIds := bs.GetCanonicalForkIds()

	// load most recent objects from indexer cache
	cachedMatches := make([]*dbtypes.ConsolidationRequest, 0)
	for slotIdx := int64(currentSlot); slotIdx >= int64(idxMinSlot); slotIdx-- {
		slot := uint64(slotIdx)
		blocks := bs.beaconIndexer.GetBlocksBySlot(phase0.Slot(slot))
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				if filter.WithOrphaned != 1 {
					isOrphaned := !bs.isCanonicalForkId(uint64(block.GetForkId()), canonicalForkIds)
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

				consolidationRequests := block.GetDbConsolidationRequests(bs.beaconIndexer)
				for idx, consolidationRequest := range consolidationRequests {
					if filter.MinSrcIndex > 0 && (consolidationRequest.SourceIndex == nil || *consolidationRequest.SourceIndex < filter.MinSrcIndex) {
						continue
					}
					if filter.MaxSrcIndex > 0 && (consolidationRequest.SourceIndex == nil || *consolidationRequest.SourceIndex > filter.MaxSrcIndex) {
						continue
					}
					if len(filter.PublicKey) > 0 {
						if !bytes.Equal(consolidationRequest.SourcePubkey[:], filter.PublicKey) && !bytes.Equal(consolidationRequest.TargetPubkey[:], filter.PublicKey) {
							continue
						}
					}
					if filter.SrcValidatorName != "" {
						if consolidationRequest.SourceIndex == nil {
							continue
						}
						validatorName := bs.validatorNames.GetValidatorName(*consolidationRequest.SourceIndex)
						if !strings.Contains(validatorName, filter.SrcValidatorName) {
							continue
						}
					}

					if filter.MinTgtIndex > 0 && (consolidationRequest.TargetIndex == nil || *consolidationRequest.TargetIndex < filter.MinTgtIndex) {
						continue
					}
					if filter.MaxTgtIndex > 0 && (consolidationRequest.TargetIndex == nil || *consolidationRequest.TargetIndex > filter.MaxTgtIndex) {
						continue
					}
					if filter.TgtValidatorName != "" {
						if consolidationRequest.TargetIndex == nil {
							continue
						}
						validatorName := bs.validatorNames.GetValidatorName(*consolidationRequest.TargetIndex)
						if !strings.Contains(validatorName, filter.TgtValidatorName) {
							continue
						}
					}

					cachedMatches = append(cachedMatches, consolidationRequests[idx])
				}
			}
		}
	}

	cachedMatchesLen := uint64(len(cachedMatches))
	cachedPages := cachedMatchesLen / uint64(pageSize)
	resObjs := make([]*dbtypes.ConsolidationRequest, 0)
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
	var dbPage uint64
	if pageIdx > cachedPages {
		dbPage = pageIdx - cachedPages
	} else {
		dbPage = 0
	}
	dbCacheOffset := uint64(pageSize) - (cachedMatchesLen % uint64(pageSize))

	var dbObjects []*dbtypes.ConsolidationRequest
	var dbCount uint64
	var err error

	if resIdx > int(pageSize) {
		// all results from cache, just get result count from db
		_, dbCount, err = db.GetConsolidationRequestsFiltered(0, 1, canonicalForkIds, filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetConsolidationRequestsFiltered(0, uint32(dbCacheOffset), canonicalForkIds, filter)
	} else {
		dbObjects, dbCount, err = db.GetConsolidationRequestsFiltered((dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, canonicalForkIds, filter)
	}

	if err != nil {
		logrus.Warnf("ChainService.GetConsolidationRequestOperationsByFilter error: %v", err)
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
