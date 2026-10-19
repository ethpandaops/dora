package consensus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// noSlot marks the clock as not having entered any slot yet.
const noSlot = phase0.Slot(math.MaxUint64)

// wallclockSegment is a span of the chain with a constant slot duration. It
// lasts until the start of the next segment, the last one is open-ended.
type wallclockSegment struct {
	epoch    phase0.Epoch  // first epoch of the segment
	slot     phase0.Slot   // first slot of the segment
	offset   time.Duration // start of the segment since genesis
	duration time.Duration // slot duration within the segment
}

// wallclockSchedule is an immutable snapshot of the slot timings of a chain.
// All cutoffs are pre-calculated, so lookups never walk the spec schedule.
type wallclockSchedule struct {
	genesisTime   time.Time
	slotsPerEpoch uint64
	segments      []wallclockSegment // ordered, never empty, first one starts at genesis
}

// Wallclock maps between wall time and slots / epochs, following the slot
// duration schedule of the chain specs (EIP-8198), and emits an event whenever
// the clock enters a new slot or epoch.
type Wallclock struct {
	setupMutex  sync.Mutex
	schedule    atomic.Pointer[wallclockSchedule]
	refreshChan chan struct{}
	clockCancel context.CancelFunc

	SlotDispatcher  utils.Dispatcher[phase0.Slot]
	EpochDispatcher utils.Dispatcher[phase0.Epoch]
}

func NewWallclock() *Wallclock {
	w := &Wallclock{}
	w.initWallclock()
	return w
}

func (w *Wallclock) initWallclock() {
	clockCtx, clockCancel := context.WithCancel(context.Background())

	w.clockCancel = clockCancel
	w.refreshChan = make(chan struct{}, 1)

	go w.runClock(clockCtx)
}

// Stop shuts down the clock loop. Lookups keep working, but no further
// slot / epoch events are emitted.
func (w *Wallclock) Stop() {
	w.clockCancel()
}

// SetupClock applies the slot timings of the given specs. It may be called
// again whenever the specs change: timings that lie ahead are replaced, but a
// change to timings that already passed is rejected and the clock keeps its
// current schedule.
func (w *Wallclock) SetupClock(genesisTime time.Time, specs *ChainSpec) error {
	schedule, err := newWallclockSchedule(genesisTime, specs)
	if err != nil {
		return err
	}

	w.setupMutex.Lock()
	defer w.setupMutex.Unlock()

	current := w.schedule.Load()
	if current != nil {
		if current.equal(schedule) {
			return nil
		}

		if err := current.checkPastUnchanged(schedule, time.Now()); err != nil {
			return err
		}
	}

	w.schedule.Store(schedule)

	select {
	case w.refreshChan <- struct{}{}:
	default:
	}

	return nil
}

// newWallclockSchedule pre-calculates the segment cutoffs from the specs:
// SLOT_DURATION_MS from genesis and SLOT_DURATION_MS_HEZE from the Heze fork
// epoch on (EIP-8198). A fork epoch that cannot be represented (far future) is ignored.
func newWallclockSchedule(genesisTime time.Time, specs *ChainSpec) (*wallclockSchedule, error) {
	if specs == nil {
		return nil, errors.New("wallclock: missing chain specs")
	}

	if specs.SlotsPerEpoch == 0 {
		return nil, errors.New("wallclock: SLOTS_PER_EPOCH is zero")
	}

	if specs.SlotDurationMs == 0 {
		return nil, errors.New("wallclock: no slot duration at genesis")
	}

	genesisDuration, err := slotDurationFromMs(specs.SlotDurationMs)
	if err != nil {
		return nil, err
	}

	segments := []wallclockSegment{{duration: genesisDuration}}

	if specs.HezeForkEpoch != nil && specs.SlotDurationMsHeze > 0 {
		forkEpoch := *specs.HezeForkEpoch

		forkDuration, err := slotDurationFromMs(specs.SlotDurationMsHeze)
		if err != nil {
			return nil, err
		}

		switch {
		case forkEpoch == 0:
			segments[0].duration = forkDuration
		case forkDuration == genesisDuration, forkEpoch > math.MaxUint64/specs.SlotsPerEpoch:
		default:
			slot := phase0.Slot(forkEpoch * specs.SlotsPerEpoch)
			if uint64(slot) <= uint64(math.MaxInt64/genesisDuration) {
				segments = append(segments, wallclockSegment{
					epoch:    phase0.Epoch(forkEpoch),
					slot:     slot,
					offset:   time.Duration(slot) * genesisDuration,
					duration: forkDuration,
				})
			}
		}
	}

	return &wallclockSchedule{
		genesisTime:   genesisTime,
		slotsPerEpoch: specs.SlotsPerEpoch,
		segments:      segments,
	}, nil
}

func slotDurationFromMs(slotDurationMs uint64) (time.Duration, error) {
	if slotDurationMs > uint64(math.MaxInt64/time.Millisecond) {
		return 0, fmt.Errorf("wallclock: slot duration %vms out of range", slotDurationMs)
	}

	return time.Duration(slotDurationMs) * time.Millisecond, nil
}

func (s *wallclockSchedule) equal(other *wallclockSchedule) bool {
	return s.genesisTime.Equal(other.genesisTime) &&
		s.slotsPerEpoch == other.slotsPerEpoch &&
		slices.Equal(s.segments, other.segments)
}

// checkPastUnchanged verifies that next has the same timings as s for
// everything up to now.
func (s *wallclockSchedule) checkPastUnchanged(next *wallclockSchedule, now time.Time) error {
	if now.Before(s.genesisTime) && now.Before(next.genesisTime) {
		return nil
	}

	if !s.genesisTime.Equal(next.genesisTime) {
		return fmt.Errorf("wallclock: cannot change genesis time from %v to %v after genesis", s.genesisTime, next.genesisTime)
	}

	if s.slotsPerEpoch != next.slotsPerEpoch {
		return fmt.Errorf("wallclock: cannot change slots per epoch from %v to %v after genesis", s.slotsPerEpoch, next.slotsPerEpoch)
	}

	// the segments are derived front to back, so equal started segments mean equal past timings
	offset := now.Sub(s.genesisTime)
	started := s.startedSegments(offset)

	if next.startedSegments(offset) != started {
		return errors.New("wallclock: cannot change slot duration schedule of past epochs")
	}

	for i := range started {
		if s.segments[i] != next.segments[i] {
			return fmt.Errorf("wallclock: cannot change slot duration of past epoch %v", s.segments[i].epoch)
		}
	}

	return nil
}

// startedSegments returns the number of segments that began at or before offset.
func (s *wallclockSchedule) startedSegments(offset time.Duration) int {
	count := 0

	for count < len(s.segments) && s.segments[count].offset <= offset {
		count++
	}

	return count
}

func (s *wallclockSchedule) segmentOfSlot(slot phase0.Slot) *wallclockSegment {
	for i := len(s.segments) - 1; i > 0; i-- {
		if slot >= s.segments[i].slot {
			return &s.segments[i]
		}
	}

	return &s.segments[0]
}

func (s *wallclockSchedule) segmentAtOffset(offset time.Duration) *wallclockSegment {
	for i := len(s.segments) - 1; i > 0; i-- {
		if offset >= s.segments[i].offset {
			return &s.segments[i]
		}
	}

	return &s.segments[0]
}

// slotOffset returns the start of slot since genesis, saturating for slots
// beyond the representable time range.
func (s *wallclockSchedule) slotOffset(slot phase0.Slot) time.Duration {
	segment := s.segmentOfSlot(slot)
	slots := uint64(slot - segment.slot)

	if slots > uint64((math.MaxInt64-segment.offset)/segment.duration) {
		return math.MaxInt64
	}

	return segment.offset + time.Duration(slots)*segment.duration
}

// slotAtOffset returns the slot at offset since genesis. Offsets before
// genesis are not part of any slot.
func (s *wallclockSchedule) slotAtOffset(offset time.Duration) (phase0.Slot, bool) {
	if offset < 0 {
		return 0, false
	}

	segment := s.segmentAtOffset(offset)

	return segment.slot + phase0.Slot((offset-segment.offset)/segment.duration), true
}

func (s *wallclockSchedule) epochOfSlot(slot phase0.Slot) phase0.Epoch {
	return phase0.Epoch(uint64(slot) / s.slotsPerEpoch)
}

func (w *Wallclock) runClock(ctx context.Context) {
	timer := time.NewTimer(math.MaxInt64)
	defer timer.Stop()

	currentSlot := noSlot
	started := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.refreshChan:
		case <-timer.C:
		}

		schedule := w.schedule.Load()
		if schedule == nil {
			continue
		}

		now := time.Now()
		slot, ok := schedule.slotAtOffset(now.Sub(schedule.genesisTime))

		switch {
		case !ok:
			// before genesis
		case !started:
			// the clock joined a running slot, events start with the next one
			currentSlot = slot
		case currentSlot == noSlot || slot > currentSlot:
			// slots that passed while the clock fell behind (blocked subscriber,
			// stalled process) are skipped, only the current one is announced
			epoch := schedule.epochOfSlot(slot)
			if currentSlot == noSlot || epoch != schedule.epochOfSlot(currentSlot) {
				w.EpochDispatcher.Fire(epoch)
			}

			currentSlot = slot

			w.SlotDispatcher.Fire(slot)
		}

		started = true

		nextTime := schedule.genesisTime
		if ok {
			nextTime = nextTime.Add(schedule.slotOffset(slot + 1))
		}

		// measured from the current time: if dispatching ran past the next slot
		// start, the timer fires right away and the loop catches up
		timer.Reset(time.Until(nextTime))
	}
}

// GetSlotDuration returns the duration of the given slot.
func (w *Wallclock) GetSlotDuration(slot phase0.Slot) time.Duration {
	schedule := w.schedule.Load()
	if schedule == nil {
		return 0
	}

	return schedule.segmentOfSlot(slot).duration
}

// GetCurrentSlotDuration returns the duration of the current wallclock slot.
func (w *Wallclock) GetCurrentSlotDuration() time.Duration {
	schedule := w.schedule.Load()
	if schedule == nil {
		return 0
	}

	return schedule.segmentAtOffset(time.Since(schedule.genesisTime)).duration
}

// GetCurrentSlot returns the current wallclock slot, or 0 before genesis.
func (w *Wallclock) GetCurrentSlot() phase0.Slot {
	return w.TimeToSlot(time.Now())
}

// GetCurrentEpoch returns the current wallclock epoch, or 0 before genesis.
func (w *Wallclock) GetCurrentEpoch() phase0.Epoch {
	schedule := w.schedule.Load()
	if schedule == nil {
		return 0
	}

	slot, _ := schedule.slotAtOffset(time.Since(schedule.genesisTime))

	return schedule.epochOfSlot(slot)
}

// SlotToTime returns the start time of the given slot.
func (w *Wallclock) SlotToTime(slot phase0.Slot) time.Time {
	schedule := w.schedule.Load()
	if schedule == nil {
		return time.Time{}
	}

	return schedule.genesisTime.Add(schedule.slotOffset(slot))
}

// EpochToTime returns the start time of the first slot of the given epoch.
func (w *Wallclock) EpochToTime(epoch phase0.Epoch) time.Time {
	schedule := w.schedule.Load()
	if schedule == nil {
		return time.Time{}
	}

	slot := noSlot
	if uint64(epoch) <= math.MaxUint64/schedule.slotsPerEpoch {
		slot = phase0.Slot(uint64(epoch) * schedule.slotsPerEpoch)
	}

	return schedule.genesisTime.Add(schedule.slotOffset(slot))
}

// TimeToSlot returns the slot at the given time, or 0 for times before genesis.
func (w *Wallclock) TimeToSlot(timestamp time.Time) phase0.Slot {
	schedule := w.schedule.Load()
	if schedule == nil {
		return 0
	}

	slot, _ := schedule.slotAtOffset(timestamp.Sub(schedule.genesisTime))

	return slot
}
