package statetransition

import (
	"github.com/ethpandaops/dora/indexer/beacon/depositsig"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
)

// processPendingDeposits implements the Electra+ version of process_pending_deposits.
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_pending_deposits
func processPendingDeposits(s *stateAccessor) error {
	nextEpoch := s.currentEpoch() + 1
	availableForProcessing := s.DepositBalanceToConsume + s.getActivationExitChurnLimit()
	processedAmount := phase0.Gwei(0)
	nextDepositIndex := uint64(0)
	var depositsToPostpone []*electra.PendingDeposit
	isChurnLimitReached := false
	finalizedSlot := phase0.Slot(uint64(s.FinalizedCheckpoint.Epoch) * s.specs.SlotsPerEpoch)

	// Build pubkey index map for O(1) validator lookups. Without this, the
	// nested loop over PendingDeposits × Validators is O(N×M), which on mainnet
	// (51K deposits × 2.2M validators) would take hours.
	pubkeyIndex := make(map[phase0.BLSPubKey]phase0.ValidatorIndex, len(s.Validators))
	for i, v := range s.Validators {
		pubkeyIndex[v.PublicKey] = phase0.ValidatorIndex(i)
	}

	// Computed once and reused: the deposit domain is fork-agnostic and depends
	// only on the genesis fork version.
	depositDomain := depositsig.Domain(s.specs.GenesisForkVersion)

	for _, deposit := range s.PendingDeposits {
		// Do not process deposit requests if Eth1 bridge deposits are not yet applied.
		if deposit.Slot > 0 && s.ETH1DepositIndex < s.DepositRequestsStartIndex {
			break
		}

		// Check if deposit has been finalized.
		if deposit.Slot > finalizedSlot {
			break
		}

		// Check the per-epoch processing limit.
		if nextDepositIndex >= s.specs.MaxPendingDepositsPerEpoch {
			break
		}

		// Read validator state.
		isValidatorExited := false
		isValidatorWithdrawn := false
		if existingIdx, ok := pubkeyIndex[deposit.Pubkey]; ok {
			v := s.Validators[existingIdx]
			isValidatorExited = v.ExitEpoch < FarFutureEpoch
			isValidatorWithdrawn = v.WithdrawableEpoch < nextEpoch
		}

		switch {
		case isValidatorWithdrawn:
			// Deposited balance will never become active. Apply without consuming churn.
			applyPendingDeposit(s, deposit, pubkeyIndex, depositDomain)

		case isValidatorExited:
			// Validator is exiting; postpone until after withdrawable epoch.
			depositsToPostpone = append(depositsToPostpone, deposit)

		default:
			// Check if deposit fits in the churn; if not, stop processing this epoch.
			if processedAmount+phase0.Gwei(deposit.Amount) > availableForProcessing {
				isChurnLimitReached = true
				break
			}
			processedAmount += phase0.Gwei(deposit.Amount)
			applyPendingDeposit(s, deposit, pubkeyIndex, depositDomain)
		}

		if isChurnLimitReached {
			break
		}

		// Regardless of how the deposit was handled, advance the queue cursor.
		nextDepositIndex++
	}

	// state.pending_deposits = state.pending_deposits[next_deposit_index:] + deposits_to_postpone
	remaining := make([]*electra.PendingDeposit, 0, len(s.PendingDeposits)-int(nextDepositIndex)+len(depositsToPostpone))
	remaining = append(remaining, s.PendingDeposits[nextDepositIndex:]...)
	remaining = append(remaining, depositsToPostpone...)
	s.PendingDeposits = remaining

	// Accumulate churn only if the churn limit has been hit.
	if isChurnLimitReached {
		s.DepositBalanceToConsume = availableForProcessing - processedAmount
	} else {
		s.DepositBalanceToConsume = 0
	}

	return nil
}

// applyPendingDeposit implements apply_pending_deposit. For an existing pubkey
// the deposit amount tops up the validator's balance. For a new pubkey a
// validator is added to the registry ONLY if the deposit carries a valid
// signature (proof-of-possession); otherwise the deposit is silently dropped,
// exactly as the chain does.
//
// The signature gate is consensus-critical: the deposit contract does not verify
// it, so invalid-signature deposits for new pubkeys reach the queue. Creating a
// validator for one would append a phantom entry and shift every subsequent
// validator index relative to the chain (and diverge the state root). The
// caller's churn/queue accounting is unaffected — an invalid signature only
// suppresses the registry append, never the processed-amount/cursor advance.
//
// pubkeyIndex is the local pubkey→index map maintained by processPendingDeposits;
// when a new validator is appended, the map is updated so subsequent deposits
// in the same loop find the new validator.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-apply_pending_deposit
func applyPendingDeposit(s *stateAccessor, deposit *electra.PendingDeposit, pubkeyIndex map[phase0.BLSPubKey]phase0.ValidatorIndex, depositDomain zrnt_common.BLSDomain) {
	if existingIdx, ok := pubkeyIndex[deposit.Pubkey]; ok {
		s.increaseBalance(existingIdx, phase0.Gwei(deposit.Amount))
		return
	}

	// New validator: only create it if the deposit signature is valid.
	if !depositsig.Valid(deposit.Pubkey, deposit.WithdrawalCredentials, phase0.Gwei(deposit.Amount), deposit.Signature, depositDomain) {
		return
	}

	addValidatorToRegistry(s, deposit.Pubkey, deposit.WithdrawalCredentials, phase0.Gwei(deposit.Amount))
	pubkeyIndex[deposit.Pubkey] = phase0.ValidatorIndex(len(s.Validators) - 1)
}

// addValidatorToRegistry implements the Electra modified add_validator_to_registry.
// Constructs a validator via get_validator_from_deposit (which sets effective_balance
// based on the deposit amount and the credential type) and appends it to the
// registry along with matching balance/participation/inactivity entries.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-add_validator_to_registry
func addValidatorToRegistry(s *stateAccessor, pubkey phase0.BLSPubKey, withdrawalCredentials []byte, amount phase0.Gwei) {
	v := getValidatorFromDeposit(s, pubkey, withdrawalCredentials, amount)
	s.Validators = append(s.Validators, v)
	s.Balances = append(s.Balances, amount)
	s.PreviousEpochParticipation = append(s.PreviousEpochParticipation, 0)
	s.CurrentEpochParticipation = append(s.CurrentEpochParticipation, 0)
	s.InactivityScores = append(s.InactivityScores, 0)
}

// getValidatorFromDeposit implements the Electra modified get_validator_from_deposit.
// Returns a validator with effective_balance computed as
// min(amount - amount % EFFECTIVE_BALANCE_INCREMENT, max_effective_balance).
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-get_validator_from_deposit
func getValidatorFromDeposit(s *stateAccessor, pubkey phase0.BLSPubKey, withdrawalCredentials []byte, amount phase0.Gwei) *phase0.Validator {
	credentials := append([]byte(nil), withdrawalCredentials...)
	v := &phase0.Validator{
		PublicKey:                  pubkey,
		WithdrawalCredentials:      credentials,
		EffectiveBalance:           0,
		Slashed:                    false,
		ActivationEligibilityEpoch: FarFutureEpoch,
		ActivationEpoch:            FarFutureEpoch,
		ExitEpoch:                  FarFutureEpoch,
		WithdrawableEpoch:          FarFutureEpoch,
	}
	maxEB := s.getMaxEffectiveBalance(v)
	increment := phase0.Gwei(s.specs.EffectiveBalanceIncrement)
	rounded := amount - amount%increment
	if rounded < maxEB {
		v.EffectiveBalance = rounded
	} else {
		v.EffectiveBalance = maxEB
	}
	return v
}

// processPendingConsolidations implements the Electra+ version of process_pending_consolidations.
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_pending_consolidations
func processPendingConsolidations(s *stateAccessor) {
	nextEpoch := s.currentEpoch() + 1
	nextPendingConsolidation := 0

	for _, consolidation := range s.PendingConsolidations {
		sourceValidator := s.Validators[consolidation.SourceIndex]
		// Slashed source: skip (count as processed) and continue.
		if sourceValidator.Slashed {
			nextPendingConsolidation++
			continue
		}
		// Source not yet withdrawable: stop processing (queue is ordered).
		if sourceValidator.WithdrawableEpoch > nextEpoch {
			break
		}

		// Move only the *active* balance — bounded by the effective balance.
		// Any balance above effective_balance becomes a normal withdrawal later.
		sourceEffectiveBalance := s.Balances[consolidation.SourceIndex]
		if sourceValidator.EffectiveBalance < sourceEffectiveBalance {
			sourceEffectiveBalance = sourceValidator.EffectiveBalance
		}

		s.decreaseBalance(consolidation.SourceIndex, sourceEffectiveBalance)
		s.increaseBalance(consolidation.TargetIndex, sourceEffectiveBalance)
		nextPendingConsolidation++
	}

	s.PendingConsolidations = s.PendingConsolidations[nextPendingConsolidation:]
}
