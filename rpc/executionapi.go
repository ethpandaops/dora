package rpc

import (
	"context"
	"fmt"
	"math/big"
	"net/url"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"golang.org/x/crypto/ssh"

	"github.com/ethpandaops/dora/rpc/sshtunnel"
	"github.com/ethpandaops/dora/types"
)

type ExecutionClient struct {
	name      string
	endpoint  string
	headers   map[string]string
	rpcClient *rpc.Client
	ethClient *ethclient.Client
	sshtunnel *sshtunnel.SSHTunnel
}

// NewExecutionClient is used to create a new execution client
func NewExecutionClient(name, endpoint string, headers map[string]string, sshcfg *types.EndpointSshConfig) (*ExecutionClient, error) {
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

type ExecutionSyncStatus struct {
	IsSyncing     bool
	StartingBlock uint64
	CurrentBlock  uint64
	HighestBlock  uint64
}

func (s *ExecutionSyncStatus) Percent() float64 {
	if !s.IsSyncing {
		return 100 //notlint:gomnd
	}

	return float64(s.CurrentBlock) / float64(s.HighestBlock) * 100 //notlint:gomnd // 100 will never change.
}

func (ec *ExecutionClient) GetNodeSyncing(ctx context.Context) (*ExecutionSyncStatus, error) {
	status, err := ec.ethClient.SyncProgress(ctx)
	if err != nil {
		return nil, err
	}

	if status == nil && err == nil {
		// Not syncing
		ss := &ExecutionSyncStatus{}
		ss.IsSyncing = false

		return ss, nil
	}

	return &ExecutionSyncStatus{
		IsSyncing:     true,
		CurrentBlock:  status.CurrentBlock,
		HighestBlock:  status.HighestBlock,
		StartingBlock: status.StartingBlock,
	}, nil
}

func (ec *ExecutionClient) GetChainId(ctx context.Context) (*big.Int, error) {
	chainId, err := ec.ethClient.ChainID(ctx)
	if err != nil {
		return nil, err
	}

	return chainId, nil
}

func (ec *ExecutionClient) GetLatestBlockHeader(ctx context.Context) (*ethtypes.Header, error) {
	block, err := ec.ethClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, err
	}

	return block, nil
}

func (ec *ExecutionClient) GetBlockHeaderByNumber(ctx context.Context, number uint64) (*ethtypes.Header, error) {
	block, err := ec.ethClient.HeaderByNumber(ctx, big.NewInt(0).SetUint64(number))
	if err != nil {
		return nil, err
	}

	return block, nil
}

func (ec *ExecutionClient) GetBlockByHash(ctx context.Context, hash common.Hash) (*ethtypes.Block, error) {
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

func (ec *ExecutionClient) GetTransactionReceipt(ctx context.Context, txHash common.Hash) (*ethtypes.Receipt, error) {
	return ec.ethClient.TransactionReceipt(ctx, txHash)
}

func (ec *ExecutionClient) SendTransaction(ctx context.Context, tx *ethtypes.Transaction) error {
	return ec.ethClient.SendTransaction(ctx, tx)
}
