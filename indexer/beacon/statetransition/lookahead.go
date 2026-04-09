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
// Electra+ compute_proposer_index with per-slot seed from Fulu's compute_proposer_indices.
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-compute_proposer_index
// Called via Fulu: https://github.com/ethereum/consensus-specs/blob/master/specs/fulu/beacon-chain.md#new-compute_proposer_indices
func computeProposerIndex(s *stateAccessor, activeIndices []phase0.ValidatorIndex, epoch phase0.Epoch, slot phase0.Slot) phase0.ValidatorIndex {
	if len(activeIndices) == 0 {
		return 0
	}

	epochSeed := getSeed(s, epoch, phase0.DomainType(s.specs.DomainBeaconProposer))

	// Compute per-slot seed: hash(epoch_seed + uint_to_bytes(slot))
	// Fulu compute_proposer_indices: seeds = [hash(seed + uint_to_bytes(slot)) for each slot]
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

	indexCount := uint64(len(activeIndices))
	maxEB := uint64(s.specs.MaxEffectiveBalanceElectra)
	if maxEB == 0 {
		maxEB = uint64(s.specs.MaxEffectiveBalance)
	}

	// Electra: 16-bit random values (MAX_RANDOM_VALUE = 2^16 - 1)
	const maxRandomValue = 65535

	i := uint64(0)
	for {
		candidateIndex := activeIndices[computeShuffledIndex(i%indexCount, indexCount, seed, s.specs)]

		// Electra: random_bytes = hash(seed + uint_to_bytes(i // 16))
		// offset = (i % 16) * 2; random_value = LE uint16 from random_bytes[offset:offset+2]
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
		h := hash256(rbuf[:])
		offset := (i % 16) * 2
		randomValue := uint64(h[offset]) | uint64(h[offset+1])<<8

		effectiveBalance := uint64(s.Validators[candidateIndex].EffectiveBalance)
		if effectiveBalance*maxRandomValue >= maxEB*randomValue {
			return candidateIndex
		}
		i++

		if i > indexCount*100 {
			return activeIndices[0]
		}
	}
}
