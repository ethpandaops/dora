package services

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

func TestGetValidatorName(t *testing.T) {
	// Create a test validator name entry
	testEntry := &validatorNameEntry{name: "test-validator"}

	tests := []struct {
		name                string
		index               uint64
		setupValidatorNames func() *ValidatorNames
		expectedResult      string
	}{
		{
			name:  "returns empty when namesByIndex is nil",
			index: 123,
			setupValidatorNames: func() *ValidatorNames {
				return &ValidatorNames{
					namesMutex: sync.RWMutex{},
				}
			},
			expectedResult: "",
		},
		{
			name:  "returns name from namesByIndex when found",
			index: 123,
			setupValidatorNames: func() *ValidatorNames {
				return &ValidatorNames{
					namesMutex:   sync.RWMutex{},
					namesByIndex: map[uint64]*validatorNameEntry{123: testEntry},
				}
			},
			expectedResult: "test-validator",
		},
		{
			name:  "returns empty when resolvedNamesByIndex is nil",
			index: 123,
			setupValidatorNames: func() *ValidatorNames {
				return &ValidatorNames{
					namesMutex:   sync.RWMutex{},
					namesByIndex: map[uint64]*validatorNameEntry{},
				}
			},
			expectedResult: "",
		},
		{
			name:  "returns name from resolvedNamesByIndex when found",
			index: 123,
			setupValidatorNames: func() *ValidatorNames {
				return &ValidatorNames{
					namesMutex:           sync.RWMutex{},
					namesByIndex:         map[uint64]*validatorNameEntry{},
					resolvedNamesByIndex: map[uint64]*validatorNameEntry{123: testEntry},
				}
			},
			expectedResult: "test-validator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vn := tt.setupValidatorNames()

			result := vn.GetValidatorName(tt.index)

			if result != tt.expectedResult {
				t.Errorf("GetValidatorName() = %q, want %q", result, tt.expectedResult)
			}
		})
	}
}

func TestGetValidatorNameAt(t *testing.T) {
	currentEntry := &validatorNameEntry{name: "current-node"}
	history := []validatorNameSnapshot{
		{
			startSlot: 100,
			endSlot:   500,
			ranges: []validatorNameRange{
				{startIndex: 0, endIndex: 99, name: "old-node-1"},
				{startIndex: 100, endIndex: 199, name: "old-node-2"},
			},
		},
		{
			startSlot: 500,
			endSlot:   phase0.Slot(math.MaxInt64),
			ranges: []validatorNameRange{
				{startIndex: 0, endIndex: 199, name: "new-node"},
			},
		},
	}

	vn := &ValidatorNames{
		namesMutex:   sync.RWMutex{},
		namesByIndex: map[uint64]*validatorNameEntry{50: currentEntry, 5000: currentEntry},
		nameHistory:  history,
	}

	tests := []struct {
		name     string
		index    uint64
		slot     phase0.Slot
		expected string
	}{
		{"first snapshot, first range", 50, 200, "old-node-1"},
		{"first snapshot, second range", 150, 200, "old-node-2"},
		{"exactly at snapshot start", 50, 100, "old-node-1"},
		{"exactly at transition boundary", 50, 500, "new-node"},
		{"open snapshot", 150, 99999, "new-node"},
		{"slot before first snapshot falls back to current", 50, 99, "current-node"},
		{"uncovered index falls back to current", 5000, 200, "current-node"},
		{"uncovered index without current name", 6000, 200, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if result := vn.GetValidatorNameAt(tt.index, tt.slot); result != tt.expected {
				t.Errorf("GetValidatorNameAt(%v, %v) = %q, want %q", tt.index, tt.slot, result, tt.expected)
			}
		})
	}
}

func TestGetValidatorNameAt_NoHistory(t *testing.T) {
	vn := &ValidatorNames{
		namesMutex:   sync.RWMutex{},
		namesByIndex: map[uint64]*validatorNameEntry{123: {name: "test-validator"}},
	}

	if result := vn.GetValidatorNameAt(123, 42); result != "test-validator" {
		t.Errorf("GetValidatorNameAt() without history = %q, want %q", result, "test-validator")
	}
	if result := vn.GetValidatorNameAt(456, 42); result != "" {
		t.Errorf("GetValidatorNameAt() for unknown index = %q, want empty", result)
	}
}

func TestBuildNameSnapshots(t *testing.T) {
	// 12s slots starting at genesis time 1000
	timeToSlot := func(ts int64) phase0.Slot {
		if ts <= 1000 {
			return 0
		}
		return phase0.Slot((ts - 1000) / 12)
	}

	// unsorted input with an entry fully shadowed by an equal effective_from slot
	entries := []validatorNamesHistoryEntry{
		{EffectiveFrom: 1000 + 1200, Ranges: map[string]string{"0-99": "node-b"}},
		{EffectiveFrom: 0, Ranges: map[string]string{"0-99": "node-a"}},
		{EffectiveFrom: 1000 + 1205, Ranges: map[string]string{"0-99": "node-c"}},
	}

	snapshots := buildNameSnapshots(entries, timeToSlot)

	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots (shadowed entry dropped), got %v", len(snapshots))
	}
	if snapshots[0].startSlot != 0 || snapshots[0].endSlot != 100 {
		t.Errorf("snapshot 0 interval = [%v, %v), want [0, 100)", snapshots[0].startSlot, snapshots[0].endSlot)
	}
	if snapshots[0].ranges[0].name != "node-a" {
		t.Errorf("snapshot 0 name = %q, want node-a", snapshots[0].ranges[0].name)
	}
	if snapshots[1].startSlot != 100 || snapshots[1].endSlot != phase0.Slot(math.MaxInt64) {
		t.Errorf("snapshot 1 interval = [%v, %v), want [100, max)", snapshots[1].startSlot, snapshots[1].endSlot)
	}
	if snapshots[1].ranges[0].name != "node-c" {
		t.Errorf("snapshot 1 name = %q, want node-c", snapshots[1].ranges[0].name)
	}
}

func TestParseValidatorNameRanges(t *testing.T) {
	ranges := parseValidatorNameRanges(map[string]string{
		"0-99":                "node-1",
		"100":                 "node-2",
		"50-150":              "overlapping",
		"invalid":             "bad-key",
		"withdrawal:0x123abc": "address-key",
		"200-100":             "inverted-bounds",
	})

	if len(ranges) != 2 {
		t.Fatalf("expected 2 valid ranges, got %v: %+v", len(ranges), ranges)
	}
	if ranges[0].startIndex != 0 || ranges[0].endIndex != 99 || ranges[0].name != "node-1" {
		t.Errorf("unexpected range 0: %+v", ranges[0])
	}
	if ranges[1].startIndex != 100 || ranges[1].endIndex != 100 || ranges[1].name != "node-2" {
		t.Errorf("unexpected range 1: %+v", ranges[1])
	}
}

func TestGetValidatorName_Concurrency(t *testing.T) {
	vn := &ValidatorNames{
		namesMutex:           sync.RWMutex{},
		namesByIndex:         make(map[uint64]*validatorNameEntry),
		namesByWithdrawal:    make(map[common.Address]*validatorNameEntry),
		namesByDepositOrigin: make(map[common.Address]*validatorNameEntry),
		namesByDepositTarget: make(map[common.Address]*validatorNameEntry),
		resolvedNamesByIndex: make(map[uint64]*validatorNameEntry),
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Goroutine 1: Continuous Reads
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = vn.GetValidatorName(123)
			}
		}
	})

	// Goroutine 2: Continuous Loader-like Map Reset/Updates
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				vn.namesMutex.Lock()
				vn.namesByIndex = make(map[uint64]*validatorNameEntry)
				vn.namesByIndex[123] = &validatorNameEntry{name: "test"}
				vn.namesMutex.Unlock()
			}
		}
	})

	// Goroutine 3: Continuous Resolver-like Map Writes
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				newResolved := make(map[uint64]*validatorNameEntry)
				newResolved[123] = &validatorNameEntry{name: "resolved"}
				vn.namesMutex.Lock()
				vn.resolvedNamesByIndex = newResolved
				vn.namesMutex.Unlock()
			}
		}
	})

	// Run for a short duration
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
