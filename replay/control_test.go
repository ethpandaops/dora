package replay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func controlServer(t *testing.T, replay *Replay) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(replay.controlHandler())
	t.Cleanup(server.Close)

	return server
}

func postCommand(t *testing.T, server *httptest.Server, body string) (*http.Response, Status) {
	t.Helper()

	rsp, err := http.Post(server.URL+"/replay/command", "application/json", strings.NewReader(body))
	require.NoError(t, err)

	t.Cleanup(func() { _ = rsp.Body.Close() })

	status := Status{}
	if rsp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(rsp.Body).Decode(&status))
	}

	return rsp, status
}

func TestCommandsDriveTheReplay(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantRunning bool
		wantSpeed   float64
		wantTarget  uint64
	}{
		{
			name:        "play at a speed",
			body:        `{"action":"play","speed":4}`,
			wantRunning: true,
			wantSpeed:   4,
		},
		{
			name:        "play at max speed",
			body:        `{"action":"play","speed":0}`,
			wantRunning: true,
			wantSpeed:   0,
		},
		{
			name:        "step a number of slots",
			body:        `{"action":"step","slots":32}`,
			wantRunning: true,
			wantTarget:  132,
		},
		{
			name:        "forward to a slot",
			body:        `{"action":"forward","slot":500,"speed":6}`,
			wantRunning: true,
			wantSpeed:   6,
			wantTarget:  500,
		},
		{
			name:        "speed alone does not start the replay",
			body:        `{"action":"speed","speed":8}`,
			wantRunning: false,
			wantSpeed:   8,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replay := testConsoleReplay(100)
			server := controlServer(t, replay)

			rsp, status := postCommand(t, server, test.body)

			require.Equal(t, http.StatusOK, rsp.StatusCode)
			require.Equal(t, test.wantRunning, status.Running)
			require.Equal(t, test.wantSpeed, status.Speed)
			require.Equal(t, test.wantTarget, status.TargetSlot)
		})
	}
}

func TestCommandRejectsUnknownAction(t *testing.T) {
	replay := testConsoleReplay(100)
	server := controlServer(t, replay)

	rsp, _ := postCommand(t, server, `{"action":"detonate"}`)
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)

	rsp, _ = postCommand(t, server, `{"action":"forward","slot":50}`)
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, "forward must refuse to rewind")
}

func TestResumeKeepsTargetAndSpeed(t *testing.T) {
	replay := testConsoleReplay(100)

	require.NoError(t, replay.Forward(500, 4))
	replay.Pause()
	replay.Resume()

	status := replay.Status()
	require.True(t, status.Running)
	require.Equal(t, 4.0, status.Speed, "resuming must keep the speed it was running at")
	require.Equal(t, uint64(500), status.TargetSlot, "resuming must keep running towards the target")
}

func TestResumeDropsAReachedTarget(t *testing.T) {
	replay := testConsoleReplay(100)

	replay.Step(1)
	replay.virtualSlot = 101 // the driver would have advanced here
	replay.Pause()
	replay.Resume()

	require.Zero(t, replay.Status().TargetSlot, "a target already reached must not pause the replay again")
	require.True(t, replay.Status().Running)
}

func TestStatusReportsChainContext(t *testing.T) {
	replay := testConsoleReplay(100)
	replay.cfg.StartSlot = 90
	replay.upstreamSlot = 4000
	replay.chain.genesisTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	status := replay.Status()

	require.Equal(t, uint64(90), status.StartSlot)
	require.Equal(t, uint64(4000), status.UpstreamSlot)
	require.Equal(t, uint64(125), status.UpstreamEpoch)
	require.Equal(t, uint64(12000), status.SlotDurationMs)
	require.Equal(t, uint64(32), status.SlotsPerEpoch)
}

func TestControlUIIsServed(t *testing.T) {
	replay := testConsoleReplay(100)
	server := controlServer(t, replay)

	rsp, err := http.Get(server.URL + "/replay/ui.js")
	require.NoError(t, err)

	defer func() { _ = rsp.Body.Close() }()

	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, rsp.Header.Get("Content-Type"), "javascript")

	body := &bytes.Buffer{}
	_, err = body.ReadFrom(rsp.Body)
	require.NoError(t, err)

	require.Contains(t, body.String(), "replay-callout", "the side-loaded UI must build its callout")
	require.Contains(t, body.String(), "/replay/command", "the side-loaded UI must call the control API")
	require.Contains(t, body.String(), "window.doraReplayApi",
		"the side-loaded UI must take the control address from the explorer, not from its own script url")
	require.Contains(t, body.String(), "window.doraIndexRefreshInterval",
		"the side-loaded UI must retune the explorer's polling to the replay's pace")
}

// TestControlAllowsCrossOriginCalls guards the reason the UI works at all: it is served
// by the replay but runs on the explorer's origin, so the control API has to accept
// cross-origin calls and their preflight.
func TestControlAllowsCrossOriginCalls(t *testing.T) {
	replay := testConsoleReplay(100)
	server := controlServer(t, replay)

	req, err := http.NewRequest(http.MethodOptions, server.URL+"/replay/command", http.NoBody)
	require.NoError(t, err)

	req.Header.Set("Origin", "http://localhost:8083")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rsp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = rsp.Body.Close() }()

	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.Equal(t, "*", rsp.Header.Get("Access-Control-Allow-Origin"))
	require.Contains(t, rsp.Header.Get("Access-Control-Allow-Methods"), "POST")
}

func TestStatusStreamPushesOnChange(t *testing.T) {
	replay := testConsoleReplay(100)
	server := controlServer(t, replay)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/replay/events", http.NoBody)
	require.NoError(t, err)

	rsp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = rsp.Body.Close() }()

	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Equal(t, "text/event-stream", rsp.Header.Get("Content-Type"))

	require.Eventually(t, func() bool { return replay.control.subscriberCount() == 1 }, 5*time.Second, 5*time.Millisecond)

	replay.Play(8)

	reader := bufio.NewReader(rsp.Body)

	eventLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "event: status", strings.TrimSpace(eventLine))

	dataLine, err := reader.ReadString('\n')
	require.NoError(t, err)

	status := Status{}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(dataLine), "data: ")), &status))
	require.True(t, status.Running)
	require.Equal(t, 8.0, status.Speed)
}

// TestStatusStreamNeverBlocksTheDriver is the property that keeps a stalled browser tab
// from stalling the replay: status updates are dropped, not queued.
func TestStatusStreamNeverBlocksTheDriver(t *testing.T) {
	replay := testConsoleReplay(100)

	stuck := newEventSubscriber(map[string]bool{"status": true})
	replay.control.add(stuck)

	// fill the subscriber's buffer well past capacity
	done := make(chan struct{})

	go func() {
		defer close(done)

		for i := 0; i < 1000; i++ {
			replay.notifyStatus()
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("notifyStatus blocked on a subscriber that is not reading")
	}
}
