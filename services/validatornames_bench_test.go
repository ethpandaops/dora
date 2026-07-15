package services

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// benchVN mimics a mature devnet: 100k named validators, 10 reassignment
// snapshots of 100 node ranges each.
func benchVN() *ValidatorNames {
	names := make(map[uint64]*validatorNameEntry, 100000)
	entry := &validatorNameEntry{name: "current-node"}
	for i := uint64(0); i < 100000; i++ {
		names[i] = entry
	}

	history := make([]validatorNameSnapshot, 0, 10)
	for s := 0; s < 10; s++ {
		ranges := make([]validatorNameRange, 0, 100)
		for r := uint64(0); r < 100; r++ {
			ranges = append(ranges, validatorNameRange{
				startIndex: r * 1000,
				endIndex:   r*1000 + 999,
				name:       fmt.Sprintf("node-%d-%d", s, r),
			})
		}
		endSlot := phase0.Slot(uint64(s+1) * 100000)
		if s == 9 {
			endSlot = phase0.Slot(math.MaxInt64)
		}
		history = append(history, validatorNameSnapshot{
			startSlot: phase0.Slot(uint64(s) * 100000),
			endSlot:   endSlot,
			ranges:    ranges,
		})
	}

	return &ValidatorNames{
		namesMutex:   sync.RWMutex{},
		namesByIndex: names,
		nameHistory:  history,
	}
}

func BenchmarkGetValidatorName(b *testing.B) {
	vn := benchVN()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vn.GetValidatorName(uint64(i) % 100000)
	}
}

func BenchmarkGetValidatorNameAt_HistoryHit(b *testing.B) {
	vn := benchVN()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vn.GetValidatorNameAt(uint64(i)%100000, phase0.Slot(uint64(i)%1000000))
	}
}

func BenchmarkGetValidatorNameAt_MissFallback(b *testing.B) {
	vn := benchVN()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// index outside all history ranges -> falls back to current name lookup
		vn.GetValidatorNameAt(100000+uint64(i)%1000, phase0.Slot(uint64(i)%1000000))
	}
}

func BenchmarkGetValidatorNameAt_NoHistory(b *testing.B) {
	vn := benchVN()
	vn.nameHistory = nil
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vn.GetValidatorNameAt(uint64(i)%100000, phase0.Slot(uint64(i)%1000000))
	}
}
