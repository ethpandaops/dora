package beacon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc64"
	"math"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

var crc64Table = crc64.MakeTable(crc64.ISO)

// validatorCache is the cache for the validator set and validator activity.
type validatorCache struct {
	indexer                  *Indexer
	valsetCache              []*validatorEntry // cache for validators
	cacheMutex               sync.RWMutex      // mutex to protect valsetCache for concurrent access
	validatorActivityMap     map[phase0.ValidatorIndex][]ValidatorActivity
	activityMutex            sync.RWMutex // mutex to protect recentActivity for concurrent access
	lastFinalized            phase0.Epoch // last finalized epoch
	lastFinalizedActiveCount uint64
	oldestActivityEpoch      phase0.Epoch // oldest epoch in activity cache
	pubkeyMap                map[phase0.BLSPubKey]phase0.ValidatorIndex
	pubkeyMutex              sync.RWMutex // mutex to protect pubkeyMap for concurrent access
	triggerDbUpdate          chan bool
}

// validatorEntry represents a validator in the validator set cache.
type validatorEntry struct {
	validatorDiffs []*validatorDiff
	finalChecksum  uint64
	finalValidator *phase0.Validator
	activeData     *ValidatorData
}

type ValidatorWithIndex struct {
	Index     uint64
	Validator *phase0.Validator
}

// ValidatorData holds the most relevant information about a active validator.
type ValidatorData struct {
	ActivationEpoch  phase0.Epoch
	ExitEpoch        phase0.Epoch
	effectiveBalance uint16
}

type ValidatorDataWithIndex struct {
	Index uint64
	Data  *ValidatorData
}

// ValidatorActivity represents a validator's activity in an epoch.
// entry size: 18 bytes (10 bytes data + 8 bytes pointer)
// max. entries per validator: 3-8 (inMemoryEpochs)
// total memory consumption:
//   - 10k active validators:
//     min: 10000 * 18 * 3 = 540kB = 0.54MB
//     max: 10000 * 18 * 8 = 1440kB = 1.44MB
//   - 100k active validators:
//     min: 100000 * 18 * 3 = 5400kB = 5.4MB
//     max: 100000 * 18 * 8 = 14400kB = 14.4MB
//   - 1M active validators:
//     min: 1000000 * 18 * 3 = 54000kB = 54MB
//     max: 1000000 * 18 * 8 = 144000kB = 144MB
type ValidatorActivity struct {
	VoteBlock *Block // the block where the vote was included
	VoteDelay uint16 // the inclusion delay of the vote in slots
}

// validatorDiff represents an updated validator entry in the validator set cache.
type validatorDiff struct {
	epoch         phase0.Epoch
	dependentRoot phase0.Root
	validator     *phase0.Validator
}

// newValidatorCache creates & returns a new instance of validatorCache.
func newValidatorCache(indexer *Indexer) *validatorCache {
	cache := &validatorCache{
		indexer:              indexer,
		validatorActivityMap: make(map[phase0.ValidatorIndex][]ValidatorActivity),
		oldestActivityEpoch:  math.MaxInt64,
		pubkeyMap:            make(map[phase0.BLSPubKey]phase0.ValidatorIndex),
		triggerDbUpdate:      make(chan bool, 1),
	}

	go cache.runPersistLoop()

	return cache
}

// EffectiveBalance returns the effective balance of the validator.
func (v *ValidatorData) EffectiveBalance() phase0.Gwei {
	return phase0.Gwei(v.effectiveBalance) * EtherGweiFactor
}

// updateValidatorSet updates the validator set cache with the new validator set.
func (cache *validatorCache) updateValidatorSet(slot phase0.Slot, dependentRoot phase0.Root, validators []*phase0.Validator) {
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
		// ignore old validator set
		cache.indexer.logger.Infof("ignoring old validator set update for epoch %d", epoch)
		return
	}

	isFinalizedValidatorSet := false
	if slot == 0 {
		isFinalizedValidatorSet = true // genesis
	} else if epoch <= finalizedEpoch {
		finalizedBlock := cache.indexer.blockCache.getBlockByRoot(finalizedRoot)
		if finalizedBlock != nil {
			finalizedDependentBlock := cache.indexer.blockCache.getDependentBlock(chainState, finalizedBlock, nil)
			if finalizedDependentBlock != nil && bytes.Equal(finalizedDependentBlock.Root[:], dependentRoot[:]) {
				isFinalizedValidatorSet = true
			}
		}
	}

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	t1 := time.Now()

	if len(cache.valsetCache) < len(validators) {
		if len(validators) > cap(cache.valsetCache) {
			newCache := make([]*validatorEntry, len(validators), len(validators)+1000)
			copy(newCache, cache.valsetCache)
			cache.valsetCache = newCache
		} else {
			cache.valsetCache = cache.valsetCache[:len(validators)]
		}
	}

	isParentMap := map[phase0.Root]bool{}
	isAheadMap := map[phase0.Root]bool{}
	updatedCount := uint64(0)

	for i := range validators {
		var parentChecksum uint64
		var parentValidator *phase0.Validator
		parentEpoch := phase0.Epoch(0)

		aheadDiffIdx := 0
		foundAhead := false
		aheadEpoch := phase0.Epoch(math.MaxInt64)

		cachedValidator := cache.valsetCache[i]
		if cachedValidator == nil {
			cachedValidator = &validatorEntry{}
			cache.valsetCache[i] = cachedValidator
			cache.pubkeyMutex.Lock()
			cache.pubkeyMap[validators[i].PublicKey] = phase0.ValidatorIndex(i)
			cache.pubkeyMutex.Unlock()
		} else {
			parentValidator = cachedValidator.finalValidator
			parentChecksum = cachedValidator.finalChecksum
		}

		deleteKeys := []int{}

		if !isFinalizedValidatorSet {
			// search for parent diffs that this update depends on
			for diffkey, diff := range cachedValidator.validatorDiffs {
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
						parentValidator = diff.validator
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

			if parentValidator != nil {
				parentChecksum = calculateValidatorChecksum(parentValidator)
			}
		}

		checksum := calculateValidatorChecksum(validators[i])
		if checksum == parentChecksum {
			// no change
			continue
		}

		if isFinalizedValidatorSet {
			cachedValidator.finalValidator = validators[i]
			cachedValidator.finalChecksum = checksum
			updatedCount++
		}

		if foundAhead && cache.checkValidatorEqual(cachedValidator.validatorDiffs[aheadDiffIdx].validator, validators[i]) {
			if isFinalizedValidatorSet {
				deleteKeys = append(deleteKeys, aheadDiffIdx)
			} else {
				diff := cachedValidator.validatorDiffs[aheadDiffIdx]
				diff.epoch = epoch
				diff.dependentRoot = dependentRoot
				cachedValidator.validatorDiffs[aheadDiffIdx] = diff
			}
		} else if isFinalizedValidatorSet {
		} else if len(deleteKeys) == 0 {
			cachedValidator.validatorDiffs = append(cachedValidator.validatorDiffs, &validatorDiff{
				epoch:         epoch,
				dependentRoot: dependentRoot,
				validator:     validators[i],
			})
		} else {
			cachedValidator.validatorDiffs[deleteKeys[0]] = &validatorDiff{
				epoch:         epoch,
				dependentRoot: dependentRoot,
				validator:     validators[i],
			}
			deleteKeys = deleteKeys[1:]
		}

		if len(deleteKeys) > 0 {
			lastIdx := len(cachedValidator.validatorDiffs) - 1
			delLen := len(deleteKeys)
			for delIdx := 0; delIdx < delLen; delIdx++ {
				for delLen > 0 && deleteKeys[delLen-1] == lastIdx {
					lastIdx--
					delLen--
				}
				if delLen == 0 {
					break
				}
				cachedValidator.validatorDiffs[deleteKeys[delIdx]] = cachedValidator.validatorDiffs[lastIdx]
				lastIdx--
			}

			cachedValidator.validatorDiffs = cachedValidator.validatorDiffs[:lastIdx+1]
		}
	}

	if updatedCount > 0 {
		select {
		case cache.triggerDbUpdate <- true:
		default:
		}
	}

	isFinalizedStr := ""
	if isFinalizedValidatorSet {
		isFinalizedStr = "finalized "
	}
	cache.indexer.logger.Infof("processed %vvalidator set update for epoch %d in %v", isFinalizedStr, epoch, time.Since(t1))
}

func (cache *validatorCache) checkValidatorEqual(validator1 *phase0.Validator, validator2 *phase0.Validator) bool {
	if validator1 == nil && validator2 == nil {
		return true
	}
	if validator1 == nil || validator2 == nil {
		return false
	}
	return bytes.Equal(validator1.PublicKey[:], validator2.PublicKey[:]) &&
		bytes.Equal(validator1.WithdrawalCredentials, validator2.WithdrawalCredentials) &&
		validator1.EffectiveBalance == validator2.EffectiveBalance &&
		validator1.Slashed == validator2.Slashed &&
		validator1.ActivationEligibilityEpoch == validator2.ActivationEligibilityEpoch &&
		validator1.ActivationEpoch == validator2.ActivationEpoch &&
		validator1.ExitEpoch == validator2.ExitEpoch &&
		validator1.WithdrawableEpoch == validator2.WithdrawableEpoch
}

// updateValidatorActivity updates the validator activity cache.
func (cache *validatorCache) updateValidatorActivity(validatorIndex phase0.ValidatorIndex, epoch phase0.Epoch, dutySlot phase0.Slot, voteBlock *Block) {
	chainState := cache.indexer.consensusPool.GetChainState()
	currentEpoch := chainState.CurrentEpoch()
	cutOffEpoch := phase0.Epoch(0)
	if currentEpoch > phase0.Epoch(cache.indexer.activityHistoryLength) {
		cutOffEpoch = currentEpoch - phase0.Epoch(cache.indexer.activityHistoryLength)
	}

	if epoch < cutOffEpoch {
		// ignore old activity
		return
	}
	if epoch < cache.oldestActivityEpoch {
		cache.oldestActivityEpoch = epoch
	} else if cache.oldestActivityEpoch < cutOffEpoch+1 {
		cache.oldestActivityEpoch = cutOffEpoch + 1
	}

	cache.activityMutex.Lock()
	defer cache.activityMutex.Unlock()

	recentActivity := cache.validatorActivityMap[validatorIndex]
	if recentActivity == nil {
		recentActivity = make([]ValidatorActivity, 0, cache.indexer.activityHistoryLength)
	}

	replaceIndex := -1
	cutOffLength := 0
	activityLength := len(recentActivity)
	for i := activityLength - 1; i >= 0; i-- {
		activity := recentActivity[i]
		if activity.VoteBlock == voteBlock {
			// already exists
			return
		}

		dutySlot := phase0.Slot(0)
		if activity.VoteBlock != nil {
			dutySlot = activity.VoteBlock.Slot
		}

		if chainState.EpochOfSlot(dutySlot) < cutOffEpoch {
			recentActivity[i].VoteBlock = nil // clear for gc
			if replaceIndex == -1 {
				replaceIndex = i
			} else if replaceIndex == activityLength-cutOffLength-1 {
				cutOffLength++
				replaceIndex = i
			} else {
				// copy last element to current index
				cutOffLength++
				recentActivity[i] = recentActivity[activityLength-cutOffLength-1]
			}
		}
	}

	if replaceIndex != -1 {
		recentActivity[replaceIndex] = ValidatorActivity{
			VoteBlock: voteBlock,
			VoteDelay: uint16(voteBlock.Slot - dutySlot),
		}

		if cutOffLength > 0 {
			recentActivity = recentActivity[:activityLength-cutOffLength]
		}
	} else {
		recentActivity = append(recentActivity, ValidatorActivity{
			VoteBlock: voteBlock,
			VoteDelay: uint16(voteBlock.Slot - dutySlot),
		})
	}

	cache.validatorActivityMap[validatorIndex] = recentActivity
}

// setFinalizedEpoch sets the last finalized epoch.
func (cache *validatorCache) setFinalizedEpoch(epoch phase0.Epoch, nextEpochDependentRoot phase0.Root) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.lastFinalized = epoch
	activeCount := uint64(0)
	updatedCount := uint64(0)

	for _, cachedValidator := range cache.valsetCache {
		if cachedValidator == nil {
			continue
		}

		// Find the finalized validator state
		for _, diff := range cachedValidator.validatorDiffs {
			if diff.dependentRoot == nextEpochDependentRoot {
				cachedValidator.finalValidator = diff.validator
				cachedValidator.finalChecksum = calculateValidatorChecksum(diff.validator)
				updatedCount++

				cachedValidator.activeData = &ValidatorData{
					ActivationEpoch:  diff.validator.ActivationEpoch,
					ExitEpoch:        diff.validator.ExitEpoch,
					effectiveBalance: uint16(diff.validator.EffectiveBalance / EtherGweiFactor),
				}
				break
			}
		}

		// Clean up old diffs
		newDiffs := make([]*validatorDiff, 0)
		for _, diff := range cachedValidator.validatorDiffs {
			if diff.epoch > epoch {
				newDiffs = append(newDiffs, diff)
			}
		}
		cachedValidator.validatorDiffs = newDiffs

		// clear old active data
		if cachedValidator.activeData != nil {
			if !cache.isActiveValidator(cachedValidator.activeData) {
				cachedValidator.activeData = nil
			} else {
				activeCount++
			}
		}
	}

	cache.lastFinalizedActiveCount = activeCount

	if updatedCount > 0 {
		select {
		case cache.triggerDbUpdate <- true:
		default:
		}
	}
}

// getActiveValidatorDataForRoot returns the active validator data for a given blockRoot.
func (cache *validatorCache) getActiveValidatorDataForRoot(epoch *phase0.Epoch, blockRoot phase0.Root) []ValidatorDataWithIndex {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	isParentMap := map[phase0.Root]bool{}
	isAheadMap := map[phase0.Root]bool{}

	validatorSet := make([]ValidatorDataWithIndex, 0, cache.lastFinalizedActiveCount+100)

	for index, cachedValidator := range cache.valsetCache {
		if cachedValidator == nil {
			continue
		}

		validatorData := cachedValidator.activeData
		validatorEpoch := cache.lastFinalized

		var aheadValidator *phase0.Validator
		aheadEpoch := phase0.Epoch(math.MaxInt64)

		for _, diff := range cachedValidator.validatorDiffs {
			isParent, checkedParent := isParentMap[diff.dependentRoot]
			if !checkedParent {
				isParent = cache.indexer.blockCache.isCanonicalBlock(diff.dependentRoot, blockRoot)
				isParentMap[diff.dependentRoot] = isParent
			}

			if isParent && diff.epoch >= validatorEpoch {
				validatorData = &ValidatorData{
					ActivationEpoch:  diff.validator.ActivationEpoch,
					ExitEpoch:        diff.validator.ExitEpoch,
					effectiveBalance: uint16(diff.validator.EffectiveBalance / EtherGweiFactor),
				}
				validatorEpoch = diff.epoch
			}

			if !isParent && validatorData == nil {
				isAhead, checkedAhead := isAheadMap[diff.dependentRoot]
				if !checkedAhead {
					isAhead = cache.indexer.blockCache.isCanonicalBlock(blockRoot, diff.dependentRoot)
					isAheadMap[diff.dependentRoot] = isAhead
				}

				if isAhead && diff.epoch < aheadEpoch {
					aheadValidator = diff.validator
					aheadEpoch = diff.epoch
				}
			}
		}

		if validatorData == nil && aheadValidator != nil {
			validatorData = &ValidatorData{
				ActivationEpoch:  aheadValidator.ActivationEpoch,
				ExitEpoch:        aheadValidator.ExitEpoch,
				effectiveBalance: uint16(aheadValidator.EffectiveBalance / EtherGweiFactor),
			}
		}

		if validatorData != nil {
			if epoch != nil && (*epoch < validatorData.ActivationEpoch || *epoch >= validatorData.ExitEpoch) {
				continue
			}

			validatorSet = append(validatorSet, ValidatorDataWithIndex{
				Index: uint64(index),
				Data:  validatorData,
			})
		}
	}

	return validatorSet
}

// getCachedValidatorSetForRoot returns the cached validator set for a given blockRoot.
// missing entries can be found in the DB
func (cache *validatorCache) getCachedValidatorSetForRoot(blockRoot phase0.Root) []ValidatorWithIndex {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	isParentMap := map[phase0.Root]bool{}
	isAheadMap := map[phase0.Root]bool{}

	validatorSet := make([]ValidatorWithIndex, 0, 100)

	for index, cachedValidator := range cache.valsetCache {
		validatorData := ValidatorWithIndex{
			Index:     uint64(index),
			Validator: nil,
		}
		validatorEpoch := cache.lastFinalized

		if cachedValidator != nil {
			var aheadValidator *phase0.Validator
			aheadEpoch := phase0.Epoch(math.MaxInt64)
			validatorData.Validator = cachedValidator.finalValidator

			for _, diff := range cachedValidator.validatorDiffs {
				isParent, checkedParent := isParentMap[diff.dependentRoot]
				if !checkedParent {
					isParent = cache.indexer.blockCache.isCanonicalBlock(diff.dependentRoot, blockRoot)
					isParentMap[diff.dependentRoot] = isParent
				}

				if isParent && diff.epoch >= validatorEpoch {
					validatorData.Validator = diff.validator
					validatorEpoch = diff.epoch
				}

				if !isParent && validatorData.Validator == nil {
					isAhead, checkedAhead := isAheadMap[diff.dependentRoot]
					if !checkedAhead {
						isAhead = cache.indexer.blockCache.isCanonicalBlock(blockRoot, diff.dependentRoot)
						isAheadMap[diff.dependentRoot] = isAhead
					}

					if isAhead && diff.epoch < aheadEpoch {
						aheadValidator = diff.validator
						aheadEpoch = diff.epoch
					}
				}
			}

			if validatorData.Validator == nil && aheadValidator != nil {
				validatorData.Validator = aheadValidator
			}
		}

		if validatorData.Validator != nil {
			validatorSet = append(validatorSet, validatorData)
		}
	}

	return validatorSet
}

// getActivationExitQueueLengths returns the activation and exit queue lengths.
func (cache *validatorCache) getActivationExitQueueLengths(epoch phase0.Epoch, blockRoot phase0.Root) (uint64, uint64) {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	activationQueueLength := uint64(0)
	exitQueueLength := uint64(0)

	for _, validator := range cache.getActiveValidatorDataForRoot(nil, blockRoot) {
		if validator.Data.ActivationEpoch < FarFutureEpoch && validator.Data.ActivationEpoch > epoch {
			activationQueueLength++
		}
		if validator.Data.ExitEpoch < FarFutureEpoch && validator.Data.ExitEpoch > epoch {
			exitQueueLength++
		}
	}

	return activationQueueLength, exitQueueLength
}

// UnwrapDbValidator unwraps a dbtypes.Validator to a phase0.Validator
func UnwrapDbValidator(dbValidator *dbtypes.Validator) *phase0.Validator {
	validator := &phase0.Validator{
		PublicKey:                  phase0.BLSPubKey(dbValidator.Pubkey),
		WithdrawalCredentials:      dbValidator.WithdrawalCredentials,
		EffectiveBalance:           phase0.Gwei(dbValidator.EffectiveBalance),
		Slashed:                    dbValidator.Slashed,
		ActivationEligibilityEpoch: phase0.Epoch(db.ConvertInt64ToUint64(dbValidator.ActivationEligibilityEpoch)),
		ActivationEpoch:            phase0.Epoch(db.ConvertInt64ToUint64(dbValidator.ActivationEpoch)),
		ExitEpoch:                  phase0.Epoch(db.ConvertInt64ToUint64(dbValidator.ExitEpoch)),
		WithdrawableEpoch:          phase0.Epoch(db.ConvertInt64ToUint64(dbValidator.WithdrawableEpoch)),
	}
	return validator
}

func (cache *validatorCache) isActiveValidator(validator *ValidatorData) bool {
	currentEpoch := cache.indexer.consensusPool.GetChainState().CurrentEpoch()
	cutOffEpoch := phase0.Epoch(0)
	if currentEpoch > 10 {
		cutOffEpoch = currentEpoch - 10
	}

	return validator.ActivationEpoch < FarFutureEpoch && validator.ExitEpoch > cutOffEpoch
}

// getValidatorByIndex returns the validator by index for a given forkId.
func (cache *validatorCache) getValidatorByIndex(index phase0.ValidatorIndex, overrideForkId *ForkKey) *phase0.Validator {
	canonicalHead := cache.indexer.GetCanonicalHead(overrideForkId)
	if canonicalHead == nil {
		return nil
	}

	return cache.getValidatorByIndexAndRoot(index, canonicalHead.Root)
}

// getValidatorByIndexAndRoot returns the validator by index for a given blockRoot.
func (cache *validatorCache) getValidatorByIndexAndRoot(index phase0.ValidatorIndex, blockRoot phase0.Root) *phase0.Validator {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	if index >= phase0.ValidatorIndex(len(cache.valsetCache)) {
		return nil
	}

	cachedValidator := cache.valsetCache[index]
	if cachedValidator == nil {
		return nil
	}

	validator := cachedValidator.finalValidator
	validatorEpoch := cache.lastFinalized

	// Find the latest valid diff
	for _, diff := range cachedValidator.validatorDiffs {
		if cache.indexer.blockCache.isCanonicalBlock(diff.dependentRoot, blockRoot) && diff.epoch >= validatorEpoch {
			validator = diff.validator
			validatorEpoch = diff.epoch
		}
	}

	// fallback to db if validator is not found in cache
	if validator == nil {
		if dbValidator := db.GetValidatorByIndex(index); dbValidator != nil {
			validator = UnwrapDbValidator(dbValidator)
		}
	}

	return validator
}

// getValidatorIndexByPubkey returns the validator index by pubkey.
func (cache *validatorCache) getValidatorIndexByPubkey(pubkey phase0.BLSPubKey) (phase0.ValidatorIndex, bool) {
	cache.pubkeyMutex.RLock()
	defer cache.pubkeyMutex.RUnlock()

	index, found := cache.pubkeyMap[pubkey]
	return index, found
}

// getValidatorActivity returns the validator activity for a given validator index.
func (cache *validatorCache) getValidatorActivity(validatorIndex phase0.ValidatorIndex) []ValidatorActivity {
	cache.activityMutex.RLock()
	defer cache.activityMutex.RUnlock()

	cachedActivity := cache.validatorActivityMap[validatorIndex]
	recentActivity := make([]ValidatorActivity, 0, len(cachedActivity))
	for _, activity := range cachedActivity {
		if activity.VoteBlock != nil {
			recentActivity = append(recentActivity, activity)
		}
	}

	sort.Slice(recentActivity, func(i, j int) bool {
		return recentActivity[i].VoteBlock.Slot > recentActivity[j].VoteBlock.Slot
	})

	return recentActivity
}

func calculateValidatorChecksum(v *phase0.Validator) uint64 {
	if v == nil {
		return 0
	}

	// Create a byte slice containing all validator fields
	data := make([]byte, 0)
	data = append(data, v.PublicKey[:]...)
	data = append(data, v.WithdrawalCredentials[:]...)
	data = append(data, uint64ToBytes(uint64(v.EffectiveBalance))...)
	if v.Slashed {
		data = append(data, 1)
	} else {
		data = append(data, 0)
	}
	data = append(data, uint64ToBytes(uint64(v.ActivationEligibilityEpoch))...)
	data = append(data, uint64ToBytes(uint64(v.ActivationEpoch))...)
	data = append(data, uint64ToBytes(uint64(v.ExitEpoch))...)
	data = append(data, uint64ToBytes(uint64(v.WithdrawableEpoch))...)

	return crc64.Checksum(data, crc64Table)
}

func uint64ToBytes(val uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, val)
	return b
}

func (cache *validatorCache) prepopulateFromDB() error {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	// Get max validator index to pre-allocate slice
	maxIndex, err := db.GetMaxValidatorIndex()
	if err != nil {
		return fmt.Errorf("error getting max validator index: %v", err)
	}

	// Pre-allocate slice
	cache.valsetCache = make([]*validatorEntry, maxIndex+1, maxIndex+1+1000)

	activeCount := uint64(0)

	// Load validators in batches
	batchSize := uint64(10000)
	for start := uint64(0); start <= maxIndex; start += batchSize {
		end := start + batchSize
		if end > maxIndex {
			end = maxIndex
		}

		validators := db.GetValidatorRange(start, end)
		for _, dbVal := range validators {
			// Convert db validator to phase0.Validator
			val := UnwrapDbValidator(dbVal)
			valEntry := &validatorEntry{
				finalChecksum: calculateValidatorChecksum(val),
			}
			valData := &ValidatorData{
				ActivationEpoch:  phase0.Epoch(db.ConvertInt64ToUint64(dbVal.ActivationEpoch)),
				ExitEpoch:        phase0.Epoch(db.ConvertInt64ToUint64(dbVal.ExitEpoch)),
				effectiveBalance: uint16(val.EffectiveBalance / EtherGweiFactor),
			}
			if cache.isActiveValidator(valData) {
				valEntry.activeData = valData
				activeCount++
			}

			// Create cache entry with checksum
			cache.valsetCache[dbVal.ValidatorIndex] = valEntry

			// Update pubkey map
			cache.pubkeyMutex.Lock()
			cache.pubkeyMap[phase0.BLSPubKey(dbVal.Pubkey)] = phase0.ValidatorIndex(dbVal.ValidatorIndex)
			cache.pubkeyMutex.Unlock()
		}
	}

	cache.lastFinalizedActiveCount = activeCount

	return nil
}

func (cache *validatorCache) runPersistLoop() {
	defer func() {
		if err := recover(); err != nil {
			cache.indexer.logger.WithError(err.(error)).Errorf("uncaught panic in indexer.beacon.validatorCache.runPersistLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go cache.runPersistLoop()
		}
	}()

	for range cache.triggerDbUpdate {
		time.Sleep(2 * time.Second)
		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			return cache.persistValidators(tx)
		})
		if err != nil {
			cache.indexer.logger.WithError(err).Errorf("error persisting validators")
		}
	}
}

func (cache *validatorCache) persistValidators(tx *sqlx.Tx) error {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	batch := make([]*dbtypes.Validator, 0, 100)
	persisted := 0

	for index, entry := range cache.valsetCache {
		if entry == nil || entry.finalValidator == nil {
			continue
		}

		// Convert to db type
		dbVal := &dbtypes.Validator{
			ValidatorIndex:             uint64(index),
			Pubkey:                     entry.finalValidator.PublicKey[:],
			WithdrawalCredentials:      entry.finalValidator.WithdrawalCredentials[:],
			EffectiveBalance:           uint64(entry.finalValidator.EffectiveBalance),
			Slashed:                    entry.finalValidator.Slashed,
			ActivationEligibilityEpoch: db.ConvertUint64ToInt64(uint64(entry.finalValidator.ActivationEligibilityEpoch)),
			ActivationEpoch:            db.ConvertUint64ToInt64(uint64(entry.finalValidator.ActivationEpoch)),
			ExitEpoch:                  db.ConvertUint64ToInt64(uint64(entry.finalValidator.ExitEpoch)),
			WithdrawableEpoch:          db.ConvertUint64ToInt64(uint64(entry.finalValidator.WithdrawableEpoch)),
		}

		batch = append(batch, dbVal)
		entry.finalValidator = nil

		if len(batch) >= 100 {
			err := db.InsertValidatorBatch(batch, tx)
			if err != nil {
				return fmt.Errorf("error persisting validator batch: %v", err)
			}
			batch = batch[:0]
			persisted += 100

			if persisted >= 10000 {
				break // Max 10k validators per run
			}
		}
	}

	// Insert remaining batch
	if len(batch) > 0 {
		err := db.InsertValidatorBatch(batch, tx)
		if err != nil {
			return fmt.Errorf("error persisting final validator batch: %v", err)
		}
	}

	return nil
}
