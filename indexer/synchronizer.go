package indexer

import (
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
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
	cachedBlocks map[uint64][]*BlockInfo
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
	sync.runMutex.Unlock()

	// start synchronizer
	sync.stateMutex.Lock()
	defer sync.stateMutex.Unlock()
	if sync.running {
		synclogger.Errorf("Cannot start synchronizer: already running")
		return
	}
	sync.currentEpoch = startEpoch
	sync.running = true

	go sync.runSync()
}

func (sync *synchronizerState) runSync() {
	sync.runMutex.Lock()
	defer sync.runMutex.Unlock()

	sync.cachedBlocks = make(map[uint64][]*BlockInfo)
	sync.cachedSlot = 0
	isComplete := false
	synclogger.Infof("Synchronization started. Head epoch: %v", sync.currentEpoch)

	for {
		// synchronize next epoch
		sync.stateMutex.Lock()
		syncEpoch := sync.currentEpoch
		sync.stateMutex.Unlock()

		synclogger.Infof("Synchronising epoch %v", syncEpoch)
		if sync.syncEpoch(syncEpoch) {
			sync.indexer.state.cacheMutex.Lock()
			indexerEpoch := sync.indexer.state.lastProcessedEpoch
			sync.indexer.state.cacheMutex.Unlock()
			sync.stateMutex.Lock()
			syncEpoch++
			sync.currentEpoch = syncEpoch
			sync.stateMutex.Unlock()
			if syncEpoch > indexerEpoch {
				isComplete = true
				break
			}
		} else {
			synclogger.Warnf("Synchronisation of epoch %v failed", syncEpoch)
		}

		if sync.checkKillChan(time.Duration(utils.Config.Indexer.SyncEpochCooldown) * time.Second) {
			break
		}
	}

	if isComplete {
		synclogger.Infof("Synchronization complete. Head epoch: %v", sync.currentEpoch)
	} else {
		synclogger.Infof("Synchronization aborted. Head epoch: %v", sync.currentEpoch)
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

	// load epoch assignments
	epochAssignments, err := sync.indexer.rpcClient.GetEpochAssignments(syncEpoch)
	if err != nil {
		synclogger.Errorf("Error fetching epoch %v duties: %v", syncEpoch, err)
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
		header, err := sync.indexer.rpcClient.GetBlockHeaderBySlot(slot)
		if err != nil {
			synclogger.Errorf("Error fetching slot %v header: %v", slot, err)
			return false
		}
		if header == nil {
			continue
		}
		if sync.checkKillChan(0) {
			return false
		}
		block, err := sync.indexer.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
		if err != nil {
			synclogger.Errorf("Error fetching slot %v block: %v", slot, err)
			return false
		}
		sync.cachedBlocks[slot] = []*BlockInfo{{
			Header: header,
			Block:  block,
		}}
	}
	sync.cachedSlot = lastSlot

	if sync.checkKillChan(0) {
		return false
	}

	// load epoch validators
	var firstBlock *BlockInfo
	lastSlot = firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch) - 1
	for slot := firstSlot; slot <= lastSlot; slot++ {
		if sync.cachedBlocks[slot] != nil {
			firstBlock = sync.cachedBlocks[slot][0]
			break
		}
	}
	if firstBlock == nil {
		// TODO: How to handle a epoch without any blocks?
		synclogger.Errorf("Syncing epoch %v without any block is not supported", syncEpoch)
		return true
	}

	epochStats := EpochStats{
		Assignments: epochAssignments,
		Validators: &EpochValidators{
			ValidatorCount:    0,
			EligibleAmount:    0,
			ValidatorBalances: make(map[uint64]uint64),
		},
	}

	// load epoch stats
	epochValidators, err := sync.indexer.rpcClient.GetStateValidators(epochAssignments.DependendState)
	if err != nil {
		logger.Errorf("Error fetching epoch %v/%v validators: %v", syncEpoch, firstBlock.Header.Data.Header.Message.Slot, err)
	} else {
		for idx := 0; idx < len(epochValidators.Data); idx++ {
			validator := epochValidators.Data[idx]
			epochStats.Validators.ValidatorBalances[uint64(validator.Index)] = uint64(validator.Validator.EffectiveBalance)
			if !strings.HasPrefix(validator.Status, "active") {
				continue
			}
			epochStats.Validators.ValidatorCount++
			epochStats.Validators.ValidatorBalance += uint64(validator.Balance)
			epochStats.Validators.EligibleAmount += uint64(validator.Validator.EffectiveBalance)
		}
	}
	if sync.checkKillChan(0) {
		return false
	}

	// process epoch vote aggregations
	var targetRoot []byte
	if uint64(firstBlock.Header.Data.Header.Message.Slot) == firstSlot {
		targetRoot = firstBlock.Header.Data.Root
	} else {
		targetRoot = firstBlock.Header.Data.Header.Message.ParentRoot
	}
	epochVotes := aggregateEpochVotes(sync.cachedBlocks, syncEpoch, &epochStats, targetRoot, false)

	// save blocks
	tx, err := db.WriterDb.Beginx()
	if err != nil {
		logger.Errorf("error starting db transactions: %v", err)
		return false
	}
	defer tx.Rollback()

	err = persistEpochData(syncEpoch, sync.cachedBlocks, &epochStats, epochVotes, tx)
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
