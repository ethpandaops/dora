package replay

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestEventHubFiltersByTopic(t *testing.T) {
	hub := newEventHub(logrus.New())

	blocks := newEventSubscriber(map[string]bool{"block": true})
	heads := newEventSubscriber(map[string]bool{"head": true})

	hub.add(blocks)
	hub.add(heads)

	require.Equal(t, 2, hub.subscriberCount())

	hub.publish(sseEvent{topic: "block", data: []byte(`{"slot":"1"}`)})

	require.Len(t, blocks.events, 1)
	require.Empty(t, heads.events, "a subscriber must not receive topics it did not ask for")

	hub.remove(blocks)
	require.Equal(t, 1, hub.subscriberCount())
}

func TestEventStreamServesSSE(t *testing.T) {
	hub := newEventHub(logrus.New())

	server := httptest.NewServer(http.HandlerFunc(hub.serveHTTP))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"?topics=head,block", http.NoBody)
	require.NoError(t, err)

	rsp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = rsp.Body.Close() }()

	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Equal(t, "text/event-stream", rsp.Header.Get("Content-Type"))

	require.Eventually(t, func() bool { return hub.subscriberCount() == 1 }, 5*time.Second, 5*time.Millisecond)

	hub.publish(sseEvent{topic: "head", data: []byte(`{"slot":"42"}`)})

	reader := bufio.NewReader(rsp.Body)

	eventLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "event: head", strings.TrimSpace(eventLine))

	dataLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, `data: {"slot":"42"}`, strings.TrimSpace(dataLine))
}

func TestEventStreamRejectsEmptyTopics(t *testing.T) {
	hub := newEventHub(logrus.New())

	recorder := httptest.NewRecorder()
	hub.serveHTTP(recorder, httptest.NewRequest(http.MethodGet, "/eth/v1/events", http.NoBody))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
