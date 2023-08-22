package rpc

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/donovanhide/eventsource"

	"github.com/pk910/light-beaconchain-explorer/rpc/eventstream"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
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
	endpoint     string
	killChan     chan bool
	CloseChan    chan bool
	ReadyChan    chan bool
	EventChan    chan *BeaconStreamEvent
	lastHeadSeen time.Time
}

func (bc *BeaconClient) NewBlockStream(events uint16) *BeaconStream {
	blockStream := BeaconStream{
		running:   true,
		events:    events,
		endpoint:  bc.endpoint,
		killChan:  make(chan bool),
		CloseChan: make(chan bool),
		ReadyChan: make(chan bool),
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

	errorChan := make(chan error)
	stream := bs.subscribeStream(bs.endpoint, bs.events)
	if stream != nil {
		bs.ready = true
		bs.ReadyChan <- true
		running := true
		for running {
			select {
			case evt := <-stream.Events:
				logger.Debugf("Event received from rpc event stream: %v", evt.Event())
				if evt.Event() == "block" {
					bs.processBlockEvent(evt)
				} else if evt.Event() == "head" {
					bs.processHeadEvent(evt)
				} else if evt.Event() == "finalized_checkpoint" {
					bs.processFinalizedEvent(evt)
				}
			case <-bs.killChan:
				running = false
			case err := <-stream.Errors:
				logger.Errorf("beacon block stream error: %v", err)
			case err := <-errorChan:
				logger.Errorf("beacon block stream error: %v", err)
			case <-time.After(60 * time.Second):
				// timeout - no block since 5 mins
				logger.Errorf("beacon block stream error, no new head retrieved since %v (%v ago)", bs.lastHeadSeen, time.Since(bs.lastHeadSeen))
				stream.Close()
				stream.Errors = errorChan
				bs.ready = false
				bs.ReadyChan <- false
				stream = bs.subscribeStream(bs.endpoint, bs.events)
				if stream == nil {
					running = false
				}
			}
		}
	}
	if stream != nil {
		stream.Close()
	}
	if bs.ready {
		bs.ready = false
		bs.ReadyChan <- false
	}
	bs.running = false
	bs.CloseChan <- true
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
		stream, err := eventstream.Subscribe(url, "")
		if err != nil {
			logger.Errorf("Error while subscribing beacon event stream %v: %v", url, err)
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
		logger.Warnf("beacon block stream failed to decode block event: %v", err)
		return
	}
	logger.Debugf("RPC block event! slot: %v, block: %v", parsed.Slot, parsed.Block)
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamBlockEvent,
		Data:  &parsed,
	}
}

func (bs *BeaconStream) processHeadEvent(evt eventsource.Event) {
	var parsed rpctypes.StandardV1StreamedHeadEvent
	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		logger.Warnf("beacon block stream failed to decode block event: %v", err)
		return
	}
	logger.Debugf("RPC head event! slot: %v, block: %v, state: %v", parsed.Slot, parsed.Block, parsed.State)
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
		logger.Warnf("beacon block stream failed to decode finalized_checkpoint event: %v", err)
		return
	}
	logger.Debugf("RPC finalized_checkpoint event! epoch: %v, block: %v, state: %v", parsed.Epoch, parsed.Block, parsed.State)
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamFinalizedEvent,
		Data:  &parsed,
	}
}
