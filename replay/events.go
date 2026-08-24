package replay

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// sseEvent is one entry of the beacon node event stream.
type sseEvent struct {
	topic string
	data  []byte
}

// eventHub is the fake node's /eth/v1/events endpoint: a fan-out of synthesized chain
// events to every connected subscriber, filtered by the topics it asked for.
type eventHub struct {
	logger logrus.FieldLogger

	// lossy hubs drop an event for a subscriber whose buffer is full instead of
	// waiting for it. Chain events must never be lost, but status snapshots should
	// never hold the replay up either.
	lossy bool

	mutex       sync.Mutex
	subscribers map[*eventSubscriber]struct{}
}

type eventSubscriber struct {
	topics map[string]bool
	events chan sseEvent

	// done is closed when the subscriber disconnects, so a publisher waiting on a
	// slow reader is released instead of blocking forever.
	done chan struct{}
}

func newEventSubscriber(topics map[string]bool) *eventSubscriber {
	return &eventSubscriber{
		topics: topics,
		events: make(chan sseEvent, 256),
		done:   make(chan struct{}),
	}
}

func newEventHub(logger logrus.FieldLogger) *eventHub {
	return &eventHub{
		logger:      logger,
		subscribers: make(map[*eventSubscriber]struct{}),
	}
}

// newLossyEventHub returns a hub that never blocks its publisher.
func newLossyEventHub(logger logrus.FieldLogger) *eventHub {
	hub := newEventHub(logger)
	hub.lossy = true

	return hub
}

// publish delivers an event to every subscriber that asked for its topic. It waits for
// a subscriber that is behind rather than dropping the event: the replay sets the pace,
// so a slow explorer should slow the replay down, not silently lose a block. This is
// what makes "advance as fast as upstream allows" self-throttle to what the explorer
// can actually index.
func (h *eventHub) publish(event sseEvent) {
	h.mutex.Lock()
	targets := make([]*eventSubscriber, 0, len(h.subscribers))

	for sub := range h.subscribers {
		if sub.topics[event.topic] {
			targets = append(targets, sub)
		}
	}
	h.mutex.Unlock()

	for _, sub := range targets {
		if h.lossy {
			select {
			case sub.events <- event:
			default:
			}

			continue
		}

		select {
		case sub.events <- event:
		case <-sub.done:
			h.logger.Debugf("subscriber disconnected while waiting to take a %v event", event.topic)
		}
	}
}

// subscriberCount reports how many event streams are currently connected.
func (h *eventHub) subscriberCount() int {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	return len(h.subscribers)
}

func (h *eventHub) add(sub *eventSubscriber) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.subscribers[sub] = struct{}{}
}

func (h *eventHub) remove(sub *eventSubscriber) {
	h.mutex.Lock()
	delete(h.subscribers, sub)
	h.mutex.Unlock()

	close(sub.done)
}

// serveHTTP implements the /eth/v1/events endpoint. Every requested topic is accepted;
// topics the replay cannot synthesize (inclusion lists, fast confirmations) simply
// never fire, which clients already tolerate.
func (h *eventHub) serveHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		writeAPIError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	topics := map[string]bool{}
	for _, topic := range strings.Split(r.URL.Query().Get("topics"), ",") {
		if topic = strings.TrimSpace(topic); topic != "" {
			topics[topic] = true
		}
	}

	if len(topics) == 0 {
		if !h.lossy {
			writeAPIError(w, http.StatusBadRequest, "no topics requested")
			return
		}

		// the control stream carries one topic, so asking for it is optional
		topics["status"] = true
	}

	sub := newEventSubscriber(topics)

	h.add(sub)
	defer h.remove(sub)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ":keepalive\n\n"); err != nil {
				return
			}

			flusher.Flush()
		case event := <-sub.events:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.topic, event.data); err != nil {
				return
			}

			flusher.Flush()
		}
	}
}
