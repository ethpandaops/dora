package rpc

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/donovanhide/eventsource"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
)

type BeaconStream struct {
	runMutex     sync.Mutex
	running      bool
	endpoint     string
	killChan     chan bool
	CloseChan    chan bool
	BlockChan    chan *rpctypes.StandardV1StreamedBlockEvent
	HeadChan     chan *rpctypes.StandardV1StreamedHeadEvent
	lastHeadSeen time.Time
}

func (bc *BeaconClient) NewBlockStream() *BeaconStream {
	blockStream := BeaconStream{
		running:   true,
		endpoint:  bc.endpoint,
		killChan:  make(chan bool),
		CloseChan: make(chan bool),
		BlockChan: make(chan *rpctypes.StandardV1StreamedBlockEvent, 10),
		HeadChan:  make(chan *rpctypes.StandardV1StreamedHeadEvent, 10),
	}
	go blockStream.startStream(bc.endpoint)

	return &blockStream
}

func (bs *BeaconStream) Start() {
	if bs.running {
		return
	}
	bs.running = true
	go bs.startStream(bs.endpoint)
}

func (bs *BeaconStream) Close() {
	if bs.running {
		bs.running = false
		bs.killChan <- true
	}
	bs.runMutex.Lock()
	defer bs.runMutex.Unlock()
}

func (bs *BeaconStream) startStream(endpoint string) {
	bs.runMutex.Lock()
	defer bs.runMutex.Unlock()

	stream := bs.subscribeStream(endpoint)
	if stream != nil {
		running := true
		for running {
			select {
			case evt := <-stream.Events:
				logger.Debugf("Event received from rpc event stream: %v", evt.Event())
				if evt.Event() == "block" {
					bs.processBlockEvent(evt)
				} else if evt.Event() == "head" {
					bs.processHeadEvent(evt)
				}
			case <-bs.killChan:
				running = false
			case <-time.After(300 * time.Second):
				// timeout - no block since 5 mins
				logger.Errorf("beacon block stream error, no new head retrieved since %v (%v ago)", bs.lastHeadSeen, time.Since(bs.lastHeadSeen))
				stream.Close()
				stream = bs.subscribeStream(endpoint)
				if stream == nil {
					running = false
				}
			}
		}
	}
	if stream != nil {
		stream.Close()
	}
	bs.running = false
	bs.CloseChan <- true
}

func (bs *BeaconStream) subscribeStream(endpoint string) *eventsource.Stream {
	for {
		stream, err := eventsource.Subscribe(fmt.Sprintf("%s/eth/v1/events?topics=block,head", endpoint), "")
		if err != nil {
			logger.Errorf("Error while subscribing beacon event stream: %v", err)
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
	bs.BlockChan <- &parsed
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
	bs.HeadChan <- &parsed
}
