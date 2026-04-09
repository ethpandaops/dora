package statetransition

import (
	"github.com/attestantio/go-eth2-client/spec/altair"
	"github.com/attestantio/go-eth2-client/spec/capella"
)

// processEth1DataReset resets the ETH1 data votes at the start of a new voting period.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#eth1-data-votes-updates
func processEth1DataReset(s *stateAccessor) {
	nextEpoch := s.currentEpoch() + 1
	if uint64(nextEpoch)%s.specs.EpochsPerEth1VotingPeriod == 0 {
		s.ETH1DataVotes = s.ETH1DataVotes[:0]
	}
}

// processSlashingsReset rotates the slashings vector.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#slashings-balances-updates
func processSlashingsReset(s *stateAccessor) {
	nextEpoch := s.currentEpoch() + 1
	idx := uint64(nextEpoch) % s.specs.EpochsPerSlashingVector
	s.Slashings[idx] = 0
}

// processRandaoMixesReset copies the current epoch mix to the next epoch slot.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#randao-mixes-updates
func processRandaoMixesReset(s *stateAccessor) {
	currentEpoch := s.currentEpoch()
	nextEpoch := currentEpoch + 1
	srcIdx := uint64(currentEpoch) % s.specs.EpochsPerHistoricalVector
	dstIdx := uint64(nextEpoch) % s.specs.EpochsPerHistoricalVector
	s.RANDAOMixes[dstIdx] = s.RANDAOMixes[srcIdx]
}

// processParticipationFlagUpdates rotates epoch participation.
// New in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#participation-flags-updates
func processParticipationFlagUpdates(s *stateAccessor) {
	s.PreviousEpochParticipation = s.CurrentEpochParticipation
	s.CurrentEpochParticipation = make([]altair.ParticipationFlags, len(s.Validators))
}

// processHistoricalSummariesUpdate appends a new historical summary at period boundaries.
// Modified in Capella: https://github.com/ethereum/consensus-specs/blob/master/specs/capella/beacon-chain.md#modified-process_historical_summaries_update
func processHistoricalSummariesUpdate(s *stateAccessor) {
	nextEpoch := s.currentEpoch() + 1
	epochsPerPeriod := s.specs.SlotsPerHistoricalRoot / s.specs.SlotsPerEpoch
	if epochsPerPeriod == 0 {
		return
	}
	if uint64(nextEpoch)%epochsPerPeriod != 0 {
		return
	}

	blockSummary := hashTreeRoot(s.BlockRoots)
	stateSummary := hashTreeRoot(s.StateRoots)

	s.HistoricalSummaries = append(s.HistoricalSummaries, &capella.HistoricalSummary{
		BlockSummaryRoot: blockSummary,
		StateSummaryRoot: stateSummary,
	})
}
