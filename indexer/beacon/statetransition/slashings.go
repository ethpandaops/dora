package statetransition

import (
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// processSlashings implements the Electra+ version of process_slashings.
//
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-process_slashings
func processSlashings(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	totalBalance := s.getTotalActiveBalance()

	totalSlashings := phase0.Gwei(0)
	for _, slashing := range s.Slashings {
		totalSlashings += slashing
	}

	adjustedTotalSlashingBalance := totalSlashings * phase0.Gwei(s.specs.ProportionalSlashingMultiplierBellatrix)
	if adjustedTotalSlashingBalance > totalBalance {
		adjustedTotalSlashingBalance = totalBalance
	}

	// Spec computes penalty_per_effective_balance_increment ONCE outside the loop:
	//   penalty_per_effective_balance_increment = adjusted // (total // increment)
	// then per validator:
	//   penalty = penalty_per_effective_balance_increment * (effective_balance // increment)
	// Doing the divisions in a different order loses precision for small slashings.
	increment := phase0.Gwei(s.specs.EffectiveBalanceIncrement)
	if increment == 0 || totalBalance < increment {
		return nil
	}
	penaltyPerIncrement := adjustedTotalSlashingBalance / (totalBalance / increment)

	halfSlashingsVector := phase0.Epoch(s.specs.EpochsPerSlashingVector / 2)
	for i, v := range s.Validators {
		if !v.Slashed {
			continue
		}
		if currentEpoch+halfSlashingsVector != v.WithdrawableEpoch {
			continue
		}

		effectiveBalanceIncrements := v.EffectiveBalance / increment
		penalty := penaltyPerIncrement * effectiveBalanceIncrements
		s.decreaseBalance(phase0.ValidatorIndex(i), penalty)
	}

	return nil
}
