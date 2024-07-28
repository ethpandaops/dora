package execution

import (
	"bytes"
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func (client *Client) runClientLoop() {
	defer func() {
		if err := recover(); err != nil {
			client.logger.WithError(err.(error)).Errorf("uncaught panic in executionClient.runClientLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
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

	// get & comare chain specs
	specs, err := client.rpcClient.GetChainSpec(ctx)
	if err != nil {
		return fmt.Errorf("error while fetching specs: %v", err)
	}

	err = client.pool.blockCache.SetClientSpecs(specs)
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

func (client *Client) runClientLogic() error {
	// get latest header
	err := client.pollClientHead()
	if err != nil {
		return err
	}

	// check latest header / sync status
	if client.isSyncing {
		return fmt.Errorf("beacon node is synchronizing")
	}

	// process events
	client.lastEvent = time.Now()
	client.isOnline = true
	client.updateChan = make(chan *clientBlockNotification, 10)

	for {
		eventTimeout := time.Since(client.lastEvent)
		if eventTimeout > 30*time.Second {
			eventTimeout = 0
		} else {
			eventTimeout = 30*time.Second - eventTimeout
		}

		select {
		case <-client.clientCtx.Done():
			return nil
		case updateNotification := <-client.updateChan:
			var err error

			retryCount := 0

			for retryCount < 3 {
				err = client.processBlock(updateNotification.hash, updateNotification.number, nil, "notified")
				if err == nil {
					break
				}

				retryCount++

				time.Sleep(1 * time.Second)
			}

			if err != nil {
				client.logger.Warnf("error processing execution block update: %v", err)
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
		}
	}
}

func (client *Client) pollClientHead() error {
	ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
	defer cancel()

	latestBlock, err := client.rpcClient.GetLatestBlock(ctx)
	if err != nil {
		return fmt.Errorf("could not get latest block: %v", err)
	}

	if latestBlock == nil {
		return fmt.Errorf("could not find latest block")
	}

	err = client.processBlock(latestBlock.Hash(), latestBlock.Number().Uint64(), latestBlock, "polled")
	if err != nil {
		client.logger.Warnf("error processing execution block: %v", err)
	}

	return nil
}

func (client *Client) processBlock(hash common.Hash, number uint64, block *types.Block, source string) error {
	cachedBlock, isNewBlock := client.pool.blockCache.AddBlock(hash, number)
	if cachedBlock == nil {
		return fmt.Errorf("could not add block to cache")
	}

	cachedBlock.SetSeenBy(client)

	if isNewBlock {
		client.logger.Infof("received el block %v [0x%x] %v", number, hash, source)
	} else {
		client.logger.Debugf("received known el block %v [0x%x] %v", number, hash, source)
	}

	loaded, err := cachedBlock.EnsureBlock(func() (*types.Block, error) {
		if block != nil {
			return block, nil
		}

		ctx, cancel := context.WithTimeout(client.clientCtx, 10*time.Second)
		defer cancel()

		block, err := client.rpcClient.GetBlockByHash(ctx, cachedBlock.Hash)
		if err != nil {
			return nil, err
		}

		return block, nil
	})
	if err != nil {
		return err
	}

	if loaded {
		client.pool.blockCache.notifyBlockReady(cachedBlock)
	}

	client.headMutex.Lock()
	if bytes.Equal(client.headHash[:], hash[:]) {
		client.headMutex.Unlock()
		return nil
	}

	client.headNumber = number
	client.headHash = hash
	client.headMutex.Unlock()

	client.pool.resetHeadForkCache()

	return nil
}
