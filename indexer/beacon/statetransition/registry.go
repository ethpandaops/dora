package statetransition

import (
	"sort"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// processRegistryUpdates implements the Electra+ version of process_registry_updates.
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-process_registry_updates
func processRegistryUpdates(s *stateAccessor) error {
	currentEpoch := s.currentEpoch()
	activationExitChurnLimit := s.getActivationExitChurnLimit()

	// Process ejections
	for i, v := range s.Validators {
		if isActiveValidator(v, currentEpoch) &&
			v.EffectiveBalance <= phase0.Gwei(s.specs.EjectionBalance) {
			initiateValidatorExit(s, phase0.ValidatorIndex(i))
		}
	}

	// Set activation eligibility
	for _, v := range s.Validators {
		if isEligibleForActivationQueue(v, s.specs) {
			v.ActivationEligibilityEpoch = currentEpoch + 1
		}
	}

	// Dequeue validators for activation (Electra+: balance-based churn)
	activationQueue := make([]phase0.ValidatorIndex, 0)
	for i, v := range s.Validators {
		if isEligibleForActivation(v, s.FinalizedCheckpoint.Epoch) {
			activationQueue = append(activationQueue, phase0.ValidatorIndex(i))
		}
	}

	// Sort by activation eligibility epoch, then by index
	sort.Slice(activationQueue, func(i, j int) bool {
		vi := s.Validators[activationQueue[i]]
		vj := s.Validators[activationQueue[j]]
		if vi.ActivationEligibilityEpoch != vj.ActivationEligibilityEpoch {
			return vi.ActivationEligibilityEpoch < vj.ActivationEligibilityEpoch
		}
		return activationQueue[i] < activationQueue[j]
	})

	// Activate validators up to the churn limit
	activatedBalance := phase0.Gwei(0)
	for _, idx := range activationQueue {
		v := s.Validators[idx]
		if activatedBalance+s.getMaxEffectiveBalance(v) > activationExitChurnLimit {
			break
		}
		activatedBalance += s.getMaxEffectiveBalance(v)
		v.ActivationEpoch = computeActivationExitEpoch(currentEpoch, s.specs)
	}

	return nil
}

// initiateValidatorExit queues a validator for exit.
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-initiate_validator_exit
func initiateValidatorExit(s *stateAccessor, index phase0.ValidatorIndex) {
	v := s.Validators[index]
	if v.ExitEpoch != FarFutureEpoch {
		return // already exiting
	}

	exitQueueEpoch := computeActivationExitEpoch(s.currentEpoch(), s.specs)
	if s.EarliestExitEpoch > exitQueueEpoch {
		exitQueueEpoch = s.EarliestExitEpoch
	}

	// Consume exit churn
	exitBalance := s.getMaxEffectiveBalance(v)
	if s.ExitBalanceToConsume < exitBalance {
		// Not enough churn left, push to next epoch
		s.ExitBalanceToConsume += s.getActivationExitChurnLimit()
		exitQueueEpoch++
	}
	s.ExitBalanceToConsume -= exitBalance
	s.EarliestExitEpoch = exitQueueEpoch

	v.ExitEpoch = exitQueueEpoch
	v.WithdrawableEpoch = exitQueueEpoch + phase0.Epoch(s.specs.MinValidatorWithdrawbilityDelay)
}
