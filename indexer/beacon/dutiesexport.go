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
		Ptc:               ptc,
	}
}

// writeEpochDutiesToBlockDb serializes and stores the per-epoch duties object.
// It is best-effort and must never block finalization or synchronization.
func (indexer *Indexer) writeEpochDutiesToBlockDb(ctx context.Context, epoch phase0.Epoch, values *EpochStatsValues) error {
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

	epochDuties := BuildEpochDuties(specs, epoch, values)
	if epochDuties == nil {
		return nil
	}

	if _, err := blockdb.GlobalBlockDb.AddEpochDuties(ctx, epochDuties); err != nil {
		return fmt.Errorf("failed to store epoch duties: %w", err)
	}

	return nil
}
