package consensus

import (
	"context"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/clients/sshtunnel"
	"github.com/ethpandaops/dora/utils"
)

type ClientConfig struct {
	URL        string
	Name       string
	Headers    map[string]string
	SshConfig  *sshtunnel.SshConfig
	DisableSSZ bool
}

type Client struct {
	pool                    *Pool
	clientIdx               uint16
	endpointConfig          *ClientConfig
	clientCtx               context.Context
	clientCtxCancel         context.CancelFunc
	rpcClient               *rpc.BeaconClient
	logger                  *logrus.Entry
	isOnline                bool
	isSyncing               bool
	isOptimistic            bool
	versionStr              string
	nodeIdentity            *rpc.NodeIdentity
	clientType              ClientType
	lastEvent               time.Time
	retryCounter            uint64
	lastError               error
	headMutex               sync.RWMutex
	headRoot                phase0.Root
	headSlot                phase0.Slot
	justifiedRoot           phase0.Root
	justifiedEpoch          phase0.Epoch
	finalizedRoot           phase0.Root
	finalizedEpoch          phase0.Epoch
	lastFinalityUpdateEpoch phase0.Epoch
	lastMetadataUpdateEpoch phase0.Epoch
	lastMetadataUpdateTime  time.Time
	lastSyncUpdateEpoch     phase0.Epoch
	peers                   []*v1.Peer
	blockDispatcher         utils.Dispatcher[*v1.BlockEvent]
	headDispatcher          utils.Dispatcher[*v1.HeadEvent]
	checkpointDispatcher    utils.Dispatcher[*v1.Finality]
	inclusionListDispatcher utils.Dispatcher[*v1.InclusionListEvent]

	specWarnings []string // warnings from incomplete spec checks
	specs        map[string]interface{}
	hasBadSpecs  bool
}

func (pool *Pool) newPoolClient(clientIdx uint16, endpoint *ClientConfig) (*Client, error) {
	logger := pool.logger.WithField("client", endpoint.Name)

	rpcClient, err := rpc.NewBeaconClient(endpoint.Name, endpoint.URL, endpoint.Headers, endpoint.SshConfig, endpoint.DisableSSZ, logger)
	if err != nil {
		return nil, err
	}

	client := Client{
		pool:           pool,
		clientIdx:      clientIdx,
		endpointConfig: endpoint,
		rpcClient:      rpcClient,
		logger:         logger,
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

func (client *Client) SubscribeBlockEvent(capacity int, blocking bool) *utils.Subscription[*v1.BlockEvent] {
	return client.blockDispatcher.Subscribe(capacity, blocking)
}

func (client *Client) SubscribeHeadEvent(capacity int, blocking bool) *utils.Subscription[*v1.HeadEvent] {
	return client.headDispatcher.Subscribe(capacity, blocking)
}

func (client *Client) SubscribeFinalizedEvent(capacity int) *utils.Subscription[*v1.Finality] {
	return client.checkpointDispatcher.Subscribe(capacity, false)
}

func (client *Client) SubscribeInclusionListEvent(capacity int, blocking bool) *utils.Subscription[*v1.InclusionListEvent] {
	return client.inclusionListDispatcher.Subscribe(capacity, blocking)
}

func (client *Client) GetPool() *Pool {
	return client.pool
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

func (client *Client) GetNodeIdentity() *rpc.NodeIdentity {
	return client.nodeIdentity
}

func (client *Client) GetEndpointConfig() *ClientConfig {
	return client.endpointConfig
}

func (client *Client) GetRPCClient() *rpc.BeaconClient {
	return client.rpcClient
}

func (client *Client) GetContext() context.Context {
	return client.clientCtx
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

func (client *Client) GetLastClientError() error {
	return client.lastError
}

func (client *Client) GetFinalityCheckpoint() (finalitedEpoch phase0.Epoch, finalizedRoot phase0.Root, justifiedEpoch phase0.Epoch, justifiedRoot phase0.Root) {
	client.headMutex.RLock()
	defer client.headMutex.RUnlock()

	return client.finalizedEpoch, client.finalizedRoot, client.justifiedEpoch, client.justifiedRoot
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

func (client *Client) GetNodePeers() []*v1.Peer {
	if client.peers == nil {
		return []*v1.Peer{}
	}
	return client.peers
}

func (client *Client) GetSpecWarnings() []string {
	return client.specWarnings
}

func (client *Client) GetSpecs() map[string]interface{} {
	return client.specs
}
