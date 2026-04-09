package statetransition

import (
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// processSlashings implements the Electra+ version of process_slashings.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#slashings
// Modified in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#modified-process_slashings
func processSlashings(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	totalBalance := s.getTotalActiveBalance()

	totalSlashings := phase0.Gwei(0)
	for _, slashing := range s.Slashings {
		totalSlashings += slashing
	}

	// Electra+: PROPORTIONAL_SLASHING_MULTIPLIER_BELLATRIX = 3
	proportionalSlashingMultiplier := s.specs.ProportionalSlashingMultiplierBellatrix
	if proportionalSlashingMultiplier == 0 {
		proportionalSlashingMultiplier = s.specs.ProportionalSlashingMultiplier
	}

	adjustedTotalSlashingBalance := totalSlashings * phase0.Gwei(proportionalSlashingMultiplier)
	if adjustedTotalSlashingBalance > totalBalance {
		adjustedTotalSlashingBalance = totalBalance
	}

	for i, v := range s.Validators {
		if !v.Slashed {
			continue
		}

		withdrawableEpoch := v.WithdrawableEpoch
		halfSlashingsVector := phase0.Epoch(s.specs.EpochsPerSlashingVector / 2)

		if currentEpoch+halfSlashingsVector != withdrawableEpoch {
			continue
		}

		// Electra+: use per-validator max effective balance
		effectiveBalance := v.EffectiveBalance
		increment := phase0.Gwei(s.specs.EffectiveBalanceIncrement)
		penaltyNumerator := effectiveBalance / increment * adjustedTotalSlashingBalance
		penalty := penaltyNumerator / totalBalance * increment

		s.decreaseBalance(phase0.ValidatorIndex(i), penalty)
	}

	return nil
}
