package beacon

import (
	"context"
	"fmt"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/indexer/beacon/duties"
	"github.com/ethpandaops/dora/utils"
)

// BuildEpochDuties resolves the attester committee and PTC duty mappings of an
// epoch into the engine-agnostic EpochDuties structure (global validator
// indices). Returns nil if the values hold no attester duties (nothing to store).
func BuildEpochDuties(specs *consensus.ChainSpec, epoch phase0.Epoch, values *EpochStatsValues) *btypes.EpochDuties {
	if values == nil || values.AttesterDuties == nil {
		return nil
	}

	slotsPerEpoch := specs.SlotsPerEpoch
	validatorCount := values.ActiveValidators
	committeesPerSlot := duties.SlotCommitteeCount(specs, validatorCount)

	resolve := func(activeIdx duties.ActiveIndiceIndex) uint64 {
		if int(activeIdx) < len(values.ActiveIndices) {
			return uint64(values.ActiveIndices[activeIdx])
		}
		return 0
	}

	// Proposer duties: one global validator index per slot. Values already hold
	// global indices (math.MaxInt64 for unknown); the encoder stores any
	// out-of-range value as the 0 sentinel.
	var proposerDuties []uint64
	if len(values.ProposerDuties) > 0 {
		proposerDuties = make([]uint64, slotsPerEpoch)
		for slotIndex := range slotsPerEpoch {
			if int(slotIndex) < len(values.ProposerDuties) {
				proposerDuties[slotIndex] = uint64(values.ProposerDuties[slotIndex])
			}
		}
	}

	committees := make([][][]uint64, slotsPerEpoch)
	for slotIndex := range slotsPerEpoch {
		if int(slotIndex) >= len(values.AttesterDuties) {
			committees[slotIndex] = make([][]uint64, 0)
			continue
		}

		srcSlot := values.AttesterDuties[slotIndex]
		slotCommittees := make([][]uint64, len(srcSlot))
		for committeeIndex, committee := range srcSlot {
			members := make([]uint64, len(committee))
			for k, activeIdx := range committee {
				members[k] = resolve(activeIdx)
			}
			slotCommittees[committeeIndex] = members
		}
		committees[slotIndex] = slotCommittees
	}

	// PTC duties are only present for Gloas+ epochs. Encode them only when every
	// slot has a full PtcSize-length set, otherwise drop the section entirely.
	ptcSize := specs.PtcSize
	var ptc [][]uint64
	if ptcSize > 0 && values.PtcDuties != nil {
		ptc = make([][]uint64, slotsPerEpoch)
		complete := true
		for slotIndex := range slotsPerEpoch {
			if int(slotIndex) >= len(values.PtcDuties) || uint64(len(values.PtcDuties[slotIndex])) != ptcSize {
				complete = false
				break
			}
			src := values.PtcDuties[slotIndex]
			members := make([]uint64, len(src))
			for k, activeIdx := range src {
				members[k] = resolve(activeIdx)
			}
			ptc[slotIndex] = members
		}
		if !complete {
			ptcSize = 0
			ptc = nil
		}
	} else {
		ptcSize = 0
	}

	return &btypes.EpochDuties{
		FirstSlot:         uint64(epoch) * slotsPerEpoch,
		Epoch:             uint64(epoch),
		ValidatorCount:    validatorCount,
		SlotsPerEpoch:     slotsPerEpoch,
		CommitteesPerSlot: committeesPerSlot,
		PtcSize:           ptcSize,
		Committees:        committees,
		ProposerDuties:    proposerDuties,
		Ptc:               ptc,
	}
}

// writeEpochDutiesToBlockDb serializes and stores the per-epoch duties object,
// returning the stored size. It is best-effort and must never block finalization
// or synchronization.
func (indexer *Indexer) writeEpochDutiesToBlockDb(ctx context.Context, epoch phase0.Epoch, canonicalDepRoot phase0.Root, values *EpochStatsValues) (int64, error) {
	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsDuties() {
		return 0, nil
	}
	if utils.Config != nil && utils.Config.Indexer.DisableBlockDBDuties {
		return 0, nil
	}

	specs := indexer.consensusPool.GetChainState().GetSpecs()
	if specs == nil {
		return 0, nil
	}

	epochDuties := BuildEpochDuties(specs, epoch, values)
	if epochDuties == nil {
		return 0, nil
	}
	// Carry the canonical dependent root so the object is written as v2 (with the
	// proposer section). Keying is unchanged (canonical is keyed by firstSlot).
	epochDuties.DependentRoot = canonicalDepRoot

	size, err := blockdb.GlobalBlockDb.AddEpochDuties(ctx, epochDuties)
	if err != nil {
		return 0, fmt.Errorf("failed to store epoch duties: %w", err)
	}

	return size, nil
}

// buildDivergingEpochDuties resolves the diverging-fork duties for an epoch. It
// walks the orphaned blocks, groups them by their fork's committee-shuffling
// dependent root, and builds one fully-materialized EpochDuties object per
// distinct diverging root (skipping the canonical root and the zero root).
//
// It MUST be called before the finalization persist-tx deletes the
// unfinalized_duties rows: GetOrLoadValues may restore a fork's values from that
// table. The returned objects are engine-agnostic and independent of the SQL
// rows, so they can be written later (in the blockdb WaitGroup) safely.
func (indexer *Indexer) buildDivergingEpochDuties(ctx context.Context, epoch phase0.Epoch, canonicalDepRoot phase0.Root, orphanedBlocks []*Block) []*btypes.EpochDuties {
	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsDuties() {
		return nil
	}
	if utils.Config != nil && utils.Config.Indexer.DisableBlockDBDuties {
		return nil
	}

	specs := indexer.consensusPool.GetChainState().GetSpecs()
	if specs == nil {
		return nil
	}

	seen := make(map[phase0.Root]bool, len(orphanedBlocks))
	result := make([]*btypes.EpochDuties, 0, len(orphanedBlocks))
	for _, block := range orphanedBlocks {
		es := indexer.GetEpochStatsByBlockRoot(epoch, block.Root)
		if es == nil {
			continue
		}

		depRoot := es.GetDependentRoot()
		if depRoot == canonicalDepRoot || depRoot == (phase0.Root{}) {
			continue
		}
		if seen[depRoot] {
			continue
		}
		seen[depRoot] = true

		values := es.GetOrLoadValues(ctx, indexer, false, false)
		if values == nil {
			indexer.logger.Warnf("could not load epoch stats values for diverging fork %v (epoch %v)", depRoot.String(), epoch)
			continue
		}

		d := BuildEpochDuties(specs, epoch, values)
		if d == nil {
			continue
		}
		d.DependentRoot = depRoot
		result = append(result, d)
	}

	return result
}

// writeDivergingEpochDutiesToBlockDb stores the pre-built diverging-fork duties
// objects. It is best-effort: a per-fork failure is logged and skipped, and it
// must never block finalization.
func (indexer *Indexer) writeDivergingEpochDutiesToBlockDb(ctx context.Context, divergingDuties []*btypes.EpochDuties) {
	for _, d := range divergingDuties {
		if _, err := blockdb.GlobalBlockDb.AddDivergingEpochDuties(ctx, d); err != nil {
			indexer.logger.Errorf("error writing diverging duties for epoch %v (%x) to blockdb: %v", d.Epoch, d.DependentRoot, err)
		}
	}
}
