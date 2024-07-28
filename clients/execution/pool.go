package execution

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type SchedulerMode uint8

var (
	RoundRobinScheduler SchedulerMode = 1
)

type PoolConfig struct {
	FollowDistance uint64 `yaml:"followDistance" envconfig:"EXECUTION_POOL_FOLLOW_DISTANCE"`
	ForkDistance   uint64 `yaml:"forkDistance" envconfig:"EXECUTION_POOL_FORK_DISTANCE"`
	SchedulerMode  string `yaml:"schedulerMode" envconfig:"EXECUTION_POOL_SCHEDULER_MODE"`
}

type Pool struct {
	config         *PoolConfig
	ctx            context.Context
	logger         logrus.FieldLogger
	clientCounter  uint16
	clients        []*Client
	blockCache     *BlockCache
	forkCacheMutex sync.Mutex
	forkCache      map[int64][]*HeadFork

	schedulerMode  SchedulerMode
	schedulerMutex sync.Mutex
	rrLastIndexes  map[ClientType]uint16
}

func NewPool(ctx context.Context, config *PoolConfig, logger logrus.FieldLogger) (*Pool, error) {
	pool := Pool{
		config:        config,
		ctx:           ctx,
		logger:        logger,
		clients:       make([]*Client, 0),
		forkCache:     map[int64][]*HeadFork{},
		rrLastIndexes: map[ClientType]uint16{},
	}

	var err error

	switch config.SchedulerMode {
	case "", "rr", "roundrobin":
		pool.schedulerMode = RoundRobinScheduler
	default:
		return nil, fmt.Errorf("unknown pool schedulerMode: %v", config.SchedulerMode)
	}

	pool.blockCache, err = NewBlockCache(ctx, logger, config.FollowDistance)
	if err != nil {
		return nil, err
	}

	return &pool, nil
}

func (pool *Pool) GetBlockCache() *BlockCache {
	return pool.blockCache
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
	readyClients := pool.GetReadyEndpoints()
	selectedClient := pool.runClientScheduler(readyClients, clientType)

	return selectedClient
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

func (pool *Pool) GetReadyEndpoints() []*Client {
	canonicalFork := pool.GetCanonicalFork(-1)
	if canonicalFork == nil {
		return nil
	}

	readyClients := canonicalFork.ReadyClients
	if len(readyClients) == 0 {
		return nil
	}

	return readyClients
}

func (pool *Pool) IsClientReady(client *Client) bool {
	if client == nil {
		return false
	}

	canonicalFork := pool.GetCanonicalFork(-1)
	if canonicalFork == nil {
		return false
	}

	readyClients := canonicalFork.ReadyClients
	for _, readyClient := range readyClients {
		if readyClient == client {
			return true
		}
	}

	return false
}

func (pool *Pool) runClientScheduler(readyClients []*Client, clientType ClientType) *Client {
	pool.schedulerMutex.Lock()
	defer pool.schedulerMutex.Unlock()

	if pool.schedulerMode == RoundRobinScheduler {
		var firstReadyClient *Client

		for _, client := range readyClients {
			if clientType != AnyClient && clientType != client.clientType {
				continue
			}

			if firstReadyClient == nil {
				firstReadyClient = client
			}

			if client.clientIdx > pool.rrLastIndexes[clientType] {
				pool.rrLastIndexes[clientType] = client.clientIdx
				return client
			}
		}

		if firstReadyClient == nil {
			return nil
		}

		pool.rrLastIndexes[clientType] = firstReadyClient.clientIdx

		return firstReadyClient
	}

	return readyClients[0]
}
