package indexer

import (
	"fmt"
	"sync"
	"time"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

var synclogger = logrus.StandardLogger().WithField("module", "synchronizer")

type synchronizerState struct {
	indexer      *Indexer
	running      bool
	runMutex     sync.Mutex
	stateMutex   sync.Mutex
	killChan     chan bool
	currentEpoch uint64
	cachedSlot   uint64
	cachedBlocks map[uint64]*CacheBlock
}

func newSynchronizer(indexer *Indexer) *synchronizerState {
	return &synchronizerState{
		indexer:  indexer,
		killChan: make(chan bool),
	}
}

func (sync *synchronizerState) isEpochAhead(epoch uint64) bool {
	sync.stateMutex.Lock()
	defer sync.stateMutex.Unlock()
	if sync.running {
		if sync.currentEpoch < epoch {
			return true
		}
	}
	return false
}

func (sync *synchronizerState) startSync(startEpoch uint64) {
	sync.stateMutex.Lock()
	if sync.running {
		sync.killChan <- true
	}
	sync.stateMutex.Unlock()
	// wait for synchronizer to stop
	sync.runMutex.Lock()
	defer sync.runMutex.Unlock()

	// start synchronizer
	sync.stateMutex.Lock()
	defer sync.stateMutex.Unlock()
	if sync.running {
		synclogger.Errorf("cannot start synchronizer: already running")
		return
	}
	sync.currentEpoch = startEpoch
	sync.running = true

	go sync.runSync()
}

func (sync *synchronizerState) runSync() {
	defer utils.HandleSubroutinePanic("runSync")

	sync.runMutex.Lock()
	defer sync.runMutex.Unlock()

	sync.cachedBlocks = make(map[uint64]*CacheBlock)
	sync.cachedSlot = 0
	isComplete := false
	retryCount := 0
	var skipClients []*IndexerClient = nil
	synclogger.Infof("synchronization started. Head epoch: %v", sync.currentEpoch)

	for {
		// synchronize next epoch
		syncEpoch := sync.currentEpoch

		lastRetry := retryCount >= 10
		if lastRetry {
			synclogger.Infof("synchronizing epoch %v (retry: %v, last retry!)", syncEpoch, retryCount)
		} else if retryCount > 0 {
			synclogger.Infof("synchronizing epoch %v (retry: %v)", syncEpoch, retryCount)
		} else {
			synclogger.Infof("synchronizing epoch %v", syncEpoch)
		}
		done, usedClient, err := sync.syncEpoch(syncEpoch, lastRetry, skipClients)
		if done || lastRetry {
			if err != nil {
				synclogger.Warnf("synchronization of epoch %v failed: %v - skipping epoch", syncEpoch, err)
			}
			retryCount = 0
			skipClients = nil
			finalizedEpoch, _ := sync.indexer.indexerCache.getFinalizedHead()
			sync.stateMutex.Lock()
			syncEpoch++
			sync.currentEpoch = syncEpoch
			sync.stateMutex.Unlock()
			if int64(syncEpoch) > finalizedEpoch {
				isComplete = true
				break
			}
		} else if err != nil {
			log := synclogger
			if usedClient != nil {
				log = synclogger.WithField("client", usedClient.clientName)
				skipClients = append(skipClients, usedClient)
			}
			log.Warnf("synchronization of epoch %v failed: %v - Retrying in 10 sec...", syncEpoch, err)
			retryCount++
			time.Sleep(10 * time.Second)
		}

		if sync.checkKillChan(time.Duration(utils.Config.Indexer.SyncEpochCooldown) * time.Second) {
			break
		}
	}

	if isComplete {
		synclogger.Infof("synchronization complete. Head epoch: %v", sync.currentEpoch)
	} else {
		synclogger.Infof("synchronization aborted. Head epoch: %v", sync.currentEpoch)
	}

	sync.running = false
}

func (sync *synchronizerState) checkKillChan(timeout time.Duration) bool {
	if timeout > 0 {
		select {
		case <-sync.killChan:
			return true
		case <-time.After(timeout):
			return false
		}
	} else {
		select {
		case <-sync.killChan:
			return true
		default:
			return false
		}
	}
}

func (sync *synchronizerState) syncEpoch(syncEpoch uint64, lastTry bool, skipClients []*IndexerClient) (bool, *IndexerClient, error) {
	if db.IsEpochSynchronized(syncEpoch) {
		return true, nil, nil
	}

	client := sync.indexer.GetReadyClient(true, nil, skipClients)

	// load headers & blocks from this & next epoch
	firstSlot := syncEpoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch * 2) - 1
	var firstBlock *CacheBlock
	for slot := firstSlot; slot <= lastSlot; slot++ {
		if sync.cachedSlot < slot {
			headerRsp, err := client.rpcClient.GetBlockHeaderBySlot(slot)
			if err != nil {
				return false, client, fmt.Errorf("error fetching slot %v header: %v", slot, err)
			}
			if headerRsp == nil {
				continue
			}
			if sync.checkKillChan(0) {
				return false, nil, nil
			}
			blockRsp, err := client.rpcClient.GetBlockBodyByBlockroot(headerRsp.Data.Root)
			if err != nil {
				return false, client, fmt.Errorf("error fetching slot %v block: %v", slot, err)
			}
			sync.cachedBlocks[slot] = &CacheBlock{
				Root:   headerRsp.Data.Root,
				Slot:   slot,
				header: &headerRsp.Data.Header,
				block:  &blockRsp.Data,
			}
		}
		if firstBlock == nil && sync.cachedBlocks[slot] != nil {
			firstBlock = sync.cachedBlocks[slot]
		}
	}
	sync.cachedSlot = lastSlot

	if sync.checkKillChan(0) {
		return false, nil, nil
	}

	// load epoch assignments
	var dependentRoot []byte
	if firstBlock != nil {
		dependentRoot = firstBlock.header.Message.ParentRoot
	} else {
		// get from db
		dependentRoot = db.GetHighestRootBeforeSlot(firstSlot, false)
	}

	epochAssignments, err := client.rpcClient.GetEpochAssignments(syncEpoch, dependentRoot)
	if err != nil || epochAssignments == nil {
		return false, client, fmt.Errorf("error fetching epoch %v duties: %v", syncEpoch, err)
	}
	if epochAssignments.AttestorAssignments == nil && !lastTry {
		return false, client, fmt.Errorf("error fetching epoch %v duties: attestor assignments empty", syncEpoch)
	}

	if sync.checkKillChan(0) {
		return false, nil, nil
	}

	// load epoch stats
	epochStats := &EpochStats{
		Epoch:               syncEpoch,
		DependentRoot:       epochAssignments.DependendRoot,
		proposerAssignments: epochAssignments.ProposerAssignments,
		attestorAssignments: epochAssignments.AttestorAssignments,
		syncAssignments:     epochAssignments.SyncAssignments,
	}
	epochStats.loadValidatorStats(client, epochAssignments.DependendStateRef)

	if epochStats.validatorStats == nil && !lastTry {
		return false, client, fmt.Errorf("error fetching validator stats for epoch %v: %v", syncEpoch, err)
	}
	if sync.checkKillChan(0) {
		return false, nil, nil
	}

	// process epoch vote aggregations
	var targetRoot []byte
	if firstBlock != nil {
		if uint64(firstBlock.header.Message.Slot) == firstSlot {
			targetRoot = firstBlock.Root
		} else {
			targetRoot = firstBlock.header.Message.ParentRoot
		}
	}
	epochVotes := aggregateEpochVotes(sync.cachedBlocks, syncEpoch, epochStats, targetRoot, false)
	if epochVotes.currentEpoch.targetVoteAmount < 10000 {
		epochVotes = aggregateEpochVotes(sync.cachedBlocks, syncEpoch, epochStats, targetRoot, false)
	}

	// save blocks
	tx, err := db.WriterDb.Beginx()
	if err != nil {
		return false, nil, fmt.Errorf("error starting db transactions: %v", err)
	}
	defer tx.Rollback()

	err = persistEpochData(syncEpoch, sync.cachedBlocks, epochStats, epochVotes, tx)
	if err != nil {
		return false, client, fmt.Errorf("error persisting epoch data to db: %v", err)
	}

	err = db.SetExplorerState("indexer.syncstate", &dbtypes.IndexerSyncState{
		Epoch: syncEpoch,
	}, tx)
	if err != nil {
		return false, nil, fmt.Errorf("error while updating sync state: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return false, nil, fmt.Errorf("error committing db transaction: %v", err)
	}

	// cleanup cache (remove blocks from this epoch)
	lastSlot = firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	for slot := firstSlot; slot <= lastSlot; slot++ {
		if sync.cachedBlocks[slot] != nil {
			delete(sync.cachedBlocks, slot)
		}
	}

	return true, nil, nil
}
