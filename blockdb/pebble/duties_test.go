package pebble

import (
	"context"
	"reflect"
	"testing"

	dtypes "github.com/ethpandaops/dora/types"

	"github.com/ethpandaops/dora/blockdb/types"
)

// buildDuties builds a small EpochDuties with deterministic committees for the
// given first slot and (optional) dependent root.
func buildDuties(firstSlot uint64, depRoot [32]byte) *types.EpochDuties {
	const slotsPerEpoch = 4
	committees := make([][][]uint64, slotsPerEpoch)
	next := uint64(0)
	for s := range committees {
		committee := make([]uint64, 3)
		for i := range committee {
			committee[i] = next
			next++
		}
		committees[s] = [][]uint64{committee}
	}
	return &types.EpochDuties{
		FirstSlot:         firstSlot,
		Epoch:             firstSlot / slotsPerEpoch,
		ValidatorCount:    next,
		SlotsPerEpoch:     slotsPerEpoch,
		CommitteesPerSlot: 1,
		DependentRoot:     depRoot,
		Committees:        committees,
	}
}

func TestDivergingDutiesStoreAndPrune(t *testing.T) {
	e := testEngine(t, dtypes.PebbleBlockDBConfig{})
	ctx := context.Background()

	const firstSlot = 4000
	var depRoot [32]byte
	for i := range depRoot {
		depRoot[i] = byte(0x10 + i)
	}

	canonical := buildDuties(firstSlot, [32]byte{})
	// Make the diverging committees differ so independence is observable.
	diverging := buildDuties(firstSlot, depRoot)
	diverging.Committees[1][0] = []uint64{777, 888, 999}

	if _, err := e.AddEpochDuties(ctx, canonical); err != nil {
		t.Fatalf("add canonical: %v", err)
	}
	if _, err := e.AddDivergingEpochDuties(ctx, diverging); err != nil {
		t.Fatalf("add diverging: %v", err)
	}

	// Read the diverging committees back by (firstSlot, slot, depRoot).
	got, err := e.GetSlotCommitteesForRoot(ctx, firstSlot, firstSlot+1, depRoot)
	if err != nil {
		t.Fatalf("get diverging slot committees: %v", err)
	}
	if !reflect.DeepEqual(got, [][]uint64{{777, 888, 999}}) {
		t.Fatalf("diverging committees mismatch: %v", got)
	}

	// The canonical object under the same firstSlot must be independent.
	canonGot, err := e.GetSlotCommittees(ctx, firstSlot, firstSlot+1)
	if err != nil {
		t.Fatalf("get canonical slot committees: %v", err)
	}
	if reflect.DeepEqual(canonGot, got) {
		t.Fatalf("canonical committees must differ from diverging")
	}

	// Full diverging duties round-trips with the dependent root preserved.
	full, err := e.GetEpochDutiesForRoot(ctx, firstSlot, depRoot)
	if err != nil || full == nil {
		t.Fatalf("get full diverging duties: %v", err)
	}
	if full.DependentRoot != depRoot {
		t.Fatalf("dependent root mismatch: %x", full.DependentRoot)
	}

	// Prune removes both canonical and diverging objects for the epoch.
	if _, err := e.PruneEpochDutiesBefore(ctx, firstSlot+1); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if c, _ := e.GetSlotCommittees(ctx, firstSlot, firstSlot+1); c != nil {
		t.Fatalf("canonical committees should be pruned")
	}
	if c, _ := e.GetSlotCommitteesForRoot(ctx, firstSlot, firstSlot+1, depRoot); c != nil {
		t.Fatalf("diverging committees should be pruned")
	}
}
