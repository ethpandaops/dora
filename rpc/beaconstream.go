package rpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	eth2client "github.com/attestantio/go-eth2-client"
	v1 "github.com/attestantio/go-eth2-client/api/v1"
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

	provider, isProvider := bs.client.clientSvc.(eth2client.EventsProvider)
	if !isProvider {
		logger.WithField("client", bs.client.name).Warnf("beacon block stream error: event subscriptions not supported")
		bs.running = false
		return
	}

	topics := []string{}
	if bs.events&StreamBlockEvent > 0 {
		topics = append(topics, "block")
	}
	if bs.events&StreamHeadEvent > 0 {
		topics = append(topics, "head")
	}
	if bs.events&StreamFinalizedEvent > 0 {
		topics = append(topics, "finalized_checkpoint")
	}

	for bs.running {
		err := bs.subscribeStream(provider, topics)
		if err != nil {
			logger.WithField("client", bs.client.name).Warnf("beacon block stream error: %v", err)
			bs.ReadyChan <- false
		}
		time.Sleep(2 * time.Second)
	}
}

func (bs *BeaconStream) subscribeStream(provider eth2client.EventsProvider, topics []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := provider.Events(ctx, topics, func(evt *v1.Event) {
		if evt.Topic == "block" {
			bs.processBlockEvent(evt)
		} else if evt.Topic == "head" {
			bs.processHeadEvent(evt)
		} else if evt.Topic == "finalized_checkpoint" {
			bs.processFinalizedEvent(evt)
		}
	})
	return err
}

func (bs *BeaconStream) processBlockEvent(evt *v1.Event) error {
	block, valid := evt.Data.(*v1.BlockEvent)
	if !valid {
		return fmt.Errorf("invalid block event")
	}
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamBlockEvent,
		Data:  block,
	}
	return nil
}

func (bs *BeaconStream) processHeadEvent(evt *v1.Event) error {
	head, valid := evt.Data.(*v1.HeadEvent)
	if !valid {
		return fmt.Errorf("invalid head event")
	}
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamHeadEvent,
		Data:  head,
	}
	return nil
}

func (bs *BeaconStream) processFinalizedEvent(evt *v1.Event) error {
	checkpoint, valid := evt.Data.(*v1.FinalizedCheckpointEvent)
	if !valid {
		return fmt.Errorf("invalid finalized_checkpoint event")
	}
	bs.EventChan <- &BeaconStreamEvent{
		Event: StreamFinalizedEvent,
		Data:  checkpoint,
	}
	return nil
}
