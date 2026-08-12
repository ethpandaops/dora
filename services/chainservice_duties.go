package services

import (
	"context"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/blockdb"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/indexer/beacon"
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

// GetEpochCommittees returns the attester committees for every slot of an epoch,
// indexed [slotIndex][committeeIndex] -> global validator indices in committee
// order. Unlike calling GetSlotCommittees per slot, it loads the epoch's duties
// exactly once (from the in-memory epoch cache, or a single blockdb read),
// avoiding a redundant blockdb/S3 fetch of the same duties object per slot.
// Returns nil if the duties are unavailable for the epoch.
func (bs *ChainService) GetEpochCommittees(ctx context.Context, epoch phase0.Epoch) [][][]phase0.ValidatorIndex {
	chainState := bs.consensusPool.GetChainState()

	if epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
		if values := epochStats.GetOrLoadValues(ctx, bs.beaconIndexer, true, false); values != nil && values.AttesterDuties != nil {
			out := make([][][]phase0.ValidatorIndex, len(values.AttesterDuties))
			for slotIndex, slotDuties := range values.AttesterDuties {
				committees := make([][]phase0.ValidatorIndex, len(slotDuties))
				for committeeIndex, committee := range slotDuties {
					members := make([]phase0.ValidatorIndex, len(committee))
					for k, activeIdx := range committee {
						if int(activeIdx) < len(values.ActiveIndices) {
							members[k] = values.ActiveIndices[activeIdx]
						}
					}
					committees[committeeIndex] = members
				}
				out[slotIndex] = committees
			}
			return out
		}
	}

	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsDuties() {
		return nil
	}

	firstSlot := uint64(chainState.EpochStartSlot(epoch))
	duties, err := blockdb.GlobalBlockDb.GetEpochDuties(ctx, firstSlot)
	if err != nil {
		bs.logger.Debugf("failed to load duties for epoch %d from blockdb: %v", epoch, err)
		return nil
	}
	if duties == nil {
		return nil
	}

	out := make([][][]phase0.ValidatorIndex, len(duties.Committees))
	for slotIndex, slotCommittees := range duties.Committees {
		committees := make([][]phase0.ValidatorIndex, len(slotCommittees))
		for committeeIndex, committee := range slotCommittees {
			committees[committeeIndex] = toValidatorIndices(committee)
		}
		out[slotIndex] = committees
	}
	return out
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

// committeesFromValues converts an epoch stats values' attester duties for a
// single slot into global validator index committees. Returns nil if the slot
// index is out of range.
func committeesFromValues(values *beacon.EpochStatsValues, slotIndex int) [][]phase0.ValidatorIndex {
	if slotIndex >= len(values.AttesterDuties) {
		return nil
	}
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

// findEpochStatsByDependentRoot returns the in-memory epoch stats for the given
// epoch whose committee-shuffling dependent root matches depRoot, or nil.
func (bs *ChainService) findEpochStatsByDependentRoot(epoch phase0.Epoch, depRoot phase0.Root) *beacon.EpochStats {
	for _, stats := range bs.beaconIndexer.GetEpochStatsByEpoch(epoch) {
		if stats.GetDependentRoot() == depRoot {
			return stats
		}
	}
	return nil
}

// GetSlotCommitteesForRoot returns the attester committees for a slot, resolved
// on the fork identified by depRoot. It uses the in-memory epoch stats matching
// depRoot when available, otherwise the diverging-fork blockdb object; it falls
// back to the canonical committees when depRoot is the canonical dependent root
// or no diverging object exists.
func (bs *ChainService) GetSlotCommitteesForRoot(ctx context.Context, slot phase0.Slot, depRoot phase0.Root) [][]phase0.ValidatorIndex {
	chainState := bs.consensusPool.GetChainState()
	epoch := chainState.EpochOfSlot(slot)
	slotIndex := int(chainState.SlotToSlotIndex(slot))

	if handle := bs.findEpochStatsByDependentRoot(epoch, depRoot); handle != nil {
		if values := handle.GetOrLoadValues(ctx, bs.beaconIndexer, true, false); values != nil && values.AttesterDuties != nil {
			if committees := committeesFromValues(values, slotIndex); committees != nil {
				return committees
			}
		}
	}

	if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsDuties() && depRoot != (phase0.Root{}) {
		firstSlot := uint64(chainState.EpochStartSlot(epoch))
		raw, err := blockdb.GlobalBlockDb.GetSlotCommitteesForRoot(ctx, firstSlot, uint64(slot), depRoot)
		if err != nil {
			bs.logger.Debugf("failed to load committees for slot %d (root %v) from blockdb: %v", slot, depRoot.String(), err)
		} else if raw != nil {
			committees := make([][]phase0.ValidatorIndex, len(raw))
			for i, committee := range raw {
				committees[i] = toValidatorIndices(committee)
			}
			return committees
		}
	}

	// Fall back to canonical committees (also covers depRoot == canonical root).
	return bs.GetSlotCommittees(ctx, slot)
}

// GetEpochCommitteesForRoot returns the attester committees for every slot of an
// epoch, resolved on the fork identified by depRoot. Falls back to the canonical
// committees when no diverging duties exist for depRoot.
func (bs *ChainService) GetEpochCommitteesForRoot(ctx context.Context, epoch phase0.Epoch, depRoot phase0.Root) [][][]phase0.ValidatorIndex {
	chainState := bs.consensusPool.GetChainState()

	if handle := bs.findEpochStatsByDependentRoot(epoch, depRoot); handle != nil {
		if values := handle.GetOrLoadValues(ctx, bs.beaconIndexer, true, false); values != nil && values.AttesterDuties != nil {
			out := make([][][]phase0.ValidatorIndex, len(values.AttesterDuties))
			for slotIndex := range values.AttesterDuties {
				out[slotIndex] = committeesFromValues(values, slotIndex)
			}
			return out
		}
	}

	if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsDuties() && depRoot != (phase0.Root{}) {
		firstSlot := uint64(chainState.EpochStartSlot(epoch))
		duties, err := blockdb.GlobalBlockDb.GetEpochDutiesForRoot(ctx, firstSlot, depRoot)
		if err != nil {
			bs.logger.Debugf("failed to load duties for epoch %d (root %v) from blockdb: %v", epoch, depRoot.String(), err)
		} else if duties != nil {
			out := make([][][]phase0.ValidatorIndex, len(duties.Committees))
			for slotIndex, slotCommittees := range duties.Committees {
				committees := make([][]phase0.ValidatorIndex, len(slotCommittees))
				for committeeIndex, committee := range slotCommittees {
					committees[committeeIndex] = toValidatorIndices(committee)
				}
				out[slotIndex] = committees
			}
			return out
		}
	}

	return bs.GetEpochCommittees(ctx, epoch)
}

// proposersFromValues copies a values' per-slot proposer indices, mapping the
// unknown sentinel (math.MaxInt64) to 0 to match the stored 0 sentinel.
func proposersFromValues(values *beacon.EpochStatsValues) []uint64 {
	out := make([]uint64, len(values.ProposerDuties))
	for i, idx := range values.ProposerDuties {
		if uint64(idx) > proposerMaxIndex {
			out[i] = 0
		} else {
			out[i] = uint64(idx)
		}
	}
	return out
}

// proposerMaxIndex is the largest validator index storable in the duties format
// (6-byte width); larger values are treated as "unknown" (0 sentinel).
const proposerMaxIndex = uint64(1)<<48 - 1

// GetEpochProposers returns the per-slot canonical proposers for an epoch (global
// validator indices; 0 where unknown). Returns nil if unavailable.
func (bs *ChainService) GetEpochProposers(ctx context.Context, epoch phase0.Epoch) []uint64 {
	chainState := bs.consensusPool.GetChainState()

	if epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
		if values := epochStats.GetOrLoadValues(ctx, bs.beaconIndexer, true, false); values != nil && len(values.ProposerDuties) > 0 {
			return proposersFromValues(values)
		}
	}

	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsDuties() {
		return nil
	}
	firstSlot := uint64(chainState.EpochStartSlot(epoch))
	duties, err := blockdb.GlobalBlockDb.GetEpochDuties(ctx, firstSlot)
	if err != nil || duties == nil {
		return nil
	}
	return duties.ProposerDuties
}

// GetEpochProposersForRoot returns the per-slot proposers for an epoch resolved
// on the fork identified by depRoot. Falls back to canonical when no diverging
// duties exist for depRoot.
func (bs *ChainService) GetEpochProposersForRoot(ctx context.Context, epoch phase0.Epoch, depRoot phase0.Root) []uint64 {
	chainState := bs.consensusPool.GetChainState()

	if handle := bs.findEpochStatsByDependentRoot(epoch, depRoot); handle != nil {
		if values := handle.GetOrLoadValues(ctx, bs.beaconIndexer, true, false); values != nil && len(values.ProposerDuties) > 0 {
			return proposersFromValues(values)
		}
	}

	if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsDuties() && depRoot != (phase0.Root{}) {
		firstSlot := uint64(chainState.EpochStartSlot(epoch))
		duties, err := blockdb.GlobalBlockDb.GetEpochDutiesForRoot(ctx, firstSlot, depRoot)
		if err != nil {
			bs.logger.Debugf("failed to load proposers for epoch %d (root %v) from blockdb: %v", epoch, depRoot.String(), err)
		} else if duties != nil {
			return duties.ProposerDuties
		}
	}

	return bs.GetEpochProposers(ctx, epoch)
}

// GetSlotPtcForRoot returns the PTC members for a slot, resolved on the fork
// identified by depRoot. Falls back to the canonical PTC when no diverging
// duties exist for depRoot.
func (bs *ChainService) GetSlotPtcForRoot(ctx context.Context, slot phase0.Slot, depRoot phase0.Root) []phase0.ValidatorIndex {
	chainState := bs.consensusPool.GetChainState()
	epoch := chainState.EpochOfSlot(slot)
	slotIndex := int(chainState.SlotToSlotIndex(slot))

	if handle := bs.findEpochStatsByDependentRoot(epoch, depRoot); handle != nil {
		if values := handle.GetOrLoadValues(ctx, bs.beaconIndexer, true, false); values != nil && values.PtcDuties != nil {
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

	if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsDuties() && depRoot != (phase0.Root{}) {
		firstSlot := uint64(chainState.EpochStartSlot(epoch))
		raw, err := blockdb.GlobalBlockDb.GetSlotPtcForRoot(ctx, firstSlot, uint64(slot), depRoot)
		if err != nil {
			bs.logger.Debugf("failed to load ptc for slot %d (root %v) from blockdb: %v", slot, depRoot.String(), err)
		} else if raw != nil {
			return toValidatorIndices(raw)
		}
	}

	return bs.GetSlotPtc(ctx, slot)
}

// ResolveDependentRoot returns the committee-shuffling dependent root for the
// attester duties of the given epoch on the fork that blockRoot sits on. The
// dependent root is the block root at the last slot before the epoch boundary
// reachable by walking blockRoot's parent chain.
//
// For unfinalized blocks the in-memory epoch stats already carry the answer. For
// finalized blocks it loads every block in [EpochStartSlot(N-1), slotOf(root)]
// in bulk from BOTH the in-memory cache (slots above the finalized cutoff) and a
// single ranged DB query (canonical + orphaned slots at/below the cutoff), then
// walks the parent chain in memory. Returns (root, true) on success.
func (bs *ChainService) ResolveDependentRoot(ctx context.Context, epoch phase0.Epoch, blockRoot phase0.Root) (phase0.Root, bool) {
	chainState := bs.consensusPool.GetChainState()

	// Fast path: unfinalized block — the epoch stats know the dependent root.
	if es := bs.beaconIndexer.GetEpochStatsByBlockRoot(epoch, blockRoot); es != nil {
		return es.GetDependentRoot(), true
	}

	if epoch == 0 {
		return phase0.Root{}, true
	}

	boundarySlot := chainState.EpochStartSlot(epoch)   // first slot of epoch N
	rangeStart := chainState.EpochStartSlot(epoch - 1) // first slot of epoch N-1

	// Determine the slot of blockRoot (finalized path: look it up in the DB).
	var upperSlot phase0.Slot
	if b := bs.beaconIndexer.GetBlockByRoot(blockRoot); b != nil {
		upperSlot = b.Slot
	} else if s := db.GetSlotByRoot(ctx, blockRoot[:]); s != nil {
		upperSlot = phase0.Slot(s.Slot)
	} else {
		// Unknown block — best effort: assume it is the last slot of the epoch.
		upperSlot = chainState.EpochStartSlot(epoch+1) - 1
	}

	blockMap := make(map[phase0.Root]blockRef)

	cutoff := chainState.GetFinalizedSlot()

	// In-memory portion: blocks above the finalized cutoff.
	if upperSlot > cutoff {
		memLow := rangeStart
		if memLow <= cutoff {
			memLow = cutoff + 1
		}
		for _, b := range bs.beaconIndexer.GetBlocksBySlotRange(memLow, upperSlot) {
			var parent phase0.Root
			if pr := b.GetParentRoot(); pr != nil {
				parent = *pr
			}
			blockMap[b.Root] = blockRef{slot: b.Slot, parent: parent}
		}
	}

	// DB portion: canonical + orphaned blocks at/below the cutoff, one ranged
	// query (orphaned finalized blocks are stored in the slots table too).
	dbUpper := upperSlot
	if dbUpper > cutoff {
		dbUpper = cutoff
	}
	if dbUpper >= rangeStart {
		for _, s := range db.GetSlotsRange(ctx, uint64(dbUpper), uint64(rangeStart), false, true) {
			if s.Block == nil || len(s.Block.Root) != 32 {
				continue
			}
			var root, parent phase0.Root
			copy(root[:], s.Block.Root)
			if len(s.Block.ParentRoot) == 32 {
				copy(parent[:], s.Block.ParentRoot)
			}
			blockMap[root] = blockRef{slot: phase0.Slot(s.Block.Slot), parent: parent}
		}
	}

	return walkDependentRoot(blockMap, blockRoot, boundarySlot)
}

// blockRef is a minimal (slot, parentRoot) reference used to walk a fork's
// parent chain in memory.
type blockRef struct {
	slot   phase0.Slot
	parent phase0.Root
}

// walkDependentRoot walks the parent chain from blockRoot down to the first
// block whose slot is below boundarySlot; that block's root is the committee-
// shuffling dependent root. It returns (zeroRoot, true) if it reaches genesis
// without crossing the boundary, and (zeroRoot, false) if the chain is broken
// (a parent is missing from blockMap) before the boundary is reached.
func walkDependentRoot(blockMap map[phase0.Root]blockRef, blockRoot phase0.Root, boundarySlot phase0.Slot) (phase0.Root, bool) {
	current := blockRoot
	for {
		ref, ok := blockMap[current]
		if !ok {
			return phase0.Root{}, false
		}
		if ref.slot < boundarySlot {
			return current, true
		}
		if ref.parent == (phase0.Root{}) {
			return phase0.Root{}, true
		}
		current = ref.parent
	}
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
