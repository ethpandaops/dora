package services

import (
	"bytes"
	"context"
	"slices"
	"sort"

	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"

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

// GetFilteredBuilderSet returns builders matching the filter criteria.
//
// A builder's identity is its pubkey; the builder index is a reusable slot (EIP-8282). When an
// index is reused the previous occupant's DB row is flagged Superseded and a new row is inserted.
// This function therefore treats each pubkey as a distinct entry rather than deduplicating by index:
//   - The in-memory cache only ever holds the current (non-superseded) occupant of each index and is
//     the freshest source for it.
//   - Superseded predecessors live only in the DB. Per product decision they are hidden by default
//     and only returned when the caller explicitly requests the Superseded status.
func (bs *ChainService) GetFilteredBuilderSet(ctx context.Context, filter *dbtypes.BuilderFilter, withBalance bool) ([]BuilderWithIndex, uint64) {
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

	// Resolve the effective status set. An empty filter defaults to the current index occupants
	// (active + exited-but-still-owning); superseded builders are only included on explicit request.
	effectiveStatus := filter.Status
	if len(effectiveStatus) == 0 {
		effectiveStatus = []dbtypes.BuilderStatus{dbtypes.BuilderStatusActiveFilter, dbtypes.BuilderStatusExitedFilter}
	}
	includeSuperseded := slices.Contains(effectiveStatus, dbtypes.BuilderStatusSupersededFilter)
	includeCurrent := slices.Contains(effectiveStatus, dbtypes.BuilderStatusActiveFilter) ||
		slices.Contains(effectiveStatus, dbtypes.BuilderStatusExitedFilter)

	applyBalance := func(row *BuilderWithIndex) {
		// Balances are indexed by the current occupant of an index; never attribute them to a
		// superseded predecessor (its index now belongs to someone else).
		if !row.Superseded && balances != nil && uint64(row.Index) < uint64(len(balances)) {
			row.Builder.Balance = balances[row.Index]
		}
	}

	results := make([]BuilderWithIndex, 0, 1000)
	cachedIndexes := map[uint64]bool{}

	// 1. Current occupants from the in-memory cache (never superseded).
	if includeCurrent {
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
			if !slices.Contains(effectiveStatus, getBuilderStatus(builder, currentEpoch, false)) {
				return nil
			}

			cachedIndexes[uint64(index)] = true
			row := BuilderWithIndex{Index: index, Builder: builder}
			applyBalance(&row)
			results = append(results, row)
			return nil
		})
	}

	// 2. Matching rows from the DB (preserving pubkey identity, so a reused index can contribute
	//    its superseded predecessors in addition to the current occupant).
	dbBuilders, err := db.GetBuildersByFilter(ctx, *filter, uint64(currentEpoch))
	if err != nil {
		bs.logger.Warnf("error getting builders by filter: %v", err)
		return nil, 0
	}
	for _, dbBuilder := range dbBuilders {
		if dbBuilder.Superseded {
			if !includeSuperseded {
				continue
			}
		} else {
			// current occupant per DB; prefer the fresher cache entry if we already have it
			if cachedIndexes[dbBuilder.BuilderIndex] || !includeCurrent {
				continue
			}
		}

		row := BuilderWithIndex{
			Index:      gloas.BuilderIndex(dbBuilder.BuilderIndex),
			Builder:    beacon.UnwrapDbBuilder(dbBuilder),
			Superseded: dbBuilder.Superseded,
		}
		applyBalance(&row)
		results = append(results, row)
	}

	// 3. Sort the merged identity set, then paginate.
	sortFn := builderSortFn(filter.OrderBy, balances)
	sort.SliceStable(results, func(i, j int) bool {
		return sortFn(results[i], results[j])
	})

	totalCount := uint64(len(results))
	if filter.Offset >= totalCount {
		return []BuilderWithIndex{}, totalCount
	}
	paged := results[filter.Offset:]
	if filter.Limit > 0 && uint64(len(paged)) > filter.Limit {
		paged = paged[:filter.Limit]
	}

	return paged, totalCount
}

// builderSortFn returns the comparison function for the requested ordering. Balance ordering falls
// back to the per-builder balance when the epoch-adjusted balances slice is unavailable; superseded
// builders (index reused) have no balance slot and compare as 0.
func builderSortFn(orderBy dbtypes.BuilderOrder, balances []phase0.Gwei) func(a, b BuilderWithIndex) bool {
	balanceOf := func(row BuilderWithIndex) phase0.Gwei {
		if !row.Superseded && balances != nil && uint64(row.Index) < uint64(len(balances)) {
			return balances[row.Index]
		}
		return row.Builder.Balance
	}

	switch orderBy {
	case dbtypes.BuilderOrderIndexAsc:
		return func(a, b BuilderWithIndex) bool { return a.Index < b.Index }
	case dbtypes.BuilderOrderIndexDesc:
		return func(a, b BuilderWithIndex) bool { return a.Index > b.Index }
	case dbtypes.BuilderOrderPubKeyAsc:
		return func(a, b BuilderWithIndex) bool {
			return bytes.Compare(a.Builder.PublicKey[:], b.Builder.PublicKey[:]) < 0
		}
	case dbtypes.BuilderOrderPubKeyDesc:
		return func(a, b BuilderWithIndex) bool {
			return bytes.Compare(a.Builder.PublicKey[:], b.Builder.PublicKey[:]) > 0
		}
	case dbtypes.BuilderOrderBalanceAsc:
		return func(a, b BuilderWithIndex) bool { return balanceOf(a) < balanceOf(b) }
	case dbtypes.BuilderOrderBalanceDesc:
		return func(a, b BuilderWithIndex) bool { return balanceOf(a) > balanceOf(b) }
	case dbtypes.BuilderOrderDepositEpochAsc:
		return func(a, b BuilderWithIndex) bool { return a.Builder.DepositEpoch < b.Builder.DepositEpoch }
	case dbtypes.BuilderOrderDepositEpochDesc:
		return func(a, b BuilderWithIndex) bool { return a.Builder.DepositEpoch > b.Builder.DepositEpoch }
	case dbtypes.BuilderOrderWithdrawableEpochAsc:
		return func(a, b BuilderWithIndex) bool { return a.Builder.WithdrawableEpoch < b.Builder.WithdrawableEpoch }
	case dbtypes.BuilderOrderWithdrawableEpochDesc:
		return func(a, b BuilderWithIndex) bool { return a.Builder.WithdrawableEpoch > b.Builder.WithdrawableEpoch }
	default:
		return func(a, b BuilderWithIndex) bool { return a.Index < b.Index }
	}
}

// GetBuilderByIndex returns the builder by index
func (bs *ChainService) GetBuilderByIndex(index gloas.BuilderIndex) *gloas.Builder {
	return bs.beaconIndexer.GetBuilderByIndex(index, nil)
}

// GetBuilderIndexByPubkey resolves a builder index from its pubkey via the dedicated
// builder pubkey cache (separate from validators, see GetValidatorIndexByPubkey).
func (bs *ChainService) GetBuilderIndexByPubkey(pubkey phase0.BLSPubKey) (gloas.BuilderIndex, bool) {
	return bs.beaconIndexer.GetBuilderIndexByPubkey(pubkey)
}

// GetActiveBuildersByIndexes batch-resolves the builder currently occupying each of the given
// indexes (cache first, then a single batched DB query for misses). Builder indexes can be
// reused (EIP-8282), so callers compare the returned builder's pubkey against the pubkey they
// expect to tell whether that pubkey still owns the index or was superseded.
func (bs *ChainService) GetActiveBuildersByIndexes(ctx context.Context, indexes []gloas.BuilderIndex) map[gloas.BuilderIndex]*gloas.Builder {
	result := make(map[gloas.BuilderIndex]*gloas.Builder, len(indexes))
	missing := make([]uint64, 0)
	seenMissing := make(map[uint64]bool)
	for _, idx := range indexes {
		if _, ok := result[idx]; ok {
			continue
		}
		if builder := bs.beaconIndexer.GetBuilderByIndex(idx, nil); builder != nil {
			result[idx] = builder
		} else if !seenMissing[uint64(idx)] {
			seenMissing[uint64(idx)] = true
			missing = append(missing, uint64(idx))
		}
	}

	if len(missing) > 0 {
		db.StreamBuildersByIndexes(ctx, missing, func(dbBuilder *dbtypes.Builder) bool {
			// the active occupant of an index is the non-superseded row
			if dbBuilder.Superseded {
				return true
			}
			result[gloas.BuilderIndex(dbBuilder.BuilderIndex)] = beacon.UnwrapDbBuilder(dbBuilder)
			return true
		})
	}

	return result
}

// GetBuilderTenureEndEpoch returns the epoch at which the builder with the given deposit epoch
// stopped owning its index, i.e. the deposit epoch of the next builder that took over the same
// index (EIP-8282 index reuse). It returns nil when the builder is the current occupant of the
// index (no successor), meaning its tenure is still open.
//
// The successor is the smallest deposit epoch strictly greater than depositEpoch among all builders
// (current cache occupant plus every persisted row) that have held the index. Callers convert the
// returned tenure to a slot window to scope index-keyed data (blocks, bids, withdrawals) to a
// specific builder's lifetime rather than mixing data from every builder that reused the index.
func (bs *ChainService) GetBuilderTenureEndEpoch(ctx context.Context, index gloas.BuilderIndex, depositEpoch phase0.Epoch) *phase0.Epoch {
	var successor *phase0.Epoch
	consider := func(candidate phase0.Epoch) {
		if candidate <= depositEpoch {
			return
		}
		if successor == nil || candidate < *successor {
			c := candidate
			successor = &c
		}
	}

	// current occupant from the cache (may not be persisted yet)
	if occupant := bs.beaconIndexer.GetBuilderByIndex(index, nil); occupant != nil {
		consider(occupant.DepositEpoch)
	}
	// every persisted builder that has held this index (includes superseded predecessors/successors)
	for _, dbBuilder := range db.GetBuildersByIndex(ctx, uint64(index)) {
		consider(phase0.Epoch(dbBuilder.DepositEpoch))
	}

	return successor
}

// GetBuilderBalances returns the current builder balances (epoch-start adjusted for in-epoch withdrawals).
func (bs *ChainService) GetBuilderBalances() []phase0.Gwei {
	return bs.beaconIndexer.GetRecentBuilderBalances(nil)
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
