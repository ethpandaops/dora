package beacon

import (
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

const (
	// bidCacheMaxSlots is the maximum number of slots to keep in the cache
	bidCacheMaxSlots = 15
	// bidCacheFlushThreshold is the slot span that triggers a flush
	bidCacheFlushThreshold = 15
	// bidCacheRetainSlots is the number of slots to retain after a flush
	bidCacheRetainSlots = 10
)

// bidCacheKey uniquely identifies a bid in the cache
type bidCacheKey struct {
	ParentRoot   phase0.Root
	ParentHash   phase0.Hash32
	BlockHash    phase0.Hash32
	BuilderIndex uint64
}

// blockBidCache caches execution payload bids for recent blocks.
// Bids for older slots are ignored. The cache is flushed to DB on shutdown
// or when the slot span exceeds the threshold.
type blockBidCache struct {
	indexer    *Indexer
	cacheMutex sync.RWMutex
	bids       map[bidCacheKey]*dbtypes.BlockBid
	minSlot    phase0.Slot
	maxSlot    phase0.Slot
}

// newBlockBidCache creates a new instance of blockBidCache.
func newBlockBidCache(indexer *Indexer) *blockBidCache {
	return &blockBidCache{
		indexer: indexer,
		bids:    make(map[bidCacheKey]*dbtypes.BlockBid, 64),
	}
}

// loadFromDB loads bids from the last N slots from the database.
func (cache *blockBidCache) loadFromDB(currentSlot phase0.Slot) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	minSlot := phase0.Slot(0)
	if currentSlot > bidCacheRetainSlots {
		minSlot = currentSlot - bidCacheRetainSlots
	}

	dbBids := db.GetBidsForSlotRange(uint64(minSlot))
	for _, bid := range dbBids {
		key := bidCacheKey{
			ParentRoot:   phase0.Root(bid.ParentRoot),
			ParentHash:   phase0.Hash32(bid.ParentHash),
			BlockHash:    phase0.Hash32(bid.BlockHash),
			BuilderIndex: bid.BuilderIndex,
		}
		cache.bids[key] = bid

		slot := phase0.Slot(bid.Slot)
		if cache.minSlot == 0 || slot < cache.minSlot {
			cache.minSlot = slot
		}
		if slot > cache.maxSlot {
			cache.maxSlot = slot
		}
	}

	if len(dbBids) > 0 {
		cache.indexer.logger.Infof("loaded %d bids from DB (slots %d-%d)", len(dbBids), cache.minSlot, cache.maxSlot)
	}
}

// AddBid adds a bid to the cache. Returns true if the bid was added,
// false if it was ignored (too old) or already exists.
func (cache *blockBidCache) AddBid(bid *dbtypes.BlockBid) bool {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	slot := phase0.Slot(bid.Slot)

	// Ignore bids for slots that are too old
	if cache.maxSlot > 0 && slot+bidCacheMaxSlots < cache.maxSlot {
		return false
	}

	key := bidCacheKey{
		ParentRoot:   phase0.Root(bid.ParentRoot),
		ParentHash:   phase0.Hash32(bid.ParentHash),
		BlockHash:    phase0.Hash32(bid.BlockHash),
		BuilderIndex: bid.BuilderIndex,
	}

	// Check if bid already exists
	if _, exists := cache.bids[key]; exists {
		return false
	}

	cache.bids[key] = bid

	// Update slot bounds
	if cache.minSlot == 0 || slot < cache.minSlot {
		cache.minSlot = slot
	}
	if slot > cache.maxSlot {
		cache.maxSlot = slot
	}

	return true
}

// GetBidsForBlockRoot returns all bids for a given parent block root.
func (cache *blockBidCache) GetBidsForBlockRoot(blockRoot phase0.Root) []*dbtypes.BlockBid {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	result := make([]*dbtypes.BlockBid, 0)
	for key, bid := range cache.bids {
		if key.ParentRoot == blockRoot {
			result = append(result, bid)
		}
	}

	return result
}

// checkAndFlush checks if the cache needs to be flushed and performs the flush if necessary.
// This should be called periodically (e.g., on each new block).
func (cache *blockBidCache) checkAndFlush() error {
	cache.cacheMutex.Lock()

	// Check if we need to flush
	if cache.maxSlot == 0 || cache.maxSlot-cache.minSlot < bidCacheFlushThreshold {
		cache.cacheMutex.Unlock()
		return nil
	}

	// Calculate the cutoff slot - we'll flush bids older than this
	cutoffSlot := cache.maxSlot - bidCacheRetainSlots

	// Collect bids to flush (from minSlot to cutoffSlot)
	bidsToFlush := make([]*dbtypes.BlockBid, 0)
	for key, bid := range cache.bids {
		if phase0.Slot(bid.Slot) < cutoffSlot {
			bidsToFlush = append(bidsToFlush, bid)
			delete(cache.bids, key)
		}
	}

	// Update minSlot
	cache.minSlot = cutoffSlot

	cache.cacheMutex.Unlock()

	// Write to DB outside of lock
	if len(bidsToFlush) > 0 {
		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			return db.InsertBids(bidsToFlush, tx)
		})
		if err != nil {
			cache.indexer.logger.Errorf("error flushing bids to db: %v", err)
			return err
		}
		cache.indexer.logger.Debugf("flushed %d bids to DB (slots < %d)", len(bidsToFlush), cutoffSlot)
	}

	return nil
}

// flushAll flushes all cached bids to the database.
// This should be called on shutdown.
func (cache *blockBidCache) flushAll() error {
	cache.cacheMutex.Lock()

	if len(cache.bids) == 0 {
		cache.cacheMutex.Unlock()
		return nil
	}

	bidsToFlush := make([]*dbtypes.BlockBid, 0, len(cache.bids))
	for _, bid := range cache.bids {
		bidsToFlush = append(bidsToFlush, bid)
	}

	// Clear the cache
	cache.bids = make(map[bidCacheKey]*dbtypes.BlockBid, 64)
	cache.minSlot = 0
	cache.maxSlot = 0

	cache.cacheMutex.Unlock()

	// Write to DB outside of lock
	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertBids(bidsToFlush, tx)
	})
	if err != nil {
		cache.indexer.logger.Errorf("error flushing all bids to db: %v", err)
		return err
	}

	cache.indexer.logger.Infof("flushed %d bids to DB on shutdown", len(bidsToFlush))
	return nil
}
