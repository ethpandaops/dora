package indexer

import (
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
	cachedBlocks map[uint64]*indexerCacheBlock
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
	defer func() {
		if err := recover(); err != nil {
			synclogger.Errorf("uncaught panic in runSync subroutine: %v", err)
		}
	}()

	sync.runMutex.Lock()
	defer sync.runMutex.Unlock()

	sync.cachedBlocks = make(map[uint64]*indexerCacheBlock)
	sync.cachedSlot = 0
	isComplete := false
	synclogger.Infof("synchronization started. Head epoch: %v", sync.currentEpoch)

	for {
		// synchronize next epoch
		syncEpoch := sync.currentEpoch

		synclogger.Infof("synchronizing epoch %v", syncEpoch)
		if sync.syncEpoch(syncEpoch) {
			finalizedEpoch, _ := sync.indexer.indexerCache.getFinalizedHead()
			sync.stateMutex.Lock()
			syncEpoch++
			sync.currentEpoch = syncEpoch
			sync.stateMutex.Unlock()
			if int64(syncEpoch) > finalizedEpoch {
				isComplete = true
				break
			}
		} else {
			synclogger.Warnf("synchronization of epoch %v failed", syncEpoch)
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

func (sync *synchronizerState) syncEpoch(syncEpoch uint64) bool {
	if db.IsEpochSynchronized(syncEpoch) {
		return true
	}

	client := sync.indexer.getReadyClient()

	// load epoch assignments
	epochAssignments, err := client.rpcClient.GetEpochAssignments(syncEpoch)
	if err != nil {
		synclogger.Errorf("error fetching epoch %v duties: %v", syncEpoch, err)
	}

	if sync.checkKillChan(0) {
		return false
	}

	// load headers & blocks from this & next epoch
	firstSlot := syncEpoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch * 2) - 1
	for slot := firstSlot; slot <= lastSlot; slot++ {
		if sync.cachedSlot >= slot {
			continue
		}
		headerRsp, err := client.rpcClient.GetBlockHeaderBySlot(slot)
		if err != nil {
			synclogger.Errorf("error fetching slot %v header: %v", slot, err)
			return false
		}
		if headerRsp == nil {
			continue
		}
		if sync.checkKillChan(0) {
			return false
		}
		blockRsp, err := client.rpcClient.GetBlockBodyByBlockroot(headerRsp.Data.Root)
		if err != nil {
			synclogger.Errorf("error fetching slot %v block: %v", slot, err)
			return false
		}
		sync.cachedBlocks[slot] = &indexerCacheBlock{
			root:   headerRsp.Data.Root,
			slot:   slot,
			header: &headerRsp.Data.Header,
			block:  &blockRsp.Data,
		}
	}
	sync.cachedSlot = lastSlot

	if sync.checkKillChan(0) {
		return false
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

	if sync.checkKillChan(0) {
		return false
	}

	// process epoch vote aggregations
	var firstBlock *indexerCacheBlock
	lastSlot = firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch) - 1
	for slot := firstSlot; slot <= lastSlot; slot++ {
		if sync.cachedBlocks[slot] != nil {
			firstBlock = sync.cachedBlocks[slot]
			break
		}
	}

	var targetRoot []byte
	if firstBlock != nil {
		if uint64(firstBlock.header.Message.Slot) == firstSlot {
			targetRoot = firstBlock.root
		} else {
			targetRoot = firstBlock.header.Message.ParentRoot
		}
	}
	epochVotes := aggregateEpochVotes(sync.cachedBlocks, syncEpoch, epochStats, targetRoot, false)

	// save blocks
	tx, err := db.WriterDb.Beginx()
	if err != nil {
		logger.Errorf("error starting db transactions: %v", err)
		return false
	}
	defer tx.Rollback()

	err = persistEpochData(syncEpoch, sync.cachedBlocks, epochStats, epochVotes, tx)
	if err != nil {
		logger.Errorf("error persisting epoch data to db: %v", err)
		return false
	}

	err = db.SetExplorerState("indexer.syncstate", &dbtypes.IndexerSyncState{
		Epoch: syncEpoch,
	}, tx)
	if err != nil {
		logger.Errorf("error while updating sync state: %v", err)
		return false
	}

	if err := tx.Commit(); err != nil {
		logger.Errorf("error committing db transaction: %v", err)
		return false
	}

	// cleanup cache (remove blocks from this epoch)
	for slot := firstSlot; slot <= lastSlot; slot++ {
		if sync.cachedBlocks[slot] != nil {
			delete(sync.cachedBlocks, slot)
		}
	}

	return true
}
