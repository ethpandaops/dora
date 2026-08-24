package replay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPausedClockHoldsItsAnchor(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newClock(anchor)

	time.Sleep(20 * time.Millisecond)

	require.Equal(t, anchor, clock.now())

	now, rate := clock.state()
	require.Equal(t, anchor, now)
	require.Zero(t, rate)
}

func TestSetJumpsTheClock(t *testing.T) {
	clock := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	target := time.Date(2026, 1, 1, 0, 0, 12, 0, time.UTC)
	clock.set(target)

	require.Equal(t, target, clock.now())
}

func TestPlayingClockAdvancesWithRealTime(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newClock(anchor)
	clock.setRate(100)

	time.Sleep(20 * time.Millisecond)

	elapsed := clock.now().Sub(anchor)
	require.Greater(t, elapsed, time.Second, "100x rate should cover >1s of virtual time in 20ms")
}

func TestSetRateKeepsElapsedVirtualTime(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newClock(anchor)
	clock.setRate(100)

	time.Sleep(20 * time.Millisecond)

	clock.setRate(0)
	frozen := clock.now()

	time.Sleep(20 * time.Millisecond)

	require.Equal(t, frozen, clock.now(), "a paused clock must not drift")
	require.True(t, frozen.After(anchor), "the virtual time gained while playing must be kept")
}

func TestRealDelayUntilScalesWithRate(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newClock(anchor)

	// a paused clock will never reach the target on its own
	require.Zero(t, clock.realDelayUntil(anchor.Add(time.Minute)))

	clock.setRate(4)

	delay := clock.realDelayUntil(anchor.Add(4 * time.Second))
	require.InDelta(t, float64(time.Second), float64(delay), float64(50*time.Millisecond))

	require.Zero(t, clock.realDelayUntil(anchor.Add(-time.Second)))
}
