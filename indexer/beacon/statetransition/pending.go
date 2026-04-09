package statetransition

import (
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// processPendingDeposits implements the Electra+ version of process_pending_deposits.
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_pending_deposits
func processPendingDeposits(s *stateAccessor) error {
	availableForProcessing := s.DepositBalanceToConsume + s.getActivationExitChurnLimit()

	processedCount := 0
	nextDepositIndex := uint64(0)
	depositsToPostpone := make([]*electra.PendingDeposit, 0)
	isChurnLimitReached := false

	for _, deposit := range s.PendingDeposits {
		// Check if deposit is finalized
		if deposit.Slot > phase0.Slot(uint64(s.FinalizedCheckpoint.Epoch)*s.specs.SlotsPerEpoch) {
			break
		}

		processedCount++

		// Check if validator exists
		validatorIndex := findValidatorIndex(s, deposit.Pubkey)
		if validatorIndex == nil {
			// New validator: apply deposit
			if !isChurnLimitReached {
				cost := phase0.Gwei(deposit.Amount)
				if cost > availableForProcessing {
					isChurnLimitReached = true
					depositsToPostpone = append(depositsToPostpone, deposit)
					continue
				}
				availableForProcessing -= cost
				applyPendingDeposit(s, deposit)
			} else {
				depositsToPostpone = append(depositsToPostpone, deposit)
			}
			continue
		}

		// Existing validator
		v := s.Validators[*validatorIndex]

		// Check for non-applied top-ups
		if hasExecutionWithdrawalCredential(v) {
			if !isChurnLimitReached {
				cost := phase0.Gwei(deposit.Amount)
				if cost > availableForProcessing {
					isChurnLimitReached = true
					depositsToPostpone = append(depositsToPostpone, deposit)
					continue
				}
				availableForProcessing -= cost
			} else {
				depositsToPostpone = append(depositsToPostpone, deposit)
				continue
			}
		}

		// Apply deposit to existing validator
		applyPendingDepositToExisting(s, *validatorIndex, deposit)

		if deposit.Slot > phase0.Slot(0) {
			nextDepositIndex = uint64(deposit.Slot)
		}
	}

	_ = nextDepositIndex // used for deposit index tracking in full spec

	// Remove processed deposits, keep postponed + unprocessed
	remaining := make([]*electra.PendingDeposit, 0, len(depositsToPostpone)+len(s.PendingDeposits)-processedCount)
	remaining = append(remaining, s.PendingDeposits[processedCount:]...)
	remaining = append(remaining, depositsToPostpone...)
	s.PendingDeposits = remaining

	// Update deposit balance to consume
	if isChurnLimitReached {
		s.DepositBalanceToConsume = availableForProcessing
	} else {
		s.DepositBalanceToConsume = 0
	}

	return nil
}

// findValidatorIndex finds a validator by pubkey. Returns nil if not found.
func findValidatorIndex(s *stateAccessor, pubkey phase0.BLSPubKey) *phase0.ValidatorIndex {
	for i, v := range s.Validators {
		if v.PublicKey == pubkey {
			idx := phase0.ValidatorIndex(i)
			return &idx
		}
	}
	return nil
}

// applyPendingDeposit processes a deposit for a new validator.
func applyPendingDeposit(s *stateAccessor, deposit *electra.PendingDeposit) {
	// Add new validator
	v := &phase0.Validator{
		PublicKey:                  deposit.Pubkey,
		WithdrawalCredentials:      deposit.WithdrawalCredentials,
		EffectiveBalance:           0,
		Slashed:                    false,
		ActivationEligibilityEpoch: FarFutureEpoch,
		ActivationEpoch:            FarFutureEpoch,
		ExitEpoch:                  FarFutureEpoch,
		WithdrawableEpoch:          FarFutureEpoch,
	}
	s.Validators = append(s.Validators, v)
	s.Balances = append(s.Balances, 0)
	s.PreviousEpochParticipation = append(s.PreviousEpochParticipation, 0)
	s.CurrentEpochParticipation = append(s.CurrentEpochParticipation, 0)
	s.InactivityScores = append(s.InactivityScores, 0)

	idx := phase0.ValidatorIndex(len(s.Validators) - 1)
	s.increaseBalance(idx, phase0.Gwei(deposit.Amount))
}

// applyPendingDepositToExisting applies a deposit to an existing validator.
func applyPendingDepositToExisting(s *stateAccessor, index phase0.ValidatorIndex, deposit *electra.PendingDeposit) {
	s.increaseBalance(index, phase0.Gwei(deposit.Amount))
}

// processPendingConsolidations implements the Electra+ version of process_pending_consolidations.
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_pending_consolidations
func processPendingConsolidations(s *stateAccessor) {
	nextEpoch := s.currentEpoch() + 1
	processedCount := 0

	for _, consolidation := range s.PendingConsolidations {
		sourceValidator := s.Validators[consolidation.SourceIndex]
		if sourceValidator.WithdrawableEpoch > nextEpoch {
			break
		}
		processedCount++

		// Move balance from source to target
		targetValidator := s.Validators[consolidation.TargetIndex]
		_ = targetValidator // used for credential check in full spec

		activeBalance := s.Balances[consolidation.SourceIndex]
		s.decreaseBalance(consolidation.SourceIndex, activeBalance)
		s.increaseBalance(consolidation.TargetIndex, activeBalance)
	}

	s.PendingConsolidations = s.PendingConsolidations[processedCount:]
}
