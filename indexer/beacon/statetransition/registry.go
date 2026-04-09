package statetransition

import (
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// processRegistryUpdates implements the Electra+ version of process_registry_updates.
// Single loop with if/elif/elif chain — Electra removed activation churn here and
// moved it to process_pending_deposits, so every eligible validator activates this
// epoch (no churn limit, no sorting).
//
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-process_registry_updates
func processRegistryUpdates(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	activationEpoch := computeActivationExitEpoch(currentEpoch, s.specs)

	for i, v := range s.Validators {
		switch {
		case isEligibleForActivationQueue(v, s.specs):
			v.ActivationEligibilityEpoch = currentEpoch + 1
		case isActiveValidator(v, currentEpoch) && v.EffectiveBalance <= phase0.Gwei(s.specs.EjectionBalance):
			initiateValidatorExit(s, phase0.ValidatorIndex(i))
		case isEligibleForActivation(v, s.FinalizedCheckpoint.Epoch):
			v.ActivationEpoch = activationEpoch
		}
	}

	return nil
}

// initiateValidatorExit queues a validator for exit, computing the exit epoch
// via compute_exit_epoch_and_update_churn (which handles multi-epoch overflow).
//
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-initiate_validator_exit
func initiateValidatorExit(s *stateAccessor, index phase0.ValidatorIndex) {
	v := s.Validators[index]
	if v.ExitEpoch != FarFutureEpoch {
		return // already exiting
	}

	// Spec uses validator.effective_balance, NOT max effective balance.
	exitQueueEpoch := computeExitEpochAndUpdateChurn(s, v.EffectiveBalance)

	v.ExitEpoch = exitQueueEpoch
	v.WithdrawableEpoch = exitQueueEpoch + phase0.Epoch(s.specs.MinValidatorWithdrawbilityDelay)
}

// computeExitEpochAndUpdateChurn returns the earliest epoch at which an exit of
// the given balance can be processed, while updating state.earliest_exit_epoch
// and state.exit_balance_to_consume in place. Handles multi-epoch overflow.
//
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-compute_exit_epoch_and_update_churn
func computeExitEpochAndUpdateChurn(s *stateAccessor, exitBalance phase0.Gwei) phase0.Epoch {
	earliestExitEpoch := computeActivationExitEpoch(s.currentEpoch(), s.specs)
	if s.EarliestExitEpoch > earliestExitEpoch {
		earliestExitEpoch = s.EarliestExitEpoch
	}
	perEpochChurn := s.getActivationExitChurnLimit()

	var exitBalanceToConsume phase0.Gwei
	if s.EarliestExitEpoch < earliestExitEpoch {
		// New epoch for exits — refill the budget.
		exitBalanceToConsume = perEpochChurn
	} else {
		exitBalanceToConsume = s.ExitBalanceToConsume
	}

	// If exit doesn't fit, push it forward by enough epochs to fit the balance.
	if exitBalance > exitBalanceToConsume {
		balanceToProcess := exitBalance - exitBalanceToConsume
		additionalEpochs := (balanceToProcess-1)/perEpochChurn + 1
		earliestExitEpoch += phase0.Epoch(additionalEpochs)
		exitBalanceToConsume += phase0.Gwei(additionalEpochs) * perEpochChurn
	}

	s.ExitBalanceToConsume = exitBalanceToConsume - exitBalance
	s.EarliestExitEpoch = earliestExitEpoch
	return s.EarliestExitEpoch
}

// getPendingBalanceToWithdraw returns the sum of pending partial withdrawal
// amounts for the given validator.
//
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-get_pending_balance_to_withdraw
func getPendingBalanceToWithdraw(s *stateAccessor, validatorIndex phase0.ValidatorIndex) phase0.Gwei {
	total := phase0.Gwei(0)
	for _, w := range s.PendingPartialWithdrawals {
		if w.ValidatorIndex == validatorIndex {
			total += w.Amount
		}
	}
	return total
}
