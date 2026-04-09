package statetransition

import (
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// processInactivityUpdates implements process_inactivity_updates (Altair+).
// https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#inactivity-scores
func processInactivityUpdates(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	if currentEpoch <= 1 {
		return nil
	}

	previousEpoch := s.previousEpoch()
	isInactivityLeak := isInInactivityLeak(s)

	// Build set of timely target participants for previous epoch
	targetParticipants := make(map[phase0.ValidatorIndex]bool)
	for _, idx := range s.getUnslashedParticipatingIndices(TimelyTargetFlagIndex, previousEpoch) {
		targetParticipants[idx] = true
	}

	for i, v := range s.Validators {
		if !isActiveValidator(v, previousEpoch) || v.Slashed {
			continue
		}

		idx := phase0.ValidatorIndex(i)
		if targetParticipants[idx] {
			// Decrease inactivity score
			if s.InactivityScores[i] > 0 {
				decrease := s.specs.InactivityScoreRecoveryRate
				if s.InactivityScores[i] < decrease {
					s.InactivityScores[i] = 0
				} else {
					s.InactivityScores[i] -= decrease
				}
			}
		} else {
			// Increase inactivity score
			s.InactivityScores[i] += s.specs.InactivityScoreBias
		}

		if !isInactivityLeak {
			// Not in inactivity leak: decrease score faster
			if s.InactivityScores[i] > 0 {
				decrease := uint64(1)
				if s.InactivityScores[i] < decrease {
					s.InactivityScores[i] = 0
				} else {
					s.InactivityScores[i] -= decrease
				}
			}
		}
	}

	return nil
}

// processRewardsAndPenalties implements the Altair+ version of process_rewards_and_penalties.
// Modified in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#modified-get_flag_index_deltas
func processRewardsAndPenalties(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	if currentEpoch <= 1 {
		return nil
	}

	previousEpoch := s.previousEpoch()
	totalActiveBalance := s.getTotalActiveBalance()
	isInactivityLeak := isInInactivityLeak(s)

	// Precompute participating balances and sets for each flag
	type flagData struct {
		participatingBalance phase0.Gwei
		participants         map[phase0.ValidatorIndex]bool
	}

	flags := make([]flagData, ParticipationFlagCount)
	for fi := 0; fi < ParticipationFlagCount; fi++ {
		indices := s.getUnslashedParticipatingIndices(fi, previousEpoch)
		balance := phase0.Gwei(0)
		pMap := make(map[phase0.ValidatorIndex]bool, len(indices))
		for _, idx := range indices {
			pMap[idx] = true
			balance += s.Validators[idx].EffectiveBalance
		}
		if balance < phase0.Gwei(s.specs.EffectiveBalanceIncrement) {
			balance = phase0.Gwei(s.specs.EffectiveBalanceIncrement)
		}
		flags[fi] = flagData{participatingBalance: balance, participants: pMap}
	}

	for i, v := range s.Validators {
		if !isActiveValidator(v, previousEpoch) && !(v.Slashed && previousEpoch+1 < v.WithdrawableEpoch) {
			continue
		}

		idx := phase0.ValidatorIndex(i)
		baseReward := s.getBaseReward(idx)

		for fi := 0; fi < ParticipationFlagCount; fi++ {
			weight := ParticipationFlagWeights[fi]

			if flags[fi].participants[idx] && !v.Slashed {
				if !isInactivityLeak {
					// Reward
					rewardNumerator := baseReward * phase0.Gwei(weight) * flags[fi].participatingBalance
					reward := rewardNumerator / (totalActiveBalance * WeightDenominator)
					s.increaseBalance(idx, reward)
				}
			} else {
				// Penalty
				penalty := baseReward * phase0.Gwei(weight) / WeightDenominator
				s.decreaseBalance(idx, penalty)
			}
		}

		// Inactivity penalty (additional penalty for validators not participating in target)
		if !flags[TimelyTargetFlagIndex].participants[idx] || v.Slashed {
			penaltyNumerator := v.EffectiveBalance * phase0.Gwei(s.InactivityScores[i])
			penaltyDenominator := phase0.Gwei(s.specs.InactivityScoreRecoveryRate * s.getInactivityPenaltyQuotient())
			if penaltyDenominator > 0 {
				penalty := penaltyNumerator / penaltyDenominator
				s.decreaseBalance(idx, penalty)
			}
		}
	}

	return nil
}

// isInInactivityLeak checks if the chain is in an inactivity leak.
func isInInactivityLeak(s *stateAccessor) bool {
	return s.previousEpoch()-s.FinalizedCheckpoint.Epoch > phase0.Epoch(s.specs.MinEpochsToInactivityPenalty)
}

// getInactivityPenaltyQuotient returns the inactivity penalty quotient for the current fork.
func (s *stateAccessor) getInactivityPenaltyQuotient() uint64 {
	// Electra/Fulu/Gloas use Bellatrix quotient
	if s.specs.InactivityPenaltyQuotientBellatrix > 0 {
		return s.specs.InactivityPenaltyQuotientBellatrix
	}
	return s.specs.InactivityPenaltyQuotient
}
