package beacon

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/crypto"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/spamoor/txtypes"

	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
)

// cachedInclusionList is an inclusion list with its per-client gossip observations.
type cachedInclusionList struct {
	list *v1.SignedInclusionList
	// seen maps observer client name -> first-seen offset (ms from slot start).
	seen map[string]int32
	// dirty is set when the list or its observations changed since the last
	// crash copy.
	dirty bool
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

// inclusionListCache caches the inclusion lists (EIP-7805) of recent slots
// with their gossip observations and their evaluations against the blocks of
// the following slot, for the bid cache's flush window. When a slot leaves the
// cache its list count and verdicts are written into the metadata of the
// blocks they concern, and its lists are handed to the slot's meta object where
// a blockdb exists. Every list and evaluation is written to the
// unfinalized_metadata table as crash copy on the slot tick.
type inclusionListCache struct {
	indexer    *Indexer
	cacheMutex sync.RWMutex
	// lists holds the inclusion lists by their slot.
	lists map[phase0.Slot][]*cachedInclusionList
	// evals holds the evaluations by list slot and target block root. Each
	// evaluation is a self-contained fragment with its own transaction and
	// list tables.
	evals map[phase0.Slot]map[phase0.Root]*btypes.SlotInclusionLists
	// dirtyEvals holds the evaluations not written as crash copy yet.
	dirtyEvals map[phase0.Slot]map[phase0.Root]bool
	// missedListCounts holds the list counts of dropped slots without a cached
	// block, which have no block metadata to carry them; a slot is dropped once
	// it is finalized.
	missedListCounts map[phase0.Slot]int16
	dirtyMissed      map[phase0.Slot]bool
}

// newInclusionListCache creates a new instance of inclusionListCache.
func newInclusionListCache(indexer *Indexer) *inclusionListCache {
	return &inclusionListCache{
		indexer:          indexer,
		lists:            make(map[phase0.Slot][]*cachedInclusionList, 32),
		evals:            make(map[phase0.Slot]map[phase0.Root]*btypes.SlotInclusionLists, 32),
		dirtyEvals:       make(map[phase0.Slot]map[phase0.Root]bool, 32),
		missedListCounts: make(map[phase0.Slot]int16, 8),
		dirtyMissed:      make(map[phase0.Slot]bool, 8),
	}
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
		list:  inclusionList,
		seen:  make(map[string]int32, 8),
		dirty: true,
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
		cached.dirty = true
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

	cache.setEvaluationLocked(slot, fragment)
	dirty := cache.dirtyEvals[slot]
	if dirty == nil {
		dirty = make(map[phase0.Root]bool, 1)
		cache.dirtyEvals[slot] = dirty
	}
	dirty[phase0.Root(fragment.Evals[0].BlockRoot)] = true
}

// setEvaluationLocked stores an evaluation fragment. Caller must hold the write lock.
func (cache *inclusionListCache) setEvaluationLocked(slot phase0.Slot, fragment *btypes.SlotInclusionLists) {
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

	return cache.buildSlotObject(slot, cache.lists[slot], cache.evals[slot])
}

// buildSlotObject assembles the meta object of a slot from the given lists and
// evaluations. Returns nil without lists.
func (cache *inclusionListCache) buildSlotObject(slot phase0.Slot, lists []*cachedInclusionList, evals map[phase0.Root]*btypes.SlotInclusionLists) *btypes.SlotMeta {
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
	for _, fragment := range evals {
		obj = btypes.MergeSlotMeta(obj, &btypes.SlotMeta{
			Slot:           uint64(slot),
			Bids:           []*btypes.SlotMetaBid{},
			InclusionLists: fragment,
		})
	}

	return obj
}

// inclusionListItemKey returns the crash copy key of a list: validator index
// and signature, so an equivocating member's lists are kept apart.
func inclusionListItemKey(list *v1.SignedInclusionList) []byte {
	key := make([]byte, 8+len(list.Signature))
	binary.BigEndian.PutUint64(key[0:8], uint64(list.Message.ValidatorIndex))
	copy(key[8:], list.Signature[:])
	return key
}

// encodeInclusionListItem encodes a cached list as crash copy: the SSZ of the
// signed list, then its observations as client name and offset.
func (cache *inclusionListCache) encodeInclusionListItem(cached *cachedInclusionList) ([]byte, error) {
	listSSZ, err := cache.indexer.dynSsz.MarshalSSZ(cached.list)
	if err != nil {
		return nil, err
	}
	if len(cached.seen) > 0xffff {
		return nil, fmt.Errorf("too many observations: %d", len(cached.seen))
	}

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(listSSZ)))
	buf.Write(listSSZ)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(cached.seen)))
	for name, offset := range cached.seen {
		if len(name) > 0xff {
			name = name[:0xff]
		}
		buf.WriteByte(uint8(len(name)))
		buf.WriteString(name)
		_ = binary.Write(&buf, binary.BigEndian, offset)
	}

	return buf.Bytes(), nil
}

// decodeInclusionListItem decodes a crash copy written by encodeInclusionListItem.
func (cache *inclusionListCache) decodeInclusionListItem(data []byte) (*v1.SignedInclusionList, map[string]int32, error) {
	if len(data) < 4 {
		return nil, nil, fmt.Errorf("inclusion list item too short")
	}
	listLen := int(binary.BigEndian.Uint32(data[0:4]))
	pos := 4
	if pos+listLen+2 > len(data) {
		return nil, nil, fmt.Errorf("inclusion list item out of range")
	}

	list := &v1.SignedInclusionList{}
	if err := cache.indexer.dynSsz.UnmarshalSSZ(list, data[pos:pos+listLen]); err != nil {
		return nil, nil, err
	}
	if list.Message == nil {
		return nil, nil, fmt.Errorf("inclusion list item without message")
	}
	pos += listLen

	count := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	seen := make(map[string]int32, count)
	for range count {
		if pos+1 > len(data) {
			return nil, nil, fmt.Errorf("inclusion list observation out of range")
		}
		nameLen := int(data[pos])
		pos++
		if pos+nameLen+4 > len(data) {
			return nil, nil, fmt.Errorf("inclusion list observation out of range")
		}
		name := string(data[pos : pos+nameLen])
		pos += nameLen
		seen[name] = int32(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
	}

	return list, seen, nil
}

// collectDirtyEntries returns the crash copies of the lists, evaluations and
// missed slot counts that changed since the last call and clears their marks.
func (cache *inclusionListCache) collectDirtyEntries() []*dbtypes.UnfinalizedMetadata {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	entries := make([]*dbtypes.UnfinalizedMetadata, 0, 16)
	for slot, lists := range cache.lists {
		for _, cached := range lists {
			if !cached.dirty {
				continue
			}
			cached.dirty = false

			data, err := cache.encodeInclusionListItem(cached)
			if err != nil {
				cache.indexer.logger.Warnf("error encoding inclusion list of slot %d: %v", slot, err)
				continue
			}
			entries = append(entries, &dbtypes.UnfinalizedMetadata{
				Slot: uint64(slot),
				Kind: dbtypes.UnfinalizedMetadataInclusionList,
				Key:  inclusionListItemKey(cached.list),
				Data: data,
			})
		}
	}
	for slot, roots := range cache.dirtyEvals {
		delete(cache.dirtyEvals, slot)
		for root := range roots {
			fragment := cache.evals[slot][root]
			if fragment == nil {
				continue
			}
			data, err := btypes.EncodeInclusionListFragment(fragment)
			if err != nil {
				cache.indexer.logger.Warnf("error encoding inclusion list evaluation of slot %d: %v", slot, err)
				continue
			}
			entries = append(entries, &dbtypes.UnfinalizedMetadata{
				Slot: uint64(slot),
				Kind: dbtypes.UnfinalizedMetadataInclusionListEval,
				Key:  root[:],
				Data: data,
			})
		}
	}
	for slot := range cache.dirtyMissed {
		delete(cache.dirtyMissed, slot)
		count, ok := cache.missedListCounts[slot]
		if !ok {
			continue
		}
		data := make([]byte, 2)
		binary.BigEndian.PutUint16(data, uint16(count))
		entries = append(entries, &dbtypes.UnfinalizedMetadata{
			Slot: uint64(slot),
			Kind: dbtypes.UnfinalizedMetadataSlotListCount,
			Key:  []byte{},
			Data: data,
		})
	}

	return entries
}

// markDirtyEntries re-marks entries whose crash copy failed to write.
func (cache *inclusionListCache) markDirtyEntries(entries []*dbtypes.UnfinalizedMetadata) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for _, entry := range entries {
		slot := phase0.Slot(entry.Slot)
		switch entry.Kind {
		case dbtypes.UnfinalizedMetadataInclusionList:
			for _, cached := range cache.lists[slot] {
				if bytes.Equal(inclusionListItemKey(cached.list), entry.Key) {
					cached.dirty = true
				}
			}
		case dbtypes.UnfinalizedMetadataInclusionListEval:
			if len(entry.Key) == 32 && cache.evals[slot][phase0.Root(entry.Key)] != nil {
				if cache.dirtyEvals[slot] == nil {
					cache.dirtyEvals[slot] = make(map[phase0.Root]bool, 1)
				}
				cache.dirtyEvals[slot][phase0.Root(entry.Key)] = true
			}
		case dbtypes.UnfinalizedMetadataSlotListCount:
			if _, ok := cache.missedListCounts[slot]; ok {
				cache.dirtyMissed[slot] = true
			}
		}
	}
}

// restoreEntry restores a crash copy into the cache. The lists of a slot
// restore before their evaluations (stream order), so the evaluation finds
// its slot.
func (cache *inclusionListCache) restoreEntry(entry *dbtypes.UnfinalizedMetadata) error {
	slot := phase0.Slot(entry.Slot)

	switch entry.Kind {
	case dbtypes.UnfinalizedMetadataInclusionList:
		list, seen, err := cache.decodeInclusionListItem(entry.Data)
		if err != nil {
			return err
		}
		if list.Message.Slot != slot {
			return fmt.Errorf("inclusion list slot mismatch: %d != %d", list.Message.Slot, slot)
		}

		cache.cacheMutex.Lock()
		defer cache.cacheMutex.Unlock()

		cached := cache.addInclusionListLocked(list)
		for name, offset := range seen {
			cached.recordSeen(name, offset)
		}
		cached.dirty = false

	case dbtypes.UnfinalizedMetadataInclusionListEval:
		fragment, err := btypes.DecodeInclusionListFragment(entry.Data)
		if err != nil {
			return err
		}
		if len(fragment.Evals) != 1 {
			return fmt.Errorf("inclusion list evaluation item with %d evaluations", len(fragment.Evals))
		}

		cache.cacheMutex.Lock()
		defer cache.cacheMutex.Unlock()

		if len(cache.lists[slot]) > 0 {
			cache.setEvaluationLocked(slot, fragment)
		}

	case dbtypes.UnfinalizedMetadataSlotListCount:
		if len(entry.Data) != 2 {
			return fmt.Errorf("invalid slot list count item")
		}

		cache.cacheMutex.Lock()
		defer cache.cacheMutex.Unlock()

		cache.missedListCounts[slot] = int16(binary.BigEndian.Uint16(entry.Data))
	}

	return nil
}

// dropBefore removes all slots before cutoffSlot from the cache. The list
// count of each slot is written into the metadata of the slot's cached blocks
// (or kept for a slot without one), the verdicts into the metadata of the
// cached blocks of the following slot. The meta objects of the dropped slots
// are returned by slot if withObjects is set (a blockdb takes them), together
// with the blocks whose metadata changed and the dropped slots.
func (cache *inclusionListCache) dropBefore(cutoffSlot phase0.Slot, withObjects bool) (map[uint64]*btypes.SlotMeta, []*Block, []uint64) {
	finalizedSlot := cache.indexer.consensusPool.GetChainState().GetFinalizedSlot()

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	objects := make(map[uint64]*btypes.SlotMeta, 8)
	touched := make([]*Block, 0, 8)
	dropped := make([]uint64, 0, 8)
	for slot, lists := range cache.lists {
		if slot >= cutoffSlot {
			continue
		}
		evals := cache.evals[slot]

		count := int16(min(len(lists), 0x7fff))
		blocks := cache.indexer.blockCache.getBlocksBySlot(slot)
		for _, block := range blocks {
			block.updateMetadata(func(metadata *BlockMetadata) { metadata.IlCount = count })
		}
		if len(blocks) == 0 {
			cache.missedListCounts[slot] = count
			cache.dirtyMissed[slot] = true
		}
		touched = append(touched, blocks...)

		for _, block := range cache.indexer.blockCache.getBlocksBySlot(slot + 1) {
			unsatisfied := inclusionListUnsatisfiedCount(evals[block.Root], block.Root)
			block.updateMetadata(func(metadata *BlockMetadata) { metadata.IlUnsatisfied = unsatisfied })
			touched = append(touched, block)
		}

		if withObjects {
			if obj := cache.buildSlotObject(slot, lists, evals); obj != nil {
				objects[uint64(slot)] = obj
			}
		}

		delete(cache.lists, slot)
		delete(cache.evals, slot)
		delete(cache.dirtyEvals, slot)
		dropped = append(dropped, uint64(slot))
	}

	for slot := range cache.missedListCounts {
		if slot < finalizedSlot {
			delete(cache.missedListCounts, slot)
			delete(cache.dirtyMissed, slot)
		}
	}

	return objects, touched, dropped
}

// inclusionListUnsatisfiedCount returns the number of transactions of the
// given evaluation fragment the block left unsatisfied: -1 if lists exist but
// were not (fully) evaluated against the block.
func inclusionListUnsatisfiedCount(fragment *btypes.SlotInclusionLists, blockRoot phase0.Root) int16 {
	if fragment == nil {
		return -1
	}

	eval := fragment.GetEval(blockRoot)
	if eval == nil {
		return -1
	}

	unsatisfied, complete := eval.CountUnsatisfied()
	if unsatisfied == 0 && !complete {
		return -1
	}

	return int16(min(unsatisfied, 0x7fff))
}

// getSlotListCount returns the number of inclusion lists published in a slot
// without a block: from the cache while the slot is collected and from the
// count kept at the drop afterwards. A slot the cache never held is unknown.
func (cache *inclusionListCache) getSlotListCount(slot phase0.Slot) int16 {
	cache.cacheMutex.RLock()
	cached := len(cache.lists[slot])
	count, known := cache.missedListCounts[slot]
	cache.cacheMutex.RUnlock()

	if cached > 0 {
		return int16(min(cached, 0x7fff))
	}
	if known {
		return count
	}
	if uint64(slot) >= cache.indexer.metadataCutoff.Load() {
		return 0
	}

	return dbtypes.MetadataUnknown
}

// getBlockListCount returns the number of inclusion lists published in the
// block's slot.
func (cache *inclusionListCache) getBlockListCount(block *Block) int16 {
	cache.cacheMutex.RLock()
	cached := len(cache.lists[block.Slot])
	cache.cacheMutex.RUnlock()

	if cached > 0 {
		return int16(min(cached, 0x7fff))
	}

	return cache.indexer.blockMetadataValue(block, func(metadata *BlockMetadata) int16 { return metadata.IlCount })
}

// getBlockUnsatisfied returns the number of transactions from the inclusion
// lists of the previous slot that the given block left unsatisfied: 0 without
// lists, -1 if the lists have not been (fully) evaluated against the block.
func (cache *inclusionListCache) getBlockUnsatisfied(block *Block) int16 {
	if block.Slot == 0 {
		return 0
	}
	slot := block.Slot - 1

	cache.cacheMutex.RLock()
	cached := len(cache.lists[slot]) > 0
	fragment := cache.evals[slot][block.Root]
	cache.cacheMutex.RUnlock()

	if cached {
		return inclusionListUnsatisfiedCount(fragment, block.Root)
	}

	return cache.indexer.blockMetadataValue(block, func(metadata *BlockMetadata) int16 { return metadata.IlUnsatisfied })
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

// loadStoredSlotObject assembles the meta object of a slot that left the cache
// from its unfinalized_metadata items, where no blockdb took them over. Returns
// nil if there are none.
func (cache *inclusionListCache) loadStoredSlotObject(ctx context.Context, slot phase0.Slot) *btypes.SlotMeta {
	lists := make([]*cachedInclusionList, 0, 16)
	err := db.StreamUnfinalizedSlotMetadata(ctx, uint64(slot), dbtypes.UnfinalizedMetadataInclusionList, func(entry *dbtypes.UnfinalizedMetadata) {
		list, seen, err := cache.decodeInclusionListItem(entry.Data)
		if err != nil {
			cache.indexer.logger.Warnf("error decoding stored inclusion list of slot %d: %v", slot, err)
			return
		}
		lists = append(lists, &cachedInclusionList{list: list, seen: seen})
	})
	if err != nil || len(lists) == 0 {
		return nil
	}

	evals := make(map[phase0.Root]*btypes.SlotInclusionLists, 2)
	_ = db.StreamUnfinalizedSlotMetadata(ctx, uint64(slot), dbtypes.UnfinalizedMetadataInclusionListEval, func(entry *dbtypes.UnfinalizedMetadata) {
		fragment, err := btypes.DecodeInclusionListFragment(entry.Data)
		if err != nil || len(fragment.Evals) != 1 {
			cache.indexer.logger.Warnf("error decoding stored inclusion list evaluation of slot %d: %v", slot, err)
			return
		}
		evals[phase0.Root(fragment.Evals[0].BlockRoot)] = fragment
	})

	return cache.buildSlotObject(slot, lists, evals)
}
