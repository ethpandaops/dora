package beacon

import (
	"bytes"
	"fmt"
	"hash/crc64"
	"math"
	"runtime/debug"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
)

// BuilderIndexFlag separates builder indices from validator indices in the pubkey cache
const BuilderIndexFlag = uint64(1 << 40)

// Builder status flag constants representing different builder states
const (
	BuilderStatusExited     uint16 = 1 << iota // Builder has exited (withdrawable_epoch reached)
	BuilderStatusSuperseded                    // Builder index was reused, this pubkey is no longer active
)

// builderCache manages the in-memory cache of builder states and handles updates
type builderCache struct {
	indexer         *Indexer
	builderSetCache []*builderEntry
	cacheMutex      sync.RWMutex
	triggerDbUpdate chan bool
}

// builderEntry represents a single builder's state in the cache
type builderEntry struct {
	builderDiffs  []*builderDiff
	finalChecksum uint64
	finalBuilder  *gloas.Builder
	activeData    *BuilderData
	statusFlags   uint16
}

// BuilderData contains the essential builder state information for active builders.
// Only WithdrawableEpoch can change during a builder's lifetime; all other fields are static.
type BuilderData struct {
	WithdrawableEpoch phase0.Epoch
}

// builderDiff represents an updated builder entry in the builder set cache.
type builderDiff struct {
	epoch         phase0.Epoch
	dependentRoot phase0.Root
	builder       *gloas.Builder
}

// newBuilderCache initializes a new builder cache instance and starts the persist loop
func newBuilderCache(indexer *Indexer) *builderCache {
	cache := &builderCache{
		indexer:         indexer,
		triggerDbUpdate: make(chan bool, 1),
	}

	go cache.runPersistLoop()

	return cache
}

// updateBuilderSet processes builder set updates and maintains the cache state
func (cache *builderCache) updateBuilderSet(slot phase0.Slot, dependentRoot phase0.Root, builders []*gloas.Builder) {
	chainState := cache.indexer.consensusPool.GetChainState()
	epoch := chainState.EpochOfSlot(slot)
	currentEpoch := chainState.CurrentEpoch()
	finalizedEpoch, finalizedRoot := chainState.GetFinalizedCheckpoint()
	cutOffEpoch := phase0.Epoch(0)
	if currentEpoch > phase0.Epoch(cache.indexer.inMemoryEpochs) {
		cutOffEpoch = currentEpoch - phase0.Epoch(cache.indexer.inMemoryEpochs)
	}
	if cutOffEpoch > finalizedEpoch {
		cutOffEpoch = finalizedEpoch
	}

	if epoch < cutOffEpoch {
		cache.indexer.logger.Infof("ignoring old builder set update for epoch %d", epoch)
		return
	}

	isFinalizedBuilderSet := false
	if slot == 0 {
		isFinalizedBuilderSet = true // genesis
	} else if epoch <= finalizedEpoch {
		finalizedBlock := cache.indexer.blockCache.getBlockByRoot(finalizedRoot)
		if finalizedBlock != nil {
			finalizedDependentBlock := cache.indexer.blockCache.getDependentBlock(chainState, finalizedBlock, nil)
			if finalizedDependentBlock != nil && bytes.Equal(finalizedDependentBlock.Root[:], dependentRoot[:]) {
				isFinalizedBuilderSet = true
			}
		}
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	t1 := time.Now()

	if len(cache.builderSetCache) < len(builders) {
		if len(builders) > cap(cache.builderSetCache) {
			newCache := make([]*builderEntry, len(builders), len(builders)+1000)
			copy(newCache, cache.builderSetCache)
			cache.builderSetCache = newCache
		} else {
			cache.builderSetCache = cache.builderSetCache[:len(builders)]
		}
	}

	isParentMap := map[phase0.Root]bool{}
	isAheadMap := map[phase0.Root]bool{}
	updatedCount := uint64(0)

	for i := range builders {
		var parentChecksum uint64
		var parentBuilder *gloas.Builder
		parentEpoch := phase0.Epoch(0)

		aheadDiffIdx := 0
		foundAhead := false
		aheadEpoch := phase0.Epoch(math.MaxInt64)

		cachedBuilder := cache.builderSetCache[i]
		if cachedBuilder == nil {
			cachedBuilder = &builderEntry{}
			cache.builderSetCache[i] = cachedBuilder

			cache.indexer.pubkeyCache.Add(builders[i].PublicKey, phase0.ValidatorIndex(uint64(i)|BuilderIndexFlag))
		} else {
			parentBuilder = cachedBuilder.finalBuilder
			parentChecksum = cachedBuilder.finalChecksum
		}

		deleteKeys := []int{}

		if !isFinalizedBuilderSet {
			for diffkey, diff := range cachedBuilder.builderDiffs {
				if diff.epoch < cutOffEpoch {
					deleteKeys = append(deleteKeys, diffkey)
					continue
				}

				if diff.epoch < epoch {
					isParent, checkedParent := isParentMap[diff.dependentRoot]
					if !checkedParent {
						isParent = cache.indexer.blockCache.isCanonicalBlock(diff.dependentRoot, dependentRoot)
						isParentMap[diff.dependentRoot] = isParent
					}

					if isParent && diff.epoch > parentEpoch {
						parentBuilder = diff.builder
						parentEpoch = diff.epoch
					}
				}

				if diff.epoch > epoch {
					isAhead, checkedAhead := isAheadMap[diff.dependentRoot]
					if !checkedAhead {
						isAhead = cache.indexer.blockCache.isCanonicalBlock(dependentRoot, diff.dependentRoot)
						isAheadMap[diff.dependentRoot] = isAhead
					}

					if isAhead && diff.epoch < aheadEpoch {
						aheadDiffIdx = diffkey
						aheadEpoch = diff.epoch
						foundAhead = true
					}
				}
			}

			if parentBuilder != nil {
				parentChecksum = calculateBuilderChecksum(parentBuilder)
			}
		}

		checksum := calculateBuilderChecksum(builders[i])
		if checksum == parentChecksum {
			continue
		}

		if isFinalizedBuilderSet {
			cachedBuilder.finalBuilder = builders[i]
			cachedBuilder.finalChecksum = checksum
			cachedBuilder.statusFlags = GetBuilderStatusFlags(builders[i])
			updatedCount++

			activeData := &BuilderData{
				WithdrawableEpoch: builders[i].WithdrawableEpoch,
			}
			if cache.isActiveBuilder(activeData) {
				cachedBuilder.activeData = activeData
			}
		}

		if foundAhead && cache.checkBuilderEqual(cachedBuilder.builderDiffs[aheadDiffIdx].builder, builders[i]) {
			if isFinalizedBuilderSet {
				deleteKeys = append(deleteKeys, aheadDiffIdx)
			} else {
				diff := cachedBuilder.builderDiffs[aheadDiffIdx]
				diff.epoch = epoch
				diff.dependentRoot = dependentRoot
				cachedBuilder.builderDiffs[aheadDiffIdx] = diff
			}
		} else if isFinalizedBuilderSet {
		} else if len(deleteKeys) == 0 {
			cachedBuilder.builderDiffs = append(cachedBuilder.builderDiffs, &builderDiff{
				epoch:         epoch,
				dependentRoot: dependentRoot,
				builder:       builders[i],
			})
		} else {
			cachedBuilder.builderDiffs[deleteKeys[0]] = &builderDiff{
				epoch:         epoch,
				dependentRoot: dependentRoot,
				builder:       builders[i],
			}
			deleteKeys = deleteKeys[1:]
		}

		if len(deleteKeys) > 0 {
			lastIdx := len(cachedBuilder.builderDiffs) - 1
			delLen := len(deleteKeys)
			for delIdx := 0; delIdx < delLen; delIdx++ {
				for delLen > 0 && deleteKeys[delLen-1] == lastIdx {
					lastIdx--
					delLen--
				}
				if delLen == 0 {
					break
				}
				cachedBuilder.builderDiffs[deleteKeys[delIdx]] = cachedBuilder.builderDiffs[lastIdx]
				lastIdx--
			}

			cachedBuilder.builderDiffs = cachedBuilder.builderDiffs[:lastIdx+1]
		}
	}

	if updatedCount > 0 {
		select {
		case cache.triggerDbUpdate <- true:
		default:
		}
	}

	isFinalizedStr := ""
	if isFinalizedBuilderSet {
		isFinalizedStr = "finalized "
	}
	cache.indexer.logger.Infof("processed %vbuilder set update for epoch %d in %v", isFinalizedStr, epoch, time.Since(t1))
}

// checkBuilderEqual compares two builder states for equality
func (cache *builderCache) checkBuilderEqual(builder1 *gloas.Builder, builder2 *gloas.Builder) bool {
	if builder1 == nil && builder2 == nil {
		return true
	}
	if builder1 == nil || builder2 == nil {
		return false
	}
	return bytes.Equal(builder1.PublicKey[:], builder2.PublicKey[:]) &&
		builder1.Version == builder2.Version &&
		bytes.Equal(builder1.ExecutionAddress[:], builder2.ExecutionAddress[:]) &&
		builder1.DepositEpoch == builder2.DepositEpoch &&
		builder1.WithdrawableEpoch == builder2.WithdrawableEpoch
}

// GetBuilderStatusFlags calculates the status flags for a builder
func GetBuilderStatusFlags(builder *gloas.Builder) uint16 {
	flags := uint16(0)
	if builder.WithdrawableEpoch != FarFutureEpoch {
		flags |= BuilderStatusExited
	}
	return flags
}

// getBuilderSetSize returns the current number of builders in the builder set
func (cache *builderCache) getBuilderSetSize() uint64 {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	return uint64(len(cache.builderSetCache))
}

// setFinalizedEpoch updates the builder cache when a new epoch is finalized
func (cache *builderCache) setFinalizedEpoch(epoch phase0.Epoch, nextEpochDependentRoot phase0.Root) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	updatedCount := uint64(0)

	for _, cachedBuilder := range cache.builderSetCache {
		if cachedBuilder == nil {
			continue
		}

		// Find the finalized builder state
		for _, diff := range cachedBuilder.builderDiffs {
			if diff.dependentRoot == nextEpochDependentRoot {
				cachedBuilder.finalBuilder = diff.builder
				cachedBuilder.finalChecksum = calculateBuilderChecksum(diff.builder)
				cachedBuilder.statusFlags = GetBuilderStatusFlags(diff.builder)
				updatedCount++

				cachedBuilder.activeData = &BuilderData{
					WithdrawableEpoch: diff.builder.WithdrawableEpoch,
				}
				break
			}
		}

		// Clean up old diffs
		newDiffs := make([]*builderDiff, 0)
		for _, diff := range cachedBuilder.builderDiffs {
			if diff.epoch > epoch {
				newDiffs = append(newDiffs, diff)
			}
		}
		cachedBuilder.builderDiffs = newDiffs

		// Clear old active data
		if cachedBuilder.activeData != nil {
			if !cache.isActiveBuilder(cachedBuilder.activeData) {
				cachedBuilder.activeData = nil
			}
		}
	}

	if updatedCount > 0 {
		select {
		case cache.triggerDbUpdate <- true:
		default:
		}
	}
}

// BuilderSetStreamer is a callback for streaming builder data
type BuilderSetStreamer func(index gloas.BuilderIndex, flags uint16, activeData *BuilderData, builder *gloas.Builder) error

// streamBuilderSetForRoot streams the builder set for a given blockRoot
func (cache *builderCache) streamBuilderSetForRoot(blockRoot phase0.Root, onlyActive bool, epoch *phase0.Epoch, cb BuilderSetStreamer) error {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	isParentMap := map[phase0.Root]bool{}
	isAheadMap := map[phase0.Root]bool{}

	for index, cachedBuilder := range cache.builderSetCache {
		if cachedBuilder == nil {
			continue
		}

		latestBuilder := cachedBuilder.finalBuilder
		builderData := cachedBuilder.activeData
		builderEpoch := phase0.Epoch(0)

		var aheadBuilder *gloas.Builder
		aheadEpoch := phase0.Epoch(math.MaxInt64)

		for _, diff := range cachedBuilder.builderDiffs {
			isParent, checkedParent := isParentMap[diff.dependentRoot]
			if !checkedParent {
				isParent = cache.indexer.blockCache.isCanonicalBlock(diff.dependentRoot, blockRoot)
				isParentMap[diff.dependentRoot] = isParent
			}

			if isParent && diff.epoch >= builderEpoch {
				builderData = &BuilderData{
					WithdrawableEpoch: diff.builder.WithdrawableEpoch,
				}
				builderEpoch = diff.epoch
				latestBuilder = diff.builder
			}

			if !isParent && builderData == nil {
				isAhead, checkedAhead := isAheadMap[diff.dependentRoot]
				if !checkedAhead {
					isAhead = cache.indexer.blockCache.isCanonicalBlock(blockRoot, diff.dependentRoot)
					isAheadMap[diff.dependentRoot] = isAhead
				}

				if isAhead && diff.epoch < aheadEpoch {
					aheadBuilder = diff.builder
					aheadEpoch = diff.epoch
				}
			}
		}

		if builderData == nil && aheadBuilder != nil {
			builderData = &BuilderData{
				WithdrawableEpoch: aheadBuilder.WithdrawableEpoch,
			}
			latestBuilder = aheadBuilder
		}

		if onlyActive && (builderData == nil || (epoch != nil && builderData.WithdrawableEpoch <= *epoch)) {
			continue
		}

		builderFlags := cachedBuilder.statusFlags
		if latestBuilder != nil {
			builderFlags = GetBuilderStatusFlags(latestBuilder)
		}

		err := cb(gloas.BuilderIndex(index), builderFlags, builderData, latestBuilder)
		if err != nil {
			return err
		}
	}

	return nil
}

// UnwrapDbBuilder converts a dbtypes.Builder to a gloas.Builder
func UnwrapDbBuilder(dbBuilder *dbtypes.Builder) *gloas.Builder {
	builder := &gloas.Builder{
		Version:           dbBuilder.Version,
		Balance:           0, // Balance not persisted
		DepositEpoch:      phase0.Epoch(db.ConvertInt64ToUint64(dbBuilder.DepositEpoch)),
		WithdrawableEpoch: phase0.Epoch(db.ConvertInt64ToUint64(dbBuilder.WithdrawableEpoch)),
	}
	copy(builder.PublicKey[:], dbBuilder.Pubkey)
	copy(builder.ExecutionAddress[:], dbBuilder.ExecutionAddress)
	return builder
}

// isActiveBuilder determines if a builder is currently active
func (cache *builderCache) isActiveBuilder(builder *BuilderData) bool {
	currentEpoch := cache.indexer.consensusPool.GetChainState().CurrentEpoch()
	cutOffEpoch := phase0.Epoch(0)
	if currentEpoch > 10 {
		cutOffEpoch = currentEpoch - 10
	}

	return builder.WithdrawableEpoch > cutOffEpoch
}

// getBuilderByIndex returns the builder by index for a given forkId
func (cache *builderCache) getBuilderByIndex(index gloas.BuilderIndex, overrideForkId *ForkKey) *gloas.Builder {
	canonicalHead := cache.indexer.GetCanonicalHead(overrideForkId)
	if canonicalHead == nil {
		return nil
	}

	return cache.getBuilderByIndexAndRoot(index, canonicalHead.Root)
}

// getBuilderByIndexAndRoot returns the builder by index for a given blockRoot
func (cache *builderCache) getBuilderByIndexAndRoot(index gloas.BuilderIndex, blockRoot phase0.Root) *gloas.Builder {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	if uint64(index) >= uint64(len(cache.builderSetCache)) {
		return nil
	}

	cachedBuilder := cache.builderSetCache[index]
	if cachedBuilder == nil {
		return nil
	}

	builder := cachedBuilder.finalBuilder
	builderEpoch := phase0.Epoch(0)

	// Find the latest valid diff
	for _, diff := range cachedBuilder.builderDiffs {
		if cache.indexer.blockCache.isCanonicalBlock(diff.dependentRoot, blockRoot) && diff.epoch >= builderEpoch {
			builder = diff.builder
			builderEpoch = diff.epoch
		}
	}

	// Fallback to db if builder is not found in cache
	if builder == nil {
		if dbBuilder := db.GetActiveBuilderByIndex(uint64(index)); dbBuilder != nil {
			builder = UnwrapDbBuilder(dbBuilder)
		}
	} else {
		// Return a copy
		builder = &gloas.Builder{
			PublicKey:         builder.PublicKey,
			Version:           builder.Version,
			ExecutionAddress:  builder.ExecutionAddress,
			Balance:           builder.Balance,
			DepositEpoch:      builder.DepositEpoch,
			WithdrawableEpoch: builder.WithdrawableEpoch,
		}
	}

	return builder
}

// calculateBuilderChecksum generates a CRC64 checksum of all builder fields (except balance)
func calculateBuilderChecksum(b *gloas.Builder) uint64 {
	if b == nil {
		return 0
	}

	data := make([]byte, 0, 80)
	data = append(data, b.PublicKey[:]...)
	data = append(data, b.Version)
	data = append(data, b.ExecutionAddress[:]...)
	data = append(data, uint64ToBytes(uint64(b.DepositEpoch))...)
	data = append(data, uint64ToBytes(uint64(b.WithdrawableEpoch))...)

	return crc64.Checksum(data, crc64Table)
}

// prepopulateFromDB pre-populates the builder set cache from the database
func (cache *builderCache) prepopulateFromDB() (uint64, error) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	maxIndex, err := db.GetMaxBuilderIndex()
	if err != nil {
		return 0, fmt.Errorf("error getting max builder index: %w", err)
	}

	if maxIndex == 0 {
		return 0, nil
	}

	cache.builderSetCache = make([]*builderEntry, maxIndex+1, maxIndex+1+1000)

	restoreCount := uint64(0)

	batchSize := uint64(10000)
	for start := uint64(0); start <= maxIndex; start += batchSize {
		end := min(start+batchSize, maxIndex)

		builders := db.GetBuilderRange(start, end)
		for _, dbBuilder := range builders {
			if dbBuilder.Superseded {
				continue
			}

			builder := UnwrapDbBuilder(dbBuilder)
			builderEntry := &builderEntry{
				finalChecksum: calculateBuilderChecksum(builder),
			}
			builderData := &BuilderData{
				WithdrawableEpoch: phase0.Epoch(db.ConvertInt64ToUint64(dbBuilder.WithdrawableEpoch)),
			}
			if cache.isActiveBuilder(builderData) {
				builderEntry.activeData = builderData
			}
			builderEntry.statusFlags = GetBuilderStatusFlags(builder)

			cache.builderSetCache[dbBuilder.BuilderIndex] = builderEntry

			cache.indexer.pubkeyCache.Add(builder.PublicKey, phase0.ValidatorIndex(dbBuilder.BuilderIndex|BuilderIndexFlag))

			restoreCount++
		}
	}

	return restoreCount, nil
}

// runPersistLoop handles the background persistence of builder states to the database
func (cache *builderCache) runPersistLoop() {
	defer func() {
		if err := recover(); err != nil {
			cache.indexer.logger.WithError(err.(error)).Errorf(
				"uncaught panic in indexer.beacon.builderCache.runPersistLoop subroutine: %v, stack: %v",
				err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go cache.runPersistLoop()
		}
	}()

	for range cache.triggerDbUpdate {
		time.Sleep(2 * time.Second)
		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			hasMore, err := cache.persistBuilders(tx)
			if hasMore {
				select {
				case cache.triggerDbUpdate <- true:
				default:
				}
			}
			return err
		})
		if err != nil {
			cache.indexer.logger.WithError(err).Errorf("error persisting builders")
		}
	}
}

// persistBuilders writes a batch of builder states to the database
func (cache *builderCache) persistBuilders(tx *sqlx.Tx) (bool, error) {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	const batchSize = 1000
	const maxPerRun = 10000

	batch := make([]*dbtypes.Builder, 0, batchSize)
	batchIndices := make([]uint64, 0, batchSize)
	supersededPubkeys := make([][]byte, 0)
	persisted := 0
	firstIndex := uint64(0)
	lastIndex := uint64(0)
	hasMore := false

	for index, entry := range cache.builderSetCache {
		if entry == nil || entry.finalBuilder == nil {
			continue
		}

		if persisted == 0 && len(batch) == 0 {
			firstIndex = uint64(index)
		}
		lastIndex = uint64(index)

		dbBuilder := &dbtypes.Builder{
			Pubkey:            entry.finalBuilder.PublicKey[:],
			BuilderIndex:      uint64(index),
			Version:           entry.finalBuilder.Version,
			ExecutionAddress:  entry.finalBuilder.ExecutionAddress[:],
			DepositEpoch:      db.ConvertUint64ToInt64(uint64(entry.finalBuilder.DepositEpoch)),
			WithdrawableEpoch: db.ConvertUint64ToInt64(uint64(entry.finalBuilder.WithdrawableEpoch)),
			Superseded:        false,
		}

		batch = append(batch, dbBuilder)
		batchIndices = append(batchIndices, uint64(index))

		if len(batch) >= batchSize {
			superseded, err := cache.persistBuilderBatch(tx, batch, batchIndices)
			if err != nil {
				return false, err
			}
			supersededPubkeys = append(supersededPubkeys, superseded...)

			// Clear finalBuilder for persisted entries
			for _, idx := range batchIndices {
				if cache.builderSetCache[idx] != nil {
					cache.builderSetCache[idx].finalBuilder = nil
				}
			}

			batch = batch[:0]
			batchIndices = batchIndices[:0]
			persisted += batchSize

			if persisted >= maxPerRun {
				hasMore = true
				break
			}
		}
	}

	// Persist remaining batch
	if len(batch) > 0 {
		superseded, err := cache.persistBuilderBatch(tx, batch, batchIndices)
		if err != nil {
			return false, err
		}
		supersededPubkeys = append(supersededPubkeys, superseded...)

		// Clear finalBuilder for persisted entries
		for _, idx := range batchIndices {
			if cache.builderSetCache[idx] != nil {
				cache.builderSetCache[idx].finalBuilder = nil
			}
		}

		persisted += len(batch)
	}

	// Batch mark superseded builders
	if len(supersededPubkeys) > 0 {
		err := db.SetBuildersSuperseded(supersededPubkeys, tx)
		if err != nil {
			return false, fmt.Errorf("error marking builders as superseded: %w", err)
		}
	}

	if persisted > 0 || len(supersededPubkeys) > 0 {
		cache.indexer.logger.Infof("persisted %d builders to db [%d-%d], marked %d as superseded",
			persisted, firstIndex, lastIndex, len(supersededPubkeys))
	}

	return hasMore, nil
}

// persistBuilderBatch persists a batch of builders and returns pubkeys that were superseded
func (cache *builderCache) persistBuilderBatch(tx *sqlx.Tx, batch []*dbtypes.Builder, indices []uint64) ([][]byte, error) {
	if len(batch) == 0 {
		return nil, nil
	}

	// Get range for this batch
	minIndex := indices[0]
	maxIndex := indices[0]
	for _, idx := range indices[1:] {
		if idx < minIndex {
			minIndex = idx
		}
		if idx > maxIndex {
			maxIndex = idx
		}
	}

	// Fetch existing builders in this batch's range
	existingBuilders := db.GetBuilderRange(minIndex, maxIndex)
	existingByIndex := make(map[uint64]*dbtypes.Builder, len(existingBuilders))
	for _, b := range existingBuilders {
		existingByIndex[b.BuilderIndex] = b
	}

	// Find superseded pubkeys
	supersededPubkeys := make([][]byte, 0)
	for i, dbBuilder := range batch {
		if existing, ok := existingByIndex[indices[i]]; ok {
			if !bytes.Equal(existing.Pubkey, dbBuilder.Pubkey) {
				supersededPubkeys = append(supersededPubkeys, existing.Pubkey)
			}
		}
	}

	// Insert batch
	err := db.InsertBuilderBatch(batch, tx)
	if err != nil {
		return nil, fmt.Errorf("error persisting builder batch: %w", err)
	}

	return supersededPubkeys, nil
}
