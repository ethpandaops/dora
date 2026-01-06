package txindexer

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/indexer/execution"
	"github.com/ethpandaops/dora/utils"
)

const (
	// lowPrioQueueLimit is the maximum number of items in the low priority queue
	// before sync processing is paused.
	lowPrioQueueLimit = 100

	// syncStateKey is the key used to store sync state in the explorer_state table.
	syncStateKey = "txindexer.syncstate"
)

// syncState represents the persisted sync state.
type syncState struct {
	CurrentEpoch uint64 `json:"current_epoch"`
}

// BlockRef represents a reference to a block for EL indexing.
type BlockRef struct {
	Slot            phase0.Slot
	BlockUID        uint64
	BlockHash       []byte
	Block           *beacon.Block // optional, may be nil for historical blocks
	ProcessTime     time.Time     // earliest time this block can be processed (zero means immediate)
	UpdateSyncEpoch *phase0.Epoch // if set, update DB sync state to this epoch after processing
}

// TxIndexer is responsible for indexing EL transactions from beacon blocks.
type TxIndexer struct {
	indexerCtx *execution.IndexerCtx
	logger     logrus.FieldLogger

	// state
	running   bool
	runMutex  sync.Mutex
	ctx       context.Context
	ctxCancel context.CancelFunc

	// processing queue
	queueMutex    sync.Mutex
	queueCond     *sync.Cond
	highPrioQueue []*BlockRef
	lowPrioQueue  []*BlockRef

	// sync state
	syncEpoch phase0.Epoch

	// balance lookup service
	balanceLookup *BalanceLookupService
}

// NewTxIndexer creates a new TxIndexer instance.
func NewTxIndexer(
	logger logrus.FieldLogger,
	indexerCtx *execution.IndexerCtx,
) *TxIndexer {
	txIndexer := &TxIndexer{
		indexerCtx:    indexerCtx,
		logger:        logger.WithField("component", "txindexer"),
		highPrioQueue: make([]*BlockRef, 0, 16),
		lowPrioQueue:  make([]*BlockRef, 0, 256),
	}
	txIndexer.queueCond = sync.NewCond(&txIndexer.queueMutex)

	// Initialize balance lookup service
	txIndexer.balanceLookup = NewBalanceLookupService(logger, txIndexer)

	return txIndexer
}

// GetSyncEpoch returns the current sync epoch.
func (t *TxIndexer) GetSyncEpoch() phase0.Epoch {
	t.queueMutex.Lock()
	defer t.queueMutex.Unlock()
	return t.syncEpoch
}

// setLocalSyncEpoch updates only the local sync epoch (not persisted to DB).
// This prevents re-queuing of epochs while processing is in progress.
func (t *TxIndexer) setLocalSyncEpoch(epoch phase0.Epoch) {
	t.queueMutex.Lock()
	t.syncEpoch = epoch
	t.queueMutex.Unlock()
}

// Start begins the tx indexer processing.
func (t *TxIndexer) Start() error {
	t.runMutex.Lock()
	defer t.runMutex.Unlock()

	if t.running {
		return nil
	}

	t.running = true
	t.ctx, t.ctxCancel = context.WithCancel(context.Background())

	// Load sync state from database
	t.loadSyncState()

	// Start the processing loop (goroutine 1)
	go t.runProcessingLoop()

	// Start the queue filler (goroutine 2)
	go t.runQueueFiller()

	// Start the cleanup routine (goroutine 3)
	go t.runCleanupLoop()

	t.logger.Info("tx indexer started")
	return nil
}

// Stop halts the tx indexer processing.
func (t *TxIndexer) Stop() error {
	t.runMutex.Lock()
	defer t.runMutex.Unlock()

	if !t.running {
		return nil
	}

	t.running = false
	t.ctxCancel()

	// Wake up the processing loop so it can exit
	t.queueCond.Broadcast()

	t.logger.Info("tx indexer stopped")
	return nil
}

// loadSyncState loads the sync state from the database.
func (t *TxIndexer) loadSyncState() {
	state := syncState{}
	_, err := db.GetExplorerState(syncStateKey, &state)
	if err != nil {
		t.logger.WithError(err).Debug("no existing sync state found, starting from epoch 0")
		t.syncEpoch = 0
		return
	}

	t.syncEpoch = phase0.Epoch(state.CurrentEpoch)
	t.logger.WithField("epoch", t.syncEpoch).Info("restored sync state from database")
}

// saveSyncState saves the sync state to the database.
func (t *TxIndexer) saveSyncState(epoch phase0.Epoch) {
	state := syncState{
		CurrentEpoch: uint64(epoch),
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.SetExplorerState(syncStateKey, &state, tx)
	})
	if err != nil {
		t.logger.WithError(err).Error("failed to save sync state")
	}
}

// runQueueFiller handles both block subscription and epoch sync in a single goroutine.
func (t *TxIndexer) runQueueFiller() {
	defer utils.HandleSubroutinePanic("TxIndexer.runQueueFiller", t.runQueueFiller)

	// Subscribe to block events
	subscription := t.indexerCtx.BeaconIndexer.SubscribeBlockEvent(100, false)
	defer subscription.Unsubscribe()

	// Ticker for sync processing
	syncTicker := time.NewTicker(10 * time.Second)
	defer syncTicker.Stop()

	// Initial delay before starting sync
	initialDelay := time.After(30 * time.Second)
	syncStarted := false

	for {
		select {
		case <-t.ctx.Done():
			return

		case block := <-subscription.Channel():
			if block == nil {
				continue
			}
			t.enqueueBeaconBlock(block, true)

		case <-initialDelay:
			syncStarted = true
			t.processSync()

		case <-syncTicker.C:
			if syncStarted {
				t.processSync()
			}
		}
	}
}

// enqueueBeaconBlock converts a beacon.Block to BlockRef and enqueues it.
func (t *TxIndexer) enqueueBeaconBlock(block *beacon.Block, highPriority bool) {
	blockIndex := block.GetBlockIndex()
	if blockIndex == nil {
		return
	}

	ref := &BlockRef{
		Slot:      block.Slot,
		BlockUID:  block.BlockUID,
		BlockHash: blockIndex.ExecutionHash[:],
		Block:     block,
	}

	// For high priority blocks (from subscription), delay processing by SecondsPerSlot + 2 seconds
	// to allow for potential reorgs to settle.
	if highPriority {
		specs := t.indexerCtx.ChainState.GetSpecs()
		delay := time.Duration(specs.SecondsPerSlot+2) * time.Second
		ref.ProcessTime = time.Now().Add(delay)
	}

	t.enqueueBlockRef(ref, highPriority)
}

// processSync checks for unsynced epochs and queues blocks for processing.
func (t *TxIndexer) processSync() {
	// Check if low priority queue is too full
	t.queueMutex.Lock()
	queueLen := len(t.lowPrioQueue)
	currentSyncEpoch := t.syncEpoch
	t.queueMutex.Unlock()

	if queueLen >= lowPrioQueueLimit {
		t.logger.WithField("queueLen", queueLen).Debug("skipping sync, low priority queue is full")
		return
	}

	chainState := t.indexerCtx.ChainState

	// Get synchronizer and block cache states from beacon indexer
	syncRunning, syncHead := t.indexerCtx.BeaconIndexer.GetSynchronizerState()
	finalizedEpoch, _ := t.indexerCtx.BeaconIndexer.GetBlockCacheState()

	// Determine the lowest epoch we can sync from
	lowestEpoch := finalizedEpoch
	if syncRunning && syncHead < lowestEpoch {
		lowestEpoch = syncHead
	}

	// Nothing to sync if we're already caught up
	if currentSyncEpoch >= lowestEpoch {
		return
	}

	t.logger.WithFields(logrus.Fields{
		"currentSyncEpoch": currentSyncEpoch,
		"lowestEpoch":      lowestEpoch,
	}).Debug("starting epoch sync")

	// Process epochs from current sync position to lowest epoch
	for epoch := currentSyncEpoch; epoch <= lowestEpoch; epoch++ {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		// Check queue limit again before processing more
		t.queueMutex.Lock()
		queueLen = len(t.lowPrioQueue)
		t.queueMutex.Unlock()

		if queueLen >= lowPrioQueueLimit {
			t.logger.WithField("queueLen", queueLen).Debug("pausing sync, low priority queue is full")
			return
		}

		firstSlot := uint64(chainState.EpochToSlot(epoch))
		lastSlot := uint64(chainState.EpochToSlot(epoch+1)) - 1

		// Query blocks from database for this epoch
		slots := db.GetSlotsRange(lastSlot, firstSlot, false, false)
		if len(slots) == 0 {
			// No slots in this epoch, persist immediately
			nextEpoch := epoch + 1
			t.setLocalSyncEpoch(nextEpoch)
			t.saveSyncState(nextEpoch)
			continue
		}

		// Collect block UIDs to check which need processing
		blockUids := make([]uint64, 0, len(slots))
		blocks := make([]*dbtypes.AssignedSlot, 0, len(slots))

		slices.Reverse(slots)
		for _, slot := range slots {
			if slot.Block != nil && slot.Block.BlockUid != 0 {
				blockUids = append(blockUids, slot.Block.BlockUid)
				blocks = append(blocks, slot)
			}
		}

		if len(blockUids) == 0 {
			// No valid blocks in this epoch, persist immediately
			nextEpoch := epoch + 1
			t.setLocalSyncEpoch(nextEpoch)
			t.saveSyncState(nextEpoch)
			continue
		}

		// Check which blocks are already synced in el_blocks
		syncedBlocks, err := db.GetElBlocksByUids(blockUids)
		if err != nil {
			t.logger.WithError(err).Error("failed to get el blocks by uids")
			continue
		}

		syncedBlockMap := make(map[uint64]bool, len(syncedBlocks))
		for _, elBlock := range syncedBlocks {
			// Consider a block synced if it has a non-zero status
			if elBlock.Status > 0 {
				syncedBlockMap[elBlock.BlockUid] = true
			}
		}

		// Queue unsynced blocks with low priority
		var lastQueuedRef *BlockRef
		for _, slot := range blocks {
			if syncedBlockMap[slot.Block.BlockUid] {
				continue
			}

			ref := t.createBlockRefFromSlot(slot)
			if ref != nil {
				t.enqueueBlockRef(ref, false)
				lastQueuedRef = ref
			}
		}

		// Update local sync epoch to prevent re-queuing
		nextEpoch := epoch + 1
		t.setLocalSyncEpoch(nextEpoch)

		// If blocks were queued, set UpdateSyncEpoch on the last one to persist after processing.
		// If no blocks were queued (all synced), persist immediately.
		if lastQueuedRef != nil {
			lastQueuedRef.UpdateSyncEpoch = &nextEpoch
		} else {
			t.saveSyncState(nextEpoch)
		}
	}
}

// createBlockRefFromSlot creates a BlockRef from a database slot.
// The Block field may be nil if the block is not in cache.
func (t *TxIndexer) createBlockRefFromSlot(slot *dbtypes.AssignedSlot) *BlockRef {
	if slot.Block == nil {
		return nil
	}

	ref := &BlockRef{
		Slot:      phase0.Slot(slot.Block.Slot),
		BlockUID:  slot.Block.BlockUid,
		BlockHash: slot.Block.EthBlockHash,
		Block:     t.indexerCtx.BeaconIndexer.GetBlockByRoot(phase0.Root(slot.Block.Root)),
	}

	return ref
}

// enqueueBlockRef adds a block reference to the processing queue.
func (t *TxIndexer) enqueueBlockRef(ref *BlockRef, highPriority bool) {
	t.queueMutex.Lock()
	defer t.queueMutex.Unlock()

	if highPriority {
		t.highPrioQueue = append(t.highPrioQueue, ref)
	} else {
		t.lowPrioQueue = append(t.lowPrioQueue, ref)
	}

	t.queueCond.Signal()
}

// runProcessingLoop is the main single-threaded processing loop.
func (t *TxIndexer) runProcessingLoop() {
	defer utils.HandleSubroutinePanic("TxIndexer.runProcessingLoop", t.runProcessingLoop)

	for {
		ref := t.dequeueBlockRef()
		if ref == nil {
			// Check if we should exit
			select {
			case <-t.ctx.Done():
				return
			default:
				continue
			}
		}

		err := t.processElBlock(ref)
		if err != nil {
			t.logger.WithFields(logrus.Fields{
				"slot":     ref.Slot,
				"blockUid": ref.BlockUID,
			}).WithError(err).Error("failed to process EL block")
		}

		// Persist sync epoch to DB if this was the last block of an epoch
		if ref.UpdateSyncEpoch != nil {
			t.saveSyncState(*ref.UpdateSyncEpoch)
		}
	}
}

// dequeueBlockRef retrieves the next block reference from the queue, prioritizing high priority entries.
// High priority entries are only returned after their ProcessTime has been reached.
func (t *TxIndexer) dequeueBlockRef() *BlockRef {
	t.queueMutex.Lock()
	defer t.queueMutex.Unlock()

	for {
		// Check if we should exit
		select {
		case <-t.ctx.Done():
			return nil
		default:
		}

		now := time.Now()

		// Check if high priority queue has a ready item
		if len(t.highPrioQueue) > 0 {
			ref := t.highPrioQueue[0]
			if ref.ProcessTime.IsZero() || !ref.ProcessTime.After(now) {
				// Item is ready, dequeue it
				t.highPrioQueue = t.highPrioQueue[1:]
				return ref
			}
		}

		// High priority not ready or empty, try low priority
		if len(t.lowPrioQueue) > 0 {
			ref := t.lowPrioQueue[0]
			t.lowPrioQueue = t.lowPrioQueue[1:]
			return ref
		}

		// Both queues empty or only high priority with pending process time
		if len(t.highPrioQueue) > 0 {
			// Wait until the first high priority item is ready
			waitDuration := time.Until(t.highPrioQueue[0].ProcessTime)
			if waitDuration > 0 {
				// Release lock while waiting
				t.queueMutex.Unlock()

				select {
				case <-t.ctx.Done():
					t.queueMutex.Lock()
					return nil
				case <-time.After(waitDuration):
					// Time elapsed, re-acquire lock and retry
					t.queueMutex.Lock()
					continue
				}
			}
		}

		// No items in either queue, wait for signal
		t.queueCond.Wait()
	}
}

// QueueAddressBalanceLookups queues all balance lookups for an address page view.
// This is called by the address handler when a user views an address page.
// The lookups are rate-limited to prevent excessive RPC calls.
func (t *TxIndexer) QueueAddressBalanceLookups(accountID uint64, address []byte) {
	if t.balanceLookup != nil {
		t.balanceLookup.QueueAddressPageLookups(accountID, address)
	}
}

// GetBalanceLookupStats returns the current balance lookup queue statistics.
func (t *TxIndexer) GetBalanceLookupStats() (highPrio, lowPrio int) {
	if t.balanceLookup != nil {
		return t.balanceLookup.GetQueueStats()
	}
	return 0, 0
}

// GetBalanceLookupService returns the balance lookup service for direct access.
func (t *TxIndexer) GetBalanceLookupService() *BalanceLookupService {
	return t.balanceLookup
}
