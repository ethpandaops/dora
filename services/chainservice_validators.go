package services

import (
	"bytes"
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

func (bs *ChainService) GetValidatorByIndex(index phase0.ValidatorIndex, withBalance bool) *v1.Validator {
	currentEpoch := bs.consensusPool.GetChainState().CurrentEpoch()
	return bs.beaconIndexer.GetFullValidatorByIndex(index, currentEpoch, nil, withBalance)
}

func (bs *ChainService) GetValidatorIndexByPubkey(pubkey phase0.BLSPubKey) (phase0.ValidatorIndex, bool) {
	return bs.beaconIndexer.GetValidatorIndexByPubkey(pubkey)
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
	chainState := bs.consensusPool.GetChainState()
	latestEpoch := chainState.CurrentEpoch()
	if latestEpoch > lookbackEpochs {
		latestEpoch -= lookbackEpochs
	} else {
		latestEpoch = 0
	}

	validatorActivity, _ := bs.beaconIndexer.GetValidatorActivityCount(validatorIndex, latestEpoch)
	if validatorActivity > uint64(lookbackEpochs) {
		validatorActivity = uint64(lookbackEpochs)
	}

	return validatorActivity
}

// ValidatorProposalStat tracks the number of expected vs actually proposed canonical blocks for a validator.
type ValidatorProposalStat struct {
	Expected uint64
	Proposed uint64
}

// ValidatorPtcStat tracks the number of expected vs actually included PTC votes for a validator.
type ValidatorPtcStat struct {
	Expected uint64
	Included uint64
}

// GetValidatorProposalStats returns expected vs canonical proposals per validator over the
// last lookbackEpochs epochs. A duty counts as "proposed" only if the canonical block for the
// duty slot was authored by the assigned proposer.
func (bs *ChainService) GetValidatorProposalStats(ctx context.Context, lookbackEpochs phase0.Epoch) map[phase0.ValidatorIndex]*ValidatorProposalStat {
	chainState := bs.consensusPool.GetChainState()
	if chainState.GetSpecs() == nil {
		return nil
	}

	currentEpoch := chainState.CurrentEpoch()
	currentSlot := chainState.CurrentSlot()
	startEpoch := phase0.Epoch(0)
	if currentEpoch > lookbackEpochs {
		startEpoch = currentEpoch - lookbackEpochs
	}

	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	result := make(map[phase0.ValidatorIndex]*ValidatorProposalStat)

	for epoch := startEpoch; epoch <= currentEpoch; epoch++ {
		epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil)
		if epochStats == nil {
			continue
		}
		values := epochStats.GetValues(true)
		if values == nil || len(values.ProposerDuties) == 0 {
			continue
		}

		startSlot := chainState.EpochToSlot(epoch)
		for slotIdx, proposer := range values.ProposerDuties {
			slot := startSlot + phase0.Slot(slotIdx)
			if slot > currentSlot {
				break
			}

			stat := result[proposer]
			if stat == nil {
				stat = &ValidatorProposalStat{}
				result[proposer] = stat
			}
			stat.Expected++

			for _, block := range bs.beaconIndexer.GetBlocksBySlot(slot) {
				if !bs.beaconIndexer.IsCanonicalBlockByHead(block, canonicalHead) {
					continue
				}
				header := block.GetHeader()
				if header == nil {
					continue
				}
				if header.Message.ProposerIndex == proposer {
					stat.Proposed++
					break
				}
			}
		}
	}

	return result
}

// GetValidatorPtcStats returns expected vs included PTC votes per validator
// over the last lookbackEpochs epochs, computed on demand from in-memory state:
// PTC duties from EpochStats.PtcDuties + payload attestations from cached
// canonical block bodies. Slots whose voting block is not in the in-memory
// block cache are skipped entirely (no expected, no included). PTC is Gloas+
// only; nil is returned for pre-Gloas epochs.
func (bs *ChainService) GetValidatorPtcStats(ctx context.Context, lookbackEpochs phase0.Epoch) map[phase0.ValidatorIndex]*ValidatorPtcStat {
	chainState := bs.consensusPool.GetChainState()
	if chainState.GetSpecs() == nil || !chainState.IsEip7732Enabled(chainState.CurrentEpoch()) {
		return nil
	}

	currentEpoch := chainState.CurrentEpoch()
	currentSlot := chainState.CurrentSlot()
	startEpoch := phase0.Epoch(0)
	if currentEpoch > lookbackEpochs {
		startEpoch = currentEpoch - lookbackEpochs
	}

	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	result := make(map[phase0.ValidatorIndex]*ValidatorPtcStat)

	for epoch := startEpoch; epoch <= currentEpoch; epoch++ {
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

		startSlot := chainState.EpochToSlot(epoch)
		for slotIdx, slotDuties := range values.PtcDuties {
			if len(slotDuties) == 0 {
				continue
			}
			dutySlot := startSlot + phase0.Slot(slotIdx)
			voteSlot := dutySlot + 1
			if voteSlot > currentSlot {
				break
			}

			// find the canonical block at voteSlot in the in-memory block cache
			var canonicalBlock *beacon.Block
			for _, b := range bs.beaconIndexer.GetBlocksBySlot(voteSlot) {
				if bs.beaconIndexer.IsCanonicalBlockByHead(b, canonicalHead) {
					canonicalBlock = b
					break
				}
			}
			if canonicalBlock == nil {
				continue
			}
			body := canonicalBlock.GetCachedBlock()
			if body == nil {
				continue
			}

			var payloadAttestations []*gloas.PayloadAttestation
			switch {
			case body.Gloas != nil:
				payloadAttestations = body.Gloas.Message.Body.PayloadAttestations
			case body.Heze != nil:
				payloadAttestations = body.Heze.Message.Body.PayloadAttestations
			default:
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
				stat := result[vidx]
				if stat == nil {
					stat = &ValidatorPtcStat{}
					result[vidx] = stat
				}
				stat.Expected++
				if included[vidx] {
					stat.Included++
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

	cachedResults := make([]ValidatorWithIndex, 0, 1000)
	cachedIndexes := map[uint64]bool{}

	// get matching entries from cached validators
	bs.beaconIndexer.StreamActiveValidatorDataForRoot(canonicalHead.Root, false, &currentEpoch, func(index phase0.ValidatorIndex, flags uint16, activeData *beacon.ValidatorData, validator *phase0.Validator) error {
		if validator == nil {
			return nil
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
		if filter.ValidatorName != "" {
			vname := bs.validatorNames.GetValidatorName(uint64(index))
			if !strings.Contains(vname, filter.ValidatorName) {
				return nil
			}
		}

		if len(filter.Status) > 0 {
			var balancePtr *phase0.Gwei
			if balances != nil {
				balancePtr = &balances[index]
			}
			validatorState := v1.ValidatorToState(validator, balancePtr, currentEpoch, beacon.FarFutureEpoch)
			if !slices.Contains(filter.Status, validatorState) {
				return nil
			}
		}

		cachedResults = append(cachedResults, ValidatorWithIndex{
			Index:     index,
			Validator: validator,
		})
		cachedIndexes[uint64(index)] = true

		return nil
	})

	// get matching entries from DB
	dbIndexes, err := db.GetValidatorIndexesByFilter(ctx, *filter, uint64(currentEpoch))
	if err != nil {
		bs.logger.Warnf("error getting validator indexes by filter: %v", err)
		return nil, 0
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
				return balances[valA.Index] < balances[valB.Index]
			}
			sort.Slice(dbIndexes, func(i, j int) bool {
				return balances[dbIndexes[i]] < balances[dbIndexes[j]]
			})
		}
	case dbtypes.ValidatorOrderBalanceDesc:
		if balances == nil {
			sortFn = func(valA, valB ValidatorWithIndex) bool {
				return valA.Validator.EffectiveBalance > valB.Validator.EffectiveBalance
			}
		} else {
			sortFn = func(valA, valB ValidatorWithIndex) bool {
				return balances[valA.Index] > balances[valB.Index]
			}
			sort.Slice(dbIndexes, func(i, j int) bool {
				return balances[dbIndexes[i]] > balances[dbIndexes[j]]
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
				if balances != nil {
					balance = balances[cachedResults[cachedIndex].Index]
					balancePtr = &balance
				}
				result = append(result, v1.Validator{
					Index:     cachedResults[cachedIndex].Index,
					Balance:   balance,
					Status:    v1.ValidatorToState(cachedResults[cachedIndex].Validator, balancePtr, currentEpoch, beacon.FarFutureEpoch),
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

		if cachedIndexes[validator.ValidatorIndex] {
			return true // skip this index, cache entry is newer
		}

		if matchingCount >= filter.Offset {
			balance := phase0.Gwei(0)
			var balancePtr *phase0.Gwei
			if balances != nil {
				balance = balances[validator.ValidatorIndex]
				balancePtr = &balance
			}
			validatorData := beacon.UnwrapDbValidator(validator)
			result = append(result, v1.Validator{
				Index:     phase0.ValidatorIndex(validator.ValidatorIndex),
				Balance:   balance,
				Status:    v1.ValidatorToState(validatorData, balancePtr, currentEpoch, beacon.FarFutureEpoch),
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
			var balancePtr *phase0.Gwei
			if balances != nil {
				balance = balances[cachedResults[cachedIndex].Index]
				balancePtr = &balance
			}
			result = append(result, v1.Validator{
				Index:     phase0.ValidatorIndex(cachedResults[cachedIndex].Index),
				Balance:   balance,
				Status:    v1.ValidatorToState(cachedResults[cachedIndex].Validator, balancePtr, currentEpoch, beacon.FarFutureEpoch),
				Validator: cachedResults[cachedIndex].Validator,
			})
			resultCount++
		}
		matchingCount++
		cachedIndex++
	}

	// add remaining cached results
	matchingCount += uint64(len(cachedResults) - cachedIndex)

	// add remaining db results
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
