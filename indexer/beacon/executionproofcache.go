package beacon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// proofCheckConcurrency bounds the parallel signature checks and verifications.
	proofCheckConcurrency = 4
	// proofHoldSlots is how long a proof of a block the indexer has not seen yet is
	// held for the block.
	proofHoldSlots = 32
	// verifierSetupRetryInterval is how long a failed verifier setup is not
	// retried; the proofs stay unverified meanwhile.
	verifierSetupRetryInterval = time.Minute
	// proofItemHeaderSize is the size of the fixed crash copy item header: flags,
	// proof type, validator index, signature, verification outcome and the
	// verifier name length; the verifier name and the proof bytes follow.
	proofItemHeaderSize = 1 + 1 + 8 + 96 + 1 + 1
	// proofItemFlagSignatureValid and proofItemFlagProverActive hold the signature
	// check outcome as in the meta object.
	proofItemFlagSignatureValid = 1
	proofItemFlagProverActive   = 2
)

// proofKey identifies a proof of a block: a prover publishes one proof per type,
// different provers may each prove the block with the same type.
type proofKey struct {
	proofType uint8
	prover    uint64
}

func keyOf(proof *btypes.SlotExecutionProof) proofKey {
	return proofKey{proofType: proof.ProofType, prover: proof.ValidatorIndex}
}

// proofEntry is a block whose proofs are collected.
type proofEntry struct {
	slot phase0.Slot
	root phase0.Root
	// checking is set while received proofs are checked in the background.
	checking bool
	// verifying is set while the proofs that waited for the block's payload are
	// verified in the background.
	verifying bool
	// proofs holds the distinct proofs in the order they arrived.
	proofs []*btypes.SlotExecutionProof
	// received holds the proofs delivered by the nodes that are not checked yet.
	received []*btypes.SlotExecutionProof
	// dirty holds the proofs not written as crash copy yet.
	dirty map[proofKey]bool
	// unverified holds the proofs whose verification waits for the block's
	// payload envelope or the verifiers.
	unverified map[proofKey]bool
}

// busy tells whether a check or verification of the entry is in flight.
func (entry *proofEntry) busy() bool {
	return entry.checking || entry.verifying
}

// findProof returns the held proof with the key, nil if there is none.
func (entry *proofEntry) findProof(key proofKey) *btypes.SlotExecutionProof {
	for _, proof := range entry.proofs {
		if keyOf(proof) == key {
			return proof
		}
	}
	return nil
}

// hasProof tells whether a proof with the key is held or queued for checking.
func (entry *proofEntry) hasProof(key proofKey) bool {
	if entry.findProof(key) != nil {
		return true
	}
	for _, proof := range entry.received {
		if keyOf(proof) == key {
			return true
		}
	}
	return false
}

// heldProof is a proof of a block the indexer has not seen yet.
type heldProof struct {
	proof *rpc.ExecutionProof
	// seen is the slot the proof arrived at; it is dropped proofHoldSlots later.
	seen phase0.Slot
}

// executionProofCache collects the EIP-8025 execution proofs of recent blocks from
// the beacon nodes' execution_proof event streams for the bid cache's flush window.
//
// A delivered proof is checked in the background (prover signature, zk
// verification) and kept with its block until the slot leaves the cache with the
// flush. The proof count is then written into the block's metadata and the proofs
// are handed to the slot's meta object where a blockdb exists. Every proof is
// written to the unfinalized_metadata table as crash copy on the slot tick. A
// proof that arrives before its block is held for a few slots.
//
// The event streams deliver live gossip only and no node keeps proofs, so the
// proofs gossiped while the indexer is down or a stream is disconnected are
// lost: the affected blocks flush with the proofs that arrived after the gap,
// as bids and inclusion lists do. Provers publish on their own schedule, so the
// blocks of the collection window are tracked again after a restart.
type executionProofCache struct {
	indexer *Indexer

	disabled bool
	checkSem chan struct{}

	// verifiers runs the embedded zkVM verifiers, created on the first proof;
	// nil while verification is disabled or the setup failed (retried after
	// verifierSetupRetryInterval).
	verifiers            *verifiers.Verifiers
	verifiersMutex       sync.Mutex
	verifiersRetryAt     time.Time
	verificationDisabled bool
	verifierCacheDir     string

	cacheMutex sync.RWMutex
	entries    map[phase0.Root]*proofEntry
	// cutoff is the slot before which the entries were dropped by the flush; a
	// proof of an older block is not taken anymore.
	cutoff phase0.Slot
	// held holds the proofs of blocks the indexer has not seen yet, by block root.
	held map[phase0.Root][]heldProof
}

func newExecutionProofCache(indexer *Indexer) *executionProofCache {
	cache := &executionProofCache{
		indexer:  indexer,
		checkSem: make(chan struct{}, proofCheckConcurrency),
		entries:  make(map[phase0.Root]*proofEntry, 64),
		held:     make(map[phase0.Root][]heldProof, 8),
	}

	if utils.Config != nil {
		cfg := utils.Config.Indexer.ExecutionProofs
		cache.disabled = cfg.Disabled
		cache.verificationDisabled = cfg.VerificationDisabled
		cache.verifierCacheDir = cfg.VerifierCacheDir
		executionproof.MockProofs = cfg.MockProofs
	}

	return cache
}

// getVerifiers returns the zkVM verifiers, created on first use; nil when
// verification is disabled or the verifiers cannot be set up right now.
func (cache *executionProofCache) getVerifiers() *verifiers.Verifiers {
	if cache.verificationDisabled {
		return nil
	}

	cache.verifiersMutex.Lock()
	defer cache.verifiersMutex.Unlock()

	if cache.verifiers != nil || time.Now().Before(cache.verifiersRetryAt) {
		return cache.verifiers
	}

	cacheDir := cache.verifierCacheDir
	if cacheDir == "" {
		// a well-known dir: shared by the instances on a host and picked up
		// again after a restart
		cacheDir = filepath.Join(os.TempDir(), "dora-verifiers")
	}
	proofVerifiers, err := verifiers.New(cache.indexer.ctx, cacheDir)
	if err != nil {
		cache.verifiersRetryAt = time.Now().Add(verifierSetupRetryInterval)
		cache.indexer.logger.WithError(err).Warnf("execution proof verifiers not available, retrying in %s", verifierSetupRetryInterval)
		return nil
	}
	cache.verifiers = proofVerifiers

	return cache.verifiers
}

// close releases the verifiers.
func (cache *executionProofCache) close() {
	cache.verifiersMutex.Lock()
	defer cache.verifiersMutex.Unlock()

	if cache.verifiers != nil {
		if err := cache.verifiers.Close(cache.indexer.ctx); err != nil {
			cache.indexer.logger.WithError(err).Warnf("error closing execution proof verifiers")
		}
	}
}

// trackBlock registers a newly seen block for proof collection.
func (cache *executionProofCache) trackBlock(slot phase0.Slot, root phase0.Root) {
	if cache.disabled {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.trackBlockLocked(slot, root)
}

// trackBlocks registers the given blocks for proof collection: the blocks of the
// collection window after a restart, whose proofs may still be published.
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

// trackBlockLocked returns the entry of a block, creating it if needed; proofs
// held for the block are queued for checking. Caller must hold the write lock.
func (cache *executionProofCache) trackBlockLocked(slot phase0.Slot, root phase0.Root) *proofEntry {
	entry := cache.entries[root]
	if entry == nil {
		entry = &proofEntry{
			slot:       slot,
			root:       root,
			dirty:      make(map[proofKey]bool, 2),
			unverified: make(map[proofKey]bool, 2),
		}
		cache.entries[root] = entry

		for _, held := range cache.held[root] {
			cache.receiveLocked(entry, held.proof)
		}
		delete(cache.held, root)
	}
	return entry
}

// addProof takes a proof delivered by a node's event stream. A proof of a block
// the indexer does not know yet is held until the block is tracked; a proof of a
// block that left the metadata window is dropped.
func (cache *executionProofCache) addProof(proof *rpc.ExecutionProof) {
	if cache.disabled {
		return
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	entry := cache.entries[proof.BeaconBlockRoot]
	if entry == nil {
		block := cache.indexer.blockCache.getBlockByRoot(proof.BeaconBlockRoot)
		if block == nil {
			cache.holdLocked(proof)
			return
		}
		if block.Slot < cache.cutoff || uint64(block.Slot) < cache.indexer.metadataCutoff.Load() {
			return
		}
		entry = cache.trackBlockLocked(block.Slot, proof.BeaconBlockRoot)
	}

	cache.receiveLocked(entry, proof)
}

// holdLocked keeps a proof of an unknown block, one per type and prover. Caller
// must hold the write lock.
func (cache *executionProofCache) holdLocked(proof *rpc.ExecutionProof) {
	held := cache.held[proof.BeaconBlockRoot]
	for _, other := range held {
		if other.proof.ProofType == proof.ProofType && other.proof.ValidatorIndex == proof.ValidatorIndex {
			return
		}
	}
	cache.held[proof.BeaconBlockRoot] = append(held, heldProof{
		proof: proof,
		seen:  cache.indexer.consensusPool.GetChainState().CurrentSlot(),
	})
}

// receiveLocked queues a proof of the entry's block for checking unless the
// prover's proof of that type is known. Caller must hold the write lock.
func (cache *executionProofCache) receiveLocked(entry *proofEntry, proof *rpc.ExecutionProof) {
	converted := convertProof(proof)
	if entry.hasProof(keyOf(converted)) {
		return
	}

	entry.received = append(entry.received, converted)
	if !entry.checking {
		entry.checking = true
		go cache.checkReceived(entry)
	}
}

// checkReceived checks the received proofs of an entry in the background and
// adds them: the prover signature against the block's validator set and the zk
// proof with the embedded verifiers.
func (cache *executionProofCache) checkReceived(entry *proofEntry) {
	defer utils.HandleSubroutinePanic("indexer.beacon.executionProofCache.checkReceived", nil)

	select {
	case cache.checkSem <- struct{}{}:
	case <-cache.indexer.ctx.Done():
		cache.cacheMutex.Lock()
		entry.checking = false
		cache.cacheMutex.Unlock()
		return
	}
	defer func() { <-cache.checkSem }()

	for {
		cache.cacheMutex.Lock()
		batch := entry.received
		entry.received = nil
		if len(batch) == 0 {
			entry.checking = false
			cache.cacheMutex.Unlock()
			return
		}
		cache.cacheMutex.Unlock()

		cache.checkSignatures(entry, batch)
		cache.verifyProofs(entry, batch)

		cache.cacheMutex.Lock()
		cache.addProofsLocked(entry, batch)
		cache.cacheMutex.Unlock()
	}
}

// slotBounds returns the lowest and highest slot of the tracked blocks (0, 0 if none).
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

// processSlot starts the verifications that waited for a block's payload or the
// verifiers, in the background bounded by the check semaphore, and drops the
// held proofs of blocks that never showed up.
func (cache *executionProofCache) processSlot(currentSlot phase0.Slot) {
	if cache.disabled {
		return
	}

	cache.cacheMutex.Lock()
	pending := make([]*proofEntry, 0, 4)
	for _, entry := range cache.entries {
		if len(entry.unverified) > 0 && !entry.verifying {
			entry.verifying = true
			pending = append(pending, entry)
		}
	}
	for root, held := range cache.held {
		kept := held[:0]
		for _, proof := range held {
			if proof.seen+proofHoldSlots >= currentSlot {
				kept = append(kept, proof)
			}
		}
		if len(kept) == 0 {
			delete(cache.held, root)
		} else {
			cache.held[root] = kept
		}
	}
	cache.cacheMutex.Unlock()

	for _, entry := range pending {
		go cache.verifyPending(entry)
	}
}

// verifyPending verifies the proofs of an entry whose verification waited for
// the block's payload envelope or the verifiers.
func (cache *executionProofCache) verifyPending(entry *proofEntry) {
	defer utils.HandleSubroutinePanic("indexer.beacon.executionProofCache.verifyPending", nil)

	defer func() {
		cache.cacheMutex.Lock()
		entry.verifying = false
		cache.cacheMutex.Unlock()
	}()

	select {
	case cache.checkSem <- struct{}{}:
	case <-cache.indexer.ctx.Done():
		return
	}
	defer func() { <-cache.checkSem }()

	cache.cacheMutex.RLock()
	proofs := make([]*btypes.SlotExecutionProof, 0, len(entry.unverified))
	for key := range entry.unverified {
		if proof := entry.findProof(key); proof != nil {
			proofs = append(proofs, proof)
		}
	}
	cache.cacheMutex.RUnlock()

	cache.verifyProofs(entry, proofs)
}

// proofVerdict is the outcome of verifying one proof, applied to the entry
// under the lock once the batch is verified.
type proofVerdict struct {
	proof      *btypes.SlotExecutionProof
	status     verifiers.Status
	generation string
	// pending is set when the verification could not run yet (the block's
	// payload envelope is unknown, the verifiers are not available); it is
	// retried on a later slot tick.
	pending bool
}

// verifyProofs verifies proofs with the embedded zkVM verifiers against the
// public input the block's payload expects and records the outcome on the
// proofs. The check is done once here; the result is persisted with the proof.
// The verifiers run without the cache lock. A proof whose verification cannot
// run yet (the block's payload envelope is not known, the verifiers are not
// available) stays unverified and is retried on a later slot tick.
func (cache *executionProofCache) verifyProofs(entry *proofEntry, proofs []*btypes.SlotExecutionProof) {
	if cache.verificationDisabled || len(proofs) == 0 {
		return
	}

	proofVerifiers := cache.getVerifiers()
	var expected *executionproof.PublicInput
	var expectedErr error
	if proofVerifiers != nil {
		expected, expectedErr = cache.expectedPublicInput(entry)
		if expectedErr != nil && !errors.Is(expectedErr, errPayloadPending) {
			cache.indexer.logger.Debugf("execution proofs of block 0x%x not verifiable: %v", entry.root[:], expectedErr)
		}
	}

	verdicts := make([]proofVerdict, 0, len(proofs))
	for _, proof := range proofs {
		verdict := proofVerdict{proof: proof}
		proofType := executionproof.Lookup(proof.ProofType)
		switch {
		case proofType == nil:
			verdict.status = verifiers.StatusUnverifiable
		case proofVerifiers == nil || errors.Is(expectedErr, errPayloadPending):
			verdict.pending = true
		case expectedErr != nil:
			verdict.status = verifiers.StatusUnverifiable
		default:
			result, err := proofVerifiers.Verify(cache.indexer.ctx, proofType.ZkVM, proofType.ProgramVK, proof.ProofData)
			if err != nil {
				// the verifier could not run; not a verdict on the proof
				cache.indexer.logger.WithError(err).Warnf("error verifying execution proof type %d of block 0x%x", proof.ProofType, entry.root[:])
				verdict.pending = true
				break
			}
			if result.Status == verifiers.StatusValid {
				// a valid proof of another payload is not a proof of this block
				if committed, err := executionproof.PublicInputFromPublicValues(proofType.ZkVM, result.PublicValues); err != nil || *committed != *expected {
					result.Status = verifiers.StatusInvalid
				}
			}
			verdict.status = result.Status
			verdict.generation = result.Generation
		}
		verdicts = append(verdicts, verdict)
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for _, verdict := range verdicts {
		key := keyOf(verdict.proof)
		if verdict.pending {
			entry.unverified[key] = true
			continue
		}
		verdict.proof.Verification = uint8(verdict.status)
		verdict.proof.Verifier = verdict.generation
		delete(entry.unverified, key)
		entry.dirty[key] = true
	}
}

// errPayloadPending is returned by expectedPublicInput while the block's
// payload envelope is not known.
var errPayloadPending = errors.New("payload envelope not known yet")

// expectedPublicInput derives the public input a valid proof of the entry's
// block commits to. errPayloadPending is returned while the block's payload
// envelope is unknown; any other error means the block's proofs cannot be
// verified.
func (cache *executionProofCache) expectedPublicInput(entry *proofEntry) (*executionproof.PublicInput, error) {
	block := cache.indexer.blockCache.getBlockByRoot(entry.root)
	if block == nil {
		return nil, errPayloadPending
	}
	body := block.GetBlock(cache.indexer.ctx)
	envelope := block.GetExecutionPayload(cache.indexer.ctx)
	if body == nil || envelope == nil {
		return nil, errPayloadPending
	}

	schemaID, ok := executionproof.SchemaIDForFork(body.Version)
	if !ok {
		return nil, fmt.Errorf("no stateless input schema assigned to the %s fork", body.Version)
	}
	request, err := executionproof.BlockNewPayloadRequest(body, envelope)
	if err != nil {
		return nil, err
	}
	root, err := request.Root(cache.indexer.dynSsz)
	if err != nil {
		return nil, fmt.Errorf("new payload request root: %w", err)
	}

	return &executionproof.PublicInput{
		NewPayloadRequestRoot: root,
		SuccessfulValidation:  true,
		ChainID:               cache.indexer.consensusPool.GetChainState().GetSpecs().DepositChainId,
		SchemaID:              schemaID,
	}, nil
}

// checkSignatures verifies the prover signatures of the proofs against the
// validator set of the proofs' block and records the outcome on the proofs. The
// check is done once here, where that validator set is at hand; the result is
// persisted with the proof. A proof whose prover cannot be resolved is invalid,
// as an envelope of an unknown validator is.
func (cache *executionProofCache) checkSignatures(entry *proofEntry, proofs []*btypes.SlotExecutionProof) {
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
// type and prover. Caller must hold the write lock.
func (cache *executionProofCache) addProofsLocked(entry *proofEntry, proofs []*btypes.SlotExecutionProof) {
	for _, proof := range proofs {
		key := keyOf(proof)
		if entry.findProof(key) != nil {
			continue
		}
		entry.proofs = append(entry.proofs, proof)
		entry.dirty[key] = true
	}
}

// convertProof turns a proof envelope delivered by a node into a meta object proof.
func convertProof(proof *rpc.ExecutionProof) *btypes.SlotExecutionProof {
	return &btypes.SlotExecutionProof{
		BlockRoot:      proof.BeaconBlockRoot,
		ProofType:      proof.ProofType,
		ValidatorIndex: proof.ValidatorIndex,
		Signature:      proof.Signature,
		DataSize:       uint32(len(proof.ProofData)),
		ProofData:      proof.ProofData,
	}
}

// executionProofItemKeySize is the size of a crash copy key: block root, proof
// type and prover.
const executionProofItemKeySize = 32 + 1 + 8

// executionProofItemKey returns the crash copy key of a proof.
func executionProofItemKey(root phase0.Root, key proofKey) []byte {
	item := make([]byte, executionProofItemKeySize)
	copy(item[0:32], root[:])
	item[32] = key.proofType
	binary.BigEndian.PutUint64(item[33:41], key.prover)
	return item
}

// parseExecutionProofItemKey decodes a crash copy key.
func parseExecutionProofItemKey(item []byte) (phase0.Root, proofKey, bool) {
	if len(item) != executionProofItemKeySize {
		return phase0.Root{}, proofKey{}, false
	}
	return phase0.Root(item[0:32]), proofKey{proofType: item[32], prover: binary.BigEndian.Uint64(item[33:41])}, true
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
		for key := range entry.dirty {
			delete(entry.dirty, key)
			proof := entry.findProof(key)
			if proof == nil {
				continue
			}
			entries = append(entries, &dbtypes.UnfinalizedMetadata{
				Slot: uint64(entry.slot),
				Kind: dbtypes.UnfinalizedMetadataExecutionProof,
				Key:  executionProofItemKey(entry.root, key),
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
		if entry.Kind != dbtypes.UnfinalizedMetadataExecutionProof {
			continue
		}
		root, key, ok := parseExecutionProofItemKey(entry.Key)
		if !ok {
			continue
		}
		if cached := cache.entries[root]; cached != nil && cached.findProof(key) != nil {
			cached.dirty[key] = true
		}
	}
}

// restoreEntry restores a crash copy into the entry of its block.
func (cache *executionProofCache) restoreEntry(entry *dbtypes.UnfinalizedMetadata) error {
	if cache.disabled {
		return nil
	}
	root, key, ok := parseExecutionProofItemKey(entry.Key)
	if entry.Kind != dbtypes.UnfinalizedMetadataExecutionProof || !ok {
		return fmt.Errorf("invalid execution proof item key")
	}

	proof, err := decodeExecutionProofItem(root, entry.Data, true)
	if err != nil {
		return err
	}
	if keyOf(proof) != key {
		return fmt.Errorf("execution proof item key does not match its proof")
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cached := cache.trackBlockLocked(phase0.Slot(entry.Slot), root)
	if cached.findProof(key) == nil {
		cached.proofs = append(cached.proofs, proof)
		// a proof whose verification had not run before the restart is verified
		// on the next slot tick
		if proof.Verification == uint8(verifiers.StatusUnknown) && !cache.verificationDisabled {
			cached.unverified[key] = true
		}
	}

	return nil
}

// dropBefore removes the blocks of slots before cutoffSlot from the cache. The
// proof count of each removed block is written into the block's metadata. The
// proofs of the dropped slots are returned as meta objects by slot if
// withObjects is set (a blockdb takes them), together with the blocks whose
// metadata changed and the slots no entry remains for. A block with a check or
// verification in flight is left for the next flush unless includeBusy is set
// (shutdown), so its result is not lost.
func (cache *executionProofCache) dropBefore(cutoffSlot phase0.Slot, includeBusy bool, withObjects bool) (map[uint64]*btypes.SlotMeta, []*Block, []uint64) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.cutoff = max(cache.cutoff, cutoffSlot)
	objects := make(map[uint64]*btypes.SlotMeta, 8)
	touched := make([]*Block, 0, 8)
	droppedSlots := make(map[uint64]bool, 8)
	for root, entry := range cache.entries {
		if entry.slot >= cutoffSlot {
			continue
		}
		if entry.busy() && !includeBusy {
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

// sortedProofs returns copies of the proofs in proof type order, the proofs of a
// type in the order they arrived; the copies are read without the lock while
// verdicts are still recorded on the entry's proofs (the proof bytes are shared,
// they never change).
func sortedProofs(proofs []*btypes.SlotExecutionProof) []*btypes.SlotExecutionProof {
	sorted := make([]*btypes.SlotExecutionProof, 0, len(proofs))
	for _, proof := range proofs {
		copied := *proof
		sorted = append(sorted, &copied)
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ProofType < sorted[j].ProofType })
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
// order (the proofs of a type in prover order, their arrival order is not
// stored). The proof bytes are only included if withData is set.
func (cache *executionProofCache) loadStoredProofs(ctx context.Context, slot phase0.Slot, root phase0.Root, withData bool) []*btypes.SlotExecutionProof {
	proofs := make([]*btypes.SlotExecutionProof, 0, 2)
	err := db.StreamUnfinalizedSlotMetadata(ctx, uint64(slot), dbtypes.UnfinalizedMetadataExecutionProof, func(entry *dbtypes.UnfinalizedMetadata) {
		itemRoot, _, ok := parseExecutionProofItemKey(entry.Key)
		if !ok || itemRoot != root {
			return
		}
		proof, err := decodeExecutionProofItem(root, entry.Data, withData)
		if err != nil {
			cache.indexer.logger.Warnf("error decoding stored execution proof of slot %d: %v", slot, err)
			return
		}
		proofs = append(proofs, proof)
	})
	if err != nil || len(proofs) == 0 {
		return nil
	}

	return sortedProofs(proofs)
}
