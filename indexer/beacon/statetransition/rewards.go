package statetransition

import (
	"github.com/ethpandaops/go-eth2-client/spec/altair"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// processInactivityUpdates implements process_inactivity_updates (Altair+).
// Skips the genesis epoch — score updates are based on the previous epoch's
// participation, which doesn't exist at epoch 0.
// https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#inactivity-scores
func processInactivityUpdates(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	if currentEpoch == 0 {
		return nil
	}

	previousEpoch := s.previousEpoch()
	isInactivityLeak := isInInactivityLeak(s)

	// Timely-target participation for the previous epoch is read inline per
	// validator (active && !slashed && flag set) rather than via a prebuilt set,
	// avoiding a map over the full validator registry.
	prevParticipation := s.PreviousEpochParticipation

	// Iterate over eligible validator indices: active in previous epoch OR (slashed and not yet withdrawable)
	for i, v := range s.Validators {
		if !isActiveValidator(v, previousEpoch) && !(v.Slashed && previousEpoch+1 < v.WithdrawableEpoch) {
			continue
		}

		isTimelyTarget := !v.Slashed && isActiveValidator(v, previousEpoch) &&
			i < len(prevParticipation) && hasFlag(prevParticipation[i], TimelyTargetFlagIndex)
		if isTimelyTarget {
			// Decrease inactivity score by min(1, score)
			if s.InactivityScores[i] >= 1 {
				s.InactivityScores[i] -= 1
			}
		} else {
			// Increase inactivity score by INACTIVITY_SCORE_BIAS
			s.InactivityScores[i] += s.specs.InactivityScoreBias
		}

		if !isInactivityLeak {
			// Not in inactivity leak: decrease score by min(INACTIVITY_SCORE_RECOVERY_RATE, score)
			recovery := s.specs.InactivityScoreRecoveryRate
			if s.InactivityScores[i] >= recovery {
				s.InactivityScores[i] -= recovery
			} else {
				s.InactivityScores[i] = 0
			}
		}
	}

	return nil
}

// processRewardsAndPenalties implements the Altair+ version of process_rewards_and_penalties.
// Skips the genesis epoch — rewards are for work done in the previous epoch.
// Modified in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#modified-get_flag_index_deltas
func processRewardsAndPenalties(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	if currentEpoch == 0 {
		return nil
	}

	previousEpoch := s.previousEpoch()
	totalActiveBalance := s.getTotalActiveBalance()
	isInactivityLeak := isInInactivityLeak(s)
	prevParticipation := s.PreviousEpochParticipation

	activeIncrements := uint64(totalActiveBalance) / s.specs.EffectiveBalanceIncrement

	// Precompute participating increments for each flag (spec: get_flag_index_deltas).
	// Participation is read inline per validator from the flag bytes rather than via
	// per-flag index sets, so this is a single sum pass with no map allocations.
	var participatingIncrements [ParticipationFlagCount]uint64
	{
		var balances [ParticipationFlagCount]phase0.Gwei
		for i, v := range s.Validators {
			if v.Slashed || !isActiveValidator(v, previousEpoch) || i >= len(prevParticipation) {
				continue
			}
			flags := prevParticipation[i]
			for fi := 0; fi < ParticipationFlagCount; fi++ {
				if hasFlag(flags, fi) {
					balances[fi] += v.EffectiveBalance
				}
			}
		}
		for fi := 0; fi < ParticipationFlagCount; fi++ {
			if balances[fi] < phase0.Gwei(s.specs.EffectiveBalanceIncrement) {
				balances[fi] = phase0.Gwei(s.specs.EffectiveBalanceIncrement)
			}
			participatingIncrements[fi] = uint64(balances[fi]) / s.specs.EffectiveBalanceIncrement
		}
	}

	for i, v := range s.Validators {
		// is_eligible_validator: active in previous epoch OR (slashed and not yet withdrawable)
		if !isActiveValidator(v, previousEpoch) && !(v.Slashed && previousEpoch+1 < v.WithdrawableEpoch) {
			continue
		}

		idx := phase0.ValidatorIndex(i)
		baseReward := uint64(s.getBaseReward(idx))
		var flags altair.ParticipationFlags
		if i < len(prevParticipation) {
			flags = prevParticipation[i]
		}

		for fi := 0; fi < ParticipationFlagCount; fi++ {
			weight := ParticipationFlagWeights[fi]

			if !v.Slashed && hasFlag(flags, fi) {
				if !isInactivityLeak {
					// Reward (spec: rewards[index] += base_reward * weight * participating_increments / (active_increments * WEIGHT_DENOMINATOR))
					rewardNumerator := baseReward * weight * participatingIncrements[fi]
					reward := rewardNumerator / (activeIncrements * WeightDenominator)
					s.increaseBalance(idx, phase0.Gwei(reward))
				}
			} else if fi != TimelyHeadFlagIndex {
				// Penalty (spec: skip TIMELY_HEAD_FLAG_INDEX for penalties)
				penalty := baseReward * weight / WeightDenominator
				s.decreaseBalance(idx, phase0.Gwei(penalty))
			}
		}

		// Inactivity penalty (spec: get_inactivity_penalty_deltas)
		// penalty = effective_balance * inactivity_score / (INACTIVITY_SCORE_BIAS * INACTIVITY_PENALTY_QUOTIENT_BELLATRIX)
		if v.Slashed || !hasFlag(flags, TimelyTargetFlagIndex) {
			penaltyNumerator := uint64(v.EffectiveBalance) * s.InactivityScores[i]
			penaltyDenominator := s.specs.InactivityScoreBias * s.getInactivityPenaltyQuotient()
			if penaltyDenominator > 0 {
				penalty := penaltyNumerator / penaltyDenominator
				s.decreaseBalance(idx, phase0.Gwei(penalty))
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
