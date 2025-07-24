package execution

import (
	"context"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/clients/sshtunnel"
)

type ClientConfig struct {
	URL       string
	Name      string
	Headers   map[string]string
	SshConfig *sshtunnel.SshConfig
}

type Client struct {
	pool               *Pool
	clientIdx          uint16
	endpointConfig     *ClientConfig
	clientCtx          context.Context
	clientCtxCancel    context.CancelFunc
	rpcClient          *rpc.ExecutionClient
	logger             *logrus.Entry
	isOnline           bool
	isSyncing          bool
	versionStr         string
	clientType         ClientType
	lastEvent          time.Time
	lastFilterPoll     time.Time
	lastMetadataUpdate time.Time
	blockFilterId      rpc.BlockFilterId
	retryCounter       uint64
	lastError          error
	headMutex          sync.RWMutex
	headHash           common.Hash
	headNumber         uint64
	nodeInfo           *p2p.NodeInfo
	peers              []*p2p.PeerInfo
	didFetchPeers      bool
}

func (pool *Pool) newPoolClient(clientIdx uint16, endpoint *ClientConfig) (*Client, error) {
	logger := pool.logger.WithField("client", endpoint.Name)

	rpcClient, err := rpc.NewExecutionClient(endpoint.Name, endpoint.URL, endpoint.Headers, endpoint.SshConfig, logger)
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

	client.clientCtx, client.clientCtxCancel = context.WithCancel(context.Background())
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

func (client *Client) GetNodeInfo() *p2p.NodeInfo {
	return client.nodeInfo
}

func (client *Client) GetEthConfig(ctx context.Context) (map[string]interface{}, error) {
	return client.rpcClient.GetEthConfig(ctx)
}

func (client *Client) GetEndpointConfig() *ClientConfig {
	return client.endpointConfig
}

func (client *Client) GetLastHead() (uint64, common.Hash) {
	client.headMutex.RLock()
	defer client.headMutex.RUnlock()

	return client.headNumber, client.headHash
}

func (client *Client) GetLastClientError() error {
	return client.lastError
}

func (client *Client) GetLastEventTime() time.Time {
	return client.lastEvent
}

func (client *Client) GetRPCClient() *rpc.ExecutionClient {
	return client.rpcClient
}

func (client *Client) GetStatus() ClientStatus {
	switch {
	case client.isSyncing:
		return ClientStatusSynchronizing
	case client.isOnline:
		return ClientStatusOnline
	default:
		return ClientStatusOffline
	}
}

func (client *Client) GetNodePeers() []*p2p.PeerInfo {
	if client.peers == nil {
		return []*p2p.PeerInfo{}
	}
	return client.peers
}

func (client *Client) DidFetchPeers() bool {
	return client.didFetchPeers
}

// ForceUpdatePeerData forces an immediate update of peer data from this client
func (client *Client) ForceUpdatePeerData(ctx context.Context) error {
	return client.updateNodeMetadata(ctx)
}
