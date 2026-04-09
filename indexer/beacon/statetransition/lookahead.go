package statetransition

import (
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// processProposerLookahead implements process_proposer_lookahead (Fulu+/EIP-7917).
// Slides the proposer lookahead window and computes new proposer indices for the
// lookahead epoch.
// New in Fulu: https://github.com/ethereum/consensus-specs/blob/master/specs/fulu/beacon-chain.md#new-process_proposer_lookahead
func processProposerLookahead(s *stateAccessor) {
	slotsPerEpoch := s.specs.SlotsPerEpoch
	lookaheadLen := uint64(len(s.ProposerLookahead))
	if lookaheadLen == 0 || slotsPerEpoch == 0 {
		return
	}

	// Slide window: shift left by SLOTS_PER_EPOCH
	if lookaheadLen > slotsPerEpoch {
		copy(s.ProposerLookahead, s.ProposerLookahead[slotsPerEpoch:])
	}

	// Compute new proposer indices for the last SLOTS_PER_EPOCH entries
	nextEpoch := s.currentEpoch() + phase0.Epoch(s.specs.MinSeedLookahead) + 1
	proposers := getBeaconProposerIndices(s, nextEpoch)

	lastStart := lookaheadLen - slotsPerEpoch
	for i := uint64(0); i < slotsPerEpoch && i < uint64(len(proposers)); i++ {
		if lastStart+i < lookaheadLen {
			s.ProposerLookahead[lastStart+i] = proposers[i]
		}
	}
}

// getBeaconProposerIndices computes the proposer index for each slot in the given epoch.
// Spec: get_beacon_proposer_index applied to each slot.
func getBeaconProposerIndices(s *stateAccessor, epoch phase0.Epoch) []phase0.ValidatorIndex {
	slotsPerEpoch := s.specs.SlotsPerEpoch
	startSlot := uint64(epoch) * slotsPerEpoch
	indices := make([]phase0.ValidatorIndex, slotsPerEpoch)

	activeIndices := s.getActiveValidatorIndices(epoch)
	if len(activeIndices) == 0 {
		return indices
	}

	for slotOffset := uint64(0); slotOffset < slotsPerEpoch; slotOffset++ {
		slot := phase0.Slot(startSlot + slotOffset)
		indices[slotOffset] = computeProposerIndex(s, activeIndices, epoch, slot)
	}

	return indices
}

// computeProposerIndex selects the proposer for a specific slot using the
// spec's compute_proposer_index function.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#compute_proposer_index
func computeProposerIndex(s *stateAccessor, activeIndices []phase0.ValidatorIndex, epoch phase0.Epoch, slot phase0.Slot) phase0.ValidatorIndex {
	if len(activeIndices) == 0 {
		return 0
	}

	seed := getSeed(s, epoch, phase0.DomainType(s.specs.DomainBeaconProposer))

	// Mix in slot
	var buf [40]byte
	copy(buf[:32], seed[:])
	buf[32] = byte(slot)
	buf[33] = byte(slot >> 8)
	buf[34] = byte(slot >> 16)
	buf[35] = byte(slot >> 24)
	buf[36] = byte(slot >> 32)
	buf[37] = byte(slot >> 40)
	buf[38] = byte(slot >> 48)
	buf[39] = byte(slot >> 56)
	slotSeed := hash256(buf[:])

	indexCount := uint64(len(activeIndices))
	maxEB := uint64(s.specs.MaxEffectiveBalanceElectra)
	if maxEB == 0 {
		maxEB = uint64(s.specs.MaxEffectiveBalance)
	}

	i := uint64(0)
	for {
		candidateIndex := activeIndices[computeShuffledIndex(i%indexCount, indexCount, seed, s.specs)]
		randomByte := getRandomByte(slotSeed, i/32, i%32)

		effectiveBalance := uint64(s.Validators[candidateIndex].EffectiveBalance)
		if effectiveBalance*255 >= maxEB*uint64(randomByte) {
			return candidateIndex
		}
		i++

		// Safety: prevent infinite loop
		if i > indexCount*100 {
			return activeIndices[0]
		}
	}
}
