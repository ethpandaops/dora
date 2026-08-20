package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const (
	testGenesisUnix   = 1700000000
	testSlotsPerEpoch = 32
	testSlotSeconds   = 12
)

// fakeBeacon is a minimal beacon node serving a synthetic chain, used to drive the
// replay end to end without a real upstream.
type fakeBeacon struct {
	// emptySlots are slots that carry no block.
	emptySlots map[uint64]bool

	// finalizedEpoch is what every finality read reports.
	finalizedEpoch uint64
}

func (f *fakeBeacon) rootOf(slot uint64) string {
	return fmt.Sprintf("0x%064x", slot)
}

func (f *fakeBeacon) stateRootOf(slot uint64) string {
	return fmt.Sprintf("0x%064x", slot+1_000_000)
}

func (f *fakeBeacon) start() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/eth/v1/beacon/genesis", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{"genesis_time": strconv.Itoa(testGenesisUnix)},
		})
	})

	mux.HandleFunc("/eth/v1/config/spec", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]string{
				"SECONDS_PER_SLOT": strconv.Itoa(testSlotSeconds),
				"SLOTS_PER_EPOCH":  strconv.Itoa(testSlotsPerEpoch),
			},
		})
	})

	mux.HandleFunc("/eth/v1/beacon/headers/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/eth/v1/beacon/headers/")

		slot, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			// resolve by root, which the synthetic chain encodes as the slot number
			parsed, parseErr := strconv.ParseUint(strings.TrimLeft(strings.TrimPrefix(id, "0x"), "0"), 16, 64)
			if parseErr != nil {
				writeAPIError(w, http.StatusNotFound, "not found")
				return
			}

			slot = parsed
		}

		if f.emptySlots[slot] {
			writeAPIError(w, http.StatusNotFound, "not found")
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"root": f.rootOf(slot),
				"header": map[string]any{
					"message": map[string]any{
						"slot":        strconv.FormatUint(slot, 10),
						"parent_root": f.rootOf(slot - 1),
						"state_root":  f.stateRootOf(slot),
					},
				},
			},
		})
	})

	mux.HandleFunc("/eth/v1/beacon/states/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"current_justified": map[string]any{
					"epoch": strconv.FormatUint(f.finalizedEpoch+1, 10),
					"root":  f.rootOf(1),
				},
				"finalized": map[string]any{
					"epoch": strconv.FormatUint(f.finalizedEpoch, 10),
					"root":  f.rootOf(2),
				},
			},
		})
	})

	mux.HandleFunc("/eth/v2/beacon/blocks/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"message": map[string]any{
					"body": map[string]any{
						"signed_execution_payload_bid": map[string]any{"message": map[string]any{"slot": "1"}},
					},
				},
			},
		})
	})

	return httptest.NewServer(mux)
}

func newTestReplay(t *testing.T, beacon *fakeBeacon, startSlot uint64) (*Replay, *httptest.Server) {
	t.Helper()

	server := beacon.start()
	t.Cleanup(server.Close)

	cfg := DefaultConfig()
	cfg.UpstreamURL = server.URL
	cfg.StartSlot = startSlot
	cfg.EmitBids = false
	cfg.StepSettle = time.Millisecond

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	replay, err := New(context.Background(), logger, cfg)
	require.NoError(t, err)

	return replay, server
}

// collectEvents subscribes to the hub the way an event stream client would.
func collectEvents(replay *Replay, topics ...string) *eventSubscriber {
	filter := make(map[string]bool, len(topics))
	for _, topic := range topics {
		filter[topic] = true
	}

	sub := newEventSubscriber(filter)
	replay.events.add(sub)

	return sub
}

func TestNewResolvesHeadAtStartSlot(t *testing.T) {
	beacon := &fakeBeacon{emptySlots: map[uint64]bool{100: true, 99: true}, finalizedEpoch: 2}

	replay, _ := newTestReplay(t, beacon, 100)

	require.Equal(t, uint64(100), replay.virtualSlot)
	require.NotNil(t, replay.head)
	require.Equal(t, uint64(98), replay.head.Slot, "head must be the newest block at or before the start slot")
	require.Equal(t, uint64(2), replay.finality.FinalizedEpoch)
}

func TestStepEmitsBlockAndHead(t *testing.T) {
	beacon := &fakeBeacon{emptySlots: map[uint64]bool{}, finalizedEpoch: 2}

	replay, _ := newTestReplay(t, beacon, 100)
	sub := collectEvents(replay, "block", "head")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go replay.runDriver(ctx)
	replay.Step(1)

	blockEvent := awaitEvent(t, sub, "block")
	require.Equal(t, "101", jsonField(t, blockEvent.data, "slot"))
	require.Equal(t, beacon.rootOf(101), jsonField(t, blockEvent.data, "block"))

	headEvent := awaitEvent(t, sub, "head")
	require.Equal(t, "101", jsonField(t, headEvent.data, "slot"))
	require.Equal(t, beacon.stateRootOf(101), jsonField(t, headEvent.data, "state"))

	requireEventually(t, func() bool { return !replay.isRunning() }, "replay should pause after the step")
	require.Equal(t, uint64(101), replay.Status().VirtualSlot)
	require.Equal(t, uint64(101), replay.Status().HeadSlot)
}

func TestEmptySlotAdvancesWithoutEvents(t *testing.T) {
	beacon := &fakeBeacon{emptySlots: map[uint64]bool{101: true}, finalizedEpoch: 2}

	replay, _ := newTestReplay(t, beacon, 100)
	sub := collectEvents(replay, "block", "head")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go replay.runDriver(ctx)
	replay.Step(1)

	requireEventually(t, func() bool { return !replay.isRunning() }, "replay should pause after the step")

	require.Equal(t, uint64(101), replay.Status().VirtualSlot)
	require.Equal(t, uint64(100), replay.Status().HeadSlot, "an empty slot must not move the head")
	require.Empty(t, sub.events, "an empty slot must not emit events")
}

func TestForwardRunsToTargetSlot(t *testing.T) {
	beacon := &fakeBeacon{emptySlots: map[uint64]bool{}, finalizedEpoch: 2}

	replay, _ := newTestReplay(t, beacon, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	go replay.runDriver(ctx)
	require.NoError(t, replay.Forward(110, 0))

	requireEventually(t, func() bool { return !replay.isRunning() }, "replay should pause at the target")
	require.Equal(t, uint64(110), replay.Status().VirtualSlot)
}

func TestVirtualClockTracksReplayedSlot(t *testing.T) {
	beacon := &fakeBeacon{emptySlots: map[uint64]bool{}, finalizedEpoch: 2}

	replay, _ := newTestReplay(t, beacon, 100)

	// before stepping, the clock sits inside the start slot
	require.Equal(t, uint64(100), slotOfVirtualTime(replay))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go replay.runDriver(ctx)
	replay.Step(3)

	requireEventually(t, func() bool { return !replay.isRunning() }, "replay should pause after the step")
	require.Equal(t, uint64(103), slotOfVirtualTime(replay))
}

func slotOfVirtualTime(replay *Replay) uint64 {
	elapsed := replay.clock.now().Sub(replay.chain.genesisTime)

	return uint64(elapsed / replay.chain.slotDuration)
}

func awaitEvent(t *testing.T, sub *eventSubscriber, topic string) sseEvent {
	t.Helper()

	deadline := time.After(10 * time.Second)

	for {
		select {
		case event := <-sub.events:
			if event.topic == topic {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for a %v event", topic)
		}
	}
}

func jsonField(t *testing.T, data []byte, field string) string {
	t.Helper()

	parsed := map[string]any{}
	require.NoError(t, json.Unmarshal(data, &parsed))

	value, ok := parsed[field].(string)
	require.Truef(t, ok, "field %v is missing or not a string in %s", field, data)

	return value
}

func requireEventually(t *testing.T, condition func() bool, message string) {
	t.Helper()

	require.Eventually(t, condition, 10*time.Second, 5*time.Millisecond, message)
}
