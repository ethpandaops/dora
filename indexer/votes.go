package indexer

import (
	"bytes"
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
	VoteCounts  bool
	ActivityMap map[uint64]bool
}

func aggregateEpochVotes(blockMap map[uint64]*CacheBlock, epoch uint64, epochStats *EpochStats, targetRoot []byte, currentOnly bool, awaitValidaotStats bool) *EpochVotes {
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	if !currentOnly {
		// check next epoch, votes could be included there too
		lastSlot += utils.Config.Chain.Config.SlotsPerEpoch
	}

	// await all lazy loaded data is available
	epochStats.dutiesMutex.RLock()
	defer epochStats.dutiesMutex.RUnlock()
	if awaitValidaotStats {
		epochStats.validatorsMutex.RLock()
		defer epochStats.validatorsMutex.RUnlock()
	}

	votes := EpochVotes{
		ActivityMap: map[uint64]bool{},
		VoteCounts:  epochStats.validatorStats == nil,
	}

	for slot := firstSlot; slot <= lastSlot; slot++ {
		block := blockMap[slot]
		if block == nil {
			continue
		}

		blockBody := block.GetBlockBody()
		if blockBody == nil {
			continue
		}

		isNextEpoch := utils.EpochOfSlot(slot) > epoch
		for _, att := range blockBody.Message.Body.Attestations {
			if utils.EpochOfSlot(uint64(att.Data.Slot)) != epoch {
				continue
			}

			attKey := fmt.Sprintf("%v-%v", uint64(att.Data.Slot), uint64(att.Data.Index))
			voteAmount := uint64(0)
			voteBitset := att.AggregationBits
			if epochStats.attestorAssignments != nil {
				voteValidators := epochStats.attestorAssignments[attKey]
				for bitIdx, validatorIdx := range voteValidators {
					if utils.BitAtVector(voteBitset, bitIdx) {
						if votes.ActivityMap[validatorIdx] {
							continue
						}
						if epochStats.validatorStats != nil {
							voteAmount += uint64(epochStats.validatorStats.ValidatorBalances[validatorIdx])
						} else {
							voteAmount += 1
						}
						votes.ActivityMap[validatorIdx] = true
					}
				}
			}

			if bytes.Equal(att.Data.Target.Root, targetRoot) {
				if isNextEpoch {
					votes.nextEpoch.targetVoteAmount += voteAmount
				} else {
					votes.currentEpoch.targetVoteAmount += voteAmount
				}
			} /*else {
				logger.Infof("vote target missmatch %v != 0x%x", att.Data.Target.Root, targetRoot)
			}*/
			if bytes.Equal(att.Data.BeaconBlockRoot, block.GetParentRoot()) {
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

	return &votes
}
