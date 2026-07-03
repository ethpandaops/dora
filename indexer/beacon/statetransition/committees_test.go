package statetransition

import (
	"testing"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// TestComputeShuffledListMatchesPerIndex verifies the batch shuffle against the
// spec-reference per-index compute_shuffled_index for sizes below and above the
// parallelization threshold.
func TestComputeShuffledListMatchesPerIndex(t *testing.T) {
	specs := &consensus.ChainSpec{ChainSpecPreset: consensus.ChainSpecPreset{ShuffleRoundCount: 90}}
	seed := phase0.Root{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc}

	for _, n := range []uint64{0, 1, 2, 255, 256, 257, 4095, 4096, 20000} {
		list := computeShuffledList(n, seed, specs)
		if uint64(len(list)) != n {
			t.Fatalf("n=%d: got list of length %d", n, len(list))
		}
		for i := uint64(0); i < n; i++ {
			if want := computeShuffledIndex(i, n, seed, specs); list[i] != want {
				t.Fatalf("n=%d: list[%d] = %d, want %d", n, i, list[i], want)
			}
		}
	}
}

func BenchmarkComputeShuffledList(b *testing.B) {
	specs := &consensus.ChainSpec{ChainSpecPreset: consensus.ChainSpecPreset{ShuffleRoundCount: 90}}
	seed := phase0.Root{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc}
	for b.Loop() {
		computeShuffledList(1_000_000, seed, specs)
	}
}
