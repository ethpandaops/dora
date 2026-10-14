package beacon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/executionproof"
	"github.com/ethpandaops/dora/executionproof/verifiers"
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
	// proofApiRecheckEpochs is how long a client that answered the proofs route with
	// 404/501 is not asked again.
	proofApiRecheckEpochs = 1
	// proofItemHeaderSize is the size of the fixed crash copy item header: flags,
	// proof type, validator index, signature, verification outcome and the
	// verifier name length; the verifier name and the proof bytes follow.
	proofItemHeaderSize = 1 + 1 + 8 + 96 + 1 + 1
	// proofItemFlagSignatureValid and proofItemFlagProverActive hold the signature
	// check outcome as in the meta object.
	proofItemFlagSignatureValid = 1
	proofItemFlagProverActive   = 2
)

// proofClientState remembers whether a client serves the execution proofs route.
type proofClientState struct {
	// unsupported is set for a client without proof engine or route; it is probed
	// again from recheckEpoch on.
	unsupported  bool
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
	// unverified holds the proof types whose verification waits for the block's
	// payload envelope.
	unverified map[uint8]bool
}

// executionProofCache collects the EIP-8025 execution proofs of recent blocks from the
// proof-capable beacon nodes for the bid cache's flush window.
//
// Proofs are produced asynchronously after a block and are only served by the nodes
// for a short window (a cache of the last payloads), and no client publishes them as
// event stream topic. So every block the
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

	// verifiers runs the embedded zkVM verifiers, created on the first proof;
	// nil when verification is disabled.
	verifiers            *verifiers.Verifiers
	verifiersOnce        sync.Once
	verificationDisabled bool
	verifierCacheDir     string
	// verifierTempDir is the compilation cache dir created for this instance
	// when none is configured; removed on close.
	verifierTempDir string

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
		cache.verificationDisabled = cfg.VerificationDisabled
		cache.verifierCacheDir = cfg.VerifierCacheDir
	}

	return cache
}

// getVerifiers returns the zkVM verifiers, created on first use; nil when
// verification is disabled or the verifiers cannot be set up.
func (cache *executionProofCache) getVerifiers() *verifiers.Verifiers {
	if cache.verificationDisabled {
		return nil
	}

	cache.verifiersOnce.Do(func() {
		cacheDir := cache.verifierCacheDir
		if cacheDir == "" {
			// one dir per instance: several explorers may share the temp dir
			tempDir, err := os.MkdirTemp("", "dora-verifiers-")
			if err != nil {
				cache.indexer.logger.WithError(err).Warnf("execution proof verification disabled")
				return
			}
			cache.verifierTempDir = tempDir
			cacheDir = tempDir
		}
		proofVerifiers, err := verifiers.New(cache.indexer.ctx, cacheDir)
		if err != nil {
			cache.indexer.logger.WithError(err).Warnf("execution proof verification disabled")
			return
		}
		cache.verifiers = proofVerifiers
	})

	return cache.verifiers
}

// close releases the verifiers and the compilation cache dir created for this
// instance.
func (cache *executionProofCache) close() {
	if cache.verifiers != nil {
		if err := cache.verifiers.Close(cache.indexer.ctx); err != nil {
			cache.indexer.logger.WithError(err).Warnf("error closing execution proof verifiers")
		}
	}
	if cache.verifierTempDir != "" {
		if err := os.RemoveAll(cache.verifierTempDir); err != nil {
			cache.indexer.logger.WithError(err).Warnf("error removing verifier cache dir")
		}
	}
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
			slot:       slot,
			root:       root,
			nextFetch:  slot + cache.fetchDelay,
			proofs:     make(map[uint8]*btypes.SlotExecutionProof, 2),
			dirty:      make(map[uint8]bool, 2),
			unverified: make(map[uint8]bool, 2),
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

// processDueFetches starts the proof requests that are due at the current slot
// and retries the verifications that waited for a block's payload. Requests
// run in the background, bounded by the fetch semaphore.
func (cache *executionProofCache) processDueFetches(currentSlot phase0.Slot) {
	if cache.disabled {
		return
	}

	cache.cacheMutex.Lock()
	due := make([]*proofFetchEntry, 0, 8)
	pending := make([]*proofFetchEntry, 0, 4)
	for _, entry := range cache.entries {
		if len(entry.unverified) > 0 && !entry.fetching {
			pending = append(pending, entry)
		}
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
	for _, entry := range pending {
		cache.verifyPending(entry)
	}
}

// verifyPending verifies the proofs of an entry whose verification waited for
// the block's payload envelope.
func (cache *executionProofCache) verifyPending(entry *proofFetchEntry) {
	cache.cacheMutex.RLock()
	proofs := make([]*btypes.SlotExecutionProof, 0, len(entry.unverified))
	for proofType := range entry.unverified {
		if proof := entry.proofs[proofType]; proof != nil {
			proofs = append(proofs, proof)
		}
	}
	cache.cacheMutex.RUnlock()

	cache.verifyProofs(entry, proofs)
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
	cache.checkSignatures(entry, proofs)
	cache.verifyProofs(entry, proofs)

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	entry.preferredClient = clientName
	cache.addProofsLocked(entry, proofs)
}

// verifyProofs verifies proofs with the embedded zkVM verifiers against the
// public input the block's payload expects and records the outcome on the
// proofs. The check is done once here; the result is persisted with the proof.
// A proof of a block whose payload envelope is not known yet stays unverified
// and is retried on the next slot tick.
func (cache *executionProofCache) verifyProofs(entry *proofFetchEntry, proofs []*btypes.SlotExecutionProof) {
	proofVerifiers := cache.getVerifiers()
	if proofVerifiers == nil || len(proofs) == 0 {
		return
	}

	expected, ok := cache.expectedPublicInput(entry)

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for _, proof := range proofs {
		proofType := executionproof.Lookup(proof.ProofType)
		if proofType == nil {
			proof.Verification = uint8(verifiers.StatusUnverifiable)
			delete(entry.unverified, proof.ProofType)
			continue
		}
		if !ok {
			entry.unverified[proof.ProofType] = true
			continue
		}

		result := proofVerifiers.Verify(cache.indexer.ctx, proofType.ZkVM, proofType.ProgramVK, proof.ProofData)
		if result.Status == verifiers.StatusValid {
			// a valid proof of another payload is not a proof of this block
			if committed, err := executionproof.PublicInputFromPublicValues(proofType.ZkVM, result.PublicValues); err != nil || *committed != *expected {
				result.Status = verifiers.StatusInvalid
			}
		}
		proof.Verification = uint8(result.Status)
		proof.Verifier = result.Generation
		delete(entry.unverified, proof.ProofType)
		entry.dirty[proof.ProofType] = true
	}
}

// expectedPublicInput derives the public input a valid proof of the entry's
// block commits to. ok is false while the block's payload envelope is unknown.
func (cache *executionProofCache) expectedPublicInput(entry *proofFetchEntry) (*executionproof.PublicInput, bool) {
	block := cache.indexer.blockCache.getBlockByRoot(entry.root)
	if block == nil {
		return nil, false
	}
	body := block.GetBlock(cache.indexer.ctx)
	envelope := block.GetExecutionPayload(cache.indexer.ctx)
	if body == nil || envelope == nil {
		return nil, false
	}

	request, err := executionproof.BlockNewPayloadRequest(body, envelope)
	if err != nil {
		cache.indexer.logger.Debugf("execution proofs of block 0x%x not verifiable: %v", entry.root[:], err)
		return nil, false
	}
	root, err := request.Root(cache.indexer.dynSsz)
	if err != nil {
		cache.indexer.logger.Warnf("new payload request root of block 0x%x: %v", entry.root[:], err)
		return nil, false
	}

	return &executionproof.PublicInput{
		NewPayloadRequestRoot: root,
		SuccessfulValidation:  true,
		ChainID:               cache.indexer.consensusPool.GetChainState().GetSpecs().DepositChainId,
		SchemaID:              executionproof.SchemaID,
	}, true
}

// checkSignatures verifies the prover signatures of the proofs against the
// validator set of the proofs' block and records the outcome on the proofs. The
// check is done once here, where that validator set is at hand; the result is
// persisted with the proof. A proof whose prover cannot be resolved is invalid,
// as an envelope of an unknown validator is.
func (cache *executionProofCache) checkSignatures(entry *proofFetchEntry, proofs []*btypes.SlotExecutionProof) {
	chainState := cache.indexer.consensusPool.GetChainState()
	specs := chainState.GetSpecs()
	if specs == nil || cache.indexer.validatorCache == nil {
		return
	}
	epoch := chainState.EpochOfSlot(entry.slot)
	forkVersion := chainState.GetForkVersionAtEpoch(epoch)
	var genesisValidatorsRoot phase0.Root
	if genesis := chainState.GetGenesis(); genesis != nil {
		genesisValidatorsRoot = genesis.GenesisValidatorsRoot
	}

	for _, proof := range proofs {
		validator := cache.indexer.validatorCache.getValidatorByIndexAndRoot(phase0.ValidatorIndex(proof.ValidatorIndex), entry.root)
		if validator == nil {
			cache.indexer.logger.Warnf("execution proof for block 0x%x signed by unknown validator %d", entry.root[:], proof.ValidatorIndex)
			continue
		}

		envelope := &executionproof.Envelope{
			ProofData:       proof.ProofData,
			ProofType:       proof.ProofType,
			BeaconBlockRoot: entry.root,
		}
		valid, err := envelope.VerifySignature(cache.indexer.dynSsz, specs.DomainExecutionProof, validator.PublicKey, proof.Signature, forkVersion, genesisValidatorsRoot)
		if err != nil {
			cache.indexer.logger.Warnf("error verifying execution proof signature for block 0x%x: %v", entry.root[:], err)
			continue
		}
		proof.SignatureValid = valid
		proof.ProverActive = validator.ActivationEpoch <= epoch && epoch < validator.ExitEpoch
	}
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

// loadProofsFromClient requests the proofs of the entry's block from one client. A
// client that answers without proof engine or route is not asked again before its
// recheck epoch.
func (cache *executionProofCache) loadProofsFromClient(client *Client, entry *proofFetchEntry, currentEpoch phase0.Epoch) ([]*btypes.SlotExecutionProof, error) {
	name := client.client.GetName()

	cache.cacheMutex.Lock()
	state := cache.clients[name]
	if state == nil {
		state = &proofClientState{}
		cache.clients[name] = state
	}
	unsupported := state.unsupported && currentEpoch < state.recheckEpoch
	cache.cacheMutex.Unlock()

	if unsupported {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(cache.indexer.ctx, proofFetchTimeout)
	defer cancel()

	proofs, err := client.client.GetRPCClient().GetExecutionProofsByBlockroot(ctx, entry.root[:])
	switch {
	case err == nil:
		cache.setClientUnsupported(state, false, 0)
		return cache.convertProofs(entry, proofs), nil
	case errors.Is(err, rpc.ErrExecutionProofsUnsupported), errors.Is(err, rpc.ErrExecutionProofsNotFound):
		// no proof engine, or no route (an unknown block on a node that serves the
		// route is the rare case: the block was seen on this client)
		cache.setClientUnsupported(state, true, currentEpoch+proofApiRecheckEpochs)
		return nil, nil
	default:
		return nil, err
	}
}

// setClientUnsupported remembers whether a client serves the proofs route.
func (cache *executionProofCache) setClientUnsupported(state *proofClientState, unsupported bool, recheckEpoch phase0.Epoch) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	state.unsupported = unsupported
	state.recheckEpoch = recheckEpoch
}

// convertProofs turns the proof envelopes served by a client into meta object proofs
// of the entry's block; envelopes of other blocks are dropped.
func (cache *executionProofCache) convertProofs(entry *proofFetchEntry, proofs []*rpc.ExecutionProof) []*btypes.SlotExecutionProof {
	converted := make([]*btypes.SlotExecutionProof, 0, len(proofs))
	for _, proof := range proofs {
		if proof.BeaconBlockRoot != entry.root {
			continue
		}
		converted = append(converted, &btypes.SlotExecutionProof{
			BlockRoot:      entry.root,
			ProofType:      proof.ProofType,
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
// index, signature, verification outcome and verifier, then the proof bytes.
func encodeExecutionProofItem(proof *btypes.SlotExecutionProof) []byte {
	verifier := proof.Verifier
	if len(verifier) > 0xff {
		verifier = verifier[:0xff]
	}
	data := make([]byte, proofItemHeaderSize+len(verifier)+len(proof.ProofData))
	if proof.SignatureValid {
		data[0] |= proofItemFlagSignatureValid
	}
	if proof.ProverActive {
		data[0] |= proofItemFlagProverActive
	}
	data[1] = proof.ProofType
	binary.BigEndian.PutUint64(data[2:10], proof.ValidatorIndex)
	copy(data[10:106], proof.Signature[:])
	data[106] = proof.Verification
	data[107] = uint8(len(verifier))
	copy(data[proofItemHeaderSize:], verifier)
	copy(data[proofItemHeaderSize+len(verifier):], proof.ProofData)
	return data
}

// decodeExecutionProofItem decodes a crash copy written by encodeExecutionProofItem.
func decodeExecutionProofItem(root phase0.Root, data []byte, withData bool) (*btypes.SlotExecutionProof, error) {
	if len(data) < proofItemHeaderSize {
		return nil, fmt.Errorf("execution proof item too short")
	}

	verifierLen := int(data[107])
	if len(data) < proofItemHeaderSize+verifierLen {
		return nil, fmt.Errorf("execution proof item too short")
	}
	proof := &btypes.SlotExecutionProof{
		BlockRoot:      root,
		ProofType:      data[1],
		SignatureValid: data[0]&proofItemFlagSignatureValid != 0,
		ProverActive:   data[0]&proofItemFlagProverActive != 0,
		ValidatorIndex: binary.BigEndian.Uint64(data[2:10]),
		Verification:   data[106],
		Verifier:       string(data[proofItemHeaderSize : proofItemHeaderSize+verifierLen]),
		DataSize:       uint32(len(data) - proofItemHeaderSize - verifierLen),
	}
	copy(proof.Signature[:], data[10:106])
	if withData {
		proof.ProofData = data[proofItemHeaderSize+verifierLen:]
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
