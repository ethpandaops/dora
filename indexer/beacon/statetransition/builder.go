package statetransition

import (
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
	// PTC window is not available for Fulu states in the accessor
	if s.version < 8 { // DataVersionGloas
		return
	}

	g := s.rawState.Gloas
	if g == nil || len(g.PTCWindow) == 0 {
		return
	}

	slotsPerEpoch := s.specs.SlotsPerEpoch

	// Slide window: remove first SLOTS_PER_EPOCH entries
	windowLen := uint64(len(g.PTCWindow))
	if windowLen <= slotsPerEpoch {
		return
	}

	copy(g.PTCWindow, g.PTCWindow[slotsPerEpoch:])

	// Compute new PTC assignments for the last SLOTS_PER_EPOCH entries
	nextEpoch := s.currentEpoch() + phase0.Epoch(s.specs.MinSeedLookahead) + 1
	startSlot := uint64(nextEpoch) * slotsPerEpoch

	lastStart := windowLen - slotsPerEpoch
	for i := uint64(0); i < slotsPerEpoch && lastStart+i < windowLen; i++ {
		g.PTCWindow[lastStart+i] = computePtc(s, phase0.Slot(startSlot+i))
	}
}

// computePtc computes the PTC (Payload Timeliness Committee) for a given slot.
// This selects PTC_SIZE validators from the beacon committees using the PTC seed.
// New in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-compute_ptc
func computePtc(s *stateAccessor, slot phase0.Slot) []phase0.ValidatorIndex {
	epoch := phase0.Epoch(uint64(slot) / s.specs.SlotsPerEpoch)
	activeIndices := s.getActiveValidatorIndices(epoch)
	if len(activeIndices) == 0 {
		return nil
	}

	ptcSize := s.specs.PtcSize
	if ptcSize == 0 {
		return nil
	}

	// Get the seed for PTC computation
	seed := getSeed(s, epoch, phase0.DomainType(s.specs.DomainPtcAttester))

	// Select PTC members using shuffling
	committeeSize := uint64(len(activeIndices))
	result := make([]phase0.ValidatorIndex, 0, ptcSize)

	slotIndex := uint64(slot) % s.specs.SlotsPerEpoch
	startOffset := (committeeSize * slotIndex * ptcSize) / (s.specs.SlotsPerEpoch * ptcSize)

	for i := uint64(0); i < ptcSize && i < committeeSize; i++ {
		idx := (startOffset + i) % committeeSize
		shuffledIdx := computeShuffledIndex(idx, committeeSize, seed, s.specs)
		result = append(result, activeIndices[shuffledIdx])
	}

	return result
}
