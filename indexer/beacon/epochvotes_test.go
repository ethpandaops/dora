package beacon

import (
	"testing"

	"github.com/ethpandaops/dora/indexer/beacon/duties"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/prysmaticlabs/go-bitfield"
)

// TestAggregateVotes_SlashedExcludedFromTarget verifies that slashed validators contribute to the
// total vote amount but are reported separately so their weight can be dropped from the FFG target,
// mirroring the consensus spec's get_unslashed_participating_indices.
func TestAggregateVotes_SlashedExcludedFromTarget(t *testing.T) {
	const effBalanceEth = uint32(32)

	epochStatsValues := &EpochStatsValues{
		ActiveValidators:  4,
		ActiveIndices:     []phase0.ValidatorIndex{10, 11, 12, 13},
		EffectiveBalances: []uint32{effBalanceEth, effBalanceEth, effBalanceEth, effBalanceEth},
		AttesterDuties: [][][]duties.ActiveIndiceIndex{
			{ // slot 0
				{0, 1, 2, 3}, // committee 0 -> active-indice positions
			},
		},
	}

	// active-indice positions 1 and 2 are slashed
	slashedSet := map[duties.ActiveIndiceIndex]bool{1: true, 2: true}

	// all four committee members attested
	aggregationBits := bitfield.NewBitlist(4)
	for i := uint64(0); i < 4; i++ {
		aggregationBits.SetBitAt(i, true)
	}

	activityBitlist := bitfield.NewBitlist(epochStatsValues.ActiveValidators)
	votes := &EpochVotes{}

	voteAmount, slashedVoteAmount, committeeSize := votes.aggregateVotes(
		epochStatsValues, 0, 0, aggregationBits, 0, &activityBitlist, slashedSet, func(phase0.ValidatorIndex) {},
	)

	wantTotal := phase0.Gwei(4*effBalanceEth) * EtherGweiFactor
	wantSlashed := phase0.Gwei(2*effBalanceEth) * EtherGweiFactor
	wantTarget := wantTotal - wantSlashed

	if committeeSize != 4 {
		t.Errorf("committeeSize = %d, want 4", committeeSize)
	}
	if voteAmount != wantTotal {
		t.Errorf("voteAmount = %d, want %d", voteAmount, wantTotal)
	}
	if slashedVoteAmount != wantSlashed {
		t.Errorf("slashedVoteAmount = %d, want %d", slashedVoteAmount, wantSlashed)
	}
	if got := voteAmount - slashedVoteAmount; got != wantTarget {
		t.Errorf("target amount = %d, want %d (only the 2 unslashed validators)", got, wantTarget)
	}
}

// TestAggregateVotes_NilSlashedSet ensures a nil slashed set (the common case) counts every vote
// towards the target with no exclusions and does not panic on lookup.
func TestAggregateVotes_NilSlashedSet(t *testing.T) {
	epochStatsValues := &EpochStatsValues{
		ActiveValidators:  2,
		ActiveIndices:     []phase0.ValidatorIndex{0, 1},
		EffectiveBalances: []uint32{32, 32},
		AttesterDuties: [][][]duties.ActiveIndiceIndex{
			{{0, 1}},
		},
	}

	aggregationBits := bitfield.NewBitlist(2)
	aggregationBits.SetBitAt(0, true)
	aggregationBits.SetBitAt(1, true)

	activityBitlist := bitfield.NewBitlist(epochStatsValues.ActiveValidators)
	votes := &EpochVotes{}

	voteAmount, slashedVoteAmount, _ := votes.aggregateVotes(
		epochStatsValues, 0, 0, aggregationBits, 0, &activityBitlist, nil, func(phase0.ValidatorIndex) {},
	)

	if slashedVoteAmount != 0 {
		t.Errorf("slashedVoteAmount = %d, want 0 for nil slashed set", slashedVoteAmount)
	}
	if want := phase0.Gwei(64) * EtherGweiFactor; voteAmount != want {
		t.Errorf("voteAmount = %d, want %d", voteAmount, want)
	}
}

// TestEpochStatsPacked_SlashedIndicesRoundTrip verifies the slashed-indice positions survive the
// SSZ pack/unpack cycle used to persist unfinalized epoch duties.
func TestEpochStatsPacked_SlashedIndicesRoundTrip(t *testing.T) {
	es := &EpochStats{
		epoch: 5,
		values: &EpochStatsValues{
			ActiveValidators:  4,
			ActiveIndices:     []phase0.ValidatorIndex{10, 11, 12, 13},
			EffectiveBalances: []uint32{32, 32, 32, 32},
			SlashedIndices:    []duties.ActiveIndiceIndex{1, 3},
		},
	}

	packed, err := es.buildPackedSSZ()
	if err != nil {
		t.Fatalf("buildPackedSSZ: %v", err)
	}

	restored := &EpochStats{epoch: 5}
	values, err := restored.parsePackedSSZ(nil, packed, false)
	if err != nil {
		t.Fatalf("parsePackedSSZ: %v", err)
	}

	if len(values.SlashedIndices) != 2 || values.SlashedIndices[0] != 1 || values.SlashedIndices[1] != 3 {
		t.Errorf("SlashedIndices = %v, want [1 3]", values.SlashedIndices)
	}
}
