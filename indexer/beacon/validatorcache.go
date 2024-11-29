package beacon

import (
	"encoding/binary"
	"math"
	"reflect"
	"sort"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// validatorCache is the cache for the validator set and validator activity.
type validatorCache struct {
	indexer *Indexer

	valsetCache         []*validatorEntry // cache for validators
	cacheMutex          sync.RWMutex      // mutex to protect valsetCache for concurrent access
	activityMutex       sync.RWMutex      // mutex to protect recentActivity for concurrent access
	lastFinalized       phase0.Epoch      // last finalized epoch
	oldestActivityEpoch phase0.Epoch      // oldest epoch in activity cache
	pubkeyMap           map[phase0.BLSPubKey]phase0.ValidatorIndex
	pubkeyMutex         sync.RWMutex // mutex to protect pubkeyMap for concurrent access
}

// validatorDiffKey is the primary key for validatorDiff entries in cache.
// consists of dependendRoot (32 byte) and epoch (8 byte).
type validatorDiffKey [32 + 8]byte

// generate validatorDiffKey from epoch and dependentRoot
func getValidatorDiffKey(epoch phase0.Epoch, dependentRoot phase0.Root) validatorDiffKey {
	var key validatorDiffKey

	copy(key[0:], dependentRoot[:])
	binary.LittleEndian.PutUint64(key[32:], uint64(epoch))

	return key
}

// validatorEntry represents a validator in the validator set cache.
type validatorEntry struct {
	index          phase0.ValidatorIndex
	finalValidator *phase0.Validator
	validatorDiffs map[validatorDiffKey]*validatorDiff
	recentActivity []ValidatorActivity
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
		indexer:             indexer,
		oldestActivityEpoch: math.MaxInt64,
		pubkeyMap:           make(map[phase0.BLSPubKey]phase0.ValidatorIndex),
	}

	return cache
}

// updateValidatorSet updates the validator set cache with the new validator set.
func (cache *validatorCache) updateValidatorSet(epoch phase0.Epoch, dependentRoot phase0.Root, validators []*phase0.Validator) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if cache.valsetCache == nil {
		cache.valsetCache = make([]*validatorEntry, 0, len(validators))
	}

	isParentMap := map[phase0.Root]bool{}
	isAheadMap := map[phase0.Root]bool{}

	currentEpoch := cache.indexer.consensusPool.GetChainState().CurrentEpoch()
	finalizedEpoch, _ := cache.indexer.consensusPool.GetChainState().GetFinalizedCheckpoint()
	cutOffEpoch := phase0.Epoch(0)
	if currentEpoch > phase0.Epoch(cache.indexer.inMemoryEpochs) {
		cutOffEpoch = currentEpoch - phase0.Epoch(cache.indexer.inMemoryEpochs)
	}
	if cutOffEpoch > finalizedEpoch {
		cutOffEpoch = finalizedEpoch
	}

	if epoch < cutOffEpoch {
		// ignore old validator set
		return
	}

	for i := range validators {
		var parentValidator *phase0.Validator
		parentEpoch := phase0.Epoch(0)

		aheadDiffIdx := validatorDiffKey{}
		foundAhead := false
		aheadEpoch := phase0.Epoch(math.MaxInt64)

		var cachedValidator *validatorEntry
		if len(cache.valsetCache) > i {
			cachedValidator = cache.valsetCache[i]
		}

		if cachedValidator == nil {
			cachedValidator = &validatorEntry{
				index:          phase0.ValidatorIndex(i),
				validatorDiffs: make(map[validatorDiffKey]*validatorDiff),
			}
			cache.valsetCache = append(cache.valsetCache, cachedValidator)
			cache.pubkeyMutex.Lock()
			cache.pubkeyMap[validators[i].PublicKey] = phase0.ValidatorIndex(i)
			cache.pubkeyMutex.Unlock()
		} else {
			parentValidator = cachedValidator.finalValidator
		}

		for diffkey, diff := range cachedValidator.validatorDiffs {
			if diff.epoch < cutOffEpoch {
				delete(cachedValidator.validatorDiffs, diffkey)
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

		if parentValidator != nil && reflect.DeepEqual(parentValidator, validators[i]) {
			// no change
			continue
		}

		diffKey := getValidatorDiffKey(epoch, dependentRoot)

		if foundAhead && reflect.DeepEqual(cachedValidator.validatorDiffs[aheadDiffIdx].validator, validators[i]) {
			diff := cachedValidator.validatorDiffs[aheadDiffIdx]
			diff.epoch = epoch
			diff.dependentRoot = dependentRoot
			cachedValidator.validatorDiffs[diffKey] = diff
			delete(cachedValidator.validatorDiffs, aheadDiffIdx)
		} else {
			cachedValidator.validatorDiffs[diffKey] = &validatorDiff{
				epoch:         epoch,
				dependentRoot: dependentRoot,
				validator:     validators[i],
			}
		}
	}
}

// updateValidatorActivity updates the validator activity cache.
func (cache *validatorCache) updateValidatorActivity(validatorIndex phase0.ValidatorIndex, epoch phase0.Epoch, dutySlot phase0.Slot, voteBlock *Block) {
	chainState := cache.indexer.consensusPool.GetChainState()
	currentEpoch := chainState.CurrentEpoch()
	cutOffEpoch := phase0.Epoch(0)
	if currentEpoch > phase0.Epoch(cache.indexer.inMemoryEpochs) {
		cutOffEpoch = currentEpoch - phase0.Epoch(cache.indexer.inMemoryEpochs)
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

	cache.cacheMutex.RLock()

	if validatorIndex >= phase0.ValidatorIndex(len(cache.valsetCache)) {
		cache.cacheMutex.RUnlock()
		return
	}

	cachedValidator := cache.valsetCache[validatorIndex]
	cache.cacheMutex.RUnlock()
	if cachedValidator == nil {
		return
	}

	cache.activityMutex.Lock()
	defer cache.activityMutex.Unlock()

	if cachedValidator.recentActivity == nil {
		cachedValidator.recentActivity = make([]ValidatorActivity, 0, cache.indexer.inMemoryEpochs)
	}

	replaceIndex := -1
	cutOffLength := 0
	activityLength := len(cachedValidator.recentActivity)
	for i := activityLength - 1; i >= 0; i-- {
		activity := cachedValidator.recentActivity[i]
		if activity.VoteBlock == voteBlock {
			// already exists
			return
		}

		dutySlot := phase0.Slot(0)
		if activity.VoteBlock != nil {
			dutySlot = activity.VoteBlock.Slot
		}

		if chainState.EpochOfSlot(dutySlot) < cutOffEpoch {
			cachedValidator.recentActivity[i].VoteBlock = nil // clear for gc
			if replaceIndex == -1 {
				replaceIndex = i
			} else if replaceIndex == activityLength-cutOffLength-1 {
				cutOffLength++
				replaceIndex = i
			} else {
				// copy last element to current index
				cutOffLength++
				cachedValidator.recentActivity[i] = cachedValidator.recentActivity[activityLength-cutOffLength-1]
			}
		}
	}

	if replaceIndex != -1 {
		cachedValidator.recentActivity[replaceIndex] = ValidatorActivity{
			VoteBlock: voteBlock,
			VoteDelay: uint16(voteBlock.Slot - dutySlot),
		}

		if cutOffLength > 0 {
			cachedValidator.recentActivity = cachedValidator.recentActivity[:activityLength-cutOffLength]
		}
	} else {
		cachedValidator.recentActivity = append(cachedValidator.recentActivity, ValidatorActivity{
			VoteBlock: voteBlock,
			VoteDelay: uint16(voteBlock.Slot - dutySlot),
		})
	}
}

// setFinalizedEpoch sets the last finalized epoch.
func (cache *validatorCache) setFinalizedEpoch(epoch phase0.Epoch, nextEpochDependentRoot phase0.Root) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	cache.lastFinalized = epoch

	for _, cachedValidator := range cache.valsetCache {
		for diffKey, diff := range cachedValidator.validatorDiffs {
			if diff.dependentRoot == nextEpochDependentRoot {
				cachedValidator.finalValidator = diff.validator
			}

			if diff.epoch <= epoch {
				delete(cachedValidator.validatorDiffs, diffKey)
			}
		}
	}
}

// getValidatorSet returns the validator set for a given forkId.
func (cache *validatorCache) getValidatorSet(overrideForkId *ForkKey) []*phase0.Validator {
	canonicalHead := cache.indexer.GetCanonicalHead(overrideForkId)
	if canonicalHead == nil {
		return []*phase0.Validator{}
	}

	return cache.getValidatorSetForRoot(canonicalHead.Root)
}

// getValidatorSetForRoot returns the validator set for a given blockRoot.
func (cache *validatorCache) getValidatorSetForRoot(blockRoot phase0.Root) []*phase0.Validator {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	isParentMap := map[phase0.Root]bool{}
	isAheadMap := map[phase0.Root]bool{}

	validatorSet := make([]*phase0.Validator, 0, len(cache.valsetCache))

	for _, cachedValidator := range cache.valsetCache {
		validator := cachedValidator.finalValidator
		validatorEpoch := cache.lastFinalized

		var aheadValidator *phase0.Validator
		aheadEpoch := phase0.Epoch(math.MaxInt64)

		for _, diff := range cachedValidator.validatorDiffs {
			isParent, checkedParent := isParentMap[diff.dependentRoot]
			if !checkedParent {
				isParent = cache.indexer.blockCache.isCanonicalBlock(diff.dependentRoot, blockRoot)
				isParentMap[diff.dependentRoot] = isParent
			}

			if isParent && diff.epoch > validatorEpoch {
				validator = diff.validator
				validatorEpoch = diff.epoch
			}

			if !isParent && validator == nil {
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

		if validator == nil && aheadValidator != nil {
			validator = aheadValidator
		}

		validatorSet = append(validatorSet, validator)
	}

	return validatorSet
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

	isParentMap := map[phase0.Root]bool{}

	if index >= phase0.ValidatorIndex(len(cache.valsetCache)) {
		return nil
	}

	cachedValidator := cache.valsetCache[index]
	if cachedValidator == nil {
		return nil
	}

	validator := cachedValidator.finalValidator
	validatorEpoch := cache.lastFinalized

	for _, diff := range cachedValidator.validatorDiffs {
		isParent, checkedParent := isParentMap[diff.dependentRoot]
		if !checkedParent {
			isParent = cache.indexer.blockCache.isCanonicalBlock(diff.dependentRoot, blockRoot)
			isParentMap[diff.dependentRoot] = isParent
		}

		if isParent && diff.epoch > validatorEpoch {
			validator = diff.validator
			validatorEpoch = diff.epoch
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
	cache.cacheMutex.RLock()

	if validatorIndex >= phase0.ValidatorIndex(len(cache.valsetCache)) {
		cache.cacheMutex.RUnlock()
		return []ValidatorActivity{}
	}

	cachedValidator := cache.valsetCache[validatorIndex]
	cache.cacheMutex.RUnlock()

	if cachedValidator == nil {
		return []ValidatorActivity{}
	}

	cache.activityMutex.RLock()
	defer cache.activityMutex.RUnlock()

	recentActivity := make([]ValidatorActivity, 0, len(cachedValidator.recentActivity))
	for _, activity := range cachedValidator.recentActivity {
		if activity.VoteBlock != nil {
			recentActivity = append(recentActivity, activity)
		}
	}

	sort.Slice(recentActivity, func(i, j int) bool {
		return recentActivity[i].VoteBlock.Slot > recentActivity[j].VoteBlock.Slot
	})

	return recentActivity
}
