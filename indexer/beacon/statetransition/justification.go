package statetransition

import (
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// processJustificationAndFinalization implements the Altair+ version of
// process_justification_and_finalization.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#justification-and-finalization
func processJustificationAndFinalization(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	if currentEpoch <= 1 {
		return nil
	}

	previousEpoch := s.previousEpoch()
	oldPreviousJustifiedCheckpoint := s.PreviousJustifiedCheckpoint
	oldCurrentJustifiedCheckpoint := s.CurrentJustifiedCheckpoint

	// Process justification
	s.PreviousJustifiedCheckpoint = s.CurrentJustifiedCheckpoint

	// Shift justification bits
	if len(s.JustificationBits) > 0 {
		s.JustificationBits[0] = (s.JustificationBits[0] << 1) & 0x0F
	}

	totalActiveBalance := s.getTotalActiveBalance()

	// Previous epoch justification
	previousTargetBalance := s.getUnslashedParticipatingBalance(TimelyTargetFlagIndex, previousEpoch)
	if previousTargetBalance*3 >= totalActiveBalance*2 {
		s.CurrentJustifiedCheckpoint = &phase0.Checkpoint{
			Epoch: previousEpoch,
			Root:  getBlockRoot(s, previousEpoch),
		}
		if len(s.JustificationBits) > 0 {
			s.JustificationBits[0] |= 0x02 // bit 1
		}
	}

	// Current epoch justification
	currentTargetBalance := s.getUnslashedParticipatingBalance(TimelyTargetFlagIndex, currentEpoch)
	if currentTargetBalance*3 >= totalActiveBalance*2 {
		s.CurrentJustifiedCheckpoint = &phase0.Checkpoint{
			Epoch: currentEpoch,
			Root:  getBlockRoot(s, currentEpoch),
		}
		if len(s.JustificationBits) > 0 {
			s.JustificationBits[0] |= 0x01 // bit 0
		}
	}

	bits := byte(0)
	if len(s.JustificationBits) > 0 {
		bits = s.JustificationBits[0]
	}

	// Process finalizations
	// The 2/3/4th most recent epochs are justified, the 2nd using the 4th as source
	if bits&0x0E == 0x0E && oldPreviousJustifiedCheckpoint.Epoch+3 == currentEpoch {
		s.FinalizedCheckpoint = oldPreviousJustifiedCheckpoint
	}
	// The 2/3rd most recent epochs are justified, the 2nd using the 3rd as source
	if bits&0x06 == 0x06 && oldPreviousJustifiedCheckpoint.Epoch+2 == currentEpoch {
		s.FinalizedCheckpoint = oldPreviousJustifiedCheckpoint
	}
	// The 1/2/3rd most recent epochs are justified, the 1st using the 3rd as source
	if bits&0x07 == 0x07 && oldCurrentJustifiedCheckpoint.Epoch+2 == currentEpoch {
		s.FinalizedCheckpoint = oldCurrentJustifiedCheckpoint
	}
	// The 1/2nd most recent epochs are justified, the 1st using the 2nd as source
	if bits&0x03 == 0x03 && oldCurrentJustifiedCheckpoint.Epoch+1 == currentEpoch {
		s.FinalizedCheckpoint = oldCurrentJustifiedCheckpoint
	}

	return nil
}

// getBlockRoot returns the block root at the start of the given epoch.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#get_block_root
func getBlockRoot(s *stateAccessor, epoch phase0.Epoch) phase0.Root {
	startSlot := uint64(epoch) * s.specs.SlotsPerEpoch
	return s.BlockRoots[startSlot%s.specs.SlotsPerHistoricalRoot]
}
