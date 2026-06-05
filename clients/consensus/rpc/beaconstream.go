package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus/rpc/eventstream"
)

const (
	StreamBlockEvent               uint16 = 0x01
	StreamHeadEvent                uint16 = 0x02
	StreamFinalizedEvent           uint16 = 0x04
	StreamExecutionPayloadEvent    uint16 = 0x08
	StreamExecutionPayloadBidEvent uint16 = 0x10
	StreamInclusionListEvent       uint16 = 0x20
)

type BeaconStreamEvent struct {
	Event uint16
	Data  interface{}
}

type BeaconStreamStatus struct {
	Ready bool
	Error error
}

type BeaconStream struct {
	ctx              context.Context
	ctxCancel        context.CancelFunc
	logger           logrus.FieldLogger
	running          bool
	events           uint16
	client           *BeaconClient
	ReadyChan        chan *BeaconStreamStatus
	EventChan        chan *BeaconStreamEvent
	lastHeadSeen     time.Time
	ancillaryLock    sync.Mutex
	ancillaryStarted uint16
}

func (bc *BeaconClient) NewBlockStream(ctx context.Context, logger logrus.FieldLogger, events uint16) *BeaconStream {
	streamCtx, ctxCancel := context.WithCancel(ctx)

	blockStream := BeaconStream{
		ctx:       streamCtx,
		ctxCancel: ctxCancel,
		logger:    logger,
		running:   true,
		events:    events,
		client:    bc,
		ReadyChan: make(chan *BeaconStreamStatus, 10),
		EventChan: make(chan *BeaconStreamEvent, 10),
	}
	go blockStream.startStream()

	return &blockStream
}

func (bs *BeaconStream) Close() {
	bs.ctxCancel()
}

func (bs *BeaconStream) startStream() {
	defer func() {
		bs.running = false
	}()

	basicEvents := bs.events & (StreamBlockEvent | StreamHeadEvent | StreamFinalizedEvent)
	basicStream := bs.subscribeStream(bs.client.endpoint, basicEvents)
	if basicStream == nil {
		return
	}
	defer basicStream.Close()

	bs.ensureAncillaryStreams(bs.events)

	for {
		select {
		case <-bs.ctx.Done():
			return
		case evt := <-basicStream.Events:
			switch evt.Event() {
			case "block":
				bs.processBlockEvent(evt)
			case "head":
				bs.processHeadEvent(evt)
			case "finalized_checkpoint":
				bs.processFinalizedEvent(evt)
			}
		case <-basicStream.Ready:
			bs.ReadyChan <- &BeaconStreamStatus{
				Ready: true,
			}
		case err := <-basicStream.Errors:
			bs.handleStreamError(basicStream, err)
		}
	}
}

// UpdateEvents opens SSE requests for any new event bits without restarting
// already-running streams. Additive only.
func (bs *BeaconStream) UpdateEvents(events uint16) {
	bs.ancillaryLock.Lock()
	bs.events |= events
	bs.ancillaryLock.Unlock()

	bs.ensureAncillaryStreams(events)
}

func (bs *BeaconStream) ensureAncillaryStreams(events uint16) {
	gloasEvents := events & (StreamExecutionPayloadEvent | StreamExecutionPayloadBidEvent)
	bs.startAncillaryStream(gloasEvents)

	hezeEvents := events & StreamInclusionListEvent
	bs.startAncillaryStream(hezeEvents)
}

func (bs *BeaconStream) startAncillaryStream(events uint16) {
	if events == 0 {
		return
	}

	bs.ancillaryLock.Lock()
	if bs.ancillaryStarted&events == events {
		bs.ancillaryLock.Unlock()
		return
	}
	bs.ancillaryStarted |= events
	bs.ancillaryLock.Unlock()

	go bs.runAncillaryStream(events)
}

func (bs *BeaconStream) runAncillaryStream(events uint16) {
	stream := bs.subscribeStream(bs.client.endpoint, events)
	if stream == nil {
		return
	}
	defer stream.Close()

	for {
		select {
		case <-bs.ctx.Done():
			return
		case evt := <-stream.Events:
			switch evt.Event() {
			case "execution_payload_available":
				bs.processExecutionPayloadAvailableEvent(evt)
			case "execution_payload_bid":
				bs.processExecutionPayloadBidEvent(evt)
			case "inclusion_list":
				bs.processInclusionListEvent(evt)
			}
		case <-stream.Ready:
		case <-stream.Errors:
			time.Sleep(10 * time.Millisecond)
			stream.RetryNow()
		}
	}
}

// handleStreamError handles stream errors and forwards them to the ReadyChan.
func (bs *BeaconStream) handleStreamError(stream *eventstream.Stream, err error) {
	if strings.Contains(err.Error(), "INTERNAL_ERROR; received from peer") {
		// this seems to be a go bug, silently reconnect to the stream
		time.Sleep(10 * time.Millisecond)
		stream.RetryNow()
	} else {
		bs.logger.Warnf("beacon block stream error: %v", err)
	}

	select {
	case bs.ReadyChan <- &BeaconStreamStatus{
		Ready: false,
		Error: err,
	}:
	case <-bs.ctx.Done():
	}
}

func (bs *BeaconStream) subscribeStream(endpoint string, events uint16) *eventstream.Stream {
	var topics strings.Builder

	topicsCount := 0

	if events&StreamBlockEvent > 0 {
		if topicsCount > 0 {
			fmt.Fprintf(&topics, ",")
		}

		fmt.Fprintf(&topics, "block")

		topicsCount++
	}

	if events&StreamHeadEvent > 0 {
		if topicsCount > 0 {
			fmt.Fprintf(&topics, ",")
		}

		fmt.Fprintf(&topics, "head")

		topicsCount++
	}

	if events&StreamFinalizedEvent > 0 {
		if topicsCount > 0 {
			fmt.Fprintf(&topics, ",")
		}

		fmt.Fprintf(&topics, "finalized_checkpoint")

		topicsCount++
	}

	if events&StreamExecutionPayloadEvent > 0 {
		if topicsCount > 0 {
			fmt.Fprintf(&topics, ",")
		}

		fmt.Fprintf(&topics, "execution_payload_available")

		topicsCount++
	}

	if events&StreamExecutionPayloadBidEvent > 0 {
		if topicsCount > 0 {
			fmt.Fprintf(&topics, ",")
		}

		fmt.Fprintf(&topics, "execution_payload_bid")

		topicsCount++
	}

	if events&StreamInclusionListEvent > 0 {
		if topicsCount > 0 {
			fmt.Fprintf(&topics, ",")
		}

		fmt.Fprintf(&topics, "inclusion_list")

		topicsCount++
	}

	if topicsCount == 0 {
		return nil
	}

	for {
		var stream *eventstream.Stream

		streamURL := fmt.Sprintf("%s/eth/v1/events?topics=%v", endpoint, topics.String())
		req, err := http.NewRequestWithContext(bs.ctx, "GET", streamURL, http.NoBody)

		if err == nil {
			for headerKey, headerVal := range bs.client.headers {
				req.Header.Set(headerKey, headerVal)
			}

			stream, err = eventstream.SubscribeWithRequest("", req)
		}

		if err != nil {
			bs.logger.Warnf("Error while subscribing beacon event stream %v: %v", getRedactedURL(streamURL), err)
			select {
			case <-bs.ctx.Done():
				return nil
			case <-time.After(10 * time.Second):
			}
		} else {
			return stream
		}
	}
}

func (bs *BeaconStream) processBlockEvent(evt eventstream.StreamEvent) {
	var parsed v1.BlockEvent

	err := json.Unmarshal([]byte(evt.Data()), &parsed)

	if err != nil {
		bs.logger.Warnf("beacon block stream failed to decode block event: %v", err)
		return
	}
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamBlockEvent,
		Data:  &parsed,
	}
}

func (bs *BeaconStream) processHeadEvent(evt eventstream.StreamEvent) {
	var parsed v1.HeadEvent

	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		bs.logger.Warnf("beacon block stream failed to decode head event: %v", err)
		return
	}

	bs.lastHeadSeen = time.Now()
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamHeadEvent,
		Data:  &parsed,
	}
}

func (bs *BeaconStream) processFinalizedEvent(evt eventstream.StreamEvent) {
	var parsed v1.FinalizedCheckpointEvent

	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		bs.logger.Warnf("beacon block stream failed to decode finalized_checkpoint event: %v", err)
		return
	}

	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamFinalizedEvent,
		Data:  &parsed,
	}
}

func (bs *BeaconStream) processExecutionPayloadAvailableEvent(evt eventstream.StreamEvent) {
	var parsed v1.ExecutionPayloadAvailableEvent

	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		bs.logger.Warnf("beacon block stream failed to decode execution_payload event: %v", err)
		return
	}

	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamExecutionPayloadEvent,
		Data:  &parsed,
	}
}

func (bs *BeaconStream) processExecutionPayloadBidEvent(evt eventstream.StreamEvent) {
	var parsed gloas.SignedExecutionPayloadBid

	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		bs.logger.Warnf("beacon block stream failed to decode execution_payload_bid event: %v", err)
		return
	}

	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamExecutionPayloadBidEvent,
		Data:  &parsed,
	}
}

func (bs *BeaconStream) processInclusionListEvent(evt eventstream.StreamEvent) {
	var parsed v1.InclusionListEvent

	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		bs.logger.Warnf("beacon block stream failed to decode inclusion_list event: %v", err)
		return
	}

	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamInclusionListEvent,
		Data:  &parsed,
	}
}

func getRedactedURL(requrl string) string {
	var logurl string

	urlData, _ := url.Parse(requrl)
	if urlData != nil {
		logurl = urlData.Redacted()
	} else {
		logurl = requrl
	}

	return logurl
}
