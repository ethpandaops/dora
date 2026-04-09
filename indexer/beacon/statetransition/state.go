package statetransition

import (
	"fmt"
	"math"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/altair"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
)

// stateAccessor provides a unified interface to access and mutate beacon state fields
// across Fulu and Gloas versions. All fields are pointers/slices into the underlying
// VersionedBeaconState, so mutations are applied in-place.
type stateAccessor struct {
	version spec.DataVersion
	specs   *consensus.ChainSpec

	// Common fields shared by Fulu and Gloas (pointers into the underlying state).
	Slot                          phase0.Slot
	Validators                    []*phase0.Validator
	Balances                      []phase0.Gwei
	RANDAOMixes                   []phase0.Root
	Slashings                     []phase0.Gwei
	PreviousEpochParticipation    []altair.ParticipationFlags
	CurrentEpochParticipation     []altair.ParticipationFlags
	JustificationBits             []byte // bitfield.Bitvector4 is []byte
	PreviousJustifiedCheckpoint   *phase0.Checkpoint
	CurrentJustifiedCheckpoint    *phase0.Checkpoint
	FinalizedCheckpoint           *phase0.Checkpoint
	InactivityScores              []uint64
	CurrentSyncCommittee          *altair.SyncCommittee
	NextSyncCommittee             *altair.SyncCommittee
	ETH1DataVotes                 []*phase0.ETH1Data
	BlockRoots                    []phase0.Root
	StateRoots                    []phase0.Root
	HistoricalSummaries           []*capella.HistoricalSummary
	DepositRequestsStartIndex     uint64
	DepositBalanceToConsume       phase0.Gwei
	ExitBalanceToConsume          phase0.Gwei
	EarliestExitEpoch             phase0.Epoch
	ConsolidationBalanceToConsume phase0.Gwei
	EarliestConsolidationEpoch    phase0.Epoch
	PendingDeposits               []*electra.PendingDeposit
	PendingPartialWithdrawals     []*electra.PendingPartialWithdrawal
	PendingConsolidations         []*electra.PendingConsolidation
	ProposerLookahead             []phase0.ValidatorIndex

	// Gloas-only fields (nil for Fulu)
	BuilderPendingPayments    []*gloas.BuilderPendingPayment
	BuilderPendingWithdrawals []*gloas.BuilderPendingWithdrawal

	// Back-references for writing mutated slices/values back to the underlying state.
	rawState *spec.VersionedBeaconState
}

func newStateAccessor(state *spec.VersionedBeaconState, specs *consensus.ChainSpec) (*stateAccessor, error) {
	s := &stateAccessor{
		version:  state.Version,
		specs:    specs,
		rawState: state,
	}

	switch state.Version {
	case spec.DataVersionFulu:
		if state.Fulu == nil {
			return nil, fmt.Errorf("nil fulu state")
		}
		f := state.Fulu
		s.Slot = f.Slot
		s.Validators = f.Validators
		s.Balances = f.Balances
		s.RANDAOMixes = f.RANDAOMixes
		s.Slashings = f.Slashings
		s.PreviousEpochParticipation = f.PreviousEpochParticipation
		s.CurrentEpochParticipation = f.CurrentEpochParticipation
		s.JustificationBits = f.JustificationBits
		s.PreviousJustifiedCheckpoint = f.PreviousJustifiedCheckpoint
		s.CurrentJustifiedCheckpoint = f.CurrentJustifiedCheckpoint
		s.FinalizedCheckpoint = f.FinalizedCheckpoint
		s.InactivityScores = f.InactivityScores
		s.CurrentSyncCommittee = f.CurrentSyncCommittee
		s.NextSyncCommittee = f.NextSyncCommittee
		s.ETH1DataVotes = f.ETH1DataVotes
		s.BlockRoots = f.BlockRoots
		s.StateRoots = f.StateRoots
		s.HistoricalSummaries = f.HistoricalSummaries
		s.DepositRequestsStartIndex = f.DepositRequestsStartIndex
		s.DepositBalanceToConsume = f.DepositBalanceToConsume
		s.ExitBalanceToConsume = f.ExitBalanceToConsume
		s.EarliestExitEpoch = f.EarliestExitEpoch
		s.ConsolidationBalanceToConsume = f.ConsolidationBalanceToConsume
		s.EarliestConsolidationEpoch = f.EarliestConsolidationEpoch
		s.PendingDeposits = f.PendingDeposits
		s.PendingPartialWithdrawals = f.PendingPartialWithdrawals
		s.PendingConsolidations = f.PendingConsolidations
		s.ProposerLookahead = f.ProposerLookahead
	case spec.DataVersionGloas:
		if state.Gloas == nil {
			return nil, fmt.Errorf("nil gloas state")
		}
		g := state.Gloas
		s.Slot = g.Slot
		s.Validators = g.Validators
		s.Balances = g.Balances
		s.RANDAOMixes = g.RANDAOMixes
		s.Slashings = g.Slashings
		s.PreviousEpochParticipation = g.PreviousEpochParticipation
		s.CurrentEpochParticipation = g.CurrentEpochParticipation
		s.JustificationBits = g.JustificationBits
		s.PreviousJustifiedCheckpoint = g.PreviousJustifiedCheckpoint
		s.CurrentJustifiedCheckpoint = g.CurrentJustifiedCheckpoint
		s.FinalizedCheckpoint = g.FinalizedCheckpoint
		s.InactivityScores = g.InactivityScores
		s.CurrentSyncCommittee = g.CurrentSyncCommittee
		s.NextSyncCommittee = g.NextSyncCommittee
		s.ETH1DataVotes = g.ETH1DataVotes
		s.BlockRoots = g.BlockRoots
		s.StateRoots = g.StateRoots
		s.HistoricalSummaries = g.HistoricalSummaries
		s.DepositRequestsStartIndex = g.DepositRequestsStartIndex
		s.DepositBalanceToConsume = g.DepositBalanceToConsume
		s.ExitBalanceToConsume = g.ExitBalanceToConsume
		s.EarliestExitEpoch = g.EarliestExitEpoch
		s.ConsolidationBalanceToConsume = g.ConsolidationBalanceToConsume
		s.EarliestConsolidationEpoch = g.EarliestConsolidationEpoch
		s.PendingDeposits = g.PendingDeposits
		s.PendingPartialWithdrawals = g.PendingPartialWithdrawals
		s.PendingConsolidations = g.PendingConsolidations
		s.ProposerLookahead = g.ProposerLookahead
		s.BuilderPendingPayments = g.BuilderPendingPayments
		s.BuilderPendingWithdrawals = g.BuilderPendingWithdrawals
	default:
		return nil, fmt.Errorf("unsupported state version: %v", state.Version)
	}

	return s, nil
}

// writeBack writes mutated slice headers and scalar fields back to the underlying
// VersionedBeaconState. This is needed because Go slice reassignment (e.g.
// s.Balances = newSlice) doesn't update the original struct field.
// Call this after all epoch processing is complete.
func (s *stateAccessor) writeBack() {
	switch s.version {
	case spec.DataVersionFulu:
		f := s.rawState.Fulu
		f.Slot = s.Slot
		f.Validators = s.Validators
		f.Balances = s.Balances
		f.RANDAOMixes = s.RANDAOMixes
		f.Slashings = s.Slashings
		f.PreviousEpochParticipation = s.PreviousEpochParticipation
		f.CurrentEpochParticipation = s.CurrentEpochParticipation
		f.JustificationBits = s.JustificationBits
		f.InactivityScores = s.InactivityScores
		f.ETH1DataVotes = s.ETH1DataVotes
		f.HistoricalSummaries = s.HistoricalSummaries
		f.DepositBalanceToConsume = s.DepositBalanceToConsume
		f.ExitBalanceToConsume = s.ExitBalanceToConsume
		f.EarliestExitEpoch = s.EarliestExitEpoch
		f.ConsolidationBalanceToConsume = s.ConsolidationBalanceToConsume
		f.EarliestConsolidationEpoch = s.EarliestConsolidationEpoch
		f.PendingDeposits = s.PendingDeposits
		f.PendingPartialWithdrawals = s.PendingPartialWithdrawals
		f.PendingConsolidations = s.PendingConsolidations
		f.ProposerLookahead = s.ProposerLookahead
	case spec.DataVersionGloas:
		g := s.rawState.Gloas
		g.Slot = s.Slot
		g.Validators = s.Validators
		g.Balances = s.Balances
		g.RANDAOMixes = s.RANDAOMixes
		g.Slashings = s.Slashings
		g.PreviousEpochParticipation = s.PreviousEpochParticipation
		g.CurrentEpochParticipation = s.CurrentEpochParticipation
		g.JustificationBits = s.JustificationBits
		g.InactivityScores = s.InactivityScores
		g.ETH1DataVotes = s.ETH1DataVotes
		g.HistoricalSummaries = s.HistoricalSummaries
		g.DepositBalanceToConsume = s.DepositBalanceToConsume
		g.ExitBalanceToConsume = s.ExitBalanceToConsume
		g.EarliestExitEpoch = s.EarliestExitEpoch
		g.ConsolidationBalanceToConsume = s.ConsolidationBalanceToConsume
		g.EarliestConsolidationEpoch = s.EarliestConsolidationEpoch
		g.PendingDeposits = s.PendingDeposits
		g.PendingPartialWithdrawals = s.PendingPartialWithdrawals
		g.PendingConsolidations = s.PendingConsolidations
		g.ProposerLookahead = s.ProposerLookahead
		g.BuilderPendingPayments = s.BuilderPendingPayments
		g.BuilderPendingWithdrawals = s.BuilderPendingWithdrawals
	}
}

// currentEpoch returns the current epoch derived from the slot.
func (s *stateAccessor) currentEpoch() phase0.Epoch {
	return phase0.Epoch(uint64(s.Slot) / s.specs.SlotsPerEpoch)
}

// previousEpoch returns the previous epoch (minimum 0).
func (s *stateAccessor) previousEpoch() phase0.Epoch {
	epoch := s.currentEpoch()
	if epoch == 0 {
		return 0
	}
	return epoch - 1
}

// isActiveValidator checks if a validator is active at the given epoch.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#is_active_validator
func isActiveValidator(v *phase0.Validator, epoch phase0.Epoch) bool {
	return v.ActivationEpoch <= epoch && epoch < v.ExitEpoch
}

// getActiveValidatorIndices returns all active validator indices for the given epoch.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#get_active_validator_indices
func (s *stateAccessor) getActiveValidatorIndices(epoch phase0.Epoch) []phase0.ValidatorIndex {
	indices := make([]phase0.ValidatorIndex, 0, len(s.Validators))
	for i, v := range s.Validators {
		if isActiveValidator(v, epoch) {
			indices = append(indices, phase0.ValidatorIndex(i))
		}
	}
	return indices
}

// getTotalActiveBalance returns the sum of effective balances for all active validators.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#get_total_active_balance
func (s *stateAccessor) getTotalActiveBalance() phase0.Gwei {
	total := phase0.Gwei(0)
	epoch := s.currentEpoch()
	for _, v := range s.Validators {
		if isActiveValidator(v, epoch) {
			total += v.EffectiveBalance
		}
	}
	if total < phase0.Gwei(s.specs.EffectiveBalanceIncrement) {
		return phase0.Gwei(s.specs.EffectiveBalanceIncrement)
	}
	return total
}

// getMaxEffectiveBalance returns the max effective balance for a validator (Electra+).
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-get_max_effective_balance
func (s *stateAccessor) getMaxEffectiveBalance(v *phase0.Validator) phase0.Gwei {
	if hasCompoundingWithdrawalCredential(v) {
		return phase0.Gwei(s.specs.MaxEffectiveBalanceElectra)
	}
	return phase0.Gwei(s.specs.MinActivationBalance)
}

// hasCompoundingWithdrawalCredential checks for 0x02 withdrawal credential prefix.
func hasCompoundingWithdrawalCredential(v *phase0.Validator) bool {
	return len(v.WithdrawalCredentials) > 0 && v.WithdrawalCredentials[0] == 0x02
}

// hasExecutionWithdrawalCredential checks for 0x01 or 0x02 withdrawal credential prefix.
func hasExecutionWithdrawalCredential(v *phase0.Validator) bool {
	if len(v.WithdrawalCredentials) == 0 {
		return false
	}
	return v.WithdrawalCredentials[0] == 0x01 || v.WithdrawalCredentials[0] == 0x02
}

// isEligibleForActivationQueue checks if a validator is eligible to be added to activation queue.
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-is_eligible_for_activation_queue
func isEligibleForActivationQueue(v *phase0.Validator, specs *consensus.ChainSpec) bool {
	return v.ActivationEligibilityEpoch == FarFutureEpoch &&
		v.EffectiveBalance >= phase0.Gwei(specs.MinActivationBalance)
}

// isEligibleForActivation checks if a validator is eligible for activation.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#is_eligible_for_activation
func isEligibleForActivation(v *phase0.Validator, finalizedEpoch phase0.Epoch) bool {
	return v.ActivationEligibilityEpoch <= finalizedEpoch &&
		v.ActivationEpoch == FarFutureEpoch
}

// intSqrt returns the integer square root of n.
func intSqrt(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	x := n
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + n/x) / 2
	}
	return x
}

// FarFutureEpoch is the sentinel value for unset epochs.
const FarFutureEpoch = phase0.Epoch(math.MaxUint64)

// Altair constants.
const (
	TimelySourceFlagIndex = 0
	TimelyTargetFlagIndex = 1
	TimelyHeadFlagIndex   = 2

	TimelySourceWeight = 14
	TimelyTargetWeight = 26
	TimelyHeadWeight   = 14
	SyncRewardWeight   = 2
	ProposerWeight     = 8
	WeightDenominator  = 64

	ParticipationFlagCount = 3
	BaseRewardsPerEpoch    = 4
)

var ParticipationFlagWeights = [ParticipationFlagCount]uint64{
	TimelySourceWeight,
	TimelyTargetWeight,
	TimelyHeadWeight,
}

// hasFlag checks if a participation flags byte has the given flag set.
func hasFlag(flags altair.ParticipationFlags, flagIndex int) bool {
	return flags&altair.ParticipationFlags(1<<flagIndex) != 0
}

// getBaseRewardPerIncrement returns the base reward per increment.
// New in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#get_base_reward_per_increment
func (s *stateAccessor) getBaseRewardPerIncrement() phase0.Gwei {
	totalBalance := s.getTotalActiveBalance()
	return phase0.Gwei(s.specs.EffectiveBalanceIncrement) * phase0.Gwei(s.specs.BaseRewardFactor) / phase0.Gwei(intSqrt(uint64(totalBalance)))
}

// getBaseReward returns the base reward for a validator.
// Modified in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#get_base_reward
func (s *stateAccessor) getBaseReward(index phase0.ValidatorIndex) phase0.Gwei {
	increments := s.Validators[index].EffectiveBalance / phase0.Gwei(s.specs.EffectiveBalanceIncrement)
	return increments * s.getBaseRewardPerIncrement()
}

// getUnslashedParticipatingIndices returns validator indices that participated with
// the given flag in the given epoch and are not slashed.
// New in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#get_unslashed_participating_indices
func (s *stateAccessor) getUnslashedParticipatingIndices(flagIndex int, epoch phase0.Epoch) []phase0.ValidatorIndex {
	var participation []altair.ParticipationFlags
	if epoch == s.currentEpoch() {
		participation = s.CurrentEpochParticipation
	} else {
		participation = s.PreviousEpochParticipation
	}

	indices := make([]phase0.ValidatorIndex, 0)
	for i, v := range s.Validators {
		if isActiveValidator(v, epoch) && !v.Slashed && int(i) < len(participation) && hasFlag(participation[i], flagIndex) {
			indices = append(indices, phase0.ValidatorIndex(i))
		}
	}
	return indices
}

// getUnslashedParticipatingBalance returns the total effective balance of
// unslashed validators that participated with the given flag.
func (s *stateAccessor) getUnslashedParticipatingBalance(flagIndex int, epoch phase0.Epoch) phase0.Gwei {
	total := phase0.Gwei(0)
	indices := s.getUnslashedParticipatingIndices(flagIndex, epoch)
	for _, idx := range indices {
		total += s.Validators[idx].EffectiveBalance
	}
	if total < phase0.Gwei(s.specs.EffectiveBalanceIncrement) {
		return phase0.Gwei(s.specs.EffectiveBalanceIncrement)
	}
	return total
}

// increaseBalance adds delta to a validator's balance.
func (s *stateAccessor) increaseBalance(index phase0.ValidatorIndex, delta phase0.Gwei) {
	s.Balances[index] += delta
}

// decreaseBalance subtracts delta from a validator's balance (clamped to 0).
func (s *stateAccessor) decreaseBalance(index phase0.ValidatorIndex, delta phase0.Gwei) {
	if s.Balances[index] >= delta {
		s.Balances[index] -= delta
	} else {
		s.Balances[index] = 0
	}
}

// getBalanceChurnLimit returns the balance churn limit (Electra+).
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-get_balance_churn_limit
func (s *stateAccessor) getBalanceChurnLimit() phase0.Gwei {
	churn := uint64(s.getTotalActiveBalance()) / s.specs.ChurnLimitQuotient
	if s.specs.MinPerEpochChurnLimitElectra > churn {
		churn = s.specs.MinPerEpochChurnLimitElectra
	}
	return phase0.Gwei(churn - churn%s.specs.EffectiveBalanceIncrement)
}

// getActivationExitChurnLimit returns the activation/exit churn limit.
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-get_activation_exit_churn_limit
func (s *stateAccessor) getActivationExitChurnLimit() phase0.Gwei {
	churn := s.getBalanceChurnLimit()
	if phase0.Gwei(s.specs.MaxPerEpochActivationExitChurnLimit) < churn {
		return phase0.Gwei(s.specs.MaxPerEpochActivationExitChurnLimit)
	}
	return churn
}
