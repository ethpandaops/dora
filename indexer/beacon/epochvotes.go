package beacon

import (
	"bytes"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/prysmaticlabs/go-bitfield"
)

// EpochVotes represents the aggregated votes for an epoch.
type EpochVotes struct {
	CurrentEpoch struct {
		TargetVoteAmount phase0.Gwei
		HeadVoteAmount   phase0.Gwei
		TotalVoteAmount  phase0.Gwei
	}
	NextEpoch struct {
		TargetVoteAmount phase0.Gwei
		HeadVoteAmount   phase0.Gwei
		TotalVoteAmount  phase0.Gwei
	}
	ActivityMap map[phase0.ValidatorIndex]bool
}

// aggregateEpochVotes aggregates the votes for an epoch based on the provided chain state, blocks, and epoch stats.
func (indexer *Indexer) aggregateEpochVotes(chainState *consensus.ChainState, blocks []*Block, epochStats *EpochStats) *EpochVotes {
	t1 := time.Now()

	epochStatsValues := epochStats.values
	specs := chainState.GetSpecs()

	votes := &EpochVotes{
		ActivityMap: map[phase0.ValidatorIndex]bool{},
	}

	if len(blocks) == 0 || epochStatsValues == nil {
		return votes
	}

	var targetRoot phase0.Root
	if chainState.SlotToSlotIndex(blocks[0].Slot) == 0 {
		targetRoot = blocks[0].Root
	} else if parentRoot := blocks[0].GetParentRoot(); parentRoot != nil {
		targetRoot = *parentRoot
	}

	for _, block := range blocks {
		blockBody := block.GetBlock()
		if blockBody == nil {
			continue
		}

		slot := block.Slot

		isNextEpoch := chainState.EpochOfSlot(slot) > epochStats.epoch
		attestations, err := blockBody.Attestations()
		if err != nil {
			continue
		}
		for attIdx, attVersioned := range attestations {
			attData, err := attVersioned.Data()
			if err != nil {
				indexer.logger.Debugf("aggregateEpochVotes slot %v failed, can't get data for attestation %v: %v", slot, attIdx, err)
				continue
			}
			if chainState.EpochOfSlot(attData.Slot) != epochStats.epoch {
				continue
			}

			attAggregationBits, err := attVersioned.AggregationBits()
			if err != nil {
				continue
			}

			voteAmount := phase0.Gwei(0)
			slotIndex := chainState.SlotToSlotIndex(slot)

			if attVersioned.Version >= spec.DataVersionElectra {
				// EIP-7549 changes the attestation aggregation
				// there can now be attestations from all committees aggregated into a single attestation aggregate
				committeeBits, err := attVersioned.CommitteeBits()
				if err != nil {
					indexer.logger.Debugf("aggregateEpochVotes slot %v failed, can't get committeeBits for attestation %v: %v", slot, attIdx, err)
					continue
				}

				aggregationBitsOffset := uint64(0)

				for _, committee := range committeeBits.BitIndices() {
					if uint64(committee) >= specs.MaxCommitteesPerSlot {
						continue
					}
					voteAmt, committeeSize := votes.aggregateVotes(epochStats, slotIndex, uint64(committee), attAggregationBits, aggregationBitsOffset)
					voteAmount += voteAmt
					aggregationBitsOffset += committeeSize
				}
			} else {
				// pre electra attestation aggregation
				voteAmt, _ := votes.aggregateVotes(epochStats, slotIndex, uint64(attData.Index), attAggregationBits, 0)
				voteAmount += voteAmt
			}

			if bytes.Equal(attData.Target.Root[:], targetRoot[:]) {
				if isNextEpoch {
					votes.NextEpoch.TargetVoteAmount += voteAmount
				} else {
					votes.CurrentEpoch.TargetVoteAmount += voteAmount
				}
			} /*else {
				indexer.logger.Infof("vote target missmatch %v != 0x%x", attData.Target.Root, targetRoot)
			}*/
			parentRoot := block.GetParentRoot()

			if parentRoot != nil && bytes.Equal(attData.BeaconBlockRoot[:], parentRoot[:]) {
				if isNextEpoch {
					votes.NextEpoch.HeadVoteAmount += voteAmount
				} else {
					votes.CurrentEpoch.HeadVoteAmount += voteAmount
				}
			}
			if isNextEpoch {
				votes.NextEpoch.TotalVoteAmount += voteAmount
			} else {
				votes.CurrentEpoch.TotalVoteAmount += voteAmount
			}
		}
	}

	indexer.logger.Debugf("aggregated epoch %v votes in %v (blocks: %v, dependent: %v)", epochStats.epoch, time.Since(t1), len(blocks), epochStats.dependentRoot)
	return votes
}

// aggregateVotes aggregates the votes for a specific slot and committee based on the provided epoch statistics, aggregation bits, and offset.
func (votes *EpochVotes) aggregateVotes(epochStats *EpochStats, slot phase0.Slot, committee uint64, aggregationBits bitfield.Bitfield, aggregationBitsOffset uint64) (phase0.Gwei, uint64) {
	voteAmount := phase0.Gwei(0)

	voteDuties := epochStats.values.AttesterDuties[slot][committee]
	for bitIdx, validatorIndex := range voteDuties {
		if aggregationBits.BitAt(uint64(bitIdx) + aggregationBitsOffset) {
			if votes.ActivityMap[validatorIndex] {
				continue
			}

			effectiveBalance := epochStats.values.EffectiveBalances[validatorIndex]
			voteAmount += effectiveBalance
			votes.ActivityMap[validatorIndex] = true
		}
	}
	return voteAmount, uint64(len(voteDuties))
}
