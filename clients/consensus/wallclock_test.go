package consensus

import (
	"math"
	"slices"
	"testing"
	"time"

	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

type wallclockTestSpecs struct {
	slotsPerEpoch uint64
	schedule      []SlotDurationScheduleEntry
}

func newWallclockTestSpecs(slotsPerEpoch uint64, slotDurationMs uint64, schedule ...SlotDurationScheduleEntry) *wallclockTestSpecs {
	return &wallclockTestSpecs{
		slotsPerEpoch: slotsPerEpoch,
		schedule:      append([]SlotDurationScheduleEntry{{Epoch: 0, SlotDurationMs: slotDurationMs}}, schedule...),
	}
}

func (w *Wallclock) setupTestClock(genesisTime time.Time, specs *wallclockTestSpecs) error {
	return w.setupSchedule(genesisTime, specs.slotsPerEpoch, specs.schedule)
}

func TestWallclockScheduleSegments(t *testing.T) {
	genesis := time.Unix(1_700_000_000, 0)

	tests := []struct {
		name     string
		specs    *wallclockTestSpecs
		segments []wallclockSegment
		wantErr  bool
	}{
		{
			name:     "no schedule",
			specs:    newWallclockTestSpecs(32, 12000),
			segments: []wallclockSegment{{duration: 12 * time.Second}},
		},
		{
			name: "unsorted schedule with far future entry",
			specs: newWallclockTestSpecs(4, 12000,
				SlotDurationScheduleEntry{Epoch: math.MaxUint64, SlotDurationMs: 3000},
				SlotDurationScheduleEntry{Epoch: 5, SlotDurationMs: 6000},
				SlotDurationScheduleEntry{Epoch: 2, SlotDurationMs: 8000},
			),
			segments: []wallclockSegment{
				{duration: 12 * time.Second},
				{epoch: 2, slot: 8, offset: 96 * time.Second, duration: 8 * time.Second},
				{epoch: 5, slot: 20, offset: 192 * time.Second, duration: 6 * time.Second},
			},
		},
		{
			name: "epoch 0 entry overrides SLOT_DURATION_MS",
			specs: newWallclockTestSpecs(4, 12000,
				SlotDurationScheduleEntry{Epoch: 0, SlotDurationMs: 6000},
			),
			segments: []wallclockSegment{{duration: 6 * time.Second}},
		},
		{
			name: "entries without a duration change are merged",
			specs: newWallclockTestSpecs(4, 12000,
				SlotDurationScheduleEntry{Epoch: 1, SlotDurationMs: 12000},
				SlotDurationScheduleEntry{Epoch: 2, SlotDurationMs: 6000},
				SlotDurationScheduleEntry{Epoch: 3, SlotDurationMs: 6000},
				SlotDurationScheduleEntry{Epoch: 4, SlotDurationMs: 0},
			),
			segments: []wallclockSegment{
				{duration: 12 * time.Second},
				{epoch: 2, slot: 8, offset: 96 * time.Second, duration: 6 * time.Second},
			},
		},
		{
			name:    "no genesis slot duration",
			specs:   newWallclockTestSpecs(4, 0, SlotDurationScheduleEntry{Epoch: 2, SlotDurationMs: 6000}),
			wantErr: true,
		},
		{
			name:    "no slots per epoch",
			specs:   newWallclockTestSpecs(0, 12000),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule, err := newWallclockSchedule(genesis, test.specs.slotsPerEpoch, test.specs.schedule)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got segments %+v", schedule.segments)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(schedule.segments) != len(test.segments) {
				t.Fatalf("segments = %+v, want %+v", schedule.segments, test.segments)
			}

			for i := range test.segments {
				if schedule.segments[i] != test.segments[i] {
					t.Errorf("segment %d = %+v, want %+v", i, schedule.segments[i], test.segments[i])
				}
			}
		})
	}
}

func TestWallclockConversions(t *testing.T) {
	genesis := time.Unix(1_700_000_000, 0)

	// 4 slots per epoch: 12s slots until epoch 2 (slot 8, 96s), then 8s slots
	// until epoch 5 (slot 20, 192s), then 6s slots
	w := &Wallclock{}
	if err := w.setupTestClock(genesis, newWallclockTestSpecs(4, 12000,
		SlotDurationScheduleEntry{Epoch: 2, SlotDurationMs: 8000},
		SlotDurationScheduleEntry{Epoch: 5, SlotDurationMs: 6000},
	)); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	slotOffsets := map[phase0.Slot]time.Duration{
		0:  0,
		7:  84 * time.Second,
		8:  96 * time.Second,
		9:  104 * time.Second,
		19: 184 * time.Second,
		20: 192 * time.Second,
		25: 222 * time.Second,
	}
	for slot, offset := range slotOffsets {
		if got := w.SlotToTime(slot); !got.Equal(genesis.Add(offset)) {
			t.Errorf("SlotToTime(%d) = genesis+%v, want genesis+%v", slot, got.Sub(genesis), offset)
		}
	}

	for slot := phase0.Slot(0); slot < 40; slot++ {
		start := w.SlotToTime(slot)

		if got := w.TimeToSlot(start); got != slot {
			t.Errorf("TimeToSlot(start of %d) = %d", slot, got)
		}

		if got := w.TimeToSlot(start.Add(w.GetSlotDuration(slot) - time.Millisecond)); got != slot {
			t.Errorf("TimeToSlot(end of %d) = %d", slot, got)
		}
	}

	slotDurations := map[phase0.Slot]time.Duration{
		0:  12 * time.Second,
		7:  12 * time.Second,
		8:  8 * time.Second,
		19: 8 * time.Second,
		20: 6 * time.Second,
		99: 6 * time.Second,
	}
	for slot, duration := range slotDurations {
		if got := w.GetSlotDuration(slot); got != duration {
			t.Errorf("GetSlotDuration(%d) = %v, want %v", slot, got, duration)
		}
	}

	if got := w.EpochToTime(5); !got.Equal(genesis.Add(192 * time.Second)) {
		t.Errorf("EpochToTime(5) = genesis+%v, want genesis+192s", got.Sub(genesis))
	}

	if got := w.TimeToSlot(genesis.Add(-time.Hour)); got != 0 {
		t.Errorf("TimeToSlot(before genesis) = %d, want 0", got)
	}

	// far future values saturate instead of wrapping around
	if got := w.SlotToTime(math.MaxUint64); got.Before(w.SlotToTime(1_000_000)) {
		t.Errorf("SlotToTime(max) = %v wrapped around", got)
	}

	if got := w.EpochToTime(math.MaxUint64); got.Before(w.SlotToTime(1_000_000)) {
		t.Errorf("EpochToTime(max) = %v wrapped around", got)
	}
}

func TestWallclockUnsetReturnsZero(t *testing.T) {
	w := &Wallclock{}

	if w.GetCurrentSlot() != 0 || w.GetCurrentEpoch() != 0 || w.TimeToSlot(time.Now()) != 0 {
		t.Error("expected zero slot / epoch before setup")
	}

	if w.GetSlotDuration(1) != 0 || w.GetCurrentSlotDuration() != 0 {
		t.Error("expected zero slot duration before setup")
	}

	if !w.SlotToTime(1).IsZero() || !w.EpochToTime(1).IsZero() {
		t.Error("expected zero time before setup")
	}
}

func TestWallclockScheduleUpdates(t *testing.T) {
	// 4 slots per epoch with 10s slots: epoch 2 started 20s ago, epoch 3 starts in 20s
	genesis := time.Now().Add(-100 * time.Second)
	base := func(schedule ...SlotDurationScheduleEntry) *wallclockTestSpecs {
		return newWallclockTestSpecs(4, 10000, append([]SlotDurationScheduleEntry{
			{Epoch: 1, SlotDurationMs: 5000},
			{Epoch: 2, SlotDurationMs: 10000},
		}, schedule...)...)
	}

	tests := []struct {
		name    string
		genesis time.Time
		specs   *wallclockTestSpecs
		wantErr bool
	}{
		{
			name:    "unchanged",
			genesis: genesis,
			specs:   base(),
		},
		{
			name:    "add future entry",
			genesis: genesis,
			specs:   base(SlotDurationScheduleEntry{Epoch: 10, SlotDurationMs: 4000}),
		},
		{
			name:    "change future entry",
			genesis: genesis,
			specs:   base(SlotDurationScheduleEntry{Epoch: 12, SlotDurationMs: 2000}),
		},
		{
			name:    "remove future entry",
			genesis: genesis,
			specs:   base(),
		},
		{
			name:    "change past entry",
			genesis: genesis,
			specs: newWallclockTestSpecs(4, 10000,
				SlotDurationScheduleEntry{Epoch: 1, SlotDurationMs: 6000},
				SlotDurationScheduleEntry{Epoch: 2, SlotDurationMs: 10000},
			),
			wantErr: true,
		},
		{
			name:    "remove past entry",
			genesis: genesis,
			specs:   newWallclockTestSpecs(4, 10000),
			wantErr: true,
		},
		{
			name:    "add past entry",
			genesis: genesis,
			specs:   base(SlotDurationScheduleEntry{Epoch: 3, SlotDurationMs: 1000}),
			wantErr: true,
		},
		{
			name:    "change genesis slot duration",
			genesis: genesis,
			specs: newWallclockTestSpecs(4, 9000,
				SlotDurationScheduleEntry{Epoch: 1, SlotDurationMs: 5000},
				SlotDurationScheduleEntry{Epoch: 2, SlotDurationMs: 10000},
			),
			wantErr: true,
		},
		{
			name:    "change genesis time",
			genesis: genesis.Add(time.Second),
			specs:   base(),
			wantErr: true,
		},
		{
			name:    "change slots per epoch",
			genesis: genesis,
			specs:   newWallclockTestSpecs(8, 10000),
			wantErr: true,
		},
	}

	w := &Wallclock{}
	if err := w.setupTestClock(genesis, base()); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := w.schedule.Load()
			err := w.setupTestClock(test.genesis, test.specs)

			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}

				if w.schedule.Load() != before {
					t.Fatal("rejected update replaced the schedule")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestWallclockPreGenesisUpdate(t *testing.T) {
	w := &Wallclock{}

	if err := w.setupTestClock(time.Now().Add(time.Hour), newWallclockTestSpecs(4, 12000)); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := w.setupTestClock(time.Now().Add(2*time.Hour), newWallclockTestSpecs(8, 6000)); err != nil {
		t.Fatalf("pre-genesis update rejected: %v", err)
	}
}

func TestWallclockEvents(t *testing.T) {
	const slotDuration = 50 * time.Millisecond

	w := NewWallclock()
	defer w.Stop()

	slots := w.SlotDispatcher.Subscribe(100, false)
	epochs := w.EpochDispatcher.Subscribe(100, false)

	// genesis lies ahead, so the clock announces slot 0 / epoch 0 onwards
	if err := w.setupTestClock(time.Now().Add(2*slotDuration), newWallclockTestSpecs(2, uint64(slotDuration.Milliseconds()))); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	timeout := time.After(5 * time.Second)

	for want := phase0.Slot(0); want < 6; want++ {
		select {
		case slot := <-slots.Channel():
			if slot != want {
				t.Fatalf("slot event = %d, want %d", slot, want)
			}

			if lag := time.Since(w.SlotToTime(slot)); lag < 0 {
				t.Fatalf("slot %d announced %v early", slot, -lag)
			}
		case <-timeout:
			t.Fatalf("timeout waiting for slot %d", want)
		}
	}

	for want := phase0.Epoch(0); want < 3; want++ {
		select {
		case epoch := <-epochs.Channel():
			if epoch != want {
				t.Fatalf("epoch event = %d, want %d", epoch, want)
			}
		case <-timeout:
			t.Fatalf("timeout waiting for epoch %d", want)
		}
	}
}

func TestWallclockSkipsMissedSlots(t *testing.T) {
	const slotDuration = 20 * time.Millisecond

	w := NewWallclock()
	defer w.Stop()

	slots := w.SlotDispatcher.Subscribe(0, true)

	if err := w.setupTestClock(time.Now().Add(-time.Second), newWallclockTestSpecs(4, uint64(slotDuration.Milliseconds()))); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	first := <-slots.Channel()

	// hold the dispatcher for several slots: the next event is already pending,
	// the one after it has to be the current slot again
	time.Sleep(10 * slotDuration)

	<-slots.Channel()

	slot := <-slots.Channel()
	if slot < first+10 {
		t.Fatalf("slot event = %d, expected the clock to skip ahead of %d", slot, first+10)
	}

	if current := w.GetCurrentSlot(); slot+1 < current {
		t.Fatalf("slot event = %d, but current slot is %d", slot, current)
	}
}

func TestChainStateWallclockEvents(t *testing.T) {
	const slotDuration = 50 * time.Millisecond

	cs := newChainState()
	defer cs.Stop()

	slots := cs.SlotDispatcher.Subscribe(10, false)

	cs.genesis = &v1.Genesis{GenesisTime: time.Now().Add(-time.Second)}
	cs.specs = &ChainSpec{}
	cs.specs.SlotsPerEpoch = 4
	cs.specs.SlotDurationMs = uint64(slotDuration.Milliseconds())

	if err := cs.updateWallclock(); err != nil {
		t.Fatalf("wallclock update failed: %v", err)
	}

	select {
	case slot := <-slots.Channel():
		if current := cs.CurrentSlot(); slot != current && slot+1 != current {
			t.Fatalf("slot event = %d, current slot %d", slot, current)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for slot event")
	}
}

func TestWallclockScheduleFromSpecs(t *testing.T) {
	genesis := time.Unix(1_700_000_000, 0)
	forkEpoch := uint64(2)
	farFuture := uint64(math.MaxUint64)

	tests := []struct {
		name      string
		forkEpoch *uint64
		eip8198Ms uint64
		segments  []wallclockSegment
	}{
		{
			name:     "no eip8198 fork",
			segments: []wallclockSegment{{duration: 12 * time.Second}},
		},
		{
			name:      "eip8198 fork scheduled",
			forkEpoch: &forkEpoch,
			eip8198Ms: 10000,
			segments: []wallclockSegment{
				{duration: 12 * time.Second},
				{epoch: 2, slot: 8, offset: 96 * time.Second, duration: 10 * time.Second},
			},
		},
		{
			name:      "eip8198 fork at far future epoch",
			forkEpoch: &farFuture,
			eip8198Ms: 10000,
			segments:  []wallclockSegment{{duration: 12 * time.Second}},
		},
		{
			name:      "eip8198 fork without slot duration",
			forkEpoch: &forkEpoch,
			segments:  []wallclockSegment{{duration: 12 * time.Second}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specs := &ChainSpec{}
			specs.SlotsPerEpoch = 4
			specs.SlotDurationMs = 12000
			specs.Eip8198ForkEpoch = test.forkEpoch
			specs.SlotDurationMsEip8198 = test.eip8198Ms

			w := &Wallclock{}
			if err := w.SetupClock(genesis, specs); err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			segments := w.schedule.Load().segments
			if !slices.Equal(segments, test.segments) {
				t.Fatalf("segments = %+v, want %+v", segments, test.segments)
			}
		})
	}

	if err := (&Wallclock{}).SetupClock(genesis, nil); err == nil {
		t.Fatal("expected error for missing specs")
	}
}
