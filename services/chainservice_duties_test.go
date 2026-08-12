package services

import (
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// r builds a deterministic phase0.Root from a single byte tag.
func r(tag byte) phase0.Root {
	var root phase0.Root
	root[0] = tag
	return root
}

func TestWalkDependentRoot(t *testing.T) {
	// Chain layout (slots per epoch = 4, epoch N = 2, boundary = slot 8):
	//   epoch N-1: A(slot 4) -> B(slot 7)   [B is the last block of epoch N-1]
	//   epoch N:   B -> C(slot 8) -> D(slot 9)
	// A canonical fork continues from B; a diverging fork forks off earlier at A.
	const boundarySlot = phase0.Slot(8)

	base := map[phase0.Root]blockRef{
		r('A'): {slot: 4, parent: phase0.Root{}}, // genesis-ish anchor for this test
		r('B'): {slot: 7, parent: r('A')},
		r('C'): {slot: 8, parent: r('B')},
		r('D'): {slot: 9, parent: r('C')},
	}

	tests := []struct {
		name     string
		blockMap map[phase0.Root]blockRef
		start    phase0.Root
		wantRoot phase0.Root
		wantOK   bool
	}{
		{
			name:     "canonical fork diverges after boundary",
			blockMap: base,
			start:    r('D'),
			wantRoot: r('B'), // last block below slot 8 on D's chain
			wantOK:   true,
		},
		{
			name: "diverging fork with different dependent root",
			// Diverging fork: E(slot 7) is an alternative last block of epoch N-1
			// (sibling of B, both children of A), F(slot 9) builds on E in epoch N.
			blockMap: map[phase0.Root]blockRef{
				r('A'): {slot: 4, parent: phase0.Root{}},
				r('B'): {slot: 7, parent: r('A')},
				r('E'): {slot: 7, parent: r('A')},
				r('F'): {slot: 9, parent: r('E')},
			},
			start:    r('F'),
			wantRoot: r('E'), // diverging dependent root differs from canonical B
			wantOK:   true,
		},
		{
			name:     "start already below boundary",
			blockMap: base,
			start:    r('B'),
			wantRoot: r('B'),
			wantOK:   true,
		},
		{
			name:     "chain broken before boundary",
			blockMap: map[phase0.Root]blockRef{r('C'): {slot: 8, parent: r('X')}},
			start:    r('C'),
			wantRoot: phase0.Root{},
			wantOK:   false,
		},
		{
			name:     "reaches genesis without crossing boundary",
			blockMap: map[phase0.Root]blockRef{r('G'): {slot: 10, parent: phase0.Root{}}},
			start:    r('G'),
			wantRoot: phase0.Root{},
			wantOK:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRoot, gotOK := walkDependentRoot(tc.blockMap, tc.start, boundarySlot)
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if gotRoot != tc.wantRoot {
				t.Fatalf("root = %x, want %x", gotRoot, tc.wantRoot)
			}
		})
	}
}
