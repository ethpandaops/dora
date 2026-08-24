package replay

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testConsoleReplay(virtualSlot uint64) *Replay {
	replay := testReplay(virtualSlot)
	replay.clock = newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	replay.wake = make(chan struct{}, 1)
	replay.cfg = DefaultConfig()

	return replay
}

func TestConsoleCommands(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantRunning bool
		wantSpeed   float64
		wantTarget  uint64
		wantOutput  string
	}{
		{
			name:        "step advances one slot by default",
			line:        "step",
			wantRunning: true,
			wantTarget:  101,
		},
		{
			name:        "step takes a slot count",
			line:        "step 32",
			wantRunning: true,
			wantTarget:  132,
		},
		{
			name:        "forward sets an absolute target",
			line:        "forward 4096",
			wantRunning: true,
			wantTarget:  4096,
		},
		{
			name:        "forward takes an optional speed",
			line:        "forward 4096 6x",
			wantRunning: true,
			wantSpeed:   6,
			wantTarget:  4096,
		},
		{
			name:        "forward refuses to rewind",
			line:        "forward 50",
			wantRunning: false,
			wantOutput:  "cannot rewind",
		},
		{
			name:        "play defaults to real time",
			line:        "play",
			wantRunning: true,
			wantSpeed:   1,
		},
		{
			name:        "play takes an x-suffixed speed",
			line:        "play 8x",
			wantRunning: true,
			wantSpeed:   8,
		},
		{
			name:       "an invalid speed is rejected",
			line:       "play banana",
			wantOutput: "invalid speed",
		},
		{
			name:       "an unknown command is reported",
			line:       "frobnicate",
			wantOutput: "unknown command",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replay := testConsoleReplay(100)
			out := &bytes.Buffer{}

			require.False(t, replay.runCommand(out, test.line))

			require.Equal(t, test.wantRunning, replay.running)
			require.Equal(t, test.wantSpeed, replay.speed)
			require.Equal(t, test.wantTarget, replay.target)

			if test.wantOutput != "" {
				require.Contains(t, out.String(), test.wantOutput)
			}
		})
	}
}

func TestConsoleQuit(t *testing.T) {
	replay := testConsoleReplay(100)
	require.True(t, replay.runCommand(&bytes.Buffer{}, "quit"))
}

func TestConsoleStopAndResume(t *testing.T) {
	replay := testConsoleReplay(100)
	out := &bytes.Buffer{}

	replay.runCommand(out, "play 4x")
	require.True(t, replay.running)

	replay.runCommand(out, "stop")
	require.False(t, replay.running)

	// the clock must not keep running while the replay is paused
	frozen := replay.clock.now()
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, frozen, replay.clock.now())

	replay.runCommand(out, "start")
	require.True(t, replay.running)
	require.Equal(t, 4.0, replay.speed)
}

func TestConsoleStatus(t *testing.T) {
	replay := testConsoleReplay(100)
	out := &bytes.Buffer{}

	replay.runCommand(out, "status")

	require.Contains(t, out.String(), "slot 100")
	require.Contains(t, out.String(), "finalized 9")
	require.Contains(t, out.String(), "paused")
}
