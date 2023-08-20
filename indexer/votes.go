package indexer

import (
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
	ActivityMap map[uint64]bool
}

func aggregateEpochVotes(blockMap map[uint64][]*BlockInfo, epoch uint64, epochStats *EpochStats, targetRoot []byte, currentOnly bool) *EpochVotes {
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	if !currentOnly {
		// check next epoch, votes could be included there too
		lastSlot += utils.Config.Chain.Config.SlotsPerEpoch
	}

	/*
		epochStats.Validators.ValidatorsStatsMutex.RLock()
		defer epochStats.Validators.ValidatorsStatsMutex.RUnlock()

		votes := EpochVotes{
			ActivityMap: map[uint64]bool{},
		}
		votedBitsets := make(map[string][]byte)

		for slot := firstSlot; slot <= lastSlot; slot++ {
			blocks := blockMap[slot]
			if blocks == nil {
				continue
			}
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]

				if !block.Orphaned {
					isNextEpoch := utils.EpochOfSlot(slot) > epoch
					for _, att := range block.Block.Data.Message.Body.Attestations {
						if utils.EpochOfSlot(uint64(att.Data.Slot)) != epoch {
							continue
						}

						attKey := fmt.Sprintf("%v-%v", uint64(att.Data.Slot), uint64(att.Data.Index))
						voteAmount := uint64(0)
						voteBitset := att.AggregationBits
						votedBitset := votedBitsets[attKey]
						if epochStats.Assignments != nil {
							voteValidators := epochStats.Assignments.AttestorAssignments[attKey]
							for bitIdx, validatorIdx := range voteValidators {
								if votedBitset != nil && utils.BitAtVector(votedBitset, bitIdx) {
									// don't "double count" votes, if a attestation aggregation has been extended and re-included
									continue
								}
								if utils.BitAtVector(voteBitset, bitIdx) {
									voteAmount += uint64(epochStats.Validators.ValidatorBalances[validatorIdx])
									votes.ActivityMap[validatorIdx] = true
								}
							}
						}

						if votedBitset != nil {
							// merge bitsets
							for i := 0; i < len(votedBitset); i++ {
								votedBitset[i] |= voteBitset[i]
							}
						} else {
							votedBitset = make([]byte, len(voteBitset))
							copy(votedBitset, voteBitset)
							votedBitsets[attKey] = voteBitset
						}

						if bytes.Equal(att.Data.Target.Root, targetRoot) {
							if isNextEpoch {
								votes.nextEpoch.targetVoteAmount += voteAmount
							} else {
								votes.currentEpoch.targetVoteAmount += voteAmount
							}
						} else {
							//logger.Infof("vote target missmatch %v != 0x%x", att.Data.Target.Root, targetRoot)
						}
						if bytes.Equal(att.Data.BeaconBlockRoot, block.Header.Data.Header.Message.ParentRoot) {
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
		}

		return &votes
	*/
	return nil
}
