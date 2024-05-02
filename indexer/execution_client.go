package indexer

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethpandaops/dora/rpc"
	"github.com/ethpandaops/dora/utils"
)

type ExecutionClient struct {
	clientIdx       uint16
	clientName      string
	rpcClient       *rpc.ExecutionClient
	archive         bool
	priority        int
	versionStr      string
	indexerCache    *indexerCache
	cacheMutex      sync.RWMutex
	lastClientError error
	lastHeadRefresh time.Time
	isSynchronizing bool
	syncDistance    uint64
	isConnected     bool
	retryCounter    uint64
	lastHeadSlot    int64
	lastHeadRoot    []byte
}

func newExecutionClient(clientIdx uint16, clientName string, rpcClient *rpc.ExecutionClient, indexerCache *indexerCache, archive bool, priority int) *ExecutionClient {
	client := ExecutionClient{
		clientIdx:    clientIdx,
		clientName:   clientName,
		rpcClient:    rpcClient,
		archive:      archive,
		priority:     priority,
		indexerCache: indexerCache,
		lastHeadSlot: -1,
	}
	go client.runExecutionClientLoop()
	return &client
}

func (client *ExecutionClient) GetIndex() uint16 {
	return client.clientIdx
}

func (client *ExecutionClient) GetName() string {
	return client.clientName
}

func (client *ExecutionClient) GetVersion() string {
	return client.versionStr
}

func (client *ExecutionClient) GetRpcClient() *rpc.ExecutionClient {
	return client.rpcClient
}

func (client *ExecutionClient) GetLastHead() (int64, []byte, time.Time) {
	client.cacheMutex.RLock()
	defer client.cacheMutex.RUnlock()
	return client.lastHeadSlot, client.lastHeadRoot, client.lastHeadRefresh
}

func (client *ExecutionClient) GetStatus() string {
	if client.isSynchronizing {
		return "synchronizing"
	} else if !client.isConnected {
		return "disconnected"
	} else {
		return "ready"
	}
}

func (client *ExecutionClient) GetLastClientError() string {
	client.cacheMutex.RLock()
	defer client.cacheMutex.RUnlock()
	if client.lastClientError == nil {
		return ""
	}
	return client.lastClientError.Error()
}

func (client *ExecutionClient) runExecutionClientLoop() {
	defer utils.HandleSubroutinePanic("runExecutionClientLoop")

	for {
		err := client.checkClient()

		if err == nil {
			genesisTime := time.Unix(int64(utils.Config.Chain.GenesisTimestamp), 0)
			genesisSince := time.Since(genesisTime)
			waitTime := 0
			if genesisSince < 0 {
				waitTime = int(time.Since(genesisTime).Abs().Seconds()) + 1
				if waitTime > 600 {
					waitTime = 600
				}
				logger.WithField("client", client.clientName).Infof("waiting for genesis (%v secs)", waitTime)

				time.Sleep(time.Duration(waitTime) * time.Second)
				continue
			}

			err = client.processClientEvents()
		}
		if err == nil {
			return
		}

		client.lastClientError = err
		client.retryCounter++
		waitTime := 10
		skipLog := false
		if client.isSynchronizing {
			waitTime = 30
			skipLog = true
		} else if client.retryCounter > 10 {
			waitTime = 300
		} else if client.retryCounter > 5 {
			waitTime = 60
		}

		if skipLog {
			logger.WithField("client", client.clientName).Debugf("execution client error: %v, retrying in %v sec...", err, waitTime)
		} else {
			logger.WithField("client", client.clientName).Warnf("execution client error: %v, retrying in %v sec...", err, waitTime)
		}
		time.Sleep(time.Duration(waitTime) * time.Second)
	}
}

func (client *ExecutionClient) checkClient() error {
	isSynchronizing := false
	defer func() {
		client.isSynchronizing = isSynchronizing
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := client.rpcClient.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialization of execution client failed: %w", err)
	}

	// get node version
	nodeVersion, err := client.rpcClient.GetClientVersion(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching node version: %v", err)
	}
	client.versionStr = nodeVersion

	// check chainId
	chainId, err := client.rpcClient.GetChainId(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching chain id: %v", err)
	}
	if chainId == nil {
		return fmt.Errorf("no chain id received")
	}

	depositChainId := big.NewInt(0).SetUint64(utils.Config.Chain.Config.DepositChainID)
	if depositChainId.Cmp(chainId) != 0 {
		return fmt.Errorf("chain id from RPC (%v) does not match the chain id from explorer configuration (%v)", chainId.String(), depositChainId.String())
	}

	// check syncronization state
	syncStatus, err := client.rpcClient.GetNodeSyncing(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching synchronization status: %v", err)
	}
	if syncStatus == nil {
		return fmt.Errorf("could not get synchronization status")
	}
	isSynchronizing = syncStatus.IsSyncing
	client.syncDistance = uint64(syncStatus.HighestBlock - syncStatus.CurrentBlock)

	return nil
}

func (client *ExecutionClient) processClientEvents() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger.WithField("client", client.clientName).Debugf("endpoint %v ready: %v ", client.clientName, client.versionStr)
	client.retryCounter = 0

	// process events
	client.lastHeadRefresh = time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(utils.Config.Chain.Config.SecondsPerSlot) * time.Second):
			logger.WithField("client", client.clientName).Debug("polling chain head")
			err := client.pollLatestHead(ctx)
			if err != nil {
				client.isConnected = false
				return err
			}
			client.lastHeadRefresh = time.Now()
		}
	}
}

func (client *ExecutionClient) pollLatestHead(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// get latest header
	latestHeader, err := client.rpcClient.GetLatestBlockHeader(ctx)
	if err != nil {
		return fmt.Errorf("could not get latest header: %v", err)
	}
	if latestHeader == nil {
		return fmt.Errorf("could not find latest header")
	}
	headNumber := latestHeader.Number.Uint64()
	headHash := latestHeader.Hash()
	client.setHeadBlock(headHash[:], headNumber)

	// check latest header / sync status
	if client.isSynchronizing {
		return fmt.Errorf("execution node is synchronizing")
	}

	headSlot := client.indexerCache.getCachedBlock(client.indexerCache.finalizedRoot)
	if headSlot == nil {
		return fmt.Errorf("could not get finalized slot from cache, consensus clients not ready")
	}

	if headSlot.Refs.ExecutionNumber > 0 && headNumber <= headSlot.Refs.ExecutionNumber {
		return fmt.Errorf("client is far behind - head is before synchronized checkpoint")
	}

	return nil
}

func (client *ExecutionClient) setHeadBlock(root []byte, slot uint64) error {
	client.cacheMutex.Lock()
	client.lastHeadRefresh = time.Now()
	if bytes.Equal(client.lastHeadRoot, root) {
		client.cacheMutex.Unlock()
		return nil
	}
	client.lastHeadSlot = int64(slot)
	client.lastHeadRoot = root
	client.cacheMutex.Unlock()

	return nil
}
