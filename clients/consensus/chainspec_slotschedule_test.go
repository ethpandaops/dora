package consensus

import (
	"testing"
	"time"

	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// Values shaped like go-eth2-client's parsed /eth/v1/config/spec map.
func slotScheduleSpecValues(schedule []any) map[string]any {
	values := map[string]any{
		"SLOT_DURATION_MS": uint64(12000),
		"SLOTS_PER_EPOCH":  uint64(32),
	}
	if schedule != nil {
		values["SLOT_DURATION_SCHEDULE"] = schedule
	}

	return values
}

func TestSlotDurationScheduleDefaultsToGenesisDuration(t *testing.T) {
	spec := &ChainSpec{}
	if err := spec.ParseAdditive(slotScheduleSpecValues(nil)); err != nil {
		t.Fatal(err)
	}

	if len(spec.SlotDurationSchedule) != 1 || spec.SlotDurationSchedule[0] != (SlotDurationScheduleEntry{0, 12000}) {
		t.Fatalf("unexpected schedule %+v", spec.SlotDurationSchedule)
	}

	if got := spec.SlotOffsetMs(100); got != 100*12000 {
		t.Fatalf("SlotOffsetMs(100) = %d", got)
	}

	if got := spec.SlotAtOffsetMs(100*12000 + 11999); got != 100 {
		t.Fatalf("SlotAtOffsetMs = %d", got)
	}
}

func TestSlotDurationSchedulePiecewise(t *testing.T) {
	spec := &ChainSpec{}
	err := spec.ParseAdditive(slotScheduleSpecValues([]any{
		// unsorted on purpose
		map[string]any{"EPOCH": uint64(4), "SLOT_DURATION_MS": uint64(8000)},
		map[string]any{"EPOCH": uint64(0), "SLOT_DURATION_MS": uint64(12000)},
		map[string]any{"EPOCH": uint64(2), "SLOT_DURATION_MS": uint64(11000)},
	}))
	if err != nil {
		t.Fatal(err)
	}

	// epochs 0-1: 64 slots x 12s, epochs 2-3: 64 x 11s, then 8s
	epoch2 := uint64(64 * 12000)
	epoch4 := epoch2 + 64*11000

	cases := []struct {
		slot   uint64
		offset uint64
	}{
		{0, 0},
		{63, 63 * 12000},
		{64, epoch2},
		{65, epoch2 + 11000},
		{128, epoch4},
		{130, epoch4 + 2*8000},
	}
	for _, c := range cases {
		if got := spec.SlotOffsetMs(c.slot); got != c.offset {
			t.Errorf("SlotOffsetMs(%d) = %d, want %d", c.slot, got, c.offset)
		}
		if got := spec.SlotAtOffsetMs(c.offset); got != c.slot {
			t.Errorf("SlotAtOffsetMs(%d) = %d, want %d", c.offset, got, c.slot)
		}
		if c.offset > 0 {
			if got := spec.SlotAtOffsetMs(c.offset - 1); got != c.slot-1 {
				t.Errorf("SlotAtOffsetMs(%d) = %d, want %d", c.offset-1, got, c.slot-1)
			}
		}
	}

	if got := spec.GetSlotDurationMs(3); got != 11000 {
		t.Errorf("GetSlotDurationMs(3) = %d", got)
	}

	genesis := time.Unix(1_700_000_000, 0)
	cs := &ChainState{specs: spec, genesis: &v1.Genesis{GenesisTime: genesis}}

	if got := cs.SlotToTime(65); !got.Equal(genesis.Add(time.Duration(epoch2+11000) * time.Millisecond)) {
		t.Errorf("SlotToTime(65) = %v", got)
	}
	if got := cs.TimeToSlot(genesis.Add(time.Duration(epoch4+8500) * time.Millisecond)); got != phase0.Slot(129) {
		t.Errorf("TimeToSlot = %d", got)
	}
	if got := cs.GetSlotDuration(phase0.Slot(70)); got != 11*time.Second {
		t.Errorf("GetSlotDuration(70) = %v", got)
	}
}
