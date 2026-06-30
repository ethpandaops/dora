package services

import (
	"sort"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

func TestBalanceAt(t *testing.T) {
	balances := []phase0.Gwei{30, 10}
	if got := balanceAt(balances, 0); got != 30 {
		t.Errorf("in-range lookup = %d, want 30", got)
	}
	// A projected (pending-deposit) validator lives past the snapshot and must
	// resolve to 0 rather than panic.
	if got := balanceAt(balances, 9); got != 0 {
		t.Errorf("out-of-range lookup = %d, want 0", got)
	}
}

func TestBuilderBalanceAt(t *testing.T) {
	balances := []phase0.Gwei{30, 10}
	if got := builderBalanceAt(balances, 0); got != 30 {
		t.Errorf("in-range lookup = %d, want 30", got)
	}
	// A builder onboarded mid-epoch lives past the snapshot and must resolve to 0.
	if got := builderBalanceAt(balances, 9); got != 0 {
		t.Errorf("out-of-range lookup = %d, want 0", got)
	}
}

// TestValidatorBalanceSortWithProjectedIndex sorts a set containing a validator
// whose index is past the balances snapshot, mirroring the balance-desc page
// sort. It must order by balance without indexing out of range.
func TestValidatorBalanceSortWithProjectedIndex(t *testing.T) {
	balances := []phase0.Gwei{30, 10} // only indexes 0 and 1 exist on-chain
	set := []ValidatorWithIndex{{Index: 1}, {Index: 9}, {Index: 0}}

	sort.Slice(set, func(i, j int) bool {
		return balanceAt(balances, set[i].Index) > balanceAt(balances, set[j].Index)
	})

	want := []phase0.ValidatorIndex{0, 1, 9} // 30, 10, then projected (0)
	for i := range want {
		if set[i].Index != want[i] {
			t.Fatalf("desc order = %d, want %v", []phase0.ValidatorIndex{set[0].Index, set[1].Index, set[2].Index}, want)
		}
	}
}

// TestBuilderBalanceSortWithOnboardedIndex is the builder-side equivalent.
func TestBuilderBalanceSortWithOnboardedIndex(t *testing.T) {
	balances := []phase0.Gwei{30, 10}
	set := []BuilderWithIndex{{Index: 1}, {Index: 9}, {Index: 0}}

	sort.Slice(set, func(i, j int) bool {
		return builderBalanceAt(balances, set[i].Index) > builderBalanceAt(balances, set[j].Index)
	})

	want := []gloas.BuilderIndex{0, 1, 9}
	for i := range want {
		if set[i].Index != want[i] {
			t.Fatalf("desc order = %v, want %v", []gloas.BuilderIndex{set[0].Index, set[1].Index, set[2].Index}, want)
		}
	}
}
