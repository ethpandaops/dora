package rpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/donovanhide/eventsource"

	"github.com/pk910/light-beaconchain-explorer/rpc/eventstream"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

const (
	StreamBlockEvent     uint16 = 0x01
	StreamHeadEvent      uint16 = 0x02
	StreamFinalizedEvent uint16 = 0x04
)

type BeaconStreamEvent struct {
	Event uint16
	Data  interface{}
}

type BeaconStream struct {
	runMutex     sync.Mutex
	running      bool
	ready        bool
	events       uint16
	client       *BeaconClient
	killChan     chan bool
	ReadyChan    chan bool
	EventChan    chan *BeaconStreamEvent
	lastHeadSeen time.Time
}

func (bc *BeaconClient) NewBlockStream(events uint16) *BeaconStream {
	blockStream := BeaconStream{
		running:   true,
		events:    events,
		client:    bc,
		killChan:  make(chan bool),
		ReadyChan: make(chan bool, 10),
		EventChan: make(chan *BeaconStreamEvent, 10),
	}
	go blockStream.startStream()

	return &blockStream
}

func (bs *BeaconStream) Start() {
	if bs.running {
		return
	}
	bs.running = true
	go bs.startStream()
}

func (bs *BeaconStream) Close() {
	if bs.running {
		bs.running = false
		bs.killChan <- true
	}
	bs.runMutex.Lock()
	defer bs.runMutex.Unlock()
}

func (bs *BeaconStream) startStream() {
	bs.runMutex.Lock()
	defer bs.runMutex.Unlock()

	stream := bs.subscribeStream(bs.client.endpoint, bs.events)
	if stream != nil {
		bs.ready = true
		running := true
		for running {
			select {
			case evt := <-stream.Events:
				if evt.Event() == "block" {
					bs.processBlockEvent(evt)
				} else if evt.Event() == "head" {
					bs.processHeadEvent(evt)
				} else if evt.Event() == "finalized_checkpoint" {
					bs.processFinalizedEvent(evt)
				}
			case <-bs.killChan:
				running = false
			case <-stream.Ready:
				bs.ReadyChan <- true
			case err := <-stream.Errors:
				logger.WithField("client", bs.client.name).Warnf("beacon block stream error: %v", err)
				bs.ReadyChan <- false
			}
		}
	}
	if stream != nil {
		stream.Close()
	}
	bs.running = false
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

	for {
		url := fmt.Sprintf("%s/eth/v1/events?topics=%v", endpoint, topics.String())
		req, err := http.NewRequest("GET", url, nil)
		var stream *eventstream.Stream
		if err == nil {
			for headerKey, headerVal := range bs.client.headers {
				req.Header.Set(headerKey, headerVal)
			}
			stream, err = eventstream.SubscribeWithRequest("", req)
		}
		if err != nil {
			logger.WithField("client", bs.client.name).Warnf("Error while subscribing beacon event stream %v: %v", utils.GetRedactedUrl(url), err)
			select {
			case <-bs.killChan:
				return nil
			case <-time.After(10 * time.Second):
			}
		} else {
			return stream
		}
	}
}

func (bs *BeaconStream) processBlockEvent(evt eventsource.Event) {
	var parsed rpctypes.StandardV1StreamedBlockEvent
	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		logger.WithField("client", bs.client.name).Warnf("beacon block stream failed to decode block event: %v", err)
		return
	}
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamBlockEvent,
		Data:  &parsed,
	}
}

func (bs *BeaconStream) processHeadEvent(evt eventsource.Event) {
	var parsed rpctypes.StandardV1StreamedHeadEvent
	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		logger.WithField("client", bs.client.name).Warnf("beacon block stream failed to decode block event: %v", err)
		return
	}
	bs.lastHeadSeen = time.Now()
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamHeadEvent,
		Data:  &parsed,
	}
}

func (bs *BeaconStream) processFinalizedEvent(evt eventsource.Event) {
	var parsed rpctypes.StandardV1StreamedFinalizedCheckpointEvent
	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		logger.WithField("client", bs.client.name).Warnf("beacon block stream failed to decode finalized_checkpoint event: %v", err)
		return
	}
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamFinalizedEvent,
		Data:  &parsed,
	}
}
