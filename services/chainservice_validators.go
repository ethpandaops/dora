package services

import (
	"bytes"
	"context"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

func (bs *ChainService) GetValidatorByIndex(index phase0.ValidatorIndex, withBalance bool) *v1.Validator {
	currentEpoch := bs.consensusPool.GetChainState().CurrentEpoch()
	return bs.beaconIndexer.GetFullValidatorByIndex(index, currentEpoch, nil, withBalance)
}

func (bs *ChainService) GetValidatorIndexByPubkey(pubkey phase0.BLSPubKey) (phase0.ValidatorIndex, bool) {
	return bs.beaconIndexer.GetValidatorIndexByPubkey(pubkey)
}

// IsProjectedValidatorIndex reports whether the given index currently resolves to a
// validator projected from the pending_deposits queue (not yet on chain), i.e. its
// index is an estimate. Returns false for real validators and unknown indexes.
func (bs *ChainService) IsProjectedValidatorIndex(index phase0.ValidatorIndex) bool {
	validator := bs.GetValidatorByIndex(index, false)
	return validator != nil && beacon.IsProjectedValidator(validator.Validator)
}

func (bs *ChainService) StreamActiveValidatorData(activeOnly bool, cb beacon.ValidatorSetStreamer) error {
	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	if canonicalHead == nil {
		return nil
	}

	currentEpoch := bs.consensusPool.GetChainState().CurrentEpoch()

	return bs.beaconIndexer.StreamActiveValidatorDataForRoot(canonicalHead.Root, activeOnly, &currentEpoch, cb)
}

func (bs *ChainService) GetValidatorStatusMap() map[v1.ValidatorState]uint64 {
	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	if canonicalHead == nil {
		return nil
	}

	currentEpoch := bs.consensusPool.GetChainState().CurrentEpoch()

	return bs.beaconIndexer.GetValidatorStatusMap(currentEpoch, canonicalHead.Root)
}

func (bs *ChainService) GetValidatorVotingActivity(validatorIndex phase0.ValidatorIndex) ([]beacon.ValidatorActivity, phase0.Epoch) {
	return bs.beaconIndexer.GetValidatorActivity(validatorIndex)
}

// GetValidatorInclusionDistance returns the attestation count and total inclusion delay
// for a validator over the last lookbackEpochs epochs, using only cached blocks.
func (bs *ChainService) GetValidatorInclusionDistance(validatorIndex phase0.ValidatorIndex, lookbackEpochs phase0.Epoch) (count uint64, totalDelay uint64) {
	return bs.beaconIndexer.GetValidatorInclusionDistance(validatorIndex, lookbackEpochs)
}

func (bs *ChainService) GetValidatorLiveness(validatorIndex phase0.ValidatorIndex, lookbackEpochs phase0.Epoch) uint64 {
	validatorActivity, _ := bs.beaconIndexer.GetValidatorActivityCount(validatorIndex, bs.livenessStartEpoch(lookbackEpochs))
	if validatorActivity > uint64(lookbackEpochs) {
		validatorActivity = uint64(lookbackEpochs)
	}

	return validatorActivity
}

// GetValidatorLivenessCounts returns the liveness of every validator (attested epochs
// within the last lookbackEpochs epochs, capped at lookbackEpochs) indexed by validator
// index. It walks the activity cache once and is meant for callers that iterate the
// whole validator set; read it with ValidatorLivenessAt.
func (bs *ChainService) GetValidatorLivenessCounts(lookbackEpochs phase0.Epoch) []uint8 {
	counts := bs.beaconIndexer.GetValidatorActivityCounts(bs.livenessStartEpoch(lookbackEpochs))
	for i, count := range counts {
		if uint64(count) > uint64(lookbackEpochs) {
			counts[i] = uint8(lookbackEpochs)
		}
	}

	return counts
}

// ValidatorLivenessAt reads a validator's liveness from a GetValidatorLivenessCounts
// result; indexes beyond the slice have no activity.
func ValidatorLivenessAt(counts []uint8, validatorIndex phase0.ValidatorIndex) uint64 {
	if uint64(validatorIndex) >= uint64(len(counts)) {
		return 0
	}

	return uint64(counts[validatorIndex])
}

// GetValidatorInclusionDistances returns the attestation count and total inclusion delay
// of every validator over the last lookbackEpochs epochs, indexed by validator index.
func (bs *ChainService) GetValidatorInclusionDistances(lookbackEpochs phase0.Epoch) []beacon.ValidatorInclusionStats {
	return bs.beaconIndexer.GetValidatorInclusionDistances(lookbackEpochs)
}

// livenessStartEpoch returns the first epoch of a liveness lookback window.
func (bs *ChainService) livenessStartEpoch(lookbackEpochs phase0.Epoch) phase0.Epoch {
	latestEpoch := bs.consensusPool.GetChainState().CurrentEpoch()
	if latestEpoch > lookbackEpochs {
		return latestEpoch - lookbackEpochs
	}

	return 0
}

// ValidatorDutyStat tracks the block production performance of a single validator: expected
// vs actually proposed canonical blocks, plus (Gloas+) execution payload delivery for its
// canonical proposals and expected vs included PTC votes.
type ValidatorDutyStat struct {
	ProposalsExpected uint64
	ProposalsProposed uint64
	PayloadsExpected  uint64
	PayloadsDelivered uint64
	PtcExpected       uint64
	PtcIncluded       uint64
}

// ValidatorDutyStats holds per-validator block production stats over a lookback window.
// PTC stats are computed from in-memory block bodies only, so their effective window can be
// smaller than the requested lookback: PtcFirstVoteSlot is the first vote slot that was
// actually considered. HasPtcStats is false pre-Gloas, where no PTC duties exist.
type ValidatorDutyStats struct {
	Validators       map[phase0.ValidatorIndex]*ValidatorDutyStat
	HasPtcStats      bool
	PtcFirstVoteSlot phase0.Slot
}

// GetValidatorDutyStats returns proposal, payload delivery (Gloas+) and PTC inclusion
// (Gloas+) stats per validator. The two metric groups take separate lookback windows:
//
// Proposal & payload stats (proposalLookbackEpochs) are based on the combined cache &
// database slot listing, so they stay correct across the block cache pruning and
// finalization boundaries and support long windows. Canonical blocks count as expected and
// proposed for their author, missed duty slots as expected for the assigned proposer, and
// slots with only reorged blocks as expected for the reorged author. Payload delivery is
// tracked separately, as a canonical beacon block does not imply the execution payload
// envelope made it onto the canonical chain.
//
// PTC stats (ptcLookbackEpochs) are computed from in-memory state only: PTC duties from
// EpochStats.PtcDuties + payload attestations from cached canonical block bodies, gated to
// the slot range whose bodies are still held in memory.
func (bs *ChainService) GetValidatorDutyStats(ctx context.Context, proposalLookbackEpochs phase0.Epoch, ptcLookbackEpochs phase0.Epoch) *ValidatorDutyStats {
	result := &ValidatorDutyStats{
		Validators: make(map[phase0.ValidatorIndex]*ValidatorDutyStat, 100),
	}

	chainState := bs.consensusPool.GetChainState()
	if chainState.GetSpecs() == nil {
		return result
	}

	currentEpoch := chainState.CurrentEpoch()
	currentSlot := chainState.CurrentSlot()
	startEpoch := phase0.Epoch(0)
	if currentEpoch > proposalLookbackEpochs {
		startEpoch = currentEpoch - proposalLookbackEpochs
	}
	startSlot := chainState.EpochToSlot(startEpoch)

	getStat := func(index phase0.ValidatorIndex) *ValidatorDutyStat {
		stat := result.Validators[index]
		if stat == nil {
			stat = &ValidatorDutyStat{}
			result.Validators[index] = stat
		}
		return stat
	}

	// proposal & payload delivery stats from the combined cache & database slot listing
	slotBlocks := bs.GetDbBlocksForSlots(ctx, uint64(currentSlot), uint32(currentSlot-startSlot), true, true)

	// a slot can yield multiple rows (canonical block, orphaned blocks, missing-duty row).
	// Only one row per slot may count: canonical wins, then the missing-duty row, then a
	// reorged-only slot counts as a miss for the orphaned block's author.
	canonicalSlots := make(map[uint64]bool, len(slotBlocks))
	missingSlots := make(map[uint64]bool, len(slotBlocks))
	for _, slotBlock := range slotBlocks {
		switch slotBlock.Status {
		case dbtypes.Canonical:
			canonicalSlots[slotBlock.Slot] = true
		case dbtypes.Missing:
			missingSlots[slotBlock.Slot] = true
		}
	}

	countedOrphanedSlots := make(map[uint64]bool, len(slotBlocks))

	for _, slotBlock := range slotBlocks {
		if slotBlock.Proposer == uint64(math.MaxInt64) {
			continue // proposer assignment unknown for this slot
		}

		switch slotBlock.Status {
		case dbtypes.Missing:
			if canonicalSlots[slotBlock.Slot] {
				continue
			}
		case dbtypes.Orphaned:
			if canonicalSlots[slotBlock.Slot] || missingSlots[slotBlock.Slot] || countedOrphanedSlots[slotBlock.Slot] {
				continue
			}
			countedOrphanedSlots[slotBlock.Slot] = true
		}

		stat := getStat(phase0.ValidatorIndex(slotBlock.Proposer))

		stat.ProposalsExpected++
		if slotBlock.Status != dbtypes.Canonical {
			continue
		}
		stat.ProposalsProposed++

		// track payload delivery separately (Gloas+). Skip the current slot as its payload
		// might still be in flight.
		slot := phase0.Slot(slotBlock.Slot)
		if slot < currentSlot && chainState.IsEip7732Enabled(chainState.EpochOfSlot(slot)) {
			stat.PayloadsExpected++
			if slotBlock.PayloadStatus == dbtypes.PayloadStatusCanonical {
				stat.PayloadsDelivered++
			}
		}
	}

	// PTC inclusion stats (Gloas+ only) from cached canonical block bodies
	if !chainState.IsEip7732Enabled(currentEpoch) {
		return result
	}

	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	if canonicalHead == nil {
		return result
	}

	ptcStartEpoch := phase0.Epoch(0)
	if currentEpoch > ptcLookbackEpochs {
		ptcStartEpoch = currentEpoch - ptcLookbackEpochs
	}

	// block bodies are only available in memory above both boundaries: pruning drops bodies
	// below the pruned epoch, finalization evicts blocks below the finalized epoch entirely.
	// Gate expected and included votes on the same boundary so the ratio stays consistent.
	finalizedEpoch, prunedEpoch := bs.beaconIndexer.GetBlockCacheState()
	minBodySlot := chainState.EpochToSlot(prunedEpoch)
	if finalizedSlot := chainState.EpochToSlot(finalizedEpoch); finalizedSlot > minBodySlot {
		minBodySlot = finalizedSlot
	}
	if ptcStartSlot := chainState.EpochToSlot(ptcStartEpoch); ptcStartSlot > minBodySlot {
		minBodySlot = ptcStartSlot
	}

	// resolve the canonical chain once by walking back from the head via parent links
	canonicalBlocks := make(map[phase0.Slot]*beacon.Block, uint64(currentSlot-minBodySlot)+1)
	for block := canonicalHead; block != nil && block.Slot >= minBodySlot; {
		canonicalBlocks[block.Slot] = block
		parentRoot := block.GetParentRoot()
		if parentRoot == nil {
			break
		}
		block = bs.beaconIndexer.GetBlockByRoot(*parentRoot)
	}

	result.HasPtcStats = true
	result.PtcFirstVoteSlot = minBodySlot

	for epoch := ptcStartEpoch; epoch <= currentEpoch; epoch++ {
		if !chainState.IsEip7732Enabled(epoch) {
			continue
		}

		epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil)
		if epochStats == nil {
			continue
		}
		values := epochStats.GetValues(true)
		if values == nil || len(values.PtcDuties) == 0 || len(values.ActiveIndices) == 0 {
			continue
		}

		epochStartSlot := chainState.EpochToSlot(epoch)
		for slotIdx, slotDuties := range values.PtcDuties {
			if len(slotDuties) == 0 {
				continue
			}
			dutySlot := epochStartSlot + phase0.Slot(slotIdx)
			voteSlot := dutySlot + 1
			if voteSlot > currentSlot {
				break
			}
			if voteSlot < minBodySlot {
				continue
			}

			canonicalBlock := canonicalBlocks[voteSlot]
			if canonicalBlock == nil {
				continue
			}
			body := canonicalBlock.GetCachedBlock()
			if body == nil || body.Message == nil || body.Message.Body == nil {
				continue
			}

			payloadAttestations := body.Message.Body.PayloadAttestations
			if len(payloadAttestations) == 0 {
				continue
			}

			voted := make([]bool, len(slotDuties))
			for _, pa := range payloadAttestations {
				if pa == nil {
					continue
				}
				bitCount := len(pa.AggregationBits) * 8
				if bitCount > len(slotDuties) {
					bitCount = len(slotDuties)
				}
				for i := 0; i < bitCount; i++ {
					if (pa.AggregationBits[i/8]>>(i%8))&1 == 1 {
						voted[i] = true
					}
				}
			}

			// Unique-validator semantics: on small validator sets a validator may
			// occupy multiple PTC positions via balance-weighted selection. Mirror
			// the slot page — one expected vote per unique validator, included if
			// any of their positions voted.
			expected := make(map[phase0.ValidatorIndex]bool, len(slotDuties))
			included := make(map[phase0.ValidatorIndex]bool, len(slotDuties))
			for pos, activeIdx := range slotDuties {
				if int(activeIdx) >= len(values.ActiveIndices) {
					continue
				}
				vidx := values.ActiveIndices[activeIdx]
				expected[vidx] = true
				if voted[pos] {
					included[vidx] = true
				}
			}

			for vidx := range expected {
				stat := getStat(vidx)
				stat.PtcExpected++
				if included[vidx] {
					stat.PtcIncluded++
				}
			}
		}
	}

	return result
}

type ValidatorWithIndex struct {
	Index     phase0.ValidatorIndex
	Validator *phase0.Validator
}

// balanceAt returns the balance at the given validator index, or 0 if the index is
// beyond the balances slice. Projected (pending-deposit) validators live at indexes
// past the on-chain balances array and always have a zero balance, so falling back to
// 0 is correct and avoids out-of-range panics.
func balanceAt(balances []phase0.Gwei, index phase0.ValidatorIndex) phase0.Gwei {
	if int(index) < len(balances) {
		return balances[index]
	}
	return 0
}

// getValidatorsByWithdrawalAddressForRoot returns validators with a specific withdrawal address for a given blockRoot
func (bs *ChainService) GetFilteredValidatorSet(ctx context.Context, filter *dbtypes.ValidatorFilter, withBalance bool) ([]v1.Validator, uint64) {
	var overrideForkId *beacon.ForkKey

	canonicalHead := bs.beaconIndexer.GetCanonicalHead(overrideForkId)
	if canonicalHead == nil {
		return nil, 0
	}

	var balances []phase0.Gwei
	if withBalance {
		balances = bs.beaconIndexer.GetRecentValidatorBalances(overrideForkId)
	}
	currentEpoch := bs.consensusPool.GetChainState().CurrentEpoch()

	// The cache only carries full validator objects for validators whose state changed
	// since startup (and for projected validators); every other validator is read from
	// the db. A cached state is newer than its db row, so cached indexes supersede their
	// db rows in the merge below. Validators are persisted contiguously from index 0, so
	// persistedCount tells which cached entries have a db row at all.
	persistedCount := uint64(0)
	if filter.Limit > 0 {
		var err error
		persistedCount, err = db.GetValidatorCountByFilter(ctx, dbtypes.ValidatorFilter{}, uint64(currentEpoch))
		if err != nil {
			bs.logger.Warnf("error counting persisted validators: %v", err)
			return nil, 0
		}
	}

	cachedResults := make([]ValidatorWithIndex, 0, 1000)
	cachedIndexes := newValidatorIndexSet(bs.beaconIndexer.GetValidatorSetSize())
	cachedCount := uint64(0)
	cachedBeyondPersisted := uint64(0)

	// get matching entries from cached validators
	bs.beaconIndexer.StreamActiveValidatorDataForRoot(canonicalHead.Root, false, &currentEpoch, func(index phase0.ValidatorIndex, flags uint16, activeData *beacon.ValidatorData, validator *phase0.Validator) error {
		if validator == nil {
			return nil
		}
		cachedIndexes.add(index)
		cachedCount++
		if uint64(index) >= persistedCount {
			cachedBeyondPersisted++
		}

		if filter.MinIndex != nil && index < phase0.ValidatorIndex(*filter.MinIndex) {
			return nil
		}
		if filter.MaxIndex != nil && index > phase0.ValidatorIndex(*filter.MaxIndex) {
			return nil
		}
		if len(filter.Indices) > 0 && !slices.Contains(filter.Indices, index) {
			return nil
		}
		if len(filter.PubKey) > 0 {
			pubkeylen := len(filter.PubKey)
			if pubkeylen > 48 {
				pubkeylen = 48
			}
			if !bytes.Equal(validator.PublicKey[:pubkeylen], filter.PubKey) {
				return nil
			}
		}
		if filter.WithdrawalAddress != nil {
			if validator.WithdrawalCredentials[0] != 0x01 && validator.WithdrawalCredentials[0] != 0x02 {
				return nil
			}
			if !bytes.Equal(validator.WithdrawalCredentials[12:], filter.WithdrawalAddress[:]) {
				return nil
			}
		}
		if filter.WithdrawalCreds != nil && !bytes.Equal(validator.WithdrawalCredentials, filter.WithdrawalCreds) {
			return nil
		}
		if len(filter.WithdrawalCredTypes) > 0 {
			if len(validator.WithdrawalCredentials) == 0 || !slices.Contains(filter.WithdrawalCredTypes, validator.WithdrawalCredentials[0]) {
				return nil
			}
		}
		if filter.ValidatorName != "" {
			vname := bs.validatorNames.GetValidatorName(uint64(index))
			if !strings.Contains(vname, filter.ValidatorName) {
				return nil
			}
		}

		if len(filter.Status) > 0 {
			var balancePtr *phase0.Gwei
			if balances != nil && len(balances) > int(index) {
				balancePtr = &balances[index]
			}
			validatorState := utils.ValidatorToState(validator, balancePtr, currentEpoch, beacon.FarFutureEpoch)
			if !slices.Contains(filter.Status, validatorState) {
				return nil
			}
		}

		cachedResults = append(cachedResults, ValidatorWithIndex{
			Index:     index,
			Validator: validator,
		})

		return nil
	})

	// get matching entries from DB. With a limit, only the rows the requested window can
	// reach are fetched and the total count is computed separately instead of by walking
	// all matches. A cached entry pushes a db row back by at most one position, so
	// offset+limit plus the number of cached entries sorting before db rows covers the
	// window. For index ordering a cached entry with a db row replaces it in place, and
	// the entries beyond the persisted rows sort after (ascending) resp. before
	// (descending) every db row - in the descending case they fill the window on their
	// own until the offset reaches past them. For other orderings every cached entry may
	// sort elsewhere than its db row.
	dbLimit := uint64(0)
	skipDb := false
	if filter.Limit > 0 {
		window := filter.Offset + filter.Limit
		switch filter.OrderBy {
		case dbtypes.ValidatorOrderIndexAsc:
			dbLimit = window
		case dbtypes.ValidatorOrderIndexDesc:
			if window > cachedBeyondPersisted {
				dbLimit = window - cachedBeyondPersisted
			} else {
				skipDb = true
			}
		default:
			dbLimit = window + cachedCount
		}
	}

	var dbIndexes []uint64
	if !skipDb {
		var err error
		dbIndexes, err = db.GetValidatorIndexesByFilter(ctx, *filter, uint64(currentEpoch), dbLimit)
		if err != nil {
			bs.logger.Warnf("error getting validator indexes by filter: %v", err)
			return nil, 0
		}
	}

	// sort results
	var sortFn func(valA, valB ValidatorWithIndex) bool
	switch filter.OrderBy {
	case dbtypes.ValidatorOrderIndexAsc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return valA.Index < valB.Index
		}
	case dbtypes.ValidatorOrderIndexDesc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return valA.Index > valB.Index
		}
	case dbtypes.ValidatorOrderPubKeyAsc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return bytes.Compare(valA.Validator.PublicKey[:], valB.Validator.PublicKey[:]) < 0
		}
	case dbtypes.ValidatorOrderPubKeyDesc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return bytes.Compare(valA.Validator.PublicKey[:], valB.Validator.PublicKey[:]) > 0
		}
	case dbtypes.ValidatorOrderBalanceAsc:
		if balances == nil {
			sortFn = func(valA, valB ValidatorWithIndex) bool {
				return valA.Validator.EffectiveBalance < valB.Validator.EffectiveBalance
			}
		} else {
			sortFn = func(valA, valB ValidatorWithIndex) bool {
				return balanceAt(balances, valA.Index) < balanceAt(balances, valB.Index)
			}
			sort.Slice(dbIndexes, func(i, j int) bool {
				return balanceAt(balances, phase0.ValidatorIndex(dbIndexes[i])) < balanceAt(balances, phase0.ValidatorIndex(dbIndexes[j]))
			})
		}
	case dbtypes.ValidatorOrderBalanceDesc:
		if balances == nil {
			sortFn = func(valA, valB ValidatorWithIndex) bool {
				return valA.Validator.EffectiveBalance > valB.Validator.EffectiveBalance
			}
		} else {
			sortFn = func(valA, valB ValidatorWithIndex) bool {
				return balanceAt(balances, valA.Index) > balanceAt(balances, valB.Index)
			}
			sort.Slice(dbIndexes, func(i, j int) bool {
				return balanceAt(balances, phase0.ValidatorIndex(dbIndexes[i])) > balanceAt(balances, phase0.ValidatorIndex(dbIndexes[j]))
			})
		}
	case dbtypes.ValidatorOrderActivationEpochAsc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return valA.Validator.ActivationEpoch < valB.Validator.ActivationEpoch
		}
	case dbtypes.ValidatorOrderActivationEpochDesc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return valA.Validator.ActivationEpoch > valB.Validator.ActivationEpoch
		}
	case dbtypes.ValidatorOrderExitEpochAsc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return valA.Validator.ExitEpoch < valB.Validator.ExitEpoch
		}
	case dbtypes.ValidatorOrderExitEpochDesc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return valA.Validator.ExitEpoch > valB.Validator.ExitEpoch
		}
	case dbtypes.ValidatorOrderWithdrawableEpochAsc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return valA.Validator.WithdrawableEpoch < valB.Validator.WithdrawableEpoch
		}
	case dbtypes.ValidatorOrderWithdrawableEpochDesc:
		sortFn = func(valA, valB ValidatorWithIndex) bool {
			return valA.Validator.WithdrawableEpoch > valB.Validator.WithdrawableEpoch
		}
	}

	sort.Slice(cachedResults, func(i, j int) bool {
		return sortFn(cachedResults[i], cachedResults[j])
	})

	// stream validator set from db and merge cached results
	resCap := filter.Limit
	if resCap == 0 {
		resCap = uint64(len(cachedResults) + len(dbIndexes))
	}
	result := make([]v1.Validator, 0, resCap)
	cachedIndex := 0
	matchingCount := uint64(0)
	resultCount := uint64(0)
	dbEntryCount := uint64(0)

	db.StreamValidatorsByIndexes(ctx, dbIndexes, func(validator *dbtypes.Validator) bool {
		dbEntryCount++
		validatorWithIndex := ValidatorWithIndex{
			Index:     phase0.ValidatorIndex(validator.ValidatorIndex),
			Validator: beacon.UnwrapDbValidator(validator),
		}

		for cachedIndex < len(cachedResults) && (cachedResults[cachedIndex].Index == validatorWithIndex.Index || sortFn(cachedResults[cachedIndex], validatorWithIndex)) {
			if matchingCount >= filter.Offset {
				balance := phase0.Gwei(0)
				var balancePtr *phase0.Gwei
				if balances != nil && len(balances) > int(cachedResults[cachedIndex].Index) {
					balance = balances[cachedResults[cachedIndex].Index]
					balancePtr = &balance
				}
				result = append(result, v1.Validator{
					Index:     cachedResults[cachedIndex].Index,
					Balance:   balance,
					Status:    utils.ValidatorToState(cachedResults[cachedIndex].Validator, balancePtr, currentEpoch, beacon.FarFutureEpoch),
					Validator: cachedResults[cachedIndex].Validator,
				})
				resultCount++
			}
			matchingCount++
			cachedIndex++

			if filter.Limit > 0 && resultCount >= filter.Limit {
				return false // stop streaming
			}
		}

		if cachedIndexes.has(phase0.ValidatorIndex(validator.ValidatorIndex)) {
			return true // skip this index, cache entry is newer
		}

		if matchingCount >= filter.Offset {
			balance := phase0.Gwei(0)
			var balancePtr *phase0.Gwei
			if balances != nil && len(balances) > int(validator.ValidatorIndex) {
				balance = balances[validator.ValidatorIndex]
				balancePtr = &balance
			}
			validatorData := beacon.UnwrapDbValidator(validator)
			result = append(result, v1.Validator{
				Index:     phase0.ValidatorIndex(validator.ValidatorIndex),
				Balance:   balance,
				Status:    utils.ValidatorToState(validatorData, balancePtr, currentEpoch, beacon.FarFutureEpoch),
				Validator: validatorData,
			})
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
			balance := phase0.Gwei(0)
			index := int(cachedResults[cachedIndex].Index)
			var balancePtr *phase0.Gwei
			if balances != nil && len(balances) > index {
				balance = balances[index]
				balancePtr = &balance
			}
			result = append(result, v1.Validator{
				Index:     phase0.ValidatorIndex(index),
				Balance:   balance,
				Status:    utils.ValidatorToState(cachedResults[cachedIndex].Validator, balancePtr, currentEpoch, beacon.FarFutureEpoch),
				Validator: cachedResults[cachedIndex].Validator,
			})
			resultCount++
		}
		matchingCount++
		cachedIndex++
	}

	if filter.Limit > 0 {
		totalCount, err := bs.countFilteredValidators(ctx, filter, currentEpoch, cachedResults, persistedCount)
		if err != nil {
			bs.logger.Warnf("error counting validators by filter: %v", err)
			return nil, 0
		}
		return result, totalCount
	}

	// add remaining cached results
	matchingCount += uint64(len(cachedResults) - cachedIndex)

	// add remaining db results
	remainingDbCount := uint64(0)
	for i := dbEntryCount; i < uint64(len(dbIndexes)); i++ {
		if cachedIndexes.has(phase0.ValidatorIndex(dbIndexes[i])) {
			continue
		}
		remainingDbCount++
	}
	matchingCount += remainingDbCount

	return result, matchingCount
}

// countFilteredValidators returns the number of validators matching the filter across
// db and cache. Validators are persisted contiguously from index 0, so the db count covers
// every cached match with a persisted row and only cached matches beyond the persisted
// rows are added. Cached state changes that are not persisted yet can make status
// dependent counts differ by the affected validators, which is fine for paging.
func (bs *ChainService) countFilteredValidators(ctx context.Context, filter *dbtypes.ValidatorFilter, currentEpoch phase0.Epoch, cachedResults []ValidatorWithIndex, persistedCount uint64) (uint64, error) {
	dbCount, err := db.GetValidatorCountByFilter(ctx, *filter, uint64(currentEpoch))
	if err != nil {
		return 0, err
	}

	unpersistedMatches := uint64(0)
	for _, cached := range cachedResults {
		if uint64(cached.Index) >= persistedCount {
			unpersistedMatches++
		}
	}

	return dbCount + unpersistedMatches, nil
}

// validatorIndexSet is a bitset over validator indexes. It replaces a map for the
// cached-index membership test in the validator set merge, which would otherwise be
// filled once per cached validator on every page build.
type validatorIndexSet []uint64

func newValidatorIndexSet(size uint64) validatorIndexSet {
	return make(validatorIndexSet, (size+63)/64)
}

func (set *validatorIndexSet) add(index phase0.ValidatorIndex) {
	word := uint64(index) / 64
	if word >= uint64(len(*set)) {
		*set = append(*set, make(validatorIndexSet, word-uint64(len(*set))+1)...)
	}
	(*set)[word] |= 1 << (uint64(index) % 64)
}

func (set validatorIndexSet) has(index phase0.ValidatorIndex) bool {
	word := uint64(index) / 64
	return word < uint64(len(set)) && set[word]&(1<<(uint64(index)%64)) != 0
}
