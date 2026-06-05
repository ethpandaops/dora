package utils

import (
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// ValidatorToState is a helper that calculates the validator status given a validator struct.
func ValidatorToState(validator *phase0.Validator,
	balance *phase0.Gwei,
	currentEpoch phase0.Epoch,
	farFutureEpoch phase0.Epoch,
) v1.ValidatorState {
	if validator == nil {
		return v1.ValidatorStateUnknown
	}

	switch {
	case validator.ActivationEligibilityEpoch == farFutureEpoch || validator.ActivationEpoch > currentEpoch:
		// Pending.
		if validator.ActivationEpoch == farFutureEpoch && validator.ActivationEligibilityEpoch == farFutureEpoch {
			return v1.ValidatorStatePendingInitialized
		}

		return v1.ValidatorStatePendingQueued
	case validator.ExitEpoch == farFutureEpoch:
		// Active ongoing.
		return v1.ValidatorStateActiveOngoing
	case validator.ExitEpoch > currentEpoch:
		// Active exiting.
		if validator.Slashed {
			return v1.ValidatorStateActiveSlashed
		}

		return v1.ValidatorStateActiveExiting
	case validator.WithdrawableEpoch > currentEpoch:
		// Exited.
		if validator.Slashed {
			return v1.ValidatorStateExitedSlashed
		}

		return v1.ValidatorStateExitedUnslashed
	case (balance != nil && *balance == 0) || (balance == nil && validator.EffectiveBalance == 0):
		return v1.ValidatorStateWithdrawalDone
	default:
		return v1.ValidatorStateWithdrawalPossible
	}
}
