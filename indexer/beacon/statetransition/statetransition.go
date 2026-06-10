// Package statetransition implements consensus-spec state transition functions
// for Fulu+ beacon states.
//
// The primary entry point is PrepareEpochPreState, which takes a post-block state
// (typically the last block of a parent epoch) and advances it to the pre-state
// of a target epoch by applying epoch transitions.
//
// This produces the normally inaccessible pre-slot-1, post-epoch-transition state
// that the beacon API cannot serve directly.
//
// Only needed for Fulu+ states. Pre-Fulu states already provide the correct
// epoch boundary values from the post-state of the parent epoch's last block.
package statetransition

import (
	"fmt"
	"time"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
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

// ApplyInfo collects optional timing information from block application.
// Pass a non-nil pointer to ApplyBlockWithInfo to receive this data.
type ApplyInfo struct {
	// EpochTransitionDur is non-zero when the block's process_slots crossed an
	// epoch boundary, triggering process_epoch.
	EpochTransitionDur time.Duration
}

// ApplyBlock applies a beacon block to the state in-place.
func (st *StateTransition) ApplyBlock(state *all.BeaconState, block *all.SignedBeaconBlock) error {
	return st.applyBlock(state, block, phase0.Root{}, nil)
}

// ApplyBlockWithStateRoot is like ApplyBlock but accepts the current state's
// hash tree root as a hint, skipping the expensive HTR computation in the first
// process_slot. The hint must match the HTR of the current state — typically
// sourced from the previously applied block's state_root field. Passing an
// incorrect hint will produce an inconsistent state and is undefined behavior.
func (st *StateTransition) ApplyBlockWithStateRoot(state *all.BeaconState, block *all.SignedBeaconBlock, parentStateRoot phase0.Root) error {
	return st.applyBlock(state, block, parentStateRoot, nil)
}

// ApplyBlockWithInfo is like ApplyBlockWithStateRoot but also populates info
// with timing details (e.g. epoch transition duration).
func (st *StateTransition) ApplyBlockWithInfo(state *all.BeaconState, block *all.SignedBeaconBlock, parentStateRoot phase0.Root, info *ApplyInfo) error {
	return st.applyBlock(state, block, parentStateRoot, info)
}

// PrepareEpochPreState advances a post-block state to the pre-state of the target epoch.
func (st *StateTransition) PrepareEpochPreState(state *all.BeaconState, epoch phase0.Epoch, info *TransitionInfo) error {
	if state.Version < spec.DataVersionFulu {
		return nil
	}

	targetSlot := phase0.Slot(uint64(epoch) * st.specs.SlotsPerEpoch)
	if err := st.processSlots(state, targetSlot, info); err != nil {
		return fmt.Errorf("process_slots to epoch %d (slot %d): %w", epoch, targetSlot, err)
	}

	return nil
}

// TransitionInfo collects metadata from the state transition that callers may
// need for downstream processing. Pass a non-nil pointer to PrepareEpochPreState
// to receive this information; pass nil if not needed.
type TransitionInfo struct {
	// DelayedBuilderPayments is a list of slots that the state transition appended delayed builder payments for.
	// This tells the state simulator which slots to reference delayed builder payments in the BuilderPendingWithdrawals list.
	DelayedBuilderPayments []uint16
}

// processSlots advances the state from its current slot to targetSlot, applying
// epoch transitions at every epoch boundary crossed.
//
// Skips per-slot state/block root caching (process_slot) since we cannot compute
// hash_tree_root efficiently and the cached roots don't affect the epoch transition
// outputs we need. Jumps directly to each epoch boundary.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#process_slots
func (st *StateTransition) processSlots(state *all.BeaconState, targetSlot phase0.Slot, info *TransitionInfo) error {
	if state.Slot >= targetSlot {
		return nil
	}

	s, err := st.newAccessor(state)
	if err != nil {
		return fmt.Errorf("failed to create state accessor: %w", err)
	}

	slotsPerEpoch := st.specs.SlotsPerEpoch

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
	if s.Version >= spec.DataVersionGloas {
		delayedSlots := processBuilderPendingPayments(s)
		if info != nil {
			info.DelayedBuilderPayments = delayedSlots
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
	if s.Version >= spec.DataVersionGloas {
		processPtcWindow(s)
	}

	return nil
}
