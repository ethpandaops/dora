package indexer

import (
	"bytes"
	"fmt"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/ethpandaops/dora/utils"
	"github.com/prysmaticlabs/go-bitfield"
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

func aggregateEpochVotes(blockMap map[uint64]*CacheBlock, epoch uint64, epochStats *EpochStats, targetRoot []byte, currentOnly bool, awaitDutiesLoaded bool) *EpochVotes {
	t1 := time.Now()

	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	if !currentOnly {
		// check next epoch, votes could be included there too
		lastSlot += utils.Config.Chain.Config.SlotsPerEpoch
	}

	// await all lazy loaded data is available
	if awaitDutiesLoaded {
		epochStats.dutiesMutex.RLock()
		defer epochStats.dutiesMutex.RUnlock()
		epochStats.stateStatsMutex.RLock()
		defer epochStats.stateStatsMutex.RUnlock()
	}

	votes := EpochVotes{
		ActivityMap: map[uint64]bool{},
		VoteCounts:  epochStats.stateStats == nil,
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
		attestations, err := blockBody.Attestations()
		if err != nil {
			continue
		}
		for attIdx, attVersioned := range attestations {
			attData, err := attVersioned.Data()
			if err != nil {
				logger.Debugf("aggregateEpochVotes slot %v failed, can't get data for attestation %v: %v", slot, attIdx, err)
				continue
			}
			if utils.EpochOfSlot(uint64(attData.Slot)) != epoch {
				continue
			}

			attAggregationBits, err := attVersioned.AggregationBits()
			if err != nil {
				continue
			}

			voteAmount := uint64(0)
			if epochStats.attestorAssignments != nil {
				if attVersioned.Version >= spec.DataVersionElectra {
					// EIP-7549 changes the attestation aggregation
					// there can now be attestations from all committees aggregated into a single attestation aggregate
					committeeBits, err := attVersioned.CommitteeBits()
					if err != nil {
						logger.Debugf("aggregateEpochVotes slot %v failed, can't get committeeBits for attestation %v: %v", slot, attIdx, err)
						continue
					}

					aggregationBitsOffset := uint64(0)

					for _, committee := range committeeBits.BitIndices() {
						if uint64(committee) >= utils.Config.Chain.Config.MaxCommitteesPerSlot {
							continue
						}
						voteAmt, committeeSize := aggregateAttestationVotes(&votes, epochStats, uint64(attData.Slot), uint64(committee), attAggregationBits, aggregationBitsOffset)
						voteAmount += voteAmt
						aggregationBitsOffset += committeeSize
					}
				} else {
					// pre electra attestation aggregation
					voteAmt, _ := aggregateAttestationVotes(&votes, epochStats, uint64(attData.Slot), uint64(attData.Index), attAggregationBits, 0)
					voteAmount += voteAmt
				}
			}

			if bytes.Equal(attData.Target.Root[:], targetRoot) {
				if isNextEpoch {
					votes.nextEpoch.targetVoteAmount += voteAmount
				} else {
					votes.currentEpoch.targetVoteAmount += voteAmount
				}
			} /*else {
				logger.Infof("vote target missmatch %v != 0x%x", attData.Target.Root, targetRoot)
			}*/
			if bytes.Equal(attData.BeaconBlockRoot[:], block.GetParentRoot()) {
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

	logger.Debugf("aggregated epoch %v votes in %v", epoch, time.Since(t1))
	return &votes
}

func aggregateAttestationVotes(votes *EpochVotes, epochStats *EpochStats, slot uint64, committee uint64, aggregationBits bitfield.Bitfield, aggregationBitsOffset uint64) (uint64, uint64) {
	voteAmount := uint64(0)
	attKey := fmt.Sprintf("%v-%v", slot, committee)
	voteValidators := epochStats.attestorAssignments[attKey]
	for bitIdx, validatorIdx := range voteValidators {
		if aggregationBits.BitAt(uint64(bitIdx) + aggregationBitsOffset) {
			if votes.ActivityMap[validatorIdx] {
				continue
			}
			if epochStats.stateStats != nil {
				voteAmount += uint64(epochStats.stateStats.ValidatorBalances[validatorIdx])
			} else {
				voteAmount += 1
			}
			votes.ActivityMap[validatorIdx] = true
		}
	}
	return voteAmount, uint64(len(voteValidators))
}
