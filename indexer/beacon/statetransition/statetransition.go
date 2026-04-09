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
)

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

// PrepareEpochPreState takes a post-block state and mutates it into the pre-state
// of the target epoch. This is the main entry point for epoch state preparation.
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
func PrepareEpochPreState(state *spec.VersionedBeaconState, epoch phase0.Epoch, payload *gloas.ExecutionPayloadEnvelope, specs *consensus.ChainSpec, info *TransitionInfo) error {
	if state.Version < spec.DataVersionFulu {
		return nil
	}

	// Step 1: For Gloas+ pre-payload states, apply the execution payload transition.
	if payload != nil && isPrePayloadState(state) {
		if err := processExecutionPayload(state, payload, specs); err != nil {
			return fmt.Errorf("process_execution_payload: %w", err)
		}
	}

	// Step 2: Advance to the first slot of the target epoch.
	targetSlot := phase0.Slot(uint64(epoch) * specs.SlotsPerEpoch)
	if err := processSlots(state, targetSlot, specs, info); err != nil {
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
func processSlots(state *spec.VersionedBeaconState, targetSlot phase0.Slot, specs *consensus.ChainSpec, info *TransitionInfo) error {
	currentSlot, err := state.Slot()
	if err != nil {
		return fmt.Errorf("failed to get state slot: %w", err)
	}

	if currentSlot >= targetSlot {
		return nil
	}

	s, err := newStateAccessor(state, specs)
	if err != nil {
		return fmt.Errorf("failed to create state accessor: %w", err)
	}

	slotsPerEpoch := specs.SlotsPerEpoch

	// Jump to each epoch boundary and apply the epoch transition.
	// The spec applies process_epoch when (state.slot + 1) % SLOTS_PER_EPOCH == 0,
	// i.e. at the last slot of each epoch, then increments the slot.
	for {
		nextBoundary := phase0.Slot(((uint64(s.Slot)/slotsPerEpoch)+1)*slotsPerEpoch - 1)

		if nextBoundary >= targetSlot {
			break
		}

		s.Slot = nextBoundary
		if err := processEpochInternal(s, info); err != nil {
			return fmt.Errorf("process_epoch at slot %d: %w", s.Slot, err)
		}

		s.Slot++ // cross into next epoch
	}

	s.Slot = targetSlot
	s.writeBack()

	return nil
}

// isPrePayloadState checks whether a Gloas state is pre-payload
// (the execution payload for the latest block has NOT been processed yet).
func isPrePayloadState(state *spec.VersionedBeaconState) bool {
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

	g := state.Gloas
	slotsPerEpoch := specs.SlotsPerEpoch

	// Process deposit requests → convert to pending deposits.
	// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_deposit_request
	if envelope.ExecutionRequests != nil {
		for _, deposit := range envelope.ExecutionRequests.Deposits {
			g.PendingDeposits = append(g.PendingDeposits, &electra.PendingDeposit{
				Pubkey:                deposit.Pubkey,
				WithdrawalCredentials: deposit.WithdrawalCredentials,
				Amount:                deposit.Amount,
				Signature:             deposit.Signature,
				Slot:                  g.Slot,
			})
		}

		// Note: withdrawal requests and consolidation requests are NOT processed here
		// since they require full validator lookup and exit queue logic.
		// For our purpose (advancing to epoch boundary), the pending deposits
		// are the critical part. Withdrawal/consolidation requests affect the
		// state sim which replays them separately.
	}

	// Queue the builder payment (direct withdrawal for delivered payload).
	// The bid was recorded in builder_pending_payments during process_execution_payload_bid.
	// Now that the payload is delivered, we move the payment to builder_pending_withdrawals
	// and clear the pending payment entry.
	paymentIdx := slotsPerEpoch + uint64(g.Slot)%slotsPerEpoch
	if paymentIdx < uint64(len(g.BuilderPendingPayments)) {
		payment := g.BuilderPendingPayments[paymentIdx]
		if payment != nil && payment.Withdrawal != nil && payment.Withdrawal.Amount > 0 {
			g.BuilderPendingWithdrawals = append(g.BuilderPendingWithdrawals, payment.Withdrawal)
		}
		g.BuilderPendingPayments[paymentIdx] = &gloas.BuilderPendingPayment{}
	}

	// Set execution payload availability bit.
	bitfieldLen := uint64(len(g.ExecutionPayloadAvailability)) * 8
	if bitfieldLen > 0 {
		idx := uint64(g.Slot) % bitfieldLen
		g.ExecutionPayloadAvailability[idx/8] |= 1 << (idx % 8)
	}

	// Cache the execution payload block hash.
	if envelope.Payload != nil {
		copy(g.LatestBlockHash[:], envelope.Payload.BlockHash[:])
	}

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
