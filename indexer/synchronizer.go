package indexer

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/db"
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
			if syncEpoch >= indexerEpoch {
				break
			}
		} else {
			synclogger.Warnf("Synchronisation of epoch %v failed", syncEpoch)
		}

		if sync.checkKillChan(4 * time.Second) {
			break
		}
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

	// load epoch assingments
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
			header: header,
			block:  block,
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
		validatorCount:    0,
		eligibleAmount:    0,
		assignments:       epochAssignments,
		validatorBalances: make(map[uint64]uint64),
	}

	// load epoch stats
	epochValidators, err := sync.indexer.rpcClient.GetStateValidators(firstBlock.header.Data.Header.Message.StateRoot)
	if err != nil {
		logger.Errorf("Error fetching epoch %v/%v validators: %v", syncEpoch, firstBlock.header.Data.Header.Message.Slot, err)
	} else {
		for idx := 0; idx < len(epochValidators.Data); idx++ {
			validator := epochValidators.Data[idx]
			epochStats.validatorBalances[uint64(validator.Index)] = uint64(validator.Validator.EffectiveBalance)
			if validator.Status != "active_ongoing" {
				continue
			}
			epochStats.validatorCount++
			epochStats.eligibleAmount += uint64(validator.Validator.EffectiveBalance)
		}
	}
	if sync.checkKillChan(0) {
		return false
	}

	// process epoch vote aggregations
	var targetRoot []byte
	if uint64(firstBlock.header.Data.Header.Message.Slot) == firstSlot {
		targetRoot = firstBlock.header.Data.Root
	} else {
		targetRoot = firstBlock.header.Data.Header.Message.ParentRoot
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

	err = db.SetExplorerState("indexer.syncstate", &indexerSyncState{
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

	return true
}
