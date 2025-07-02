package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"

	"github.com/ethpandaops/dora/clients/sshtunnel"
)

// ClientType represents execution client types
type ClientType int8

const (
	UnknownClientType    ClientType = -1
	NethermindClientType ClientType = 5
)

// ConvertClientType converts execution.ClientType to rpc.ClientType
func ConvertClientType(clientTypeValue uint8) ClientType {
	switch clientTypeValue {
	case 5: // NethermindClient from execution package
		return NethermindClientType
	default:
		return UnknownClientType
	}
}

// NethermindPeerInfo represents the Nethermind-specific peer format
type NethermindPeerInfo struct {
	Name       string                 `json:"name,omitempty"`
	ID         string                 `json:"id"`
	Host       string                 `json:"host,omitempty"`
	Port       uint16                 `json:"port,omitempty"`
	Address    string                 `json:"address,omitempty"`
	IsBootnode bool                   `json:"isBootnode,omitempty"`
	IsTrusted  bool                   `json:"isTrusted,omitempty"`
	IsStatic   bool                   `json:"isStatic,omitempty"`
	Enode      string                 `json:"enode"`
	Inbound    bool                   `json:"inbound"`
	Caps       []string               `json:"caps,omitempty"`
	Protocols  map[string]interface{} `json:"protocols,omitempty"`
}

// convertNethermindPeer converts a NethermindPeerInfo to p2p.PeerInfo format
func convertNethermindPeer(np *NethermindPeerInfo) *p2p.PeerInfo {
	peer := &p2p.PeerInfo{
		ID:        np.ID,
		Name:      np.Name,
		Caps:      np.Caps,
		Enode:     np.Enode,
		Protocols: np.Protocols,
	}

	// Set the Network fields directly on the anonymous struct
	peer.Network.LocalAddress = "" // Not provided by Nethermind
	peer.Network.RemoteAddress = np.Address
	peer.Network.Inbound = np.Inbound
	peer.Network.Trusted = np.IsTrusted
	peer.Network.Static = np.IsStatic

	return peer
}

type ExecutionClient struct {
	name      string
	endpoint  string
	headers   map[string]string
	sshtunnel *sshtunnel.SSHTunnel
	rpcClient *rpc.Client
	ethClient *ethclient.Client
}

// NewExecutionClient is used to create a new execution client
func NewExecutionClient(name, endpoint string, headers map[string]string, sshcfg *sshtunnel.SshConfig, logger logrus.FieldLogger) (*ExecutionClient, error) {
	client := &ExecutionClient{
		name:     name,
		endpoint: endpoint,
		headers:  headers,
	}

	if sshcfg != nil {
		// create ssh tunnel to remote host
		sshPort := 0
		if sshcfg.Port != "" {
			sshPort, _ = strconv.Atoi(sshcfg.Port)
		}
		if sshPort == 0 {
			sshPort = 22
		}
		sshEndpoint := fmt.Sprintf("%v@%v:%v", sshcfg.User, sshcfg.Host, sshPort)
		var sshAuth ssh.AuthMethod
		if sshcfg.Keyfile != "" {
			var err error
			sshAuth, err = sshtunnel.PrivateKeyFile(sshcfg.Keyfile)
			if err != nil {
				return nil, fmt.Errorf("could not load ssh keyfile: %w", err)
			}
		} else {
			sshAuth = ssh.Password(sshcfg.Password)
		}

		// get tunnel target from endpoint url
		endpointUrl, _ := url.Parse(endpoint)
		tunTarget := endpointUrl.Host
		if endpointUrl.Port() != "" {
			tunTarget = fmt.Sprintf("%v:%v", tunTarget, endpointUrl.Port())
		} else {
			tunTargetPort := 80
			if endpointUrl.Scheme == "https:" {
				tunTargetPort = 443
			}
			tunTarget = fmt.Sprintf("%v:%v", tunTarget, tunTargetPort)
		}

		client.sshtunnel = sshtunnel.NewSSHTunnel(sshEndpoint, sshAuth, tunTarget)
		client.sshtunnel.Log = logger.WithField("sshtun", sshcfg.Host)
		err := client.sshtunnel.Start()
		if err != nil {
			return nil, fmt.Errorf("could not start ssh tunnel: %w", err)
		}

		// override endpoint to use local tunnel end
		endpointUrl.Host = fmt.Sprintf("localhost:%v", client.sshtunnel.Local.Port)

		client.endpoint = endpointUrl.String()
	}

	return client, nil
}

func (ec *ExecutionClient) Initialize(ctx context.Context) error {
	if ec.ethClient != nil {
		return nil
	}

	rpcClient, err := rpc.DialContext(ctx, ec.endpoint)
	if err != nil {
		return err
	}

	for hKey, hVal := range ec.headers {
		rpcClient.SetHeader(hKey, hVal)
	}

	ec.rpcClient = rpcClient
	ec.ethClient = ethclient.NewClient(rpcClient)

	return nil
}

func (ec *ExecutionClient) GetEthClient() *ethclient.Client {
	return ec.ethClient
}

func (ec *ExecutionClient) GetClientVersion(ctx context.Context) (string, error) {
	var result string
	err := ec.rpcClient.CallContext(ctx, &result, "web3_clientVersion")

	return result, err
}

func (ec *ExecutionClient) GetChainSpec(ctx context.Context) (*ChainSpec, error) {
	chainID, err := ec.ethClient.ChainID(ctx)
	if err != nil {
		return nil, err
	}

	return &ChainSpec{
		ChainID: chainID.String(),
	}, nil
}

func (ec *ExecutionClient) GetAdminPeers(ctx context.Context) ([]*p2p.PeerInfo, error) {
	return ec.GetAdminPeersWithClientType(ctx, UnknownClientType)
}

func (ec *ExecutionClient) GetAdminPeersWithClientType(ctx context.Context, clientType ClientType) ([]*p2p.PeerInfo, error) {
	if clientType == NethermindClientType {
		logrus.Debugf("Using Nethermind-specific parsing for client %s", ec.name)
		// For Nethermind, go directly to raw JSON parsing
		var rawResult json.RawMessage
		err := ec.rpcClient.CallContext(ctx, &rawResult, "admin_peers", false)
		if err != nil {
			// If that fails, try without the boolean parameter
			err = ec.rpcClient.CallContext(ctx, &rawResult, "admin_peers")
			if err != nil {
				return nil, fmt.Errorf("failed to get admin_peers from Nethermind: %v", err)
			}
		}

		logrus.Debugf("Raw JSON response from Nethermind client %s: %s", ec.name, string(rawResult))

		// Try to unmarshal as Nethermind format
		var nethermindPeers []*NethermindPeerInfo
		if err := json.Unmarshal(rawResult, &nethermindPeers); err != nil {
			return nil, fmt.Errorf("failed to parse Nethermind admin_peers response: %v", err)
		}

		logrus.Debugf("Successfully parsed Nethermind format for client %s, converting %d peers", ec.name, len(nethermindPeers))

		// Convert Nethermind format to standard format
		result := make([]*p2p.PeerInfo, len(nethermindPeers))
		for i, np := range nethermindPeers {
			result[i] = convertNethermindPeer(np)
			logrus.Debugf("Converted peer %d: ID=%s, Inbound=%t -> %t", i, np.ID, np.Inbound, result[i].Network.Inbound)
		}

		return result, nil
	}

	// For non-Nethermind clients, use standard format
	var standardResult []*p2p.PeerInfo
	err := ec.rpcClient.CallContext(ctx, &standardResult, "admin_peers")

	if err == nil {
		logrus.Debugf("Successfully parsed admin_peers in standard format for client %s", ec.name)
		return standardResult, nil
	}

	// If that fails with "Invalid params", try with boolean parameter (some clients need this)
	if err.Error() == "Invalid params" {
		logrus.Debugf("Trying admin_peers with boolean parameter for client %s", ec.name)
		standardResult = nil
		err = ec.rpcClient.CallContext(ctx, &standardResult, "admin_peers", false)
		if err == nil {
			logrus.Debugf("Successfully parsed admin_peers with boolean parameter for client %s", ec.name)
			return standardResult, nil
		}
	}

	return nil, fmt.Errorf("failed to get admin_peers: %v", err)
}

func (ec *ExecutionClient) GetAdminNodeInfo(ctx context.Context) (*p2p.NodeInfo, error) {
	var result *p2p.NodeInfo
	err := ec.rpcClient.CallContext(ctx, &result, "admin_nodeInfo")
	return result, err
}

func (ec *ExecutionClient) GetNodeSyncing(ctx context.Context) (*SyncStatus, error) {
	status, err := ec.ethClient.SyncProgress(ctx)
	if err != nil {
		return nil, err
	}

	if status == nil {
		// Not syncing
		ss := &SyncStatus{}
		ss.IsSyncing = false

		return ss, nil
	}

	return &SyncStatus{
		IsSyncing:     true,
		CurrentBlock:  status.CurrentBlock,
		HighestBlock:  status.HighestBlock,
		StartingBlock: status.StartingBlock,
	}, nil
}

type BlockFilterId string

func (ec *ExecutionClient) NewBlockFilter(ctx context.Context) (BlockFilterId, error) {
	var result BlockFilterId
	err := ec.rpcClient.CallContext(ctx, &result, "eth_newBlockFilter")
	return result, err
}

func (ec *ExecutionClient) GetFilterChanges(ctx context.Context, filterId BlockFilterId) ([]string, error) {
	var result []string
	err := ec.rpcClient.CallContext(ctx, &result, "eth_getFilterChanges", filterId)
	return result, err
}

func (ec *ExecutionClient) UninstallBlockFilter(ctx context.Context, filterId BlockFilterId) (bool, error) {
	var result bool
	err := ec.rpcClient.CallContext(ctx, &result, "eth_uninstallFilter", filterId)
	return result, err
}

func (ec *ExecutionClient) GetLatestHeader(ctx context.Context) (*types.Header, error) {
	header, err := ec.ethClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, err
	}

	return header, nil
}

func (ec *ExecutionClient) GetLatestBlock(ctx context.Context) (*types.Block, error) {
	block, err := ec.ethClient.BlockByNumber(ctx, nil)
	if err != nil {
		return nil, err
	}

	return block, nil
}

func (ec *ExecutionClient) GetHeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
	header, err := ec.ethClient.HeaderByHash(ctx, hash)
	if err != nil {
		return nil, err
	}

	return header, nil
}

func (ec *ExecutionClient) GetHeaderByNumber(ctx context.Context, number uint64) (*types.Header, error) {
	block, err := ec.ethClient.HeaderByNumber(ctx, big.NewInt(0).SetUint64(number))
	if err != nil {
		return nil, err
	}

	return block, nil
}

func (ec *ExecutionClient) GetBlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	block, err := ec.ethClient.BlockByHash(ctx, hash)
	if err != nil {
		return nil, err
	}

	return block, nil
}

func (ec *ExecutionClient) GetNonceAt(ctx context.Context, wallet common.Address, blockNumber *big.Int) (uint64, error) {
	return ec.ethClient.NonceAt(ctx, wallet, blockNumber)
}

func (ec *ExecutionClient) GetBalanceAt(ctx context.Context, wallet common.Address, blockNumber *big.Int) (*big.Int, error) {
	return ec.ethClient.BalanceAt(ctx, wallet, blockNumber)
}

func (ec *ExecutionClient) GetTransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return ec.ethClient.TransactionReceipt(ctx, txHash)
}

func (ec *ExecutionClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	return ec.ethClient.SendTransaction(ctx, tx)
}
