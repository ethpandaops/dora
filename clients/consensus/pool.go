package consensus

import (
	"context"
	"time"

	"math/rand/v2"

	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/ethwallclock"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/sirupsen/logrus"
)

// PeerScoresPersister is invoked by the per-client peer-scores poll
// loop after a successful fetch. The pool consumer (chain service) is
// expected to wire this when database persistence is enabled.
//
// Implementations must be non-blocking or short-lived; the poll loop
// invokes them synchronously after each tick.
type PeerScoresPersister func(client *Client, scores []*rpc.PeerScore)

type Pool struct {
	ctx                 context.Context
	logger              logrus.FieldLogger
	clientCounter       uint16
	clients             []*Client
	chainState          *ChainState
	peerScoresPersister PeerScoresPersister
}

func NewPool(ctx context.Context, logger logrus.FieldLogger) *Pool {
	return &Pool{
		ctx:        ctx,
		logger:     logger,
		clients:    make([]*Client, 0),
		chainState: newChainState(),
	}
}

func (pool *Pool) SubscribeFinalizedEvent(capacity int) *utils.Subscription[*v1.Finality] {
	return pool.chainState.checkpointDispatcher.Subscribe(capacity, false)
}

func (pool *Pool) SubscribeWallclockEpochEvent(capacity int) *utils.Subscription[*ethwallclock.Epoch] {
	return pool.chainState.wallclockEpochDispatcher.Subscribe(capacity, false)
}

func (pool *Pool) SubscribeWallclockSlotEvent(capacity int) *utils.Subscription[*ethwallclock.Slot] {
	return pool.chainState.wallclockSlotDispatcher.Subscribe(capacity, false)
}

func (pool *Pool) GetChainState() *ChainState {
	return pool.chainState
}

// SetPeerScoresPersister registers a callback that the per-client
// peer-scores poll loop will invoke after each successful fetch.
// Passing nil disables persistence.
func (pool *Pool) SetPeerScoresPersister(p PeerScoresPersister) {
	pool.peerScoresPersister = p
}

func (pool *Pool) AddEndpoint(endpoint *ClientConfig) (*Client, error) {
	clientIdx := pool.clientCounter
	pool.clientCounter++

	client, err := pool.newPoolClient(clientIdx, endpoint)
	if err != nil {
		return nil, err
	}

	pool.clients = append(pool.clients, client)

	return client, nil
}

func (pool *Pool) GetAllEndpoints() []*Client {
	return pool.clients
}

func (pool *Pool) GetReadyEndpoint(clientType ClientType) *Client {
	readyClients := []*Client{}

	for _, client := range pool.clients {
		if !client.isOnline {
			continue
		}

		if clientType > 0 && clientType != client.clientType {
			continue
		}

		readyClients = append(readyClients, client)
	}

	rand.Shuffle(len(readyClients), func(i, j int) {
		readyClients[i], readyClients[j] = readyClients[j], readyClients[i]
	})

	if len(readyClients) == 0 {
		return nil
	}

	return readyClients[0]
}

func (pool *Pool) AwaitReadyEndpoint(ctx context.Context, clientType ClientType) *Client {
	for {
		client := pool.GetReadyEndpoint(clientType)
		if client != nil {
			return client
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(1 * time.Second):
		}
	}
}
