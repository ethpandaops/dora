package rpc

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/donovanhide/eventsource"
	logger "github.com/sirupsen/logrus"

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

	stream, err := eventsource.Subscribe(fmt.Sprintf("%s/eth/v1/events?topics=block,head", endpoint), "")
	if err != nil {
		logger.Errorf("Error while subscribing beacon block stream: %v", err)
	} else {
		defer stream.Close()

		running := true
		for running {
			select {
			case evt := <-stream.Events:
				if evt.Event() == "block" {
					bs.processBlockEvent(evt)
				} else if evt.Event() == "head" {
					bs.processHeadEvent(evt)
				}
			case <-bs.killChan:
				running = false
			case <-time.After(120 * time.Second):
				// timeout - no block since 2 mins
				logger.Errorf("beacon block stream error, no new head retrieved since %v (%v ago)", bs.lastHeadSeen, time.Since(bs.lastHeadSeen))
			}
		}
	}
	bs.running = false
	bs.CloseChan <- true
}

func (bs *BeaconStream) processBlockEvent(evt eventsource.Event) {
	var parsed rpctypes.StandardV1StreamedBlockEvent
	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		logger.Warnf("beacon block stream failed to decode block event: %v", err)
		return
	}
	bs.BlockChan <- &parsed
}

func (bs *BeaconStream) processHeadEvent(evt eventsource.Event) {
	var parsed rpctypes.StandardV1StreamedHeadEvent
	err := json.Unmarshal([]byte(evt.Data()), &parsed)
	if err != nil {
		logger.Warnf("beacon block stream failed to decode block event: %v", err)
		return
	}
	bs.lastHeadSeen = time.Now()
	bs.HeadChan <- &parsed
}
