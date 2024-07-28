package consensus

import (
	"context"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus/rpc"
)

type ClientStatus uint8

var (
	ClientStatusOnline        ClientStatus = 1
	ClientStatusOffline       ClientStatus = 2
	ClientStatusSynchronizing ClientStatus = 3
	ClientStatusOptimistic    ClientStatus = 4
)

type ClientConfig struct {
	URL     string
	Name    string
	Headers map[string]string
}

type Client struct {
	pool                 *Pool
	clientIdx            uint16
	endpointConfig       *ClientConfig
	clientCtx            context.Context
	clientCtxCancel      context.CancelFunc
	rpcClient            *rpc.BeaconClient
	logger               *logrus.Entry
	isOnline             bool
	isSyncing            bool
	isOptimistic         bool
	versionStr           string
	clientType           ClientType
	lastEvent            time.Time
	retryCounter         uint64
	lastError            error
	headMutex            sync.RWMutex
	headRoot             phase0.Root
	headSlot             phase0.Slot
	finalizedRoot        phase0.Root
	finalizedEpoch       phase0.Epoch
	blockDispatcher      Dispatcher[*v1.BlockEvent]
	checkpointDispatcher Dispatcher[*FinalizedCheckpoint]
}

func (pool *Pool) newPoolClient(clientIdx uint16, endpoint *ClientConfig) (*Client, error) {
	rpcClient, err := rpc.NewBeaconClient(endpoint.Name, endpoint.URL, endpoint.Headers)
	if err != nil {
		return nil, err
	}

	client := Client{
		pool:           pool,
		clientIdx:      clientIdx,
		endpointConfig: endpoint,
		rpcClient:      rpcClient,
		logger:         pool.logger.WithField("client", endpoint.Name),
	}
	client.resetContext()

	go client.runClientLoop()

	return &client, nil
}

func (client *Client) resetContext() {
	if client.clientCtxCancel != nil {
		client.clientCtxCancel()
	}

	client.clientCtx, client.clientCtxCancel = context.WithCancel(client.pool.ctx)
}

func (client *Client) SubscribeBlockEvent(capacity int, blocking bool) *Subscription[*v1.BlockEvent] {
	return client.blockDispatcher.Subscribe(capacity, blocking)
}

func (client *Client) SubscribeFinalizedEvent(capacity int) *Subscription[*FinalizedCheckpoint] {
	return client.checkpointDispatcher.Subscribe(capacity, false)
}

func (client *Client) GetIndex() uint16 {
	return client.clientIdx
}

func (client *Client) GetName() string {
	return client.endpointConfig.Name
}

func (client *Client) GetVersion() string {
	return client.versionStr
}

func (client *Client) GetEndpointConfig() *ClientConfig {
	return client.endpointConfig
}

func (client *Client) GetRPCClient() *rpc.BeaconClient {
	return client.rpcClient
}

func (client *Client) GetLastHead() (phase0.Slot, phase0.Root) {
	client.headMutex.RLock()
	defer client.headMutex.RUnlock()

	return client.headSlot, client.headRoot
}

func (client *Client) GetLastError() error {
	return client.lastError
}

func (client *Client) GetLastEventTime() time.Time {
	return client.lastEvent
}

func (client *Client) GetStatus() ClientStatus {
	switch {
	case client.isSyncing:
		return ClientStatusSynchronizing
	case client.isOptimistic:
		return ClientStatusOptimistic
	case client.isOnline:
		return ClientStatusOnline
	default:
		return ClientStatusOffline
	}
}
