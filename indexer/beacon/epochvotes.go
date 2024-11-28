package beacon

import (
	"bytes"
	"encoding/binary"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/prysmaticlabs/go-bitfield"
)

// epochVotesKey is the primary key for EpochVotes entries in cache.
// consists of dependendRoot (32 byte), epoch (8 byte), highestRoot (32 byte) and blockCount/hasValues (1 byte).
type epochVotesKey [32 + 8 + 32 + 1]byte

// generate epochStatsKey from epoch and dependentRoot
func getEpochVotesKey(epoch phase0.Epoch, dependentRoot phase0.Root, highestRoot phase0.Root, blockCount uint8, hasValues bool) epochVotesKey {
	var key epochVotesKey

	copy(key[0:], dependentRoot[:])
	binary.LittleEndian.PutUint64(key[32:], uint64(epoch))
	copy(key[40:], highestRoot[:])
	key[72] = blockCount
	if hasValues {
		key[72] |= 0x80
	}

	return key
}

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
	TargetVotePercent float64
	HeadVotePercent   float64
	TotalVotePercent  float64
	AmountIsCount     bool
}

// EpochVoteActivity represents the aggregated activity for an epoch.
type EpochVoteActivity struct {
	Epoch            phase0.Epoch
	ActiveIndices    []phase0.ValidatorIndex
	ActivityBitfield bitfield.Bitfield
}

// aggregateEpochVotes aggregates the votes for an epoch based on the provided chain state, blocks, and epoch stats.
func (indexer *Indexer) aggregateEpochVotes(epoch phase0.Epoch, chainState *consensus.ChainState, blocks []*Block, epochStats *EpochStats) *EpochVotes {
	if len(blocks) == 0 {
		return &EpochVotes{}
	}

	var targetRoot phase0.Root
	if chainState.SlotToSlotIndex(blocks[0].Slot) == 0 {
		targetRoot = blocks[0].Root
	} else if parentRoot := blocks[0].GetParentRoot(); parentRoot != nil {
		targetRoot = *parentRoot
	}

	votesWithValues := epochStats != nil && epochStats.ready

	votesKey := getEpochVotesKey(epoch, targetRoot, blocks[len(blocks)-1].Root, uint8(len(blocks)), votesWithValues)
	if cachedVotes, isOk := indexer.epochCache.votesCache.Get(votesKey); isOk {
		indexer.epochCache.votesCacheHit++
		return cachedVotes
	}

	votes, _ := indexer.aggregateEpochVotesAndActivity(epoch, chainState, blocks, epochStats, targetRoot, votesKey)

	indexer.epochCache.votesCache.Add(votesKey, votes)
	indexer.epochCache.votesCacheMiss++

	return votes
}

func (indexer *Indexer) aggregateEpochVotesAndActivity(epoch phase0.Epoch, chainState *consensus.ChainState, blocks []*Block, epochStats *EpochStats, targetRoot phase0.Root, votesKey epochVotesKey) (*EpochVotes, *EpochVoteActivity) {
	t1 := time.Now()

	var epochStatsValues *EpochStatsValues
	if epochStats != nil {
		epochStatsValues = epochStats.GetOrLoadValues(indexer, true, false)
	}
	specs := chainState.GetSpecs()

	votes := &EpochVotes{
		AmountIsCount: epochStatsValues == nil,
	}

	var activity *EpochVoteActivity

	if epochStatsValues != nil {
		activity = &EpochVoteActivity{
			Epoch:            epoch,
			ActiveIndices:    epochStatsValues.ActiveIndices,
			ActivityBitfield: bitfield.NewBitlist(epochStatsValues.ActiveValidators),
		}
	}

	deduplicationMap := map[voteDeduplicationKey]bool{}

	for _, block := range blocks {
		blockBody := block.GetBlock()
		if blockBody == nil {
			continue
		}

		slot := block.Slot

		isNextEpoch := chainState.EpochOfSlot(slot) > epoch
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
			if chainState.EpochOfSlot(attData.Slot) != epoch {
				continue
			}

			attAggregationBits, err := attVersioned.AggregationBits()
			if err != nil {
				indexer.logger.Debugf("aggregateEpochVotes slot %v failed, can't get grregation bits for attestation %v: %v", slot, attIdx, err)
				continue
			}

			voteAmount := phase0.Gwei(0)
			slotIndex := chainState.SlotToSlotIndex(attData.Slot)

			if attVersioned.Version >= spec.DataVersionElectra {
				// EIP-7549 changes the attestation aggregation
				// there can now be attestations from all committees aggregated into a single attestation aggregate
				committeeBits, err := attVersioned.CommitteeBits()
				if err != nil {
					indexer.logger.Debugf("aggregateEpochVotes slot %v failed, can't get committeeBits for attestation %v: %v", slot, attIdx, err)
					continue
				}

				aggregationBitsOffset := uint64(0)
				aggregationBitsIndex := uint64(0)

				for _, committee := range committeeBits.BitIndices() {
					if uint64(committee) >= specs.MaxCommitteesPerSlot {
						continue
					}

					if epochStatsValues != nil {
						voteAmt, committeeSize := votes.aggregateVotes(epochStatsValues, slotIndex, uint64(committee), attAggregationBits, aggregationBitsOffset, activity)
						voteAmount += voteAmt
						aggregationBitsOffset += committeeSize
					} else {
						voteAmt := votes.aggregateVotesWithoutDuties(deduplicationMap, slotIndex, uint64(committee), attAggregationBits, committeeBits.Count(), aggregationBitsIndex)
						aggregationBitsIndex++
						voteAmount += voteAmt
					}
				}
			} else {
				// pre electra attestation aggregation
				if epochStatsValues != nil {
					voteAmt, _ := votes.aggregateVotes(epochStatsValues, slotIndex, uint64(attData.Index), attAggregationBits, 0, activity)
					voteAmount += voteAmt
				} else {
					voteAmt := votes.aggregateVotesWithoutDuties(deduplicationMap, slotIndex, uint64(attData.Index), attAggregationBits, 1, 0)
					voteAmount += voteAmt
				}
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

	if epochStatsValues != nil {
		votes.TargetVotePercent = float64(votes.CurrentEpoch.TargetVoteAmount+votes.NextEpoch.TargetVoteAmount) * 100 / float64(epochStatsValues.EffectiveBalance)
		votes.HeadVotePercent = float64(votes.CurrentEpoch.HeadVoteAmount+votes.NextEpoch.HeadVoteAmount) * 100 / float64(epochStatsValues.EffectiveBalance)
		votes.TotalVotePercent = float64(votes.CurrentEpoch.TotalVoteAmount+votes.NextEpoch.TotalVoteAmount) * 100 / float64(epochStatsValues.EffectiveBalance)
	}

	indexer.logger.Debugf("aggregated epoch %v votes in %v (blocks: %v) [0x%x]", epoch, time.Since(t1), len(blocks), votesKey[:])

	return votes, activity
}

// aggregateVotes aggregates the votes for a specific slot and committee based on the provided epoch statistics, aggregation bits, and offset.
func (votes *EpochVotes) aggregateVotes(epochStatsValues *EpochStatsValues, slotIndex phase0.Slot, committee uint64, aggregationBits bitfield.Bitfield, aggregationBitsOffset uint64, activity *EpochVoteActivity) (phase0.Gwei, uint64) {
	voteAmount := phase0.Gwei(0)

	voteDuties := epochStatsValues.AttesterDuties[slotIndex][committee]
	for bitIdx, validatorIndex := range voteDuties {
		if aggregationBits.BitAt(uint64(bitIdx) + aggregationBitsOffset) {

			if activity.ActivityBitfield.BitAt(uint64(validatorIndex)) {
				continue
			}

			effectiveBalance := epochStatsValues.EffectiveBalances[validatorIndex]
			voteAmount += phase0.Gwei(effectiveBalance) * EtherGweiFactor

			activity.ActivityBitfield.SetBitAt(uint64(validatorIndex), true)
		}
	}
	return voteAmount, uint64(len(voteDuties))
}

type voteDeduplicationKey [14]byte // slotIndex (8) + committee (2) + index (4) = 14 bytes

func getVoteDeduplicationKey(slotIndex phase0.Slot, committee uint16, index uint32) voteDeduplicationKey {
	key := voteDeduplicationKey{}
	binary.LittleEndian.PutUint64(key[:], uint64(slotIndex))
	binary.LittleEndian.PutUint16(key[8:], committee)
	binary.LittleEndian.PutUint32(key[10:], index)
	return key
}

func (votes *EpochVotes) aggregateVotesWithoutDuties(deduplicationMap map[voteDeduplicationKey]bool, slotIndex phase0.Slot, committee uint64, aggregationBits bitfield.Bitfield, splitAggregationBits uint64, splitAggregationIndex uint64) phase0.Gwei {
	voteAmount := phase0.Gwei(0)

	bitsLen := aggregationBits.Len() / splitAggregationBits
	aggregationBitsOffset := splitAggregationIndex * bitsLen
	if splitAggregationBits == splitAggregationIndex+1 {
		bitsLen = aggregationBits.Len() - (splitAggregationIndex * splitAggregationBits)
	}

	for bitIdx := uint64(0); bitIdx < bitsLen; bitIdx++ {
		if aggregationBits.BitAt(bitIdx + aggregationBitsOffset) {
			voteKey := getVoteDeduplicationKey(slotIndex, uint16(committee), uint32(bitIdx))
			if deduplicationMap[voteKey] {
				continue
			}

			voteAmount++
			deduplicationMap[voteKey] = true
		}
	}
	return voteAmount
}
