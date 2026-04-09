package statetransition

import (
	"github.com/attestantio/go-eth2-client/spec/altair"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/phase0"
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

// processSyncCommitteeUpdates rotates the sync committee at period boundaries.
// New in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#sync-committee-updates
func processSyncCommitteeUpdates(s *stateAccessor) {
	nextEpoch := s.currentEpoch() + 1
	if s.specs.EpochsPerSyncCommitteePeriod == 0 {
		return
	}
	if uint64(nextEpoch)%s.specs.EpochsPerSyncCommitteePeriod != 0 {
		return
	}

	s.CurrentSyncCommittee = s.NextSyncCommittee
	s.NextSyncCommittee = computeNextSyncCommittee(s)
}

// computeNextSyncCommittee computes the next sync committee.
// https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#get_next_sync_committee
func computeNextSyncCommittee(s *stateAccessor) *altair.SyncCommittee {
	indices := s.getActiveValidatorIndices(s.currentEpoch() + 1)
	if len(indices) == 0 {
		return s.NextSyncCommittee // fallback: keep current
	}

	epoch := s.currentEpoch() + 1
	seed := getSeed(s, epoch, phase0.DomainType(s.specs.DomainSyncCommittee))

	syncCommitteeSize := s.specs.SyncCommitteeSize
	committee := make([]phase0.ValidatorIndex, 0, syncCommitteeSize)
	pubkeys := make([]phase0.BLSPubKey, 0, syncCommitteeSize)

	i := uint64(0)
	for uint64(len(committee)) < syncCommitteeSize {
		shuffledIndex := computeShuffledIndex(i%uint64(len(indices)), uint64(len(indices)), seed, s.specs)
		candidateIndex := indices[shuffledIndex]

		randomByte := getRandomByte(seed, i/32, i%32)
		effectiveBalance := s.Validators[candidateIndex].EffectiveBalance
		maxEB := s.getMaxEffectiveBalance(s.Validators[candidateIndex])

		if effectiveBalance*255 >= maxEB*phase0.Gwei(randomByte) {
			committee = append(committee, candidateIndex)
			pubkeys = append(pubkeys, s.Validators[candidateIndex].PublicKey)
		}
		i++
	}

	// Compute aggregate pubkey placeholder (we don't actually need it for the explorer,
	// but the struct requires it). Use empty value.
	return &altair.SyncCommittee{
		Pubkeys:         pubkeys,
		AggregatePubkey: phase0.BLSPubKey{},
	}
}
