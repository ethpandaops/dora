package services

import (
	"context"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/blockdb"
)

// GetSlotCommittees returns the attester committees for a slot (global validator
// indices, in committee order), from the in-memory epoch cache for unfinalized
// epochs or the blockdb duties store for finalized ones. Returns nil if unavailable.
func (bs *ChainService) GetSlotCommittees(ctx context.Context, slot phase0.Slot) [][]phase0.ValidatorIndex {
	chainState := bs.consensusPool.GetChainState()
	epoch := chainState.EpochOfSlot(slot)
	slotIndex := int(chainState.SlotToSlotIndex(slot))

	if epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
		// GetOrLoadValues restores duties from the unfinalized_duties table when
		// they have been pruned from memory but the epoch is not yet finalized
		// (and thus not yet in the blockdb).
		if values := epochStats.GetOrLoadValues(ctx, bs.beaconIndexer, true, false); values != nil && values.AttesterDuties != nil {
			if slotIndex < len(values.AttesterDuties) {
				src := values.AttesterDuties[slotIndex]
				committees := make([][]phase0.ValidatorIndex, len(src))
				for committeeIndex, committee := range src {
					members := make([]phase0.ValidatorIndex, len(committee))
					for k, activeIdx := range committee {
						if int(activeIdx) < len(values.ActiveIndices) {
							members[k] = values.ActiveIndices[activeIdx]
						}
					}
					committees[committeeIndex] = members
				}
				return committees
			}
		}
	}

	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsDuties() {
		return nil
	}

	firstSlot := uint64(chainState.EpochStartSlot(epoch))
	raw, err := blockdb.GlobalBlockDb.GetSlotCommittees(ctx, firstSlot, uint64(slot))
	if err != nil {
		bs.logger.Debugf("failed to load committees for slot %d from blockdb: %v", slot, err)
		return nil
	}
	if raw == nil {
		return nil
	}

	committees := make([][]phase0.ValidatorIndex, len(raw))
	for i, committee := range raw {
		committees[i] = toValidatorIndices(committee)
	}
	return committees
}

// GetSlotPtc returns the PTC members for a slot (global validator indices),
// from the in-memory epoch cache or the blockdb duties store. Returns nil if unavailable.
func (bs *ChainService) GetSlotPtc(ctx context.Context, slot phase0.Slot) []phase0.ValidatorIndex {
	chainState := bs.consensusPool.GetChainState()
	epoch := chainState.EpochOfSlot(slot)
	slotIndex := int(chainState.SlotToSlotIndex(slot))

	if epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
		if values := epochStats.GetOrLoadValues(ctx, bs.beaconIndexer, true, false); values != nil && values.PtcDuties != nil {
			if slotIndex < len(values.PtcDuties) && values.PtcDuties[slotIndex] != nil {
				src := values.PtcDuties[slotIndex]
				members := make([]phase0.ValidatorIndex, len(src))
				for k, activeIdx := range src {
					if int(activeIdx) < len(values.ActiveIndices) {
						members[k] = values.ActiveIndices[activeIdx]
					}
				}
				return members
			}
		}
	}

	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsDuties() {
		return nil
	}

	firstSlot := uint64(chainState.EpochStartSlot(epoch))
	raw, err := blockdb.GlobalBlockDb.GetSlotPtc(ctx, firstSlot, uint64(slot))
	if err != nil {
		bs.logger.Debugf("failed to load ptc for slot %d from blockdb: %v", slot, err)
		return nil
	}
	return toValidatorIndices(raw)
}

// toValidatorIndices converts raw uint64 indices to phase0.ValidatorIndex.
func toValidatorIndices(raw []uint64) []phase0.ValidatorIndex {
	if raw == nil {
		return nil
	}
	out := make([]phase0.ValidatorIndex, len(raw))
	for i, v := range raw {
		out[i] = phase0.ValidatorIndex(v)
	}
	return out
}
