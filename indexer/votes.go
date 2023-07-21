package indexer

import (
	"fmt"

	"github.com/pk910/light-beaconchain-explorer/utils"
)

type EpochVotes struct {
	currentEpoch struct {
		targetVoteAmount uint64
		headVoteAmount   uint64
		totalVoteAmount  uint64
	}
	nextEpoch struct {
		targetVoteAmount uint64
		headVoteAmount   uint64
		totalVoteAmount  uint64
	}
}

func aggregateEpochVotes(blockMap map[uint64]*BlockInfo, epoch uint64, epochStats *EpochStats, targetRoot string, currentOnly bool) *EpochVotes {
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	if !currentOnly {
		// check next epoch, votes could be included there too
		lastSlot += utils.Config.Chain.Config.SlotsPerEpoch
	}

	votes := EpochVotes{}
	votedBitsets := make(map[string][]byte)

	for slot := firstSlot; slot <= lastSlot; slot++ {
		block := blockMap[slot]
		if block != nil && !block.orphaned {
			isNextEpoch := utils.EpochOfSlot(slot) > epoch
			for _, att := range block.block.Data.Message.Body.Attestations {
				if utils.EpochOfSlot(uint64(att.Data.Slot)) != epoch {
					continue
				}

				attKey := fmt.Sprintf("%v-%v", uint64(att.Data.Slot), uint64(att.Data.Index))
				voteAmount := uint64(0)
				voteBitset := utils.MustParseHex(att.AggregationBits)
				votedBitset := votedBitsets[attKey]
				votedBitsets[attKey] = voteBitset

				voteValidators := epochStats.assignments.AttestorAssignments[attKey]
				for bitIdx, validatorIdx := range voteValidators {
					if votedBitset != nil && utils.BitAtVector(votedBitset, bitIdx) {
						// don't "double count" votes, if a attestation aggregation has been extended and re-included
						continue
					}
					if utils.BitAtVector(voteBitset, bitIdx) {
						validator := epochStats.validators[validatorIdx]
						if validator != nil {
							voteAmount += uint64(validator.EffectiveBalance)
						}
					}
				}

				if att.Data.Target.Root == targetRoot {
					if isNextEpoch {
						votes.nextEpoch.targetVoteAmount += voteAmount
					} else {
						votes.currentEpoch.targetVoteAmount += voteAmount
					}
				}
				if att.Data.BeaconBlockRoot == block.header.Data.Header.Message.ParentRoot {
					if isNextEpoch {
						votes.nextEpoch.headVoteAmount += voteAmount
					} else {
						votes.currentEpoch.headVoteAmount += voteAmount
					}
				}
				if isNextEpoch {
					votes.nextEpoch.totalVoteAmount += voteAmount
				} else {
					votes.currentEpoch.totalVoteAmount += voteAmount
				}
			}
		}
	}

	return &votes
}
