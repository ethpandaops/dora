package beacon

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus/rpc"
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
}

// proofCountEntry is the remembered proof count of a block whose proofs were flushed.
type proofCountEntry struct {
	slot  phase0.Slot
	count uint8
}

// executionProofCache collects the EIP-8025 execution proofs of recent blocks from the
// proof-capable beacon nodes and hands them to the per-slot meta object flush.
//
// Proofs are produced asynchronously after a block and are only served by the nodes
// for a short window (Lighthouse keeps them in a 32-payload LRU, Lodestar in its op
// pool), and no client publishes them as event stream topic. So every block the
// indexer sees is queued, its proofs are requested a few slots after its slot and
// again every retry interval, until the slot's meta object is flushed. The proofs of
// a slot are written once, together with its bids and inclusion lists, and the proof
// bytes leave memory with that flush; only the proof count per block is remembered
// until the block is persisted on finalization.
type executionProofCache struct {
	indexer *Indexer

	disabled      bool
	fetchDelay    phase0.Slot
	retryInterval phase0.Slot
	fetchSem      chan struct{}

	cacheMutex sync.RWMutex
	entries    map[phase0.Root]*proofFetchEntry
	counts     map[phase0.Root]*proofCountEntry
	clients    map[string]*proofClientState
	// liveFromSlot is the first slot the cache has complete knowledge of; the proof
	// counts of earlier slots are read from the blockdb.
	liveFromSlot phase0.Slot
	liveKnown    bool
}

func newExecutionProofCache(indexer *Indexer) *executionProofCache {
	cache := &executionProofCache{
		indexer:       indexer,
		fetchDelay:    proofFetchDelaySlotsDefault,
		retryInterval: proofRetryIntervalSlotsDefault,
		fetchSem:      make(chan struct{}, proofFetchConcurrency),
		entries:       make(map[phase0.Root]*proofFetchEntry, 64),
		counts:        make(map[phase0.Root]*proofCountEntry, 256),
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

	go cache.cleanupLoop()

	return cache
}

// enabled reports whether proofs are collected: the proofs are only kept via the
// blockdb meta objects, so without them there is nothing to collect into.
func (cache *executionProofCache) enabled() bool {
	return !cache.disabled && blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsSlotMeta()
}

// setLiveFromSlot sets the first slot the cache has complete knowledge of.
func (cache *executionProofCache) setLiveFromSlot(slot phase0.Slot) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.liveFromSlot = slot
	cache.liveKnown = true
}

// trackBlock queues a newly seen block for proof collection.
func (cache *executionProofCache) trackBlock(slot phase0.Slot, root phase0.Root) {
	if !cache.enabled() {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if cache.entries[root] != nil {
		return
	}
	cache.entries[root] = &proofFetchEntry{
		slot:      slot,
		root:      root,
		nextFetch: slot + cache.fetchDelay,
		proofs:    make(map[uint8]*btypes.SlotExecutionProof, 2),
	}
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
	if !cache.enabled() {
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
	for _, proof := range proofs {
		entry.proofs[proof.ProofType] = proof
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

// collectFlushObjects removes the blocks of slots before cutoffSlot from the cache
// and returns their proofs as per-slot meta objects, keyed by slot. The proof count
// of each flushed block stays remembered. A block with a request in flight is left
// for the next flush unless includeFetching is set (shutdown), so the request's
// result is not lost.
func (cache *executionProofCache) collectFlushObjects(cutoffSlot phase0.Slot, includeFetching bool) map[uint64]*btypes.SlotMeta {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	objects := make(map[uint64]*btypes.SlotMeta, 8)
	for root, entry := range cache.entries {
		if entry.slot >= cutoffSlot || (entry.fetching && !includeFetching) {
			continue
		}
		delete(cache.entries, root)
		cache.counts[root] = &proofCountEntry{slot: entry.slot, count: uint8(min(len(entry.proofs), 0xff))}
		if len(entry.proofs) == 0 {
			continue
		}

		obj := objects[uint64(entry.slot)]
		if obj == nil {
			obj = &btypes.SlotMeta{Slot: uint64(entry.slot)}
			objects[uint64(entry.slot)] = obj
		}
		obj.ExecutionProofs = append(obj.ExecutionProofs, sortedProofs(entry.proofs)...)
	}

	return objects
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

// getProofCount returns the number of execution proofs known for a block.
func (cache *executionProofCache) getProofCount(slot phase0.Slot, root phase0.Root) uint8 {
	cache.cacheMutex.RLock()
	entry := cache.entries[root]
	remembered := cache.counts[root]
	isLive := cache.liveKnown && slot >= cache.liveFromSlot
	cache.cacheMutex.RUnlock()

	if entry != nil {
		return uint8(min(len(entry.proofs), 0xff))
	}
	if remembered != nil {
		return remembered.count
	}
	if isLive || !cache.enabled() {
		return 0
	}

	// a block that precedes the cache's knowledge: count the stored proofs
	count := uint8(0)
	stored, err := blockdb.GlobalBlockDb.GetSlotMeta(cache.indexer.ctx, uint64(slot), btypes.SlotMetaFlagExecutionProofs)
	if err != nil {
		cache.indexer.logger.Warnf("error loading meta object for slot %d: %v", slot, err)
		return 0
	}
	if stored != nil {
		for _, proof := range stored.ExecutionProofs {
			if proof.BlockRoot == root && count < 0xff {
				count++
			}
		}
	}

	cache.cacheMutex.Lock()
	cache.counts[root] = &proofCountEntry{slot: slot, count: count}
	cache.cacheMutex.Unlock()

	return count
}

// cleanupLoop periodically drops the remembered proof counts of finalized blocks.
func (cache *executionProofCache) cleanupLoop() {
	defer utils.HandleSubroutinePanic("indexer.beacon.executionProofCache.cleanupLoop", func() {
		cache.cleanupLoop()
	})

	for {
		select {
		case <-cache.indexer.ctx.Done():
			return
		case <-time.After(30 * time.Minute):
		}
		cache.cleanupCache()
	}
}

// cleanupCache removes the remembered proof counts of finalized blocks. The counts are
// read when a block is persisted on finalization, so they are kept some epochs beyond it.
func (cache *executionProofCache) cleanupCache() {
	chainState := cache.indexer.consensusPool.GetChainState()
	specs := chainState.GetSpecs()
	if specs == nil {
		return
	}
	finalizedSlot := chainState.GetFinalizedSlot()
	retention := phase0.Slot(2 * specs.SlotsPerEpoch)

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for root, entry := range cache.counts {
		if entry.slot+retention < finalizedSlot {
			delete(cache.counts, root)
		}
	}
}
