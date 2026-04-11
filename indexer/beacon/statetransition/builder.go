package statetransition

import (
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// Gloas-specific spec constants for builder payment quorum.
const (
	BuilderPaymentThresholdNumerator   = 6
	BuilderPaymentThresholdDenominator = 10
)

// processBuilderPendingPayments implements process_builder_pending_payments (Gloas).
// Evaluates the first SLOTS_PER_EPOCH entries of BuilderPendingPayments against
// the quorum threshold. Qualifying payments are promoted to BuilderPendingWithdrawals.
// Then the 2-epoch window shifts forward.
// New in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-process_builder_pending_payments
// processBuilderPendingPayments returns the number of delayed payments appended to
// BuilderPendingWithdrawals.
func processBuilderPendingPayments(s *stateAccessor) uint32 {
	slotsPerEpoch := s.specs.SlotsPerEpoch
	quorum := getBuilderPaymentQuorumThreshold(s)

	// Evaluate first half (epoch K-1 payments)
	limit := slotsPerEpoch
	if limit > uint64(len(s.BuilderPendingPayments)) {
		limit = uint64(len(s.BuilderPendingPayments))
	}

	count := uint32(0)
	for i := uint64(0); i < limit; i++ {
		payment := s.BuilderPendingPayments[i]
		if payment == nil || payment.Withdrawal == nil {
			continue
		}
		if uint64(payment.Weight) >= quorum {
			s.BuilderPendingWithdrawals = append(s.BuilderPendingWithdrawals, payment.Withdrawal)
			count++
		}
	}

	// Shift window: move second half to first, fill second with empty entries
	if uint64(len(s.BuilderPendingPayments)) >= 2*slotsPerEpoch {
		copy(s.BuilderPendingPayments[:slotsPerEpoch], s.BuilderPendingPayments[slotsPerEpoch:2*slotsPerEpoch])
		for i := slotsPerEpoch; i < 2*slotsPerEpoch; i++ {
			s.BuilderPendingPayments[i] = &gloas.BuilderPendingPayment{}
		}
	}

	return count
}

// getBuilderPaymentQuorumThreshold computes the quorum threshold for builder payments.
// New in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-get_builder_payment_quorum_threshold
func getBuilderPaymentQuorumThreshold(s *stateAccessor) uint64 {
	totalActiveBalance := uint64(s.getTotalActiveBalance())
	perSlotBalance := totalActiveBalance / s.specs.SlotsPerEpoch
	return perSlotBalance * BuilderPaymentThresholdNumerator / BuilderPaymentThresholdDenominator
}

// processPtcWindow implements process_ptc_window (Gloas).
// Slides the PTC assignment window and computes new assignments for the lookahead epoch.
// New in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-process_ptc_window
func processPtcWindow(s *stateAccessor) {
	// PTC window is Gloas-only.
	if s.version < spec.DataVersionGloas {
		return
	}

	if len(s.PTCWindow) == 0 {
		return
	}

	slotsPerEpoch := s.specs.SlotsPerEpoch

	// Slide window: remove first SLOTS_PER_EPOCH entries
	windowLen := uint64(len(s.PTCWindow))
	if windowLen <= slotsPerEpoch {
		return
	}

	copy(s.PTCWindow, s.PTCWindow[slotsPerEpoch:])

	// Compute new PTC assignments for the last SLOTS_PER_EPOCH entries
	nextEpoch := s.currentEpoch() + phase0.Epoch(s.specs.MinSeedLookahead) + 1
	startSlot := uint64(nextEpoch) * slotsPerEpoch

	lastStart := windowLen - slotsPerEpoch
	for i := uint64(0); i < slotsPerEpoch && lastStart+i < windowLen; i++ {
		s.PTCWindow[lastStart+i] = computePtc(s, phase0.Slot(startSlot+i))
	}
}

// computePtc computes the PTC (Payload Timeliness Committee) for a given slot.
// Concatenates all beacon committees for the slot, then uses balance-weighted
// selection (without shuffling) to pick PTC_SIZE members.
// New in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-compute_ptc
func computePtc(s *stateAccessor, slot phase0.Slot) []phase0.ValidatorIndex {
	epoch := phase0.Epoch(uint64(slot) / s.specs.SlotsPerEpoch)
	ptcSize := s.specs.PtcSize
	if ptcSize == 0 {
		return nil
	}

	// seed = hash(get_seed(state, epoch, DOMAIN_PTC_ATTESTER) + uint_to_bytes(slot))
	epochSeed := getSeed(s, epoch, phase0.DomainType(s.specs.DomainPtcAttester))
	var buf [40]byte
	copy(buf[:32], epochSeed[:])
	buf[32] = byte(slot)
	buf[33] = byte(slot >> 8)
	buf[34] = byte(slot >> 16)
	buf[35] = byte(slot >> 24)
	buf[36] = byte(slot >> 32)
	buf[37] = byte(slot >> 40)
	buf[38] = byte(slot >> 48)
	buf[39] = byte(slot >> 56)
	seed := hash256(buf[:])

	// Concatenate all committees for this slot
	cc := newCommitteeCache()
	committeesPerSlot := s.getCommitteeCountPerSlot(epoch)
	var indices []phase0.ValidatorIndex
	for ci := uint64(0); ci < committeesPerSlot; ci++ {
		committee := s.getBeaconCommittee(slot, ci, cc)
		indices = append(indices, committee...)
	}

	if len(indices) == 0 {
		return nil
	}

	// compute_balance_weighted_selection(state, indices, seed, size=PTC_SIZE, shuffle_indices=False)
	return computeBalanceWeightedSelection(s, indices, seed, ptcSize, false)
}

// computeBalanceWeightedSelection implements compute_balance_weighted_selection.
// Selects `size` validators from `indices` using balance-weighted rejection sampling.
// If shuffleIndices is true, candidates are sampled via compute_shuffled_index;
// otherwise they are traversed in order.
// https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-compute_balance_weighted_selection
func computeBalanceWeightedSelection(s *stateAccessor, indices []phase0.ValidatorIndex, seed phase0.Root, size uint64, shuffleIndices bool) []phase0.ValidatorIndex {
	const maxRandomValue = 65535 // 2^16 - 1
	total := uint64(len(indices))
	if total == 0 {
		return nil
	}

	maxEB := uint64(s.specs.MaxEffectiveBalanceElectra)
	if maxEB == 0 {
		maxEB = uint64(s.specs.MaxEffectiveBalance)
	}

	// Pre-compute effective balances for the candidate indices
	effectiveBalances := make([]uint64, total)
	for j, idx := range indices {
		effectiveBalances[j] = uint64(s.Validators[idx].EffectiveBalance)
	}

	selected := make([]phase0.ValidatorIndex, 0, size)
	var randomBytes phase0.Root
	i := uint64(0)

	for uint64(len(selected)) < size {
		offset := (i % 16) * 2
		if offset == 0 {
			// random_bytes = hash(seed + uint_to_bytes(i // 16))
			var rbuf [40]byte
			copy(rbuf[:32], seed[:])
			quotient := i / 16
			rbuf[32] = byte(quotient)
			rbuf[33] = byte(quotient >> 8)
			rbuf[34] = byte(quotient >> 16)
			rbuf[35] = byte(quotient >> 24)
			rbuf[36] = byte(quotient >> 32)
			rbuf[37] = byte(quotient >> 40)
			rbuf[38] = byte(quotient >> 48)
			rbuf[39] = byte(quotient >> 56)
			randomBytes = hash256(rbuf[:])
		}

		nextIndex := i % total
		if shuffleIndices {
			nextIndex = computeShuffledIndex(nextIndex, total, seed, s.specs)
		}

		weight := effectiveBalances[nextIndex] * maxRandomValue
		randomValue := uint64(randomBytes[offset]) | uint64(randomBytes[offset+1])<<8
		threshold := maxEB * randomValue

		if weight >= threshold {
			selected = append(selected, indices[nextIndex])
		}
		i++
	}

	return selected
}
