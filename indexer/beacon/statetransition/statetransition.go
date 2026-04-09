// Package statetransition implements consensus-spec state transition functions
// for Fulu+ beacon states.
//
// The primary entry point is PrepareEpochPreState, which takes a post-block state
// (typically the last block of a parent epoch) and advances it to the pre-state
// of a target epoch by applying payload processing (Gloas+) and epoch transitions.
//
// This produces the normally inaccessible pre-slot-1, post-epoch-transition state
// that the beacon API cannot serve directly.
//
// Only needed for Fulu+ states. Pre-Fulu states already provide the correct
// epoch boundary values from the post-state of the parent epoch's last block.
package statetransition

import (
	"fmt"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	dynssz "github.com/pk910/dynamic-ssz"
)

// StateTransition holds the chain spec, dynamic SSZ encoder, and reusable caches
// for applying multiple blocks and epoch transitions to the same state.
// Create one per state replay session and reuse across ApplyBlock calls.
type StateTransition struct {
	specs  *consensus.ChainSpec
	dynSsz *dynssz.DynSsz
	caches *stateTransitionCaches
}

// NewStateTransition creates a new StateTransition with the given chain spec and dynssz encoder.
func NewStateTransition(specs *consensus.ChainSpec, ds *dynssz.DynSsz) *StateTransition {
	return &StateTransition{
		specs:  specs,
		dynSsz: ds,
		caches: newStateTransitionCaches(),
	}
}

// ApplyBlock applies a beacon block to the state in-place.
func (st *StateTransition) ApplyBlock(state *spec.VersionedBeaconState, block *spec.VersionedSignedBeaconBlock) error {
	return applyBlockInternal(state, block, st.specs, st.dynSsz, st.caches, phase0.Root{})
}

// ApplyBlockWithStateRoot is like ApplyBlock but accepts the current state's
// hash tree root as a hint, skipping the expensive HTR computation in the first
// process_slot. The hint must match the HTR of the current state — typically
// sourced from the previously applied block's state_root field. Passing an
// incorrect hint will produce an inconsistent state and is undefined behavior.
func (st *StateTransition) ApplyBlockWithStateRoot(state *spec.VersionedBeaconState, block *spec.VersionedSignedBeaconBlock, parentStateRoot phase0.Root) error {
	return applyBlockInternal(state, block, st.specs, st.dynSsz, st.caches, parentStateRoot)
}

// ApplyExecutionPayload applies a Gloas execution payload to the state.
func (st *StateTransition) ApplyExecutionPayload(state *spec.VersionedBeaconState, payload *gloas.SignedExecutionPayloadEnvelope) error {
	if payload == nil || payload.Message == nil {
		return nil
	}
	return processExecutionPayload(state, payload.Message, st.specs)
}

// PrepareEpochPreState advances a post-block state to the pre-state of the target epoch.
func (st *StateTransition) PrepareEpochPreState(state *spec.VersionedBeaconState, epoch phase0.Epoch, payload *gloas.ExecutionPayloadEnvelope, info *TransitionInfo) error {
	return prepareEpochPreStateInternal(state, epoch, payload, st.specs, info, st.caches)
}

// TransitionInfo collects metadata from the state transition that callers may
// need for downstream processing. Pass a non-nil pointer to PrepareEpochPreState
// to receive this information; pass nil if not needed.
type TransitionInfo struct {
	// DelayedBuilderPayments is the number of delayed builder payments appended
	// to BuilderPendingWithdrawals by the last epoch transition's
	// process_builder_pending_payments. This tells the state simulator how many
	// entries at the tail of the queue are delayed (vs direct payments from block payloads).
	DelayedBuilderPayments uint32
}

// PrepareEpochPreState is the standalone entry point (creates fresh caches).
// Prefer StateTransition.PrepareEpochPreState for repeated use.
//
// The input state is typically the post-state of the last block of a parent epoch.
// The function:
//  1. For Gloas+: if the state is pre-payload and a payload envelope is provided,
//     applies the execution payload transition first.
//  2. Advances the state to the first slot of the target epoch, applying epoch
//     transitions at every epoch boundary crossed (handles skipped slots and epochs).
//
// After this call, the state represents the pre-block state at the first slot of
// the target epoch, with all epoch transitions applied — including builder payment
// conversions, balance updates, proposer lookahead, etc.
//
// If info is non-nil, it is populated with metadata from the transition.
func prepareEpochPreStateInternal(state *spec.VersionedBeaconState, epoch phase0.Epoch, payload *gloas.ExecutionPayloadEnvelope, specs *consensus.ChainSpec, info *TransitionInfo, caches *stateTransitionCaches) error {
	if state.Version < spec.DataVersionFulu {
		return nil
	}

	// Step 1: For Gloas+ pre-payload states, apply the execution payload transition.
	if payload != nil && IsPrePayloadState(state) {
		if err := processExecutionPayload(state, payload, specs); err != nil {
			return fmt.Errorf("process_execution_payload: %w", err)
		}
	}

	// Step 2: Advance to the first slot of the target epoch.
	targetSlot := phase0.Slot(uint64(epoch) * specs.SlotsPerEpoch)
	if err := processSlots(state, targetSlot, specs, info, caches); err != nil {
		return fmt.Errorf("process_slots to epoch %d (slot %d): %w", epoch, targetSlot, err)
	}

	return nil
}

// processSlots advances the state from its current slot to targetSlot, applying
// epoch transitions at every epoch boundary crossed.
//
// Skips per-slot state/block root caching (process_slot) since we cannot compute
// hash_tree_root efficiently and the cached roots don't affect the epoch transition
// outputs we need. Jumps directly to each epoch boundary.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#process_slots
func processSlots(state *spec.VersionedBeaconState, targetSlot phase0.Slot, specs *consensus.ChainSpec, info *TransitionInfo, caches *stateTransitionCaches) error {
	currentSlot, err := state.Slot()
	if err != nil {
		return fmt.Errorf("failed to get state slot: %w", err)
	}

	if currentSlot >= targetSlot {
		return nil
	}

	s, err := newStateAccessorWithCaches(state, specs, caches)
	if err != nil {
		return fmt.Errorf("failed to create state accessor: %w", err)
	}

	slotsPerEpoch := specs.SlotsPerEpoch

	for s.Slot < targetSlot {
		processSlotBlockRootCaching(s)

		// Apply epoch transition at epoch boundary (last slot of epoch).
		if (uint64(s.Slot)+1)%slotsPerEpoch == 0 {
			if err := processEpochInternal(s, info); err != nil {
				return fmt.Errorf("process_epoch at slot %d: %w", s.Slot, err)
			}
		}

		s.Slot++
	}

	s.writeBack()

	return nil
}

// processSlotBlockRootCaching implements the essential parts of process_slot:
// computes the state root, fills latest_block_header.state_root if zero,
// then caches the block root. The state root must be computed first because
// the block root depends on the header's state_root field.
func processSlotBlockRootCaching(s *stateAccessor) {
	stateRoot, err := s.computeStateHTR()
	if err != nil {
		return
	}

	idx := uint64(s.Slot) % s.specs.SlotsPerHistoricalRoot
	s.StateRoots[idx] = stateRoot

	// Fill latest_block_header.state_root if zero (set after each processBlockHeader).
	if s.LatestBlockHeader != nil && s.LatestBlockHeader.StateRoot == (phase0.Root{}) {
		s.LatestBlockHeader.StateRoot = stateRoot
	}

	blockRoot, err := s.computeLatestBlockHeaderHTR()
	if err != nil {
		return
	}

	s.BlockRoots[idx] = blockRoot

	// Gloas: clear the next slot's execution payload availability bit.
	s.clearNextSlotAvailabilityBit()
}

// IsPrePayloadState checks whether a Gloas state is pre-payload
// (the execution payload for the latest block has NOT been processed yet).
func IsPrePayloadState(state *spec.VersionedBeaconState) bool {
	if state.Version < spec.DataVersionGloas || state.Gloas == nil {
		return false
	}

	slot, err := state.Slot()
	if err != nil {
		return false
	}

	bitfieldLen := uint64(len(state.Gloas.ExecutionPayloadAvailability)) * 8
	if bitfieldLen == 0 {
		return true
	}

	idx := uint64(slot) % bitfieldLen
	return state.Gloas.ExecutionPayloadAvailability[idx/8]&(1<<(idx%8)) == 0
}

// processExecutionPayload applies the Gloas execution payload state transition
// on a pre-payload state. Processes execution requests and records the builder
// payment, transitioning the state from pre-payload to post-payload.
//
// New in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#modified-process_execution_payload
func processExecutionPayload(state *spec.VersionedBeaconState, envelope *gloas.ExecutionPayloadEnvelope, specs *consensus.ChainSpec) error {
	if state.Version < spec.DataVersionGloas || state.Gloas == nil || envelope == nil {
		return nil
	}

	s, err := newStateAccessor(state, specs)
	if err != nil {
		return fmt.Errorf("failed to create state accessor: %w", err)
	}

	// Cache latest block header state root (spec: fill before payload processing).
	// https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#modified-process_execution_payload
	if s.LatestBlockHeader != nil && s.LatestBlockHeader.StateRoot == (phase0.Root{}) {
		stateRoot, htrErr := s.computeStateHTR()
		if htrErr != nil {
			return fmt.Errorf("failed to compute state root for header fill: %w", htrErr)
		}
		s.LatestBlockHeader.StateRoot = stateRoot
	}

	// Process execution requests (deposits, withdrawals, consolidations).
	if envelope.ExecutionRequests != nil {
		for _, deposit := range envelope.ExecutionRequests.Deposits {
			s.PendingDeposits = append(s.PendingDeposits, &electra.PendingDeposit{
				Pubkey:                deposit.Pubkey,
				WithdrawalCredentials: deposit.WithdrawalCredentials,
				Amount:                deposit.Amount,
				Signature:             deposit.Signature,
				Slot:                  s.Slot,
			})
		}

		for _, withdrawal := range envelope.ExecutionRequests.Withdrawals {
			processWithdrawalRequest(s, withdrawal)
		}

		for _, consolidation := range envelope.ExecutionRequests.Consolidations {
			processConsolidationRequest(s, consolidation)
		}
	}

	// Queue the builder payment (direct withdrawal for delivered payload).
	slotsPerEpoch := specs.SlotsPerEpoch
	paymentIdx := slotsPerEpoch + uint64(s.Slot)%slotsPerEpoch
	if paymentIdx < uint64(len(s.BuilderPendingPayments)) {
		payment := s.BuilderPendingPayments[paymentIdx]
		if payment != nil && payment.Withdrawal != nil && payment.Withdrawal.Amount > 0 {
			s.BuilderPendingWithdrawals = append(s.BuilderPendingWithdrawals, payment.Withdrawal)
		}
		s.BuilderPendingPayments[paymentIdx] = &gloas.BuilderPendingPayment{}
	}

	// Set execution payload availability bit.
	s.setAvailabilityBit()

	// Cache the execution payload block hash.
	if envelope.Payload != nil {
		s.LatestBlockHash = envelope.Payload.BlockHash
	}

	s.writeBack()
	return nil
}

// processEpochInternal runs the epoch transition on the accessor without writeBack.
// Used by processSlots at each epoch boundary.
//
// Fulu: https://github.com/ethereum/consensus-specs/blob/master/specs/fulu/beacon-chain.md#modified-process_epoch
// Modified in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#modified-process_epoch
func processEpochInternal(s *stateAccessor, info *TransitionInfo) error {
	if err := processJustificationAndFinalization(s); err != nil {
		return fmt.Errorf("process_justification_and_finalization: %w", err)
	}

	if err := processInactivityUpdates(s); err != nil {
		return fmt.Errorf("process_inactivity_updates: %w", err)
	}

	if err := processRewardsAndPenalties(s); err != nil {
		return fmt.Errorf("process_rewards_and_penalties: %w", err)
	}

	if err := processRegistryUpdates(s); err != nil {
		return fmt.Errorf("process_registry_updates: %w", err)
	}

	if err := processSlashings(s); err != nil {
		return fmt.Errorf("process_slashings: %w", err)
	}

	processEth1DataReset(s)

	if err := processPendingDeposits(s); err != nil {
		return fmt.Errorf("process_pending_deposits: %w", err)
	}

	processPendingConsolidations(s)

	// Gloas-only: process builder pending payments
	if s.version >= spec.DataVersionGloas {
		delayedCount := processBuilderPendingPayments(s)
		if info != nil {
			info.DelayedBuilderPayments = delayedCount
		}
	}

	processEffectiveBalanceUpdates(s)
	// Effective balances may have changed; clear caches that depend on them.
	s.caches.invalidateBalanceCaches()
	processSlashingsReset(s)
	processRandaoMixesReset(s)
	processHistoricalSummariesUpdate(s)
	processParticipationFlagUpdates(s)
	processSyncCommitteeUpdates(s)
	processProposerLookahead(s)

	// Gloas-only: process PTC window
	if s.version >= spec.DataVersionGloas {
		processPtcWindow(s)
	}

	return nil
}
