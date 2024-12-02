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
		return cwr.Request.Amount
	}
	if cwr.Transaction != nil {
		return cwr.Transaction.Amount
	}
	return 0
}

func (bs *ChainService) GetWithdrawalRequestsByFilter(filter *CombinedWithdrawalRequestFilter, pageIdx uint64, pageSize uint32) ([]*CombinedWithdrawalRequest, uint64) {
	totalResults := uint64(0)
	combinedResults := make([]*CombinedWithdrawalRequest, 0)
	canonicalForkIds := bs.GetCanonicalForkIds()

	initiatedFilter := &dbtypes.WithdrawalRequestTxFilter{
		PublicKey:     filter.Filter.PublicKey,
		MinIndex:      filter.Filter.MinIndex,
		MaxIndex:      filter.Filter.MaxIndex,
		ValidatorName: filter.Filter.ValidatorName,
		WithOrphaned:  filter.Filter.WithOrphaned,
	}

	var dbOperations []*dbtypes.WithdrawalRequest

	if filter.Request != 1 {
		dbOperations, totalResults = bs.GetWithdrawalRequestOperationsByFilter(filter.Filter, pageIdx, pageSize)
		if len(dbOperations) > 0 {
			initiatedFilter.MinDequeue = dbOperations[0].BlockNumber + 1
		}
	}

	if filter.Request != 2 {
		dbTransactions, totalDbTransactions, _ := db.GetWithdrawalRequestTxsFiltered(0, 20, canonicalForkIds, initiatedFilter)
		totalResults += totalDbTransactions

		for _, withdrawal := range dbTransactions {
			combinedResults = append(combinedResults, &CombinedWithdrawalRequest{
				Transaction:         withdrawal,
				TransactionOrphaned: !bs.isCanonicalForkId(withdrawal.ForkId, canonicalForkIds),
			})
		}
	}

	if filter.Request != 1 {
		requestTxDetailsFor := [][]byte{}

		for _, dbOperation := range dbOperations {
			combinedResult := &CombinedWithdrawalRequest{
				Request:         dbOperation,
				RequestOrphaned: !bs.isCanonicalForkId(dbOperation.ForkId, canonicalForkIds),
			}

			if len(dbOperation.TxHash) > 0 {
				requestTxDetailsFor = append(requestTxDetailsFor, dbOperation.TxHash)
			} else if matcherHeight := bs.GetWithdrawalIndexer().GetMatcherHeight(); dbOperation.BlockNumber > matcherHeight {
				// withdrawal request has not been matched with a tx yet, try to find the tx on the fly
				requestTxs := db.GetWithdrawalRequestTxsByDequeueRange(dbOperation.BlockNumber, dbOperation.BlockNumber)
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
			for _, txDetails := range db.GetWithdrawalRequestTxsByTxHashes(requestTxDetailsFor) {
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

func (bs *ChainService) GetWithdrawalRequestOperationsByFilter(filter *dbtypes.WithdrawalRequestFilter, pageIdx uint64, pageSize uint32) ([]*dbtypes.WithdrawalRequest, uint64) {
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

				withdrawalRequests := block.GetDbWithdrawalRequests(bs.beaconIndexer)
				for idx, withdrawalRequest := range withdrawalRequests {
					if filter.MinIndex > 0 && (withdrawalRequest.ValidatorIndex == nil || *withdrawalRequest.ValidatorIndex < filter.MinIndex) {
						continue
					}
					if filter.MaxIndex > 0 && (withdrawalRequest.ValidatorIndex == nil || *withdrawalRequest.ValidatorIndex > filter.MaxIndex) {
						continue
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

	cachedMatchesLen := uint64(len(cachedMatches))
	cachedPages := cachedMatchesLen / uint64(pageSize)
	resObjs := make([]*dbtypes.WithdrawalRequest, 0)
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

	var dbObjects []*dbtypes.WithdrawalRequest
	var dbCount uint64
	var err error

	if resIdx > int(pageSize) {
		// all results from cache, just get result count from db
		_, dbCount, err = db.GetWithdrawalRequestsFiltered(0, 1, canonicalForkIds, filter)
	} else if dbPage == 0 {
		// first page, load first `pagesize-cachedResults` items from db
		dbObjects, dbCount, err = db.GetWithdrawalRequestsFiltered(0, uint32(dbCacheOffset), canonicalForkIds, filter)
	} else {
		dbObjects, dbCount, err = db.GetWithdrawalRequestsFiltered((dbPage-1)*uint64(pageSize)+dbCacheOffset, pageSize, canonicalForkIds, filter)
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
