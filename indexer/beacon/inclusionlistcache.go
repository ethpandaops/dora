package beacon

import (
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/spamoor/txtypes"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/utils"
)

// cachedInclusionList is an inclusion list with its per-client gossip observations.
type cachedInclusionList struct {
	list *v1.SignedInclusionList
	// seen maps observer client name -> first-seen offset (ms from slot start).
	seen map[string]int32
}

// InclusionListObservation is a cached inclusion list with the time it was
// first seen by any client.
type InclusionListObservation struct {
	InclusionList *v1.SignedInclusionList
	// FirstSeen is the earliest observation as ms offset from the start of the
	// list's slot. Valid if HasSeen is set.
	FirstSeen int32
	HasSeen   bool
}

// inclusionListUnsatisfied is the remembered verdict of a flushed slot's lists
// against one block of the following slot.
type inclusionListUnsatisfied struct {
	slot  phase0.Slot // slot of the lists
	count int16
}

// inclusionListCache caches the inclusion lists (EIP-7805) of recent slots
// with their gossip observations and their evaluations against the blocks of
// the following slot. The lists are persisted to the blockdb as part of the
// per-slot meta object, driven by the bid cache flush.
type inclusionListCache struct {
	indexer    *Indexer
	cacheMutex sync.RWMutex
	// lists holds the inclusion lists by their slot.
	lists map[phase0.Slot][]*cachedInclusionList
	// evals holds the evaluations by list slot and target block root. Each
	// evaluation is a self-contained fragment with its own transaction and
	// list tables.
	evals map[phase0.Slot]map[phase0.Root]*btypes.SlotInclusionLists
	// listCounts and unsatisfied hold the index values of slots that left the
	// cache, until the blocks they belong to are persisted.
	listCounts  map[phase0.Slot]int16
	unsatisfied map[phase0.Root]*inclusionListUnsatisfied
	// liveFromSlot is the first slot the cache has complete knowledge of
	// (observed live or restored), once liveKnown is set. Earlier slots are
	// resolved from the blockdb.
	liveFromSlot phase0.Slot
	liveKnown    bool
}

// newInclusionListCache creates a new instance of inclusionListCache.
func newInclusionListCache(indexer *Indexer) *inclusionListCache {
	cache := &inclusionListCache{
		indexer:     indexer,
		lists:       make(map[phase0.Slot][]*cachedInclusionList, 32),
		evals:       make(map[phase0.Slot]map[phase0.Root]*btypes.SlotInclusionLists, 32),
		listCounts:  make(map[phase0.Slot]int16, 128),
		unsatisfied: make(map[phase0.Root]*inclusionListUnsatisfied, 128),
	}

	go cache.cleanupLoop()

	return cache
}

// addInclusionList adds the given inclusion list to the cache. seenClient
// identifies the client that observed the list on gossip and seenOffset is the
// observation time as ms offset from the start of the list's slot.
func (cache *inclusionListCache) addInclusionList(inclusionList *v1.SignedInclusionList, seenClient string, seenOffset int32) {
	if inclusionList == nil || inclusionList.Message == nil {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.addInclusionListLocked(inclusionList).recordSeen(seenClient, seenOffset)
}

// addInclusionListLocked returns the cache entry of the given inclusion list,
// creating it if needed. Caller must hold the write lock.
func (cache *inclusionListCache) addInclusionListLocked(inclusionList *v1.SignedInclusionList) *cachedInclusionList {
	slot := inclusionList.Message.Slot

	for _, cached := range cache.lists[slot] {
		// The same list is reported by every client that saw it. A different
		// list of the same member is an equivocation; both are kept to display
		// them in the explorer.
		if cached.list.Message.ValidatorIndex == inclusionList.Message.ValidatorIndex &&
			cached.list.Signature == inclusionList.Signature {
			return cached
		}
	}

	cached := &cachedInclusionList{
		list: inclusionList,
		seen: make(map[string]int32, 8),
	}
	cache.lists[slot] = append(cache.lists[slot], cached)

	return cached
}

// recordSeen records a gossip observation, keeping the earliest offset per client.
func (cached *cachedInclusionList) recordSeen(client string, offset int32) {
	if client == "" {
		return
	}
	if existing, ok := cached.seen[client]; !ok || offset < existing {
		cached.seen[client] = offset
	}
}

// getInclusionLists returns the cached inclusion lists of the given slot.
func (cache *inclusionListCache) getInclusionLists(slot phase0.Slot) []*InclusionListObservation {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	lists := cache.lists[slot]
	result := make([]*InclusionListObservation, 0, len(lists))
	for _, cached := range lists {
		observation := &InclusionListObservation{InclusionList: cached.list}
		for _, offset := range cached.seen {
			if !observation.HasSeen || offset < observation.FirstSeen {
				observation.FirstSeen, observation.HasSeen = offset, true
			}
		}
		result = append(result, observation)
	}

	return result
}

// getSlots returns the slots the cache holds inclusion lists for.
func (cache *inclusionListCache) getSlots() []phase0.Slot {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	slots := make([]phase0.Slot, 0, len(cache.lists))
	for slot := range cache.lists {
		slots = append(slots, slot)
	}

	return slots
}

// getEvaluation returns the cached evaluation of the slot's lists against the
// given block of the following slot, or nil.
func (cache *inclusionListCache) getEvaluation(slot phase0.Slot, blockRoot phase0.Root) *btypes.SlotInclusionListEval {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	fragment := cache.evals[slot][blockRoot]
	if fragment == nil {
		return nil
	}

	return fragment.Evals[0]
}

// setEvaluation stores the evaluation of a slot's lists against one block of
// the following slot. The fragment must hold exactly one evaluation with its
// own transaction and list tables.
func (cache *inclusionListCache) setEvaluation(slot phase0.Slot, fragment *btypes.SlotInclusionLists) {
	if fragment == nil || len(fragment.Evals) != 1 {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if len(cache.lists[slot]) == 0 {
		// The slot left the cache while it was evaluated.
		return
	}

	evals := cache.evals[slot]
	if evals == nil {
		evals = make(map[phase0.Root]*btypes.SlotInclusionLists, 1)
		cache.evals[slot] = evals
	}
	evals[phase0.Root(fragment.Evals[0].BlockRoot)] = fragment
}

// InclusionListTxHash returns the hash of a raw inclusion list transaction.
func InclusionListTxHash(rawTx []byte) [32]byte {
	if tx, err := txtypes.DecodeTx(rawTx); err == nil {
		return tx.Hash()
	}
	return crypto.Keccak256Hash(rawTx)
}

// getSlotObject assembles the slot's inclusion lists with their committee,
// observations and evaluations as a meta object without bids. Returns nil if
// the cache holds no lists for the slot.
func (cache *inclusionListCache) getSlotObject(slot phase0.Slot) *btypes.SlotMeta {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	return cache.buildSlotObjectLocked(slot)
}

// buildSlotObjectLocked assembles the meta object of a slot from the cache.
// Caller must hold at least the read lock.
func (cache *inclusionListCache) buildSlotObjectLocked(slot phase0.Slot) *btypes.SlotMeta {
	lists := cache.lists[slot]
	if len(lists) == 0 {
		return nil
	}

	clients := make([]string, 0, len(cache.indexer.clients))
	clientIdx := make(map[string]int, len(cache.indexer.clients))
	addClient := func(name string) int {
		if idx, ok := clientIdx[name]; ok {
			return idx
		}
		idx := len(clients)
		clients = append(clients, name)
		clientIdx[name] = idx
		return idx
	}
	// All session clients stay in the table so silent clients are part of the
	// "not seen" denominator.
	for _, client := range cache.indexer.clients {
		addClient(client.client.GetName())
	}
	for _, cached := range lists {
		for name := range cached.seen {
			addClient(name)
		}
	}

	slotLists := &btypes.SlotInclusionLists{
		Committees:   []*btypes.SlotInclusionListCommittee{},
		TxHashes:     make([][32]byte, 0, 64),
		Transactions: make([][]byte, 0, 64),
		Lists:        make([]*btypes.SlotInclusionList, 0, len(lists)),
		Evals:        []*btypes.SlotInclusionListEval{},
	}
	if committee := cache.indexer.getInclusionListCommittee(slot); committee != nil {
		slotLists.Committees = append(slotLists.Committees, committee)
	}

	txIdx := make(map[string]uint16, 64)
	for _, cached := range lists {
		message := cached.list.Message
		entry := &btypes.SlotInclusionList{
			ValidatorIndex: uint64(message.ValidatorIndex),
			DependentRoot:  message.DependentRoot,
			Signature:      cached.list.Signature,
			TxRefs:         make([]uint16, 0, len(message.Transactions)),
		}
		for _, tx := range message.Transactions {
			idx, ok := txIdx[string(tx)]
			if !ok {
				if len(slotLists.Transactions) >= 0xffff {
					continue
				}
				idx = uint16(len(slotLists.Transactions))
				txIdx[string(tx)] = idx
				slotLists.Transactions = append(slotLists.Transactions, tx)
				slotLists.TxHashes = append(slotLists.TxHashes, InclusionListTxHash(tx))
			}
			entry.TxRefs = append(entry.TxRefs, idx)
		}

		observations := make(map[int]int32, len(cached.seen))
		for name, offset := range cached.seen {
			observations[clientIdx[name]] = offset
		}
		entry.SeenMask, entry.SeenTimes = btypes.NewSeenObservations(observations, len(clients))

		slotLists.Lists = append(slotLists.Lists, entry)
	}

	obj := &btypes.SlotMeta{
		Slot:           uint64(slot),
		Clients:        clients,
		Bids:           []*btypes.SlotMetaBid{},
		InclusionLists: slotLists,
	}

	// Each evaluation carries its own tables; merging aligns them with the
	// slot's tables.
	for _, fragment := range cache.evals[slot] {
		obj = btypes.MergeSlotMeta(obj, &btypes.SlotMeta{
			Slot:           uint64(slot),
			Bids:           []*btypes.SlotMetaBid{},
			InclusionLists: fragment,
		})
	}

	return obj
}

// restoreSlotObject restores the inclusion lists, observations and
// evaluations of a stored meta object into the cache.
func (cache *inclusionListCache) restoreSlotObject(obj *btypes.SlotMeta) {
	if obj == nil || obj.InclusionLists == nil || len(obj.InclusionLists.Lists) == 0 {
		return
	}

	stored := obj.InclusionLists
	if len(stored.Transactions) != len(stored.TxHashes) {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	slot := phase0.Slot(obj.Slot)

	for _, entry := range stored.Lists {
		list := &v1.SignedInclusionList{
			Message: &v1.InclusionList{
				Slot:           slot,
				ValidatorIndex: phase0.ValidatorIndex(entry.ValidatorIndex),
				DependentRoot:  entry.DependentRoot,
				Transactions:   make([]bellatrix.Transaction, 0, len(entry.TxRefs)),
			},
			Signature: entry.Signature,
		}
		for _, ref := range entry.TxRefs {
			if int(ref) < len(stored.Transactions) {
				list.Message.Transactions = append(list.Message.Transactions, stored.Transactions[ref])
			}
		}

		cached := cache.addInclusionListLocked(list)
		for clientIdx, offset := range entry.SeenByClientIndex() {
			if clientIdx < len(obj.Clients) {
				cached.recordSeen(obj.Clients[clientIdx], offset)
			}
		}
	}

	for _, eval := range stored.Evals {
		evals := cache.evals[slot]
		if evals == nil {
			evals = make(map[phase0.Root]*btypes.SlotInclusionLists, 1)
			cache.evals[slot] = evals
		}
		if evals[phase0.Root(eval.BlockRoot)] != nil {
			continue
		}

		lists := make([]*btypes.SlotInclusionList, 0, len(stored.Lists))
		for _, entry := range stored.Lists {
			lists = append(lists, &btypes.SlotInclusionList{
				ValidatorIndex: entry.ValidatorIndex,
				DependentRoot:  entry.DependentRoot,
				Signature:      entry.Signature,
				TxRefs:         entry.TxRefs,
			})
		}
		evals[phase0.Root(eval.BlockRoot)] = &btypes.SlotInclusionLists{
			TxHashes:     stored.TxHashes,
			Transactions: stored.Transactions,
			Lists:        lists,
			Evals:        []*btypes.SlotInclusionListEval{eval},
		}
	}
}

// collectFlushObjects removes all slots before cutoffSlot from the cache and
// returns their meta objects by slot. The index values of the removed slots
// stay available through getListCount and getUnsatisfied.
func (cache *inclusionListCache) collectFlushObjects(cutoffSlot phase0.Slot) map[uint64]*btypes.SlotMeta {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	objects := make(map[uint64]*btypes.SlotMeta, 8)
	for slot := range cache.lists {
		if slot >= cutoffSlot {
			continue
		}

		if obj := cache.buildSlotObjectLocked(slot); obj != nil {
			objects[uint64(slot)] = obj
			cache.rememberIndexLocked(obj)
		}

		delete(cache.lists, slot)
		delete(cache.evals, slot)
	}

	return objects
}

// rememberIndexLocked keeps the index values of a slot's object: its list
// count and the verdict for every known block of the following slot. Caller
// must hold the write lock.
func (cache *inclusionListCache) rememberIndexLocked(obj *btypes.SlotMeta) {
	slot := phase0.Slot(obj.Slot)
	cache.listCounts[slot] = inclusionListCount(obj.InclusionLists)

	for _, block := range cache.indexer.blockCache.getBlocksBySlot(slot + 1) {
		cache.unsatisfied[block.Root] = &inclusionListUnsatisfied{
			slot:  slot,
			count: inclusionListUnsatisfiedCount(obj.InclusionLists, block.Root),
		}
	}
	for _, eval := range obj.InclusionLists.Evals {
		cache.unsatisfied[phase0.Root(eval.BlockRoot)] = &inclusionListUnsatisfied{
			slot:  slot,
			count: inclusionListUnsatisfiedCount(obj.InclusionLists, eval.BlockRoot),
		}
	}
}

// inclusionListCount returns the number of lists as stored in the slots index.
func inclusionListCount(lists *btypes.SlotInclusionLists) int16 {
	if lists == nil {
		return 0
	}
	return int16(min(len(lists.Lists), 0x7fff))
}

// inclusionListUnsatisfiedCount returns the number of transactions of the
// given lists the block left unsatisfied: 0 without lists, -1 if the lists
// have not been (fully) evaluated against the block.
func inclusionListUnsatisfiedCount(lists *btypes.SlotInclusionLists, blockRoot [32]byte) int16 {
	if lists == nil || len(lists.Lists) == 0 {
		return 0
	}

	eval := lists.GetEval(blockRoot)
	if eval == nil {
		return -1
	}

	unsatisfied, complete := eval.CountUnsatisfied()
	if unsatisfied == 0 && !complete {
		return -1
	}

	return int16(min(unsatisfied, 0x7fff))
}

// loadStoredLists loads the inclusion lists of a slot that precedes the
// cache's knowledge from the blockdb, without their raw transactions. Returns
// nil if the slot is covered by the cache or nothing is stored.
func (cache *inclusionListCache) loadStoredLists(slot phase0.Slot, isLive bool) *btypes.SlotInclusionLists {
	chainState := cache.indexer.consensusPool.GetChainState()
	if isLive || !chainState.IsEip7805Enabled(chainState.EpochOfSlot(slot)) || !blockdb.GlobalBlockDb.SupportsSlotMeta() {
		return nil
	}

	stored, err := blockdb.GlobalBlockDb.GetSlotMeta(cache.indexer.ctx, uint64(slot), btypes.SlotMetaFlagInclusionLists)
	if err != nil {
		cache.indexer.logger.Warnf("error loading meta object for slot %d: %v", slot, err)
		return nil
	}
	if stored == nil {
		return nil
	}

	return stored.InclusionLists
}

// getListCount returns the number of inclusion lists published in the slot.
func (cache *inclusionListCache) getListCount(slot phase0.Slot) int16 {
	cache.cacheMutex.RLock()
	cached := len(cache.lists[slot])
	count, known := cache.listCounts[slot]
	isLive := cache.liveKnown && slot >= cache.liveFromSlot
	cache.cacheMutex.RUnlock()

	if cached > 0 {
		return int16(min(cached, 0x7fff))
	}
	if known {
		return count
	}

	count = inclusionListCount(cache.loadStoredLists(slot, isLive))
	if !isLive {
		cache.cacheMutex.Lock()
		cache.listCounts[slot] = count
		cache.cacheMutex.Unlock()
	}

	return count
}

// getUnsatisfied returns the number of transactions from the inclusion lists
// of the previous slot that the given block left unsatisfied: 0 without lists,
// -1 if the lists have not been (fully) evaluated against the block.
func (cache *inclusionListCache) getUnsatisfied(blockSlot phase0.Slot, blockRoot phase0.Root) int16 {
	if blockSlot == 0 {
		return 0
	}
	slot := blockSlot - 1

	cache.cacheMutex.RLock()
	cached := len(cache.lists[slot]) > 0
	fragment := cache.evals[slot][blockRoot]
	entry := cache.unsatisfied[blockRoot]
	isLive := cache.liveKnown && slot >= cache.liveFromSlot
	cache.cacheMutex.RUnlock()

	if cached {
		if fragment == nil {
			return -1
		}
		// The fragment holds exactly the evaluation against this block.
		return inclusionListUnsatisfiedCount(fragment, blockRoot)
	}
	if entry != nil {
		return entry.count
	}

	// A flushed live slot is fully remembered; a block that is not is one
	// that appeared after its lists were flushed and was never evaluated.
	cache.cacheMutex.RLock()
	count, known := cache.listCounts[slot]
	cache.cacheMutex.RUnlock()
	if known && isLive {
		if count > 0 {
			return -1
		}
		return 0
	}

	unsatisfied := inclusionListUnsatisfiedCount(cache.loadStoredLists(slot, isLive), blockRoot)
	if !isLive {
		cache.cacheMutex.Lock()
		cache.unsatisfied[blockRoot] = &inclusionListUnsatisfied{slot: slot, count: unsatisfied}
		cache.cacheMutex.Unlock()
	}

	return unsatisfied
}

// setLiveFromSlot sets the first slot the cache has complete knowledge of.
func (cache *inclusionListCache) setLiveFromSlot(slot phase0.Slot) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.liveFromSlot = slot
	cache.liveKnown = true
}

// slotBounds returns the lowest and highest slot in the cache (0, 0 if empty).
func (cache *inclusionListCache) slotBounds() (minSlot phase0.Slot, maxSlot phase0.Slot) {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	for slot := range cache.lists {
		if minSlot == 0 || slot < minSlot {
			minSlot = slot
		}
		if slot > maxSlot {
			maxSlot = slot
		}
	}

	return minSlot, maxSlot
}

// cleanupLoop periodically cleans up old entries from the cache.
func (cache *inclusionListCache) cleanupLoop() {
	defer utils.HandleSubroutinePanic("indexer.beacon.inclusionListCache.cleanupLoop", func() {
		cache.cleanupLoop()
	})

	for {
		time.Sleep(30 * time.Minute)
		cache.cleanupCache()
	}
}

// cleanupCache removes the remembered index values of finalized slots and,
// where the lists cannot be persisted to the blockdb, the lists older than the
// activity history length.
func (cache *inclusionListCache) cleanupCache() {
	chainState := cache.indexer.consensusPool.GetChainState()
	finalizedSlot := chainState.GetFinalizedSlot()

	// The index values are read when a block is persisted on finalization, so
	// they are kept some epochs beyond it.
	retention := phase0.Slot(2 * chainState.GetSpecs().SlotsPerEpoch)

	var cutOffEpoch phase0.Epoch
	if currentEpoch := chainState.CurrentEpoch(); currentEpoch > phase0.Epoch(cache.indexer.activityHistoryLength) {
		cutOffEpoch = currentEpoch - phase0.Epoch(cache.indexer.activityHistoryLength)
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for slot := range cache.listCounts {
		if slot+retention < finalizedSlot {
			delete(cache.listCounts, slot)
		}
	}
	for root, entry := range cache.unsatisfied {
		if entry.slot+retention < finalizedSlot {
			delete(cache.unsatisfied, root)
		}
	}

	deleted := 0
	if !blockdb.GlobalBlockDb.SupportsSlotMeta() {
		for slot, lists := range cache.lists {
			if chainState.EpochOfSlot(slot) < cutOffEpoch {
				deleted += len(lists)
				delete(cache.lists, slot)
				delete(cache.evals, slot)
			}
		}
	}

	cache.indexer.logger.Infof("cleaned up inclusion list cache, deleted %d entries", deleted)
}
