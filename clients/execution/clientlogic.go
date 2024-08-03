package execution

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func (client *Client) runClientLoop() {
	defer func() {
		if err := recover(); err != nil {
			client.logger.WithError(err.(error)).Errorf("uncaught panic in clients.execution.Client.runClientLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go client.runClientLoop()
		}
	}()

	for {
		err := client.checkClient()

		if err == nil {
			err = client.runClientLogic()
		}

		if err == nil {
			client.retryCounter = 0
			return
		}

		client.isOnline = false
		client.lastError = err
		client.lastEvent = time.Now()
		client.retryCounter++

		waitTime := 10
		if client.retryCounter > 10 {
			waitTime = 300
		} else if client.retryCounter > 5 {
			waitTime = 60
		}

		client.logger.Warnf("execution client error: %v, retrying in %v sec...", err, waitTime)
		time.Sleep(time.Duration(waitTime) * time.Second)
	}
}

func (client *Client) checkClient() error {
	ctx, cancel := context.WithTimeout(client.clientCtx, 60*time.Second)
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
	client.parseClientVersion(nodeVersion)

	// get peers
	err = client.updateNodePeers(ctx)
	if err != nil {
		client.logger.Warnf("error updating node peers: %v", err)
	}

	// get & comare chain specs
	specs, err := client.rpcClient.GetChainSpec(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching specs: %v", err)
	}

	err = client.pool.chainState.SetClientSpecs(specs)
	if err != nil {
		return fmt.Errorf("invalid node specs: %v", err)
	}

	// check synchronization state
	syncStatus, err := client.rpcClient.GetNodeSyncing(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching synchronization status: %v", err)
	}

	if syncStatus == nil {
		return fmt.Errorf("could not get synchronization status")
	}

	client.isSyncing = syncStatus.IsSyncing

	return nil
}

func (client *Client) updateNodePeers(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client.lastPeersUpdate = time.Now()

	var err error
	client.nodeInfo, err = client.rpcClient.GetAdminNodeInfo(ctx)
	if err != nil {
		client.didFetchPeers = false
		return fmt.Errorf("could not get node info: %v", err)
	}

	peers, err := client.rpcClient.GetAdminPeers(ctx)
	if err != nil {
		client.didFetchPeers = false
		return fmt.Errorf("could not get peers: %v", err)
	}

	client.peers = peers
	client.didFetchPeers = true
	return nil
}

func (client *Client) runClientLogic() error {
	// get latest header
	err := client.pollClientHead()
	if err != nil {
		return err
	}

	// check sync status
	if client.isSyncing {
		return fmt.Errorf("execution client is synchronizing")
	}

	// register new block filter
	blockFilter, err := client.rpcClient.NewBlockFilter(client.clientCtx)
	if err != nil {
		client.logger.Warnf("could not create block filter: %v", err)
	} else {
		client.blockFilterId = blockFilter

		defer func() {
			ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
			defer cancel()
			client.rpcClient.UninstallBlockFilter(ctx, client.blockFilterId)
		}()
	}

	// process events
	client.lastEvent = time.Now()
	client.isOnline = true

	for {
		eventTimeout := time.Since(client.lastEvent)
		if eventTimeout > 30*time.Second {
			eventTimeout = 0
		} else {
			eventTimeout = 30*time.Second - eventTimeout
		}

		pollTimeout := time.Since(client.lastFilterPoll)
		if pollTimeout > 12*time.Second {
			pollTimeout = 0
		} else {
			pollTimeout = 12*time.Second - pollTimeout
		}

		peerRefreshTimeout := time.Since(client.lastPeersUpdate)
		if peerRefreshTimeout > 5*time.Minute {
			peerRefreshTimeout = 0
		} else {
			peerRefreshTimeout = 5*time.Minute - peerRefreshTimeout
		}

		select {
		case <-client.clientCtx.Done():
			return nil
		case <-time.After(pollTimeout):
			client.lastFilterPoll = time.Now()

			if blockFilter == "" {
				continue
			}

			// get filter changes
			latestHash, err := client.pollBlockFilter()
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					client.logger.Warnf("error polling block filter changes: filter not found, creating new filter...")
					blockFilter, err = client.rpcClient.NewBlockFilter(client.clientCtx)
					if err != nil {
						client.logger.Warnf("could not create block filter: %v", err)
					} else {
						client.blockFilterId = blockFilter
					}

					continue
				} else {
					client.logger.Warnf("error polling block filter changes: %v", err)
				}
			} else if latestHash == nil {
				continue
			} else if err = client.loadBlockFilterHeader(*latestHash); err != nil {
				client.logger.Warnf("error loading block: %v", err)
			} else {
				client.lastEvent = time.Now()
			}
		case <-time.After(eventTimeout):
			err := client.pollClientHead()
			if err != nil {
				client.isOnline = false
				return err
			}

			client.lastEvent = time.Now()
		case <-time.After(peerRefreshTimeout):
			err := client.updateNodePeers(client.clientCtx)
			if err != nil {
				client.logger.Warnf("error updating node peers: %v", err)
			}

		}
	}
}

func (client *Client) pollClientHead() error {
	ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
	defer cancel()

	latestHeader, err := client.rpcClient.GetLatestHeader(ctx)
	if err != nil {
		return fmt.Errorf("could not get latest header: %v", err)
	}

	if latestHeader == nil {
		return fmt.Errorf("could not find latest header")
	}

	client.headMutex.Lock()
	defer client.headMutex.Unlock()

	client.headNumber = latestHeader.Number.Uint64()
	client.headHash = latestHeader.Hash()

	return nil
}

func (client *Client) pollBlockFilter() (*common.Hash, error) {
	ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
	defer cancel()

	blockHashes, err := client.rpcClient.GetFilterChanges(ctx, client.blockFilterId)
	if err != nil {
		return nil, fmt.Errorf("could not get filter changes: %v", err)
	}

	if len(blockHashes) == 0 {
		return nil, nil
	}

	lastHash := common.HexToHash(blockHashes[len(blockHashes)-1])

	return &lastHash, nil
}

func (client *Client) loadBlockFilterHeader(hash common.Hash) error {
	ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
	defer cancel()

	header, err := client.rpcClient.GetHeaderByHash(ctx, hash)
	if err != nil {
		return fmt.Errorf("could not get header by hash %v: %v", hash.String(), err)
	}

	if header == nil {
		return fmt.Errorf("could not find header %v", hash.String())
	}

	client.headMutex.Lock()
	defer client.headMutex.Unlock()

	client.headNumber = header.Number.Uint64()
	client.headHash = header.Hash()

	return nil
}
