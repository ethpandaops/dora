package services

import (
	"bytes"
	"slices"
	"sort"

	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
)

// BuilderIndexFlag separates builder indices from validator indices
// A validator/builder index with this flag set is a builder index
const BuilderIndexFlag = beacon.BuilderIndexFlag

type BuilderWithIndex struct {
	Index      gloas.BuilderIndex
	Builder    *gloas.Builder
	Superseded bool
}

// GetFilteredBuilderSet returns builders matching the filter criteria
func (bs *ChainService) GetFilteredBuilderSet(filter *dbtypes.BuilderFilter, withBalance bool) ([]BuilderWithIndex, uint64) {
	var overrideForkId *beacon.ForkKey

	canonicalHead := bs.beaconIndexer.GetCanonicalHead(overrideForkId)
	if canonicalHead == nil {
		return nil, 0
	}

	var balances []phase0.Gwei
	if withBalance {
		balances = bs.beaconIndexer.GetRecentBuilderBalances(overrideForkId)
	}
	currentEpoch := bs.consensusPool.GetChainState().CurrentEpoch()

	cachedResults := make([]BuilderWithIndex, 0, 1000)
	cachedIndexes := map[uint64]bool{}

	// Get matching entries from cached builders
	bs.beaconIndexer.StreamActiveBuilderDataForRoot(canonicalHead.Root, false, &currentEpoch, func(index gloas.BuilderIndex, flags uint16, activeData *beacon.BuilderData, builder *gloas.Builder) error {
		if builder == nil {
			return nil
		}
		if filter.MinIndex != nil && uint64(index) < *filter.MinIndex {
			return nil
		}
		if filter.MaxIndex != nil && uint64(index) > *filter.MaxIndex {
			return nil
		}
		if len(filter.PubKey) > 0 {
			pubkeylen := min(len(filter.PubKey), 48)
			if !bytes.Equal(builder.PublicKey[:pubkeylen], filter.PubKey) {
				return nil
			}
		}
		if len(filter.ExecutionAddress) > 0 {
			if !bytes.Equal(builder.ExecutionAddress[:], filter.ExecutionAddress) {
				return nil
			}
		}

		if len(filter.Status) > 0 {
			builderStatus := getBuilderStatus(builder, currentEpoch, false)
			if !slices.Contains(filter.Status, builderStatus) {
				return nil
			}
		}

		cachedResults = append(cachedResults, BuilderWithIndex{
			Index:   index,
			Builder: builder,
		})
		cachedIndexes[uint64(index)] = true

		return nil
	})

	// Get matching entries from DB
	dbIndexes, err := db.GetBuilderIndexesByFilter(*filter, uint64(currentEpoch))
	if err != nil {
		bs.logger.Warnf("error getting builder indexes by filter: %v", err)
		return nil, 0
	}

	// Sort results
	var sortFn func(builderA, builderB BuilderWithIndex) bool
	switch filter.OrderBy {
	case dbtypes.BuilderOrderIndexAsc:
		sortFn = func(builderA, builderB BuilderWithIndex) bool {
			return builderA.Index < builderB.Index
		}
	case dbtypes.BuilderOrderIndexDesc:
		sortFn = func(builderA, builderB BuilderWithIndex) bool {
			return builderA.Index > builderB.Index
		}
	case dbtypes.BuilderOrderPubKeyAsc:
		sortFn = func(builderA, builderB BuilderWithIndex) bool {
			return bytes.Compare(builderA.Builder.PublicKey[:], builderB.Builder.PublicKey[:]) < 0
		}
	case dbtypes.BuilderOrderPubKeyDesc:
		sortFn = func(builderA, builderB BuilderWithIndex) bool {
			return bytes.Compare(builderA.Builder.PublicKey[:], builderB.Builder.PublicKey[:]) > 0
		}
	case dbtypes.BuilderOrderBalanceAsc:
		if balances == nil {
			sortFn = func(builderA, builderB BuilderWithIndex) bool {
				return builderA.Builder.Balance < builderB.Builder.Balance
			}
		} else {
			sortFn = func(builderA, builderB BuilderWithIndex) bool {
				return balances[builderA.Index] < balances[builderB.Index]
			}
			sort.Slice(dbIndexes, func(i, j int) bool {
				if dbIndexes[i] >= uint64(len(balances)) || dbIndexes[j] >= uint64(len(balances)) {
					return dbIndexes[i] < dbIndexes[j]
				}
				return balances[dbIndexes[i]] < balances[dbIndexes[j]]
			})
		}
	case dbtypes.BuilderOrderBalanceDesc:
		if balances == nil {
			sortFn = func(builderA, builderB BuilderWithIndex) bool {
				return builderA.Builder.Balance > builderB.Builder.Balance
			}
		} else {
			sortFn = func(builderA, builderB BuilderWithIndex) bool {
				return balances[builderA.Index] > balances[builderB.Index]
			}
			sort.Slice(dbIndexes, func(i, j int) bool {
				if dbIndexes[i] >= uint64(len(balances)) || dbIndexes[j] >= uint64(len(balances)) {
					return dbIndexes[i] > dbIndexes[j]
				}
				return balances[dbIndexes[i]] > balances[dbIndexes[j]]
			})
		}
	case dbtypes.BuilderOrderDepositEpochAsc:
		sortFn = func(builderA, builderB BuilderWithIndex) bool {
			return builderA.Builder.DepositEpoch < builderB.Builder.DepositEpoch
		}
	case dbtypes.BuilderOrderDepositEpochDesc:
		sortFn = func(builderA, builderB BuilderWithIndex) bool {
			return builderA.Builder.DepositEpoch > builderB.Builder.DepositEpoch
		}
	case dbtypes.BuilderOrderWithdrawableEpochAsc:
		sortFn = func(builderA, builderB BuilderWithIndex) bool {
			return builderA.Builder.WithdrawableEpoch < builderB.Builder.WithdrawableEpoch
		}
	case dbtypes.BuilderOrderWithdrawableEpochDesc:
		sortFn = func(builderA, builderB BuilderWithIndex) bool {
			return builderA.Builder.WithdrawableEpoch > builderB.Builder.WithdrawableEpoch
		}
	}

	sort.Slice(cachedResults, func(i, j int) bool {
		return sortFn(cachedResults[i], cachedResults[j])
	})

	// Stream builder set from db and merge cached results
	resCap := filter.Limit
	if resCap == 0 {
		resCap = uint64(len(cachedResults) + len(dbIndexes))
	}
	result := make([]BuilderWithIndex, 0, resCap)
	cachedIndex := 0
	matchingCount := uint64(0)
	resultCount := uint64(0)
	dbEntryCount := uint64(0)

	db.StreamBuildersByIndexes(dbIndexes, func(dbBuilder *dbtypes.Builder) bool {
		dbEntryCount++
		builderWithIndex := BuilderWithIndex{
			Index:      gloas.BuilderIndex(dbBuilder.BuilderIndex),
			Builder:    beacon.UnwrapDbBuilder(dbBuilder),
			Superseded: dbBuilder.Superseded,
		}

		for cachedIndex < len(cachedResults) && (cachedResults[cachedIndex].Index == builderWithIndex.Index || sortFn(cachedResults[cachedIndex], builderWithIndex)) {
			if matchingCount >= filter.Offset {
				resultBuilder := cachedResults[cachedIndex]
				if balances != nil && uint64(resultBuilder.Index) < uint64(len(balances)) {
					resultBuilder.Builder.Balance = balances[resultBuilder.Index]
				}
				result = append(result, resultBuilder)
				resultCount++
			}
			matchingCount++
			cachedIndex++

			if filter.Limit > 0 && resultCount >= filter.Limit {
				return false // stop streaming
			}
		}

		if cachedIndexes[dbBuilder.BuilderIndex] {
			return true // skip this index, cache entry is newer
		}

		if matchingCount >= filter.Offset {
			if !builderWithIndex.Superseded && balances != nil && dbBuilder.BuilderIndex < uint64(len(balances)) {
				builderWithIndex.Builder.Balance = balances[dbBuilder.BuilderIndex]
			}
			result = append(result, builderWithIndex)
			resultCount++
		}
		matchingCount++

		if filter.Limit > 0 && resultCount >= filter.Limit {
			return false // stop streaming
		}

		return true // get more from db
	})

	for cachedIndex < len(cachedResults) && (filter.Limit == 0 || resultCount < filter.Limit) {
		if matchingCount >= filter.Offset {
			resultBuilder := cachedResults[cachedIndex]
			if balances != nil && uint64(resultBuilder.Index) < uint64(len(balances)) {
				resultBuilder.Builder.Balance = balances[resultBuilder.Index]
			}
			result = append(result, resultBuilder)
			resultCount++
		}
		matchingCount++
		cachedIndex++
	}

	// Add remaining cached results
	matchingCount += uint64(len(cachedResults) - cachedIndex)

	// Add remaining db results
	remainingDbCount := uint64(0)
	for i := dbEntryCount; i < uint64(len(dbIndexes)); i++ {
		if cachedIndexes[dbIndexes[i]] {
			continue
		}
		remainingDbCount++
	}
	matchingCount += remainingDbCount

	return result, matchingCount
}

// GetBuilderByIndex returns the builder by index
func (bs *ChainService) GetBuilderByIndex(index gloas.BuilderIndex) *gloas.Builder {
	return bs.beaconIndexer.GetBuilderByIndex(index, nil)
}

// getBuilderStatus determines the status of a builder
func getBuilderStatus(builder *gloas.Builder, currentEpoch phase0.Epoch, superseded bool) dbtypes.BuilderStatus {
	if superseded {
		return dbtypes.BuilderStatusSupersededFilter
	}
	if builder.WithdrawableEpoch <= currentEpoch {
		return dbtypes.BuilderStatusExitedFilter
	}
	return dbtypes.BuilderStatusActiveFilter
}
