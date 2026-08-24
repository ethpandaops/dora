package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// defaultReplayPollInterval is how often the virtual clock is refreshed from the
	// control server. Between polls the clock is interpolated locally from the last
	// reported rate, so a coarse interval does not make the clock jumpy.
	defaultReplayPollInterval = 100 * time.Millisecond

	// replayConnectTimeout bounds how long EnableReplayClock waits for the control
	// server to become reachable before giving up.
	replayConnectTimeout = 2 * time.Minute
)

// replayClockState is the payload served by a dora-replay control server at
// GET /replay/clock. Rate is the number of virtual milliseconds that pass per real
// millisecond; it is 0 while the replay is paused.
type replayClockState struct {
	TimeMs int64   `json:"time_ms"`
	Rate   float64 `json:"rate"`
}

// replayClock mirrors the virtual clock of a dora-replay control server, so the whole
// explorer sees a simulated "now" while a past slot range is stepped through.
type replayClock struct {
	ctx    context.Context
	logger logrus.FieldLogger
	url    string
	client *http.Client

	mutex      sync.RWMutex
	anchorReal time.Time
	anchorVirt time.Time
	rate       float64
}

// newReplayClock connects to the control server and blocks until the first clock
// state has been read, so callers never observe an uninitialized virtual time.
func newReplayClock(ctx context.Context, logger logrus.FieldLogger, controlURL string, pollInterval time.Duration) (*replayClock, error) {
	if controlURL == "" {
		return nil, fmt.Errorf("no replay control url configured")
	}

	if pollInterval <= 0 {
		pollInterval = defaultReplayPollInterval
	}

	clock := &replayClock{
		ctx:    ctx,
		logger: logger,
		url:    strings.TrimSuffix(controlURL, "/") + "/replay/clock",
		client: &http.Client{Timeout: 10 * time.Second},
	}

	if err := clock.awaitFirstPoll(); err != nil {
		return nil, err
	}

	go clock.runPollLoop(pollInterval)

	return clock, nil
}

// awaitFirstPoll retries the control server until it answers or the connect timeout
// expires. The explorer cannot compute any slot before this succeeds.
func (c *replayClock) awaitFirstPoll() error {
	deadline := time.Now().Add(replayConnectTimeout)
	lastLog := time.Time{}

	for {
		err := c.poll()
		if err == nil {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("could not reach replay control server at %v: %w", c.url, err)
		}

		if time.Since(lastLog) > 5*time.Second {
			c.logger.WithError(err).Warnf("waiting for replay control server at %v", c.url)
			lastLog = time.Now()
		}

		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (c *replayClock) runPollLoop(pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if err := c.poll(); err != nil {
				c.logger.WithError(err).Debugf("failed polling replay clock")
			}
		}
	}
}

func (c *replayClock) poll() error {
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, http.NoBody)
	if err != nil {
		return err
	}

	rsp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = rsp.Body.Close() }()

	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %v", rsp.StatusCode)
	}

	state := replayClockState{}
	if err := json.NewDecoder(rsp.Body).Decode(&state); err != nil {
		return fmt.Errorf("error parsing replay clock response: %w", err)
	}

	c.mutex.Lock()
	c.anchorReal = time.Now()
	c.anchorVirt = time.UnixMilli(state.TimeMs)
	c.rate = state.Rate
	c.mutex.Unlock()

	return nil
}

// now returns the current virtual time, interpolated from the last polled state so
// the clock keeps moving smoothly between polls while the replay is playing.
func (c *replayClock) now() time.Time {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if c.rate == 0 {
		return c.anchorVirt
	}

	elapsed := time.Since(c.anchorReal)

	return c.anchorVirt.Add(time.Duration(float64(elapsed) * c.rate))
}
