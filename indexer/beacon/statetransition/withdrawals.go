package statetransition

import (
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// expectedWithdrawals holds the result of get_expected_withdrawals.
type expectedWithdrawals struct {
	withdrawals                      []*capella.Withdrawal
	processedBuilderWithdrawalsCount uint64
	processedPartialWithdrawalsCount uint64
	processedBuildersSweepCount      uint64
	processedValidatorsSweepCount    uint64
}

// processWithdrawals implements the Gloas version of process_withdrawals.
// Modified in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#modified-process_withdrawals
func processWithdrawals(s *stateAccessor) {
	if !isParentBlockFull(s) {
		return
	}

	expected := getExpectedWithdrawals(s)

	// apply_withdrawals
	for _, w := range expected.withdrawals {
		if isBuilderIndex(w.ValidatorIndex) {
			// Builder withdrawal: decrease builder balance
			builderIdx := convertValidatorIndexToBuilderIndex(w.ValidatorIndex)
			if int(builderIdx) < len(s.Builders) {
				builder := s.Builders[builderIdx]
				amount := phase0.Gwei(w.Amount)
				if amount > builder.Balance {
					amount = builder.Balance
				}
				builder.Balance -= amount
			}
		} else {
			if int(w.ValidatorIndex) < len(s.Balances) {
				s.decreaseBalance(w.ValidatorIndex, phase0.Gwei(w.Amount))
			}
		}
	}

	// update_next_withdrawal_index
	if len(expected.withdrawals) > 0 {
		lastIdx := expected.withdrawals[len(expected.withdrawals)-1].Index
		s.NextWithdrawalIndex = lastIdx + 1
	}

	// update_payload_expected_withdrawals
	s.PayloadExpectedWithdrawals = expected.withdrawals

	// update_builder_pending_withdrawals: remove processed entries from the front
	if expected.processedBuilderWithdrawalsCount > 0 {
		n := expected.processedBuilderWithdrawalsCount
		if n > uint64(len(s.BuilderPendingWithdrawals)) {
			n = uint64(len(s.BuilderPendingWithdrawals))
		}
		s.BuilderPendingWithdrawals = s.BuilderPendingWithdrawals[n:]
	}

	// update_pending_partial_withdrawals: remove processed entries from the front
	if expected.processedPartialWithdrawalsCount > 0 {
		n := expected.processedPartialWithdrawalsCount
		if n > uint64(len(s.PendingPartialWithdrawals)) {
			n = uint64(len(s.PendingPartialWithdrawals))
		}
		s.PendingPartialWithdrawals = s.PendingPartialWithdrawals[n:]
	}

	// update_next_withdrawal_builder_index
	if expected.processedBuildersSweepCount > 0 && len(s.Builders) > 0 {
		nextIdx := uint64(s.NextWithdrawalBuilderIndex) + expected.processedBuildersSweepCount
		s.NextWithdrawalBuilderIndex = gloas.BuilderIndex(nextIdx % uint64(len(s.Builders)))
	}

	// update_next_withdrawal_validator_index (Capella spec, unchanged in Gloas)
	if uint64(len(expected.withdrawals)) == s.specs.MaxWithdrawalsPerPayload {
		lastW := expected.withdrawals[len(expected.withdrawals)-1]
		s.NextWithdrawalValidatorIndex = phase0.ValidatorIndex((uint64(lastW.ValidatorIndex) + 1) % uint64(len(s.Validators)))
	} else {
		s.NextWithdrawalValidatorIndex = phase0.ValidatorIndex((uint64(s.NextWithdrawalValidatorIndex) + s.specs.MaxValidatorsPerWithdrawalsSweep) % uint64(len(s.Validators)))
	}
}

// BuilderIndexFlag separates builder indices from validator indices.
const BuilderIndexFlag = uint64(1 << 40)

func isBuilderIndex(idx phase0.ValidatorIndex) bool {
	return uint64(idx)&BuilderIndexFlag != 0
}

func convertBuilderIndexToValidatorIndex(builderIdx gloas.BuilderIndex) phase0.ValidatorIndex {
	return phase0.ValidatorIndex(uint64(builderIdx) | BuilderIndexFlag)
}

func convertValidatorIndexToBuilderIndex(validatorIdx phase0.ValidatorIndex) gloas.BuilderIndex {
	return gloas.BuilderIndex(uint64(validatorIdx) &^ BuilderIndexFlag)
}

// getBalanceAfterWithdrawals returns a validator's balance minus any amounts
// already scheduled in prior withdrawals within the same batch.
// https://github.com/ethereum/consensus-specs/blob/master/specs/capella/beacon-chain.md#new-get_balance_after_withdrawals
func getBalanceAfterWithdrawals(s *stateAccessor, vidx phase0.ValidatorIndex, withdrawals []*capella.Withdrawal) phase0.Gwei {
	balance := s.Balances[vidx]
	for _, w := range withdrawals {
		if w.ValidatorIndex == vidx {
			if balance >= w.Amount {
				balance -= w.Amount
			} else {
				balance = 0
			}
		}
	}
	return balance
}

// isFullyWithdrawableValidator checks if a validator is fully withdrawable.
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-is_fully_withdrawable_validator
func isFullyWithdrawableValidator(v *phase0.Validator, balance phase0.Gwei, epoch phase0.Epoch) bool {
	return hasExecutionWithdrawalCredential(v) && v.WithdrawableEpoch <= epoch && balance > 0
}

// isPartiallyWithdrawableValidator checks if a validator is partially withdrawable (sweep).
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-is_partially_withdrawable_validator
func isPartiallyWithdrawableValidator(v *phase0.Validator, balance phase0.Gwei, maxEB phase0.Gwei) bool {
	return hasExecutionWithdrawalCredential(v) && v.EffectiveBalance == maxEB && balance > maxEB
}

// isEligibleForPartialWithdrawals checks if a validator can process a pending partial withdrawal.
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-is_eligible_for_partial_withdrawals
func isEligibleForPartialWithdrawals(v *phase0.Validator, balance phase0.Gwei, minActivationBalance phase0.Gwei) bool {
	return v.ExitEpoch == FarFutureEpoch &&
		v.EffectiveBalance >= minActivationBalance &&
		balance > minActivationBalance
}

// getExpectedWithdrawals computes the expected withdrawals for the current slot.
// Modified in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#modified-get_expected_withdrawals
func getExpectedWithdrawals(s *stateAccessor) *expectedWithdrawals {
	result := &expectedWithdrawals{}
	nextIdx := s.NextWithdrawalIndex
	maxWithdrawals := s.specs.MaxWithdrawalsPerPayload
	epoch := s.currentEpoch()

	// 1. Builder pending withdrawals (Gloas-specific)
	if len(s.BuilderPendingWithdrawals) > 0 {
		builderLimit := maxWithdrawals - 1
		for _, bpw := range s.BuilderPendingWithdrawals {
			if uint64(len(result.withdrawals)) >= builderLimit {
				break
			}
			result.withdrawals = append(result.withdrawals, &capella.Withdrawal{
				Index:          nextIdx,
				ValidatorIndex: convertBuilderIndexToValidatorIndex(bpw.BuilderIndex),
				Address:        bpw.FeeRecipient,
				Amount:         bpw.Amount,
			})
			nextIdx++
			result.processedBuilderWithdrawalsCount++
		}
	}

	// 2. Pending partial withdrawals
	// Limit: min(prior_count + MAX_PENDING_PARTIALS_PER_WITHDRAWALS_SWEEP, MAX_WITHDRAWALS_PER_PAYLOAD - 1)
	// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-get_pending_partial_withdrawals
	{
		partialLimit := uint64(len(result.withdrawals)) + s.specs.MaxPendingPartialsPerWithdrawalsSweep
		if partialLimit > maxWithdrawals-1 {
			partialLimit = maxWithdrawals - 1
		}

		minBalance := phase0.Gwei(s.specs.MinActivationBalance)
		for _, pw := range s.PendingPartialWithdrawals {
			if pw.WithdrawableEpoch > epoch || uint64(len(result.withdrawals)) >= partialLimit {
				break
			}
			result.processedPartialWithdrawalsCount++

			validator := s.Validators[pw.ValidatorIndex]
			balance := getBalanceAfterWithdrawals(s, pw.ValidatorIndex, result.withdrawals)
			if !isEligibleForPartialWithdrawals(validator, balance, minBalance) {
				continue
			}

			amount := balance - minBalance
			if phase0.Gwei(pw.Amount) < amount {
				amount = phase0.Gwei(pw.Amount)
			}

			result.withdrawals = append(result.withdrawals, &capella.Withdrawal{
				Index:          nextIdx,
				ValidatorIndex: pw.ValidatorIndex,
				Address:        getWithdrawalAddress(validator),
				Amount:         amount,
			})
			nextIdx++
		}
	}

	// 3. Builder sweep withdrawals (Gloas-specific)
	if len(s.Builders) > 0 {
		builderSweepLimit := maxWithdrawals - 1
		buildersLimit := uint64(len(s.Builders))
		if s.specs.MaxBuildersPerWithdrawalsSweep > 0 && s.specs.MaxBuildersPerWithdrawalsSweep < buildersLimit {
			buildersLimit = s.specs.MaxBuildersPerWithdrawalsSweep
		}

		builderIdx := s.NextWithdrawalBuilderIndex
		for i := uint64(0); i < buildersLimit; i++ {
			if uint64(len(result.withdrawals)) >= builderSweepLimit {
				break
			}

			builder := s.Builders[builderIdx]
			if builder.WithdrawableEpoch <= epoch && builder.Balance > 0 {
				result.withdrawals = append(result.withdrawals, &capella.Withdrawal{
					Index:          nextIdx,
					ValidatorIndex: convertBuilderIndexToValidatorIndex(builderIdx),
					Address:        builder.ExecutionAddress,
					Amount:         builder.Balance,
				})
				nextIdx++
			}

			builderIdx = gloas.BuilderIndex((uint64(builderIdx) + 1) % uint64(len(s.Builders)))
			result.processedBuildersSweepCount++
		}
	}

	// 4. Validator sweep withdrawals (uses full MAX limit)
	// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-get_validators_sweep_withdrawals
	validatorCount := uint64(len(s.Validators))
	if validatorCount > 0 {
		startIdx := uint64(s.NextWithdrawalValidatorIndex)
		bound := s.specs.MaxValidatorsPerWithdrawalsSweep
		if validatorCount < bound {
			bound = validatorCount
		}

		for i := uint64(0); i < bound && uint64(len(result.withdrawals)) < maxWithdrawals; i++ {
			vidx := phase0.ValidatorIndex((startIdx + i) % validatorCount)
			validator := s.Validators[vidx]
			balance := getBalanceAfterWithdrawals(s, vidx, result.withdrawals)

			result.processedValidatorsSweepCount++

			// Full withdrawal: exited and withdrawable
			if isFullyWithdrawableValidator(validator, balance, epoch) {
				result.withdrawals = append(result.withdrawals, &capella.Withdrawal{
					Index:          nextIdx,
					ValidatorIndex: vidx,
					Address:        getWithdrawalAddress(validator),
					Amount:         balance,
				})
				nextIdx++
				continue
			}

			// Partial (sweep) withdrawal: excess balance above max effective balance,
			// only when effective_balance has reached the max.
			maxEB := s.getMaxEffectiveBalance(validator)
			if isPartiallyWithdrawableValidator(validator, balance, maxEB) {
				result.withdrawals = append(result.withdrawals, &capella.Withdrawal{
					Index:          nextIdx,
					ValidatorIndex: vidx,
					Address:        getWithdrawalAddress(validator),
					Amount:         balance - maxEB,
				})
				nextIdx++
			}
		}
	}

	return result
}

// getWithdrawalAddress extracts the withdrawal address from validator credentials.
func getWithdrawalAddress(v *phase0.Validator) [20]byte {
	var addr [20]byte
	if len(v.WithdrawalCredentials) >= 32 {
		copy(addr[:], v.WithdrawalCredentials[12:32])
	}
	return addr
}

// isParentBlockFull checks if the parent block had an execution payload (Gloas).
// Spec: return state.latest_execution_payload_bid.block_hash == state.latest_block_hash
// https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-is_parent_block_full
func isParentBlockFull(s *stateAccessor) bool {
	if s.version < spec.DataVersionGloas {
		return true // Pre-Gloas: always full
	}

	if s.LatestExecutionPayloadBid == nil {
		return false
	}

	return s.LatestExecutionPayloadBid.BlockHash == s.LatestBlockHash
}
