package beacon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
)

const (
	// proofFetchDelaySlotsDefault is the number of slots to wait after a block's slot
	// before its proofs are requested for the first time: proving takes a while.
	proofFetchDelaySlotsDefault = 2
	// proofRetryIntervalSlotsDefault is the number of slots between two requests for
	// the proofs of the same block.
	proofRetryIntervalSlotsDefault = 2
	// proofFetchConcurrency bounds the parallel proof requests.
	proofFetchConcurrency = 4
	// proofFetchTimeout bounds one proof request.
	proofFetchTimeout = 20 * time.Second
	// proofApiRecheckEpochs is how long a client that answered the proof routes with
	// 404/501 is not asked again.
	proofApiRecheckEpochs = 1
	// proofItemHeaderSize is the size of the crash copy item header: flags, proof
	// type, validator index and signature precede the proof bytes.
	proofItemHeaderSize = 1 + 1 + 8 + 96
	// proofItemFlagSigned marks a proof that carries a validator signature.
	proofItemFlagSigned = 1
)

// proofApiKind is the execution proofs API shape a client serves.
type proofApiKind uint8

const (
	proofApiUnknown proofApiKind = iota
	proofApiNone                 // no proof engine or no route
	proofApiByRoot               // GET /eth/v1/beacon/execution_proofs/{block_root} (Lighthouse)
	proofApiBySlot               // GET /eth/v1/beacon/pool/execution_proofs?slot= (Lodestar)
)

// proofClientState remembers which proof API a client serves.
type proofClientState struct {
	kind proofApiKind
	// recheckEpoch is the epoch from which a client without proof API is probed again.
	recheckEpoch phase0.Epoch
}

// proofFetchEntry is a block whose proofs are still being collected.
type proofFetchEntry struct {
	slot      phase0.Slot
	root      phase0.Root
	nextFetch phase0.Slot
	fetching  bool
	// preferredClient served proofs for this block before and is asked first.
	preferredClient string
	proofs          map[uint8]*btypes.SlotExecutionProof
	// dirty holds the proof types not written as crash copy yet.
	dirty map[uint8]bool
}

// executionProofCache collects the EIP-8025 execution proofs of recent blocks from the
// proof-capable beacon nodes for the bid cache's flush window.
//
// Proofs are produced asynchronously after a block and are only served by the nodes
// for a short window (Lighthouse keeps them in a 32-payload LRU, Lodestar in its op
// pool), and no client publishes them as event stream topic. So every block the
// indexer sees is queued, its proofs are requested a few slots after its slot and
// again every retry interval, until the slot leaves the cache with the flush. The
// proof count is then written into the block's metadata and the proofs are handed to
// the slot's meta object where a blockdb exists. Every proof is written to the
// unfinalized_metadata table as crash copy on the slot tick.
type executionProofCache struct {
	indexer *Indexer

	disabled      bool
	fetchDelay    phase0.Slot
	retryInterval phase0.Slot
	fetchSem      chan struct{}

	cacheMutex sync.RWMutex
	entries    map[phase0.Root]*proofFetchEntry
	clients    map[string]*proofClientState
}

func newExecutionProofCache(indexer *Indexer) *executionProofCache {
	cache := &executionProofCache{
		indexer:       indexer,
		fetchDelay:    proofFetchDelaySlotsDefault,
		retryInterval: proofRetryIntervalSlotsDefault,
		fetchSem:      make(chan struct{}, proofFetchConcurrency),
		entries:       make(map[phase0.Root]*proofFetchEntry, 64),
		clients:       make(map[string]*proofClientState, 8),
	}

	if utils.Config != nil {
		cfg := utils.Config.Indexer.ExecutionProofs
		cache.disabled = cfg.Disabled
		if cfg.FetchDelaySlots > 0 {
			cache.fetchDelay = phase0.Slot(cfg.FetchDelaySlots)
		}
		if cfg.RetryIntervalSlots > 0 {
			cache.retryInterval = phase0.Slot(cfg.RetryIntervalSlots)
		}
	}

	return cache
}

// trackBlock queues a newly seen block for proof collection.
func (cache *executionProofCache) trackBlock(slot phase0.Slot, root phase0.Root) {
	if cache.disabled {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.trackBlockLocked(slot, root)
}

// trackBlocks queues the given blocks for proof collection (the blocks of the
// collection window after a restart).
func (cache *executionProofCache) trackBlocks(blocks []*Block) {
	if cache.disabled {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for _, block := range blocks {
		cache.trackBlockLocked(block.Slot, block.Root)
	}
}

// trackBlockLocked returns the entry of a block, creating it if needed. Caller
// must hold the write lock.
func (cache *executionProofCache) trackBlockLocked(slot phase0.Slot, root phase0.Root) *proofFetchEntry {
	entry := cache.entries[root]
	if entry == nil {
		entry = &proofFetchEntry{
			slot:      slot,
			root:      root,
			nextFetch: slot + cache.fetchDelay,
			proofs:    make(map[uint8]*btypes.SlotExecutionProof, 2),
			dirty:     make(map[uint8]bool, 2),
		}
		cache.entries[root] = entry
	}
	return entry
}

// slotBounds returns the lowest and highest slot of the queued blocks (0, 0 if none).
func (cache *executionProofCache) slotBounds() (minSlot phase0.Slot, maxSlot phase0.Slot) {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	for _, entry := range cache.entries {
		if minSlot == 0 || entry.slot < minSlot {
			minSlot = entry.slot
		}
		if entry.slot > maxSlot {
			maxSlot = entry.slot
		}
	}

	return minSlot, maxSlot
}

// processDueFetches starts the proof requests that are due at the current slot.
// Requests run in the background, bounded by the fetch semaphore.
func (cache *executionProofCache) processDueFetches(currentSlot phase0.Slot) {
	if cache.disabled {
		return
	}

	cache.cacheMutex.Lock()
	due := make([]*proofFetchEntry, 0, 8)
	for _, entry := range cache.entries {
		if entry.fetching || entry.nextFetch > currentSlot {
			continue
		}
		entry.fetching = true
		entry.nextFetch = currentSlot + cache.retryInterval
		due = append(due, entry)
	}
	cache.cacheMutex.Unlock()

	for _, entry := range due {
		go cache.fetchEntry(entry)
	}
}

// fetchEntry requests the proofs of one block from the proof-capable clients and merges
// them into the entry.
func (cache *executionProofCache) fetchEntry(entry *proofFetchEntry) {
	defer utils.HandleSubroutinePanic("indexer.beacon.executionProofCache.fetchEntry", nil)

	select {
	case cache.fetchSem <- struct{}{}:
	case <-cache.indexer.ctx.Done():
		return
	}
	defer func() { <-cache.fetchSem }()

	defer func() {
		cache.cacheMutex.Lock()
		entry.fetching = false
		cache.cacheMutex.Unlock()
	}()

	proofs, clientName := cache.loadProofs(entry)
	if len(proofs) == 0 {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	entry.preferredClient = clientName
	cache.addProofsLocked(entry, proofs)
}

// addProofsLocked merges proofs into an entry, keeping the first proof of each
// type. Caller must hold the write lock.
func (cache *executionProofCache) addProofsLocked(entry *proofFetchEntry, proofs []*btypes.SlotExecutionProof) {
	for _, proof := range proofs {
		if entry.proofs[proof.ProofType] != nil {
			continue
		}
		entry.proofs[proof.ProofType] = proof
		entry.dirty[proof.ProofType] = true
	}
}

// loadProofs asks the proof-capable clients for the proofs of the entry's block, the
// client that served proofs for it before first, and returns the proofs of the first
// client that has any together with the client's name.
func (cache *executionProofCache) loadProofs(entry *proofFetchEntry) ([]*btypes.SlotExecutionProof, string) {
	cache.cacheMutex.RLock()
	preferred := entry.preferredClient
	cache.cacheMutex.RUnlock()

	clients := cache.indexer.GetReadyClients(false)
	ordered := make([]*Client, 0, len(clients))
	for _, client := range clients {
		if client.client.GetName() == preferred {
			ordered = append([]*Client{client}, ordered...)
		} else {
			ordered = append(ordered, client)
		}
	}

	currentEpoch := cache.indexer.consensusPool.GetChainState().CurrentEpoch()
	for _, client := range ordered {
		proofs, err := cache.loadProofsFromClient(client, entry, currentEpoch)
		if err != nil {
			cache.indexer.logger.WithError(err).Debugf("error loading execution proofs for block 0x%x from %s", entry.root, client.client.GetName())
			continue
		}
		if len(proofs) > 0 {
			return proofs, client.client.GetName()
		}
	}

	return nil, ""
}

// loadProofsFromClient requests the proofs of the entry's block from one client via
// the API shape the client serves, probing the shapes on first contact. A client that
// serves none is not asked again before its recheck epoch.
func (cache *executionProofCache) loadProofsFromClient(client *Client, entry *proofFetchEntry, currentEpoch phase0.Epoch) ([]*btypes.SlotExecutionProof, error) {
	name := client.client.GetName()

	cache.cacheMutex.Lock()
	state := cache.clients[name]
	if state == nil {
		state = &proofClientState{}
		cache.clients[name] = state
	}
	kind := state.kind
	if kind == proofApiNone && currentEpoch >= state.recheckEpoch {
		kind = proofApiUnknown
	}
	cache.cacheMutex.Unlock()

	if kind == proofApiNone {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(cache.indexer.ctx, proofFetchTimeout)
	defer cancel()

	rpcClient := client.client.GetRPCClient()
	kinds := []proofApiKind{kind}
	if kind == proofApiUnknown {
		kinds = []proofApiKind{proofApiByRoot, proofApiBySlot}
	}

	for _, tryKind := range kinds {
		var proofs []*rpc.ExecutionProof
		var err error
		switch tryKind {
		case proofApiByRoot:
			proofs, err = rpcClient.GetExecutionProofsByBlockroot(ctx, entry.root[:])
		case proofApiBySlot:
			proofs, err = rpcClient.GetExecutionProofsBySlot(ctx, entry.slot)
		}

		switch {
		case err == nil:
			cache.setClientApi(state, tryKind, 0)
			return cache.convertProofs(entry, tryKind, proofs), nil
		case errors.Is(err, rpc.ErrExecutionProofsUnsupported):
			// no proof engine: the node answers the same for every block
			cache.setClientApi(state, proofApiNone, currentEpoch+proofApiRecheckEpochs)
			return nil, nil
		case errors.Is(err, rpc.ErrExecutionProofsNotFound):
			// unknown route (or unknown block on a route the node serves - the block
			// was seen on this client, so that is the rare case): try the next shape
			continue
		default:
			return nil, err
		}
	}

	if kind == proofApiUnknown {
		cache.setClientApi(state, proofApiNone, currentEpoch+proofApiRecheckEpochs)
	}

	return nil, nil
}

// setClientApi remembers the proof API shape of a client.
func (cache *executionProofCache) setClientApi(state *proofClientState, kind proofApiKind, recheckEpoch phase0.Epoch) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	state.kind = kind
	state.recheckEpoch = recheckEpoch
}

// convertProofs turns the proofs served by a client into meta object proofs of the
// entry's block. By-root proofs are bound to a block root by the node; by-slot proofs
// (Lodestar's pool, keyed by slot without block root) are attributed to the block
// only when it is the only block the indexer knows in the slot.
func (cache *executionProofCache) convertProofs(entry *proofFetchEntry, kind proofApiKind, proofs []*rpc.ExecutionProof) []*btypes.SlotExecutionProof {
	if len(proofs) == 0 {
		return nil
	}
	if kind == proofApiBySlot && len(cache.indexer.blockCache.getBlocksBySlot(entry.slot)) != 1 {
		cache.indexer.logger.Debugf("ignoring %d by-slot execution proofs for slot %d: multiple blocks in slot", len(proofs), entry.slot)
		return nil
	}

	converted := make([]*btypes.SlotExecutionProof, 0, len(proofs))
	for _, proof := range proofs {
		if kind == proofApiByRoot && proof.BeaconBlockRoot != entry.root {
			continue
		}
		converted = append(converted, &btypes.SlotExecutionProof{
			BlockRoot:      entry.root,
			ProofType:      proof.ProofType,
			Signed:         proof.Signed,
			ValidatorIndex: proof.ValidatorIndex,
			Signature:      proof.Signature,
			DataSize:       uint32(len(proof.ProofData)),
			ProofData:      proof.ProofData,
		})
	}

	return converted
}

// executionProofItemKey returns the crash copy key of a proof: block root and type.
func executionProofItemKey(root phase0.Root, proofType uint8) []byte {
	key := make([]byte, 33)
	copy(key[0:32], root[:])
	key[32] = proofType
	return key
}

// encodeExecutionProofItem encodes a proof as crash copy: flags, type, validator
// index and signature, then the proof bytes.
func encodeExecutionProofItem(proof *btypes.SlotExecutionProof) []byte {
	data := make([]byte, proofItemHeaderSize+len(proof.ProofData))
	if proof.Signed {
		data[0] |= proofItemFlagSigned
	}
	data[1] = proof.ProofType
	binary.BigEndian.PutUint64(data[2:10], proof.ValidatorIndex)
	copy(data[10:106], proof.Signature[:])
	copy(data[proofItemHeaderSize:], proof.ProofData)
	return data
}

// decodeExecutionProofItem decodes a crash copy written by encodeExecutionProofItem.
func decodeExecutionProofItem(root phase0.Root, data []byte, withData bool) (*btypes.SlotExecutionProof, error) {
	if len(data) < proofItemHeaderSize {
		return nil, fmt.Errorf("execution proof item too short")
	}

	proof := &btypes.SlotExecutionProof{
		BlockRoot:      root,
		ProofType:      data[1],
		Signed:         data[0]&proofItemFlagSigned != 0,
		ValidatorIndex: binary.BigEndian.Uint64(data[2:10]),
		DataSize:       uint32(len(data) - proofItemHeaderSize),
	}
	copy(proof.Signature[:], data[10:106])
	if withData {
		proof.ProofData = data[proofItemHeaderSize:]
	}

	return proof, nil
}

// collectDirtyEntries returns the crash copies of the proofs collected since
// the last call and clears their marks.
func (cache *executionProofCache) collectDirtyEntries() []*dbtypes.UnfinalizedMetadata {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	entries := make([]*dbtypes.UnfinalizedMetadata, 0, 8)
	for _, entry := range cache.entries {
		for proofType := range entry.dirty {
			delete(entry.dirty, proofType)
			proof := entry.proofs[proofType]
			if proof == nil {
				continue
			}
			entries = append(entries, &dbtypes.UnfinalizedMetadata{
				Slot: uint64(entry.slot),
				Kind: dbtypes.UnfinalizedMetadataExecutionProof,
				Key:  executionProofItemKey(entry.root, proofType),
				Data: encodeExecutionProofItem(proof),
			})
		}
	}

	return entries
}

// markDirtyEntries re-marks entries whose crash copy failed to write.
func (cache *executionProofCache) markDirtyEntries(entries []*dbtypes.UnfinalizedMetadata) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for _, entry := range entries {
		if entry.Kind != dbtypes.UnfinalizedMetadataExecutionProof || len(entry.Key) != 33 {
			continue
		}
		if cached := cache.entries[phase0.Root(entry.Key[0:32])]; cached != nil && cached.proofs[entry.Key[32]] != nil {
			cached.dirty[entry.Key[32]] = true
		}
	}
}

// restoreEntry restores a crash copy into the entry of its block.
func (cache *executionProofCache) restoreEntry(entry *dbtypes.UnfinalizedMetadata) error {
	if cache.disabled {
		return nil
	}
	if entry.Kind != dbtypes.UnfinalizedMetadataExecutionProof || len(entry.Key) != 33 {
		return fmt.Errorf("invalid execution proof item key")
	}

	root := phase0.Root(entry.Key[0:32])
	proof, err := decodeExecutionProofItem(root, entry.Data, true)
	if err != nil {
		return err
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cached := cache.trackBlockLocked(phase0.Slot(entry.Slot), root)
	if cached.proofs[proof.ProofType] == nil {
		cached.proofs[proof.ProofType] = proof
	}

	return nil
}

// dropBefore removes the blocks of slots before cutoffSlot from the cache. The
// proof count of each removed block is written into the block's metadata. The
// proofs of the dropped slots are returned as meta objects by slot if
// withObjects is set (a blockdb takes them), together with the blocks whose
// metadata changed and the slots no entry remains for. A block with a request
// in flight is left for the next flush unless includeFetching is set (shutdown),
// so the request's result is not lost.
func (cache *executionProofCache) dropBefore(cutoffSlot phase0.Slot, includeFetching bool, withObjects bool) (map[uint64]*btypes.SlotMeta, []*Block, []uint64) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	objects := make(map[uint64]*btypes.SlotMeta, 8)
	touched := make([]*Block, 0, 8)
	droppedSlots := make(map[uint64]bool, 8)
	for root, entry := range cache.entries {
		if entry.slot >= cutoffSlot {
			continue
		}
		if entry.fetching && !includeFetching {
			droppedSlots[uint64(entry.slot)] = false
			continue
		}
		delete(cache.entries, root)
		if _, ok := droppedSlots[uint64(entry.slot)]; !ok {
			droppedSlots[uint64(entry.slot)] = true
		}

		count := int16(min(len(entry.proofs), 0x7fff))
		if block := cache.indexer.blockCache.getBlockByRoot(root); block != nil {
			block.updateMetadata(func(metadata *BlockMetadata) { metadata.ProofCount = count })
			touched = append(touched, block)
		}

		if withObjects && len(entry.proofs) > 0 {
			obj := objects[uint64(entry.slot)]
			if obj == nil {
				obj = &btypes.SlotMeta{Slot: uint64(entry.slot), Bids: []*btypes.SlotMetaBid{}}
				objects[uint64(entry.slot)] = obj
			}
			obj.ExecutionProofs = append(obj.ExecutionProofs, sortedProofs(entry.proofs)...)
		}
	}

	dropped := make([]uint64, 0, len(droppedSlots))
	for slot, complete := range droppedSlots {
		if complete {
			dropped = append(dropped, slot)
		}
	}

	return objects, touched, dropped
}

// sortedProofs returns the proofs of an entry in proof type order.
func sortedProofs(proofs map[uint8]*btypes.SlotExecutionProof) []*btypes.SlotExecutionProof {
	sorted := make([]*btypes.SlotExecutionProof, 0, len(proofs))
	for proofType := range 256 {
		if proof := proofs[uint8(proofType)]; proof != nil {
			sorted = append(sorted, proof)
		}
	}
	return sorted
}

// getProofs returns the proofs of a block that are still held in memory (not flushed
// yet), in proof type order.
func (cache *executionProofCache) getProofs(root phase0.Root) []*btypes.SlotExecutionProof {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	entry := cache.entries[root]
	if entry == nil {
		return nil
	}

	return sortedProofs(entry.proofs)
}

// getBlockProofCount returns the number of execution proofs known for a block.
func (cache *executionProofCache) getBlockProofCount(block *Block) int16 {
	cache.cacheMutex.RLock()
	entry := cache.entries[block.Root]
	count := 0
	if entry != nil {
		count = len(entry.proofs)
	}
	cache.cacheMutex.RUnlock()

	if entry != nil {
		return int16(min(count, 0x7fff))
	}

	return cache.indexer.blockMetadataValue(block, func(metadata *BlockMetadata) int16 { return metadata.ProofCount })
}

// loadStoredProofs returns the proofs of a block that left the cache from its
// unfinalized_metadata items, where no blockdb took them over, in proof type
// order. The proof bytes are only included if withData is set.
func (cache *executionProofCache) loadStoredProofs(ctx context.Context, slot phase0.Slot, root phase0.Root, withData bool) []*btypes.SlotExecutionProof {
	proofs := make(map[uint8]*btypes.SlotExecutionProof, 2)
	err := db.StreamUnfinalizedSlotMetadata(ctx, uint64(slot), dbtypes.UnfinalizedMetadataExecutionProof, func(entry *dbtypes.UnfinalizedMetadata) {
		if len(entry.Key) != 33 || phase0.Root(entry.Key[0:32]) != root {
			return
		}
		proof, err := decodeExecutionProofItem(root, entry.Data, withData)
		if err != nil {
			cache.indexer.logger.Warnf("error decoding stored execution proof of slot %d: %v", slot, err)
			return
		}
		proofs[proof.ProofType] = proof
	})
	if err != nil || len(proofs) == 0 {
		return nil
	}

	return sortedProofs(proofs)
}
