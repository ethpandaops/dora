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
	runMutex      sync.Mutex
	running       bool
	closeChan     chan bool
	BlockChan     chan *rpctypes.StandardV1StreamedHeadEvent
	lastBlockSeen time.Time
}

func (bc *BeaconClient) NewBlockStream() *BeaconStream {
	blockStream := BeaconStream{
		running:   true,
		closeChan: make(chan bool),
		BlockChan: make(chan *rpctypes.StandardV1StreamedHeadEvent, 10),
	}
	go blockStream.startStream(bc.endpoint)

	return &blockStream
}

func (bs *BeaconStream) Close() {
	if bs.running {
		bs.running = false
		bs.closeChan <- true
	}
	bs.runMutex.Lock()
	defer bs.runMutex.Unlock()
}

func (bs *BeaconStream) startStream(endpoint string) {
	bs.runMutex.Lock()
	defer bs.runMutex.Unlock()

	stream, err := eventsource.Subscribe(fmt.Sprintf("%s/eth/v1/events?topics=head", endpoint), "")
	if err != nil {
		logger.Errorf("Error while subscribing beacon block stream: %v", err)
	} else {
		defer stream.Close()

		running := true
		for running {
			select {
			case blockEvt := <-stream.Events:
				var parsed rpctypes.StandardV1StreamedHeadEvent
				err = json.Unmarshal([]byte(blockEvt.Data()), &parsed)
				if err != nil {
					logger.Warnf("beacon block stream failed to decode block event: %v", err)
					continue
				}
				bs.lastBlockSeen = time.Now()
				bs.BlockChan <- &parsed
			case <-bs.closeChan:
				running = false
			case <-time.After(120 * time.Second):
				// timeout - no block since 2 mins
				logger.Errorf("beacon block stream error, no new block retrieved since %v (%v ago)", bs.lastBlockSeen, time.Since(bs.lastBlockSeen))

			}
		}
	}
	bs.running = false
}
