package statetransition

import (
	"math"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/altair"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
)

// stateAccessor wraps a fork-agnostic *all.BeaconState with ChainSpec, dynSsz,
// and the per-StateTransition lazy caches. The agnostic state already provides
// flat field access for the union of all fork-specific BeaconState fields, so
// the accessor just embeds it and exposes those fields (and Version) directly.
// Fork-conditional logic in this package checks s.Version against the
// spec.DataVersion* constants.
type stateAccessor struct {
	*all.BeaconState

	specs  *consensus.ChainSpec
	dynSsz *dynssz.DynSsz

	// Caches (lazily populated, not written back).
	// Shared via StateTransition to persist across multiple ApplyBlock calls.
	caches *stateTransitionCaches
}

// stateTransitionCaches holds lazily-populated caches that persist across
// multiple ApplyBlock / epoch transition calls on the same state.
//
// Caches that depend on the active validator set (totalActiveBal,
// baseRewardPerIncr, activeIndices) are keyed by epoch and auto-invalidate
// when crossing an epoch boundary. Effective-balance-dependent caches must
// also be cleared explicitly via invalidateBalanceCaches after the epoch
// transition runs processEffectiveBalanceUpdates / processRegistryUpdates /
// processPendingDeposits, since those mutate effective balances within an
// epoch without changing the cache key.
type stateTransitionCaches struct {
	pubkeyCache            map[phase0.BLSPubKey]phase0.ValidatorIndex
	activeIndicesEpoch     phase0.Epoch
	activeIndices          []phase0.ValidatorIndex
	totalActiveBalEpoch    phase0.Epoch
	totalActiveBalCache    *phase0.Gwei
	baseRewardPerIncrEpoch phase0.Epoch
	baseRewardPerIncrCache *phase0.Gwei
	committeeCache         *committeeCache
}

func newStateTransitionCaches() *stateTransitionCaches {
	return &stateTransitionCaches{
		committeeCache: newCommitteeCache(),
	}
}

// invalidateBalanceCaches clears caches that depend on validator effective
// balances (must be called after processEffectiveBalanceUpdates).
func (c *stateTransitionCaches) invalidateBalanceCaches() {
	c.totalActiveBalCache = nil
	c.baseRewardPerIncrCache = nil
	c.activeIndices = nil
	c.committeeCache = newCommitteeCache()
	// pubkeyCache filters validators by activeness against the epoch in which
	// it was first built. Once that epoch is past, newly activated validators
	// aren't in the cache and findValidatorByPubkey returns nil for them —
	// breaking sync-committee rewards and any other pubkey lookup. Invalidate
	// it on every epoch transition so the next call rebuilds it against the
	// current epoch's active set.
	c.pubkeyCache = nil
}

// newAccessor creates a stateAccessor wrapping the given fork-agnostic state
// with the StateTransition's specs, dynSsz, and lazy caches.
func (st *StateTransition) newAccessor(state *all.BeaconState) (*stateAccessor, error) {
	return &stateAccessor{
		BeaconState: state,
		specs:       st.specs,
		dynSsz:      st.dynSsz,
		caches:      st.caches,
	}, nil
}

// hasLatestPayloadBid reports whether a latest execution payload bid is set
// on the underlying state. Only Gloas+ states carry one.
func (s *stateAccessor) hasLatestPayloadBid() bool {
	return s.LatestExecutionPayloadBid != nil
}

// latestPayloadBlockHash returns the block hash from the latest execution
// payload bid, or the zero hash if no bid is set.
func (s *stateAccessor) latestPayloadBlockHash() phase0.Hash32 {
	if s.LatestExecutionPayloadBid == nil {
		return phase0.Hash32{}
	}

	return s.LatestExecutionPayloadBid.BlockHash
}

// computeStateHTR computes the hash tree root of the underlying state.
func (s *stateAccessor) computeStateHTR() (phase0.Root, error) {
	return s.dynSsz.HashTreeRoot(s.BeaconState)
}

// computeLatestBlockHeaderHTR computes hash_tree_root(state.latest_block_header).
func (s *stateAccessor) computeLatestBlockHeaderHTR() (phase0.Root, error) {
	if s.LatestBlockHeader == nil {
		return phase0.Root{}, nil
	}
	return s.LatestBlockHeader.HashTreeRoot()
}

// clearNextSlotAvailabilityBit clears the execution payload availability bit
// for the next slot (Gloas-specific process_slot step).
func (s *stateAccessor) clearNextSlotAvailabilityBit() {
	if s.Version < spec.DataVersionGloas || len(s.ExecutionPayloadAvailability) == 0 {
		return
	}
	nextIdx := (uint64(s.Slot) + 1) % s.specs.SlotsPerHistoricalRoot
	byteIdx := nextIdx / 8
	bitIdx := nextIdx % 8
	if byteIdx < uint64(len(s.ExecutionPayloadAvailability)) {
		s.ExecutionPayloadAvailability[byteIdx] &^= 1 << bitIdx
	}
}

// setAvailabilityBit sets the execution payload availability bit for the given slot.
func (s *stateAccessor) setAvailabilityBit(slot phase0.Slot) {
	if s.Version < spec.DataVersionGloas || len(s.ExecutionPayloadAvailability) == 0 {
		return
	}
	bitfieldLen := uint64(len(s.ExecutionPayloadAvailability)) * 8
	idx := uint64(slot) % bitfieldLen
	s.ExecutionPayloadAvailability[idx/8] |= 1 << (idx % 8)
}

// getAvailabilityBit returns the execution payload availability bit for a given slot.
func (s *stateAccessor) getAvailabilityBit(slot phase0.Slot) bool {
	if s.Version < spec.DataVersionGloas || len(s.ExecutionPayloadAvailability) == 0 {
		return false
	}
	idx := uint64(slot) % s.specs.SlotsPerHistoricalRoot
	byteIdx := idx / 8
	bitIdx := idx % 8
	if byteIdx >= uint64(len(s.ExecutionPayloadAvailability)) {
		return false
	}
	return s.ExecutionPayloadAvailability[byteIdx]&(1<<bitIdx) != 0
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
	if s.caches.activeIndices != nil && s.caches.activeIndicesEpoch == epoch {
		return s.caches.activeIndices
	}

	indices := make([]phase0.ValidatorIndex, 0, len(s.Validators))
	for i, v := range s.Validators {
		if isActiveValidator(v, epoch) {
			indices = append(indices, phase0.ValidatorIndex(i))
		}
	}

	s.caches.activeIndicesEpoch = epoch
	s.caches.activeIndices = indices

	return indices
}

// getTotalActiveBalance returns the sum of effective balances for all active validators.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#get_total_active_balance
func (s *stateAccessor) getTotalActiveBalance() phase0.Gwei {
	epoch := s.currentEpoch()
	if s.caches.totalActiveBalCache != nil && s.caches.totalActiveBalEpoch == epoch {
		return *s.caches.totalActiveBalCache
	}
	total := phase0.Gwei(0)
	for _, v := range s.Validators {
		if isActiveValidator(v, epoch) {
			total += v.EffectiveBalance
		}
	}
	if total < phase0.Gwei(s.specs.EffectiveBalanceIncrement) {
		total = phase0.Gwei(s.specs.EffectiveBalanceIncrement)
	}
	s.caches.totalActiveBalCache = &total
	s.caches.totalActiveBalEpoch = epoch
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
	epoch := s.currentEpoch()
	if s.caches.baseRewardPerIncrCache != nil && s.caches.baseRewardPerIncrEpoch == epoch {
		return *s.caches.baseRewardPerIncrCache
	}
	totalBalance := s.getTotalActiveBalance()
	result := phase0.Gwei(s.specs.EffectiveBalanceIncrement) * phase0.Gwei(s.specs.BaseRewardFactor) / phase0.Gwei(intSqrt(uint64(totalBalance)))
	s.caches.baseRewardPerIncrCache = &result
	s.caches.baseRewardPerIncrEpoch = epoch
	return result
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
