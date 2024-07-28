package consensus

import (
	"context"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/ethpandaops/ethwallclock"
	"github.com/sirupsen/logrus"
)

type SchedulerMode uint8

var (
	RoundRobinScheduler SchedulerMode = 1
)

type PoolConfig struct {
	ForkDistance  uint64 `yaml:"forkDistance" envconfig:"CONSENSUS_POOL_FORK_DISTANCE"`
	SchedulerMode string `yaml:"schedulerMode" envconfig:"CONSENSUS_POOL_SCHEDULER_MODE"`
}

type Pool struct {
	config        *PoolConfig
	ctx           context.Context
	logger        logrus.FieldLogger
	clientCounter uint16
	clients       []*Client
	chainState    *chainState
}

func NewPool(ctx context.Context, config *PoolConfig, logger logrus.FieldLogger) *Pool {
	return &Pool{
		config:     config,
		ctx:        ctx,
		logger:     logger,
		clients:    make([]*Client, 0),
		chainState: newChainState(),
	}
}

func (pool *Pool) SubscribeFinalizedEvent(capacity int) *Subscription[*FinalizedCheckpoint] {
	return pool.chainState.checkpointDispatcher.Subscribe(capacity, false)
}

func (pool *Pool) SubscribeWallclockEpochEvent(capacity int) *Subscription[*ethwallclock.Epoch] {
	return pool.chainState.wallclockEpochDispatcher.Subscribe(capacity, false)
}

func (pool *Pool) SubscribeWallclockSlotEvent(capacity int) *Subscription[*ethwallclock.Slot] {
	return pool.chainState.wallclockSlotDispatcher.Subscribe(capacity, false)
}

func (pool *Pool) GetGenesis() *v1.Genesis {
	return pool.chainState.genesis
}

func (pool *Pool) GetSpecs() *ChainSpec {
	return pool.chainState.getSpecs()
}

func (pool *Pool) GetWallclock() *ethwallclock.EthereumBeaconChain {
	return pool.chainState.wallclock
}

func (pool *Pool) GetBlockCache() *chainState {
	return pool.chainState
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
