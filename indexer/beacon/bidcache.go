package beacon

import (
	"sync"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
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
	BuilderIndex int64
}

// cachedBid is a bid with its per-client gossip observations.
type cachedBid struct {
	bid *dbtypes.BlockBid
	// seen maps cache client-table index -> first-seen offset (ms from slot start)
	seen map[int]int32
}

// blockBidCache caches execution payload bids for recent blocks and tracks
// which clients observed each bid on gossip. Bids for older slots are ignored.
// The cache is flushed to DB (and, when available, the per-slot observations
// to the blockdb) on shutdown or when the slot span exceeds the threshold.
type blockBidCache struct {
	indexer    *Indexer
	cacheMutex sync.RWMutex
	bids       map[bidCacheKey]*cachedBid
	minSlot    phase0.Slot
	maxSlot    phase0.Slot

	// clientNames interns observer client names; per-bid observations
	// reference table indexes. The table only grows within a session.
	clientNames []string
	clientIdx   map[string]int
}

// newBlockBidCache creates a new instance of blockBidCache.
func newBlockBidCache(indexer *Indexer) *blockBidCache {
	return &blockBidCache{
		indexer:     indexer,
		bids:        make(map[bidCacheKey]*cachedBid, 64),
		clientNames: make([]string, 0, 32),
		clientIdx:   make(map[string]int, 32),
	}
}

// makeBidCacheKey builds the cache key for a bid.
func makeBidCacheKey(bid *dbtypes.BlockBid) bidCacheKey {
	return bidCacheKey{
		ParentRoot:   phase0.Root(bid.ParentRoot),
		ParentHash:   phase0.Hash32(bid.ParentHash),
		BlockHash:    phase0.Hash32(bid.BlockHash),
		BuilderIndex: bid.BuilderIndex,
	}
}

// registerClientLocked interns a client name and returns its table index.
// Caller must hold the write lock.
func (cache *blockBidCache) registerClientLocked(name string) int {
	if idx, ok := cache.clientIdx[name]; ok {
		return idx
	}
	idx := len(cache.clientNames)
	cache.clientNames = append(cache.clientNames, name)
	cache.clientIdx[name] = idx
	return idx
}

// recordSeenLocked records a gossip observation for a cached bid, keeping the
// earliest offset per client. Caller must hold the write lock.
func (cache *blockBidCache) recordSeenLocked(cached *cachedBid, client string, offset int32) {
	if client == "" {
		return
	}
	idx := cache.registerClientLocked(client)
	if existing, ok := cached.seen[idx]; !ok || offset < existing {
		cached.seen[idx] = offset
	}
}

// annotateBidLocked returns a copy of the cached bid with up-to-date seen
// counters. Caller must hold at least the read lock.
func (cache *blockBidCache) annotateBidLocked(cached *cachedBid) *dbtypes.BlockBid {
	bid := *cached.bid
	if count := uint32(len(cached.seen)); count > bid.SeenCount {
		bid.SeenCount = count
	}
	if total := uint32(len(cache.indexer.clients)); total > bid.SeenTotal {
		bid.SeenTotal = total
	}
	if bid.SeenCount > bid.SeenTotal {
		bid.SeenTotal = bid.SeenCount
	}
	return &bid
}

// seenByNameLocked converts a cached bid's observations to name-keyed form.
// Caller must hold at least the read lock.
func (cache *blockBidCache) seenByNameLocked(cached *cachedBid) map[string]int32 {
	seen := make(map[string]int32, len(cached.seen))
	for idx, offset := range cached.seen {
		if idx < len(cache.clientNames) {
			seen[cache.clientNames[idx]] = offset
		}
	}
	return seen
}

// sessionClientNames returns the names of all clients attached to the indexer.
func (cache *blockBidCache) sessionClientNames() []string {
	names := make([]string, 0, len(cache.indexer.clients))
	for _, client := range cache.indexer.clients {
		names = append(names, client.client.GetName())
	}
	return names
}

// loadFromDB loads bids from the last N slots from the database and restores
// their gossip observations from the blockdb where available.
func (cache *blockBidCache) loadFromDB(currentSlot phase0.Slot) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	minSlot := phase0.Slot(0)
	if currentSlot > bidCacheRetainSlots {
		minSlot = currentSlot - bidCacheRetainSlots
	}

	slots := map[uint64]bool{}
	dbBids := db.GetBidsForSlotRange(cache.indexer.ctx, uint64(minSlot))
	for _, bid := range dbBids {
		cache.bids[makeBidCacheKey(bid)] = &cachedBid{
			bid:  bid,
			seen: make(map[int]int32, 8),
		}
		slots[bid.Slot] = true

		slot := phase0.Slot(bid.Slot)
		if cache.minSlot == 0 || slot < cache.minSlot {
			cache.minSlot = slot
		}
		if slot > cache.maxSlot {
			cache.maxSlot = slot
		}
	}

	// Restore per-client observations so a later re-flush merges instead of
	// starting from scratch.
	if blockdb.GlobalBlockDb.SupportsSlotBids() {
		for slot := range slots {
			slotBids, err := blockdb.GlobalBlockDb.GetSlotBids(cache.indexer.ctx, slot)
			if err != nil {
				cache.indexer.logger.Warnf("error loading bid observations for slot %d: %v", slot, err)
				continue
			}
			if slotBids == nil {
				continue
			}

			for _, entry := range slotBids.Bids {
				cached := cache.bids[makeBidCacheKey(entry.Bid)]
				if cached == nil {
					continue
				}

				for clientIdx, offset := range entry.SeenByClientIndex() {
					if clientIdx < len(slotBids.Clients) {
						cache.recordSeenLocked(cached, slotBids.Clients[clientIdx], offset)
					}
				}
			}
		}
	}

	if len(dbBids) > 0 {
		cache.indexer.logger.Infof("loaded %d bids from DB (slots %d-%d)", len(dbBids), cache.minSlot, cache.maxSlot)
	}
}

// AddBid adds a bid to the cache. seenClient identifies the client that
// observed the bid on gossip ("" for bids extracted from block bodies) and
// seenOffset is the observation time as ms offset from the bid's slot start.
// Returns true if the bid was added, false if it was ignored (too old) or
// already exists (the observation is still recorded).
func (cache *blockBidCache) AddBid(bid *dbtypes.BlockBid, seenClient string, seenOffset int32) bool {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	slot := phase0.Slot(bid.Slot)

	// Ignore bids for slots that are too old
	if cache.maxSlot > 0 && slot+bidCacheMaxSlots < cache.maxSlot {
		return false
	}

	key := makeBidCacheKey(bid)

	cached, exists := cache.bids[key]
	if exists {
		cache.recordSeenLocked(cached, seenClient, seenOffset)
		return false
	}

	cached = &cachedBid{
		bid:  bid,
		seen: make(map[int]int32, 8),
	}
	cache.bids[key] = cached
	cache.recordSeenLocked(cached, seenClient, seenOffset)

	// Update slot bounds
	if cache.minSlot == 0 || slot < cache.minSlot {
		cache.minSlot = slot
	}
	if slot > cache.maxSlot {
		cache.maxSlot = slot
	}

	return true
}

// GetBidsForSlot returns all cached bids for a slot regardless of their parent root,
// so bids targeting other forks or deeper ancestors (reorg bids) are included.
func (cache *blockBidCache) GetBidsForSlot(slot phase0.Slot) []*dbtypes.BlockBid {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	result := make([]*dbtypes.BlockBid, 0)
	for _, cached := range cache.bids {
		if phase0.Slot(cached.bid.Slot) == slot {
			result = append(result, cache.annotateBidLocked(cached))
		}
	}

	return result
}

// GetBidsByBuilderIndex returns all cached bids for a builder index within the [minSlot, maxSlot]
// slot window (maxSlot nil = no upper bound). Recent bids live only in this cache until they are
// flushed to the DB, so callers must merge these with the DB results to get a complete picture.
func (cache *blockBidCache) GetBidsByBuilderIndex(builderIndex int64, minSlot uint64, maxSlot *uint64) []*dbtypes.BlockBid {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	result := make([]*dbtypes.BlockBid, 0)
	for key, cached := range cache.bids {
		if key.BuilderIndex != builderIndex {
			continue
		}
		if cached.bid.Slot < minSlot {
			continue
		}
		if maxSlot != nil && cached.bid.Slot > *maxSlot {
			continue
		}
		result = append(result, cache.annotateBidLocked(cached))
	}

	return result
}

// GetSlotBids assembles the slot's bids with their per-client observations
// from the cache. Returns nil if the cache holds no bids for the slot.
func (cache *blockBidCache) GetSlotBids(slot phase0.Slot) *btypes.SlotBids {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	bids := make([]*dbtypes.BlockBid, 0, 16)
	seen := make([]map[string]int32, 0, 16)
	for _, cached := range cache.bids {
		if phase0.Slot(cached.bid.Slot) != slot {
			continue
		}
		bids = append(bids, cached.bid)
		seen = append(seen, cache.seenByNameLocked(cached))
	}

	if len(bids) == 0 {
		return nil
	}

	return buildSlotBids(uint64(slot), cache.sessionClientNames(), bids, seen)
}

// buildSlotBids assembles a SlotBids object for one slot. The client table
// holds all session clients (so silent clients stay part of the "not seen"
// denominator) plus any other observer names present in the data.
func buildSlotBids(slot uint64, sessionClients []string, bids []*dbtypes.BlockBid, seen []map[string]int32) *btypes.SlotBids {
	clients := make([]string, 0, len(sessionClients))
	clientIdx := make(map[string]int, len(sessionClients))
	addClient := func(name string) int {
		if idx, ok := clientIdx[name]; ok {
			return idx
		}
		idx := len(clients)
		clients = append(clients, name)
		clientIdx[name] = idx
		return idx
	}
	for _, name := range sessionClients {
		addClient(name)
	}
	for _, bidSeen := range seen {
		for name := range bidSeen {
			addClient(name)
		}
	}

	obj := &btypes.SlotBids{
		Slot:    slot,
		Clients: clients,
		Bids:    make([]*btypes.SlotBidsEntry, 0, len(bids)),
	}
	for i, bid := range bids {
		observations := make(map[int]int32, len(seen[i]))
		for name, offset := range seen[i] {
			observations[clientIdx[name]] = offset
		}
		mask, times := btypes.NewSeenObservations(observations, len(clients))
		obj.Bids = append(obj.Bids, &btypes.SlotBidsEntry{
			Bid:       bid,
			SeenMask:  mask,
			SeenTimes: times,
		})
	}

	return obj
}

// flushEntry carries a bid and its name-keyed observations out of the lock.
type flushEntry struct {
	bid  *dbtypes.BlockBid
	seen map[string]int32
}

// collectFlushEntriesLocked removes the given cached bids from the cache and
// converts them to flush entries with final seen counters. Caller must hold
// the write lock.
func (cache *blockBidCache) collectFlushEntriesLocked(toFlush map[bidCacheKey]*cachedBid) []*flushEntry {
	entries := make([]*flushEntry, 0, len(toFlush))
	for key, cached := range toFlush {
		annotated := cache.annotateBidLocked(cached)
		entries = append(entries, &flushEntry{
			bid:  annotated,
			seen: cache.seenByNameLocked(cached),
		})
		delete(cache.bids, key)
	}
	return entries
}

// persistFlushEntries writes flushed bids to the SQL DB and their per-slot
// observations to the blockdb (merged with any previously stored object).
// Must be called without holding the cache lock.
func (cache *blockBidCache) persistFlushEntries(entries []*flushEntry, sessionClients []string) error {
	if len(entries) == 0 {
		return nil
	}

	if blockdb.GlobalBlockDb.SupportsSlotBids() {
		bySlot := make(map[uint64][]*flushEntry, bidCacheMaxSlots)
		for _, entry := range entries {
			bySlot[entry.bid.Slot] = append(bySlot[entry.bid.Slot], entry)
		}

		for slot, slotEntries := range bySlot {
			bids := make([]*dbtypes.BlockBid, 0, len(slotEntries))
			seen := make([]map[string]int32, 0, len(slotEntries))
			for _, entry := range slotEntries {
				bids = append(bids, entry.bid)
				seen = append(seen, entry.seen)
			}
			obj := buildSlotBids(slot, sessionClients, bids, seen)

			// Merge with a previously stored object: the slot may have been
			// flushed before (partial late bids, restart overlap).
			stored, err := blockdb.GlobalBlockDb.GetSlotBids(cache.indexer.ctx, slot)
			if err != nil {
				cache.indexer.logger.Warnf("error loading stored bids object for slot %d: %v", slot, err)
			}
			merged := btypes.MergeSlotBids(stored, obj)

			if _, err := blockdb.GlobalBlockDb.AddSlotBids(cache.indexer.ctx, merged); err != nil {
				cache.indexer.logger.Errorf("error persisting bids object for slot %d: %v", slot, err)
			}
		}
	}

	bidsToFlush := make([]*dbtypes.BlockBid, 0, len(entries))
	for _, entry := range entries {
		bidsToFlush = append(bidsToFlush, entry.bid)
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertBids(bidsToFlush, tx)
	})
	if err != nil {
		cache.indexer.logger.Errorf("error flushing bids to db: %v", err)
		return err
	}

	return nil
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
	toFlush := make(map[bidCacheKey]*cachedBid, len(cache.bids))
	for key, cached := range cache.bids {
		if phase0.Slot(cached.bid.Slot) < cutoffSlot {
			toFlush[key] = cached
		}
	}
	entries := cache.collectFlushEntriesLocked(toFlush)
	sessionClients := cache.sessionClientNames()

	// Update minSlot
	cache.minSlot = cutoffSlot

	cache.cacheMutex.Unlock()

	// Write to DB outside of lock
	if len(entries) > 0 {
		if err := cache.persistFlushEntries(entries, sessionClients); err != nil {
			return err
		}
		cache.indexer.logger.Debugf("flushed %d bids to DB (slots < %d)", len(entries), cutoffSlot)
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

	toFlush := make(map[bidCacheKey]*cachedBid, len(cache.bids))
	for key, cached := range cache.bids {
		toFlush[key] = cached
	}
	entries := cache.collectFlushEntriesLocked(toFlush)
	sessionClients := cache.sessionClientNames()

	cache.minSlot = 0
	cache.maxSlot = 0

	cache.cacheMutex.Unlock()

	// Write to DB outside of lock
	if err := cache.persistFlushEntries(entries, sessionClients); err != nil {
		return err
	}

	cache.indexer.logger.Infof("flushed %d bids to DB on shutdown", len(entries))
	return nil
}
