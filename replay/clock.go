package replay

import (
	"sync"
	"time"
)

// clock is the virtual time the replay serves to the explorer. It is anchored to a
// point in virtual time and advances at `rate` virtual seconds per real second, so a
// paused replay (rate 0) simply holds its anchor while a playing one interpolates.
type clock struct {
	mutex      sync.RWMutex
	anchorReal time.Time
	anchorVirt time.Time
	rate       float64

	// holds freezes the clock without forgetting the rate it was running at, so the
	// replay can wait for the explorer without any virtual time passing. Holds nest.
	holds int
}

func newClock(virt time.Time) *clock {
	return &clock{
		anchorReal: time.Now(),
		anchorVirt: virt,
	}
}

// now returns the current virtual time.
func (c *clock) now() time.Time {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.nowLocked()
}

func (c *clock) nowLocked() time.Time {
	rate := c.effectiveRateLocked()
	if rate == 0 {
		return c.anchorVirt
	}

	return c.anchorVirt.Add(time.Duration(float64(time.Since(c.anchorReal)) * rate))
}

// effectiveRateLocked is the rate the clock is actually running at: zero while held,
// whatever was configured otherwise.
func (c *clock) effectiveRateLocked() float64 {
	if c.holds > 0 {
		return 0
	}

	return c.rate
}

// set jumps the virtual clock to an absolute point in time, keeping the current rate.
func (c *clock) set(virt time.Time) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.anchorReal = time.Now()
	c.anchorVirt = virt
}

// setRate changes the playback rate, re-anchoring so no virtual time is lost.
func (c *clock) setRate(rate float64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.anchorVirt = c.nowLocked()
	c.anchorReal = time.Now()
	c.rate = rate
}

// hold freezes the clock where it is. The explorer mirrors the effective rate, so it
// stops moving too rather than drifting ahead of what the replay has actually served.
func (c *clock) hold() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.anchorVirt = c.nowLocked()
	c.anchorReal = time.Now()
	c.holds++
}

// release lifts one hold, resuming at the configured rate once the last one is gone.
func (c *clock) release() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.holds == 0 {
		return
	}

	c.anchorVirt = c.nowLocked()
	c.anchorReal = time.Now()
	c.holds--
}

func (c *clock) isHeld() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.holds > 0
}

// state returns the current virtual time and the rate it is moving at, as served to
// the explorer.
func (c *clock) state() (time.Time, float64) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.nowLocked(), c.effectiveRateLocked()
}

// realDelayUntil returns how long to wait in real time for the virtual clock to reach
// the given point, at the rate it is configured to run at. It returns 0 when the clock
// is paused or already past it, since a paused clock would never get there on its own.
func (c *clock) realDelayUntil(virt time.Time) time.Duration {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if c.rate <= 0 {
		return 0
	}

	remaining := virt.Sub(c.nowLocked())
	if remaining <= 0 {
		return 0
	}

	return time.Duration(float64(remaining) / c.rate)
}
