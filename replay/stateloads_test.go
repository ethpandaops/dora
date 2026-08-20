package replay

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStateLoadsTracksActiveReads(t *testing.T) {
	loads := newStateLoads()

	requireClosed(t, loads.idleChan(), "nothing is loading yet")
	require.Zero(t, loads.count())

	endFirst := loads.begin()
	endSecond := loads.begin()

	require.Equal(t, 2, loads.count())
	requireOpen(t, loads.idleChan(), "two reads are in flight")

	endFirst()
	require.Equal(t, 1, loads.count())
	requireOpen(t, loads.idleChan(), "one read is still in flight")

	endSecond()
	require.Zero(t, loads.count())
	requireClosed(t, loads.idleChan(), "the last read finished")

	// ending twice must not double-count
	endSecond()
	require.Zero(t, loads.count())
}

func TestStateLoadsBecomeBusyAgain(t *testing.T) {
	loads := newStateLoads()

	end := loads.begin()
	end()

	requireClosed(t, loads.idleChan(), "idle after the first read")

	loads.begin()
	requireOpen(t, loads.idleChan(), "a later read must make it busy again")
}

// TestClockDoesNotMoveWhileHeld is the whole point of the state gate: waiting for the
// explorer must cost real time but no virtual time, or the replay runs ahead of what
// the explorer has actually seen.
func TestClockDoesNotMoveWhileHeld(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newClock(anchor)
	clock.setRate(100)

	time.Sleep(20 * time.Millisecond)

	clock.hold()
	frozen := clock.now()

	_, rate := clock.state()
	require.Zero(t, rate, "the explorer must see a stopped clock while the replay is holding")

	time.Sleep(30 * time.Millisecond)
	require.Equal(t, frozen, clock.now(), "no virtual time may pass while held")

	clock.release()

	_, rate = clock.state()
	require.Equal(t, 100.0, rate, "releasing must restore the rate it was running at")

	time.Sleep(20 * time.Millisecond)
	require.True(t, clock.now().After(frozen), "the clock must resume after the hold")
}

func TestClockHoldsNest(t *testing.T) {
	clock := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	clock.setRate(10)

	clock.hold()
	clock.hold()
	clock.release()

	require.True(t, clock.isHeld(), "the clock stays held until every hold is released")

	clock.release()
	require.False(t, clock.isHeld())

	// releasing more often than holding must not unbalance the counter
	clock.release()
	require.False(t, clock.isHeld())
}

func TestAwaitStateLoadsHoldsUntilTheReadFinishes(t *testing.T) {
	replay := testConsoleReplay(100)
	replay.clock.setRate(50)

	end := replay.states.begin()

	released := make(chan struct{})

	// the gate only holds once the replay is more than stateLoadLeadSlots ahead of the
	// slot at which the read started
	replay.stateLeadFrom = 100

	go func() {
		defer close(released)
		require.NoError(t, replay.awaitStateLoads(context.Background(), 100+stateLoadLeadSlots))
	}()

	require.Eventually(t, func() bool { return replay.clock.isHeld() }, 5*time.Second, 5*time.Millisecond,
		"the gate must freeze the clock while a state is loading")

	frozen := replay.clock.now()

	// the gate must keep holding, not return
	time.Sleep(50 * time.Millisecond)

	select {
	case <-released:
		t.Fatal("the gate returned while a state was still loading")
	default:
	}

	require.True(t, replay.clock.isHeld())
	require.True(t, replay.Status().Holding)
	require.Equal(t, 1, replay.Status().StateLoads)
	require.Equal(t, frozen, replay.clock.now(), "the 50ms wait must have cost no virtual time")

	end()

	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate did not release when the state load finished")
	}

	require.False(t, replay.clock.isHeld())
	require.False(t, replay.Status().Holding)
}

func TestAwaitStateLoadsReturnsImmediatelyWhenIdle(t *testing.T) {
	replay := testConsoleReplay(100)

	done := make(chan struct{})

	go func() {
		defer close(done)
		require.NoError(t, replay.awaitStateLoads(context.Background(), 100))
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the gate must not wait when no state is loading")
	}

	require.False(t, replay.clock.isHeld())
}

func TestAwaitStateLoadsGivesUpAfterTheTimeout(t *testing.T) {
	replay := testConsoleReplay(100)
	replay.cfg.StateHoldTimeout = 50 * time.Millisecond

	replay.states.begin() // never finishes
	replay.stateLeadFrom = 100

	done := make(chan struct{})

	go func() {
		defer close(done)
		require.NoError(t, replay.awaitStateLoads(context.Background(), 100+stateLoadLeadSlots))
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stuck state load must not wedge the replay for good")
	}

	require.False(t, replay.clock.isHeld(), "the hold must be lifted when the gate gives up")
}

func TestAwaitStateLoadsStopsOnShutdown(t *testing.T) {
	replay := testConsoleReplay(100)
	replay.states.begin() // never finishes
	replay.stateLeadFrom = 100

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)

	go func() { done <- replay.awaitStateLoads(ctx, 100+stateLoadLeadSlots) }()

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the gate must give up when the replay shuts down")
	}
}

func requireClosed(t *testing.T, channel <-chan struct{}, message string) {
	t.Helper()

	select {
	case <-channel:
	default:
		t.Fatalf("expected an idle (closed) channel: %v", message)
	}
}

func requireOpen(t *testing.T, channel <-chan struct{}, message string) {
	t.Helper()

	select {
	case <-channel:
		t.Fatalf("expected a busy (open) channel: %v", message)
	default:
	}
}

// TestStateLoadGateLetsTheReplayRunOnBriefly is the behaviour that keeps blocks flowing
// during a multi-second state read: the explorer indexes blocks on a different goroutine
// than it loads states on, so the replay serves a few more slots before it holds.
func TestStateLoadGateLetsTheReplayRunOnBriefly(t *testing.T) {
	replay := testConsoleReplay(100)
	replay.clock.setRate(10)

	replay.states.begin() // never finishes

	for slot := uint64(100); slot < 100+stateLoadLeadSlots; slot++ {
		done := make(chan struct{})

		go func() {
			defer close(done)
			require.NoError(t, replay.awaitStateLoads(context.Background(), slot))
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("the gate must not hold at slot %v, only %v slots into the read", slot, slot-100)
		}

		require.False(t, replay.clock.isHeld(), "the clock must keep running within the lead window")
	}

	// beyond the lead window it holds
	held := make(chan struct{})

	go func() {
		defer close(held)
		require.NoError(t, replay.awaitStateLoads(context.Background(), 100+stateLoadLeadSlots))
	}()

	require.Eventually(t, func() bool { return replay.clock.isHeld() }, 5*time.Second, 5*time.Millisecond,
		"the gate must hold once the replay would get further ahead than the lead window")
}

// TestStateLoadGateResetsBetweenReads guards that the lead window is per read, not
// cumulative: a finished read must not leave the next one starting mid-window.
func TestStateLoadGateResetsBetweenReads(t *testing.T) {
	replay := testConsoleReplay(100)

	end := replay.states.begin()
	require.NoError(t, replay.awaitStateLoads(context.Background(), 100))
	require.Equal(t, uint64(100), replay.stateLeadFrom)

	end()
	require.NoError(t, replay.awaitStateLoads(context.Background(), 101))
	require.Zero(t, replay.stateLeadFrom, "an idle tracker must clear the lead window")

	replay.states.begin()
	require.NoError(t, replay.awaitStateLoads(context.Background(), 200))
	require.Equal(t, uint64(200), replay.stateLeadFrom, "the next read starts its own window")
}
