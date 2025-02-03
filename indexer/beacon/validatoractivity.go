package beacon

import (
	"bytes"
	"math"
	"sort"
	"strconv"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/syndtr/goleveldb/leveldb"
)

// validatorActivityCache is the cache for the validator activity.
type validatorActivityCache struct {
	indexer             *Indexer
	activityDb          *leveldb.DB
	activityMap         map[phase0.ValidatorIndex][]ValidatorActivity
	activityMutex       sync.RWMutex // mutex to protect recentActivity for concurrent access
	oldestActivityEpoch phase0.Epoch // oldest epoch in activity cache
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

type validatorActivityDbEntry struct {
	Slot  phase0.Slot
	Root  phase0.Root
	Delay uint16
}

// newValidatorActivityCache creates & returns a new instance of validatorActivityCache.
func newValidatorActivityCache(indexer *Indexer, cacheFile string) *validatorActivityCache {
	cache := &validatorActivityCache{
		indexer:             indexer,
		oldestActivityEpoch: math.MaxInt64,
	}

	if cacheFile != "" {
		db, err := leveldb.OpenFile(cacheFile, nil)
		if err != nil {
			indexer.logger.WithError(err).Error("failed to open activity cache")
		} else {
			cache.activityDb = db
		}
	}

	if cache.activityDb == nil {
		cache.activityMap = make(map[phase0.ValidatorIndex][]ValidatorActivity)
	}

	return cache
}

// updateValidatorActivity updates the validator activity cache.
func (cache *validatorActivityCache) updateValidatorActivity(validatorIndex phase0.ValidatorIndex, epoch phase0.Epoch, dutySlot phase0.Slot, voteBlock *Block) {
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

	if cache.activityDb != nil {
		cache.updateValidatorActivityDb(validatorIndex, dutySlot, voteBlock, cutOffEpoch)
	} else {
		cache.updateValidatorActivityMap(validatorIndex, dutySlot, voteBlock, cutOffEpoch)
	}
}

func (cache *validatorActivityCache) updateValidatorActivityMap(validatorIndex phase0.ValidatorIndex, dutySlot phase0.Slot, voteBlock *Block, cutOffEpoch phase0.Epoch) {
	chainState := cache.indexer.consensusPool.GetChainState()

	cache.activityMutex.Lock()
	defer cache.activityMutex.Unlock()

	recentActivity := cache.activityMap[validatorIndex]
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

	cache.activityMap[validatorIndex] = recentActivity
}

func (cache *validatorActivityCache) updateValidatorActivityDb(validatorIndex phase0.ValidatorIndex, dutySlot phase0.Slot, voteBlock *Block, cutOffEpoch phase0.Epoch) {
	chainState := cache.indexer.consensusPool.GetChainState()
	key := strconv.FormatUint(uint64(validatorIndex), 10)

	var recentActivity []validatorActivityDbEntry

	if value, err := cache.activityDb.Get([]byte(key), nil); err == nil {
		cache.indexer.dynSsz.UnmarshalSSZ(&recentActivity, value)
	}

	if recentActivity == nil {
		recentActivity = make([]validatorActivityDbEntry, 0)
	}

	replaceIndex := -1
	cutOffLength := 0
	activityLength := len(recentActivity)
	for i := activityLength - 1; i >= 0; i-- {
		activity := recentActivity[i]
		if bytes.Equal(activity.Root[:], voteBlock.Root[:]) {
			// already exists
			return
		}

		dutySlot := phase0.Slot(0)

		if chainState.EpochOfSlot(dutySlot) < cutOffEpoch {
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

	entry := validatorActivityDbEntry{
		Slot:  voteBlock.Slot,
		Root:  voteBlock.Root,
		Delay: uint16(voteBlock.Slot - dutySlot),
	}

	if replaceIndex != -1 {
		recentActivity[replaceIndex] = entry

		if cutOffLength > 0 {
			recentActivity = recentActivity[:activityLength-cutOffLength]
		}
	} else {
		recentActivity = append(recentActivity, entry)
	}

	value, err := cache.indexer.dynSsz.MarshalSSZ(recentActivity)
	if err != nil {
		cache.indexer.logger.WithError(err).Error("failed to marshal activity cache entry")
		return
	}

	err = cache.activityDb.Put([]byte(key), value, nil)
	if err != nil {
		cache.indexer.logger.WithError(err).Error("failed to update activity cache")
	}
}

// getValidatorActivity returns the validator activity for a given validator index.
func (cache *validatorActivityCache) getValidatorActivity(validatorIndex phase0.ValidatorIndex) []ValidatorActivity {
	var recentActivity []ValidatorActivity

	if cache.activityDb != nil {
		key := strconv.FormatUint(uint64(validatorIndex), 10)
		var recentActivityDb []validatorActivityDbEntry
		if value, err := cache.activityDb.Get([]byte(key), nil); err == nil {
			cache.indexer.dynSsz.UnmarshalSSZ(&recentActivityDb, value)
		}

		recentActivity = make([]ValidatorActivity, 0, len(recentActivityDb))
		for _, entry := range recentActivityDb {
			block := cache.indexer.blockCache.getBlockByRoot(entry.Root)
			if block == nil {
				block = newBlock(cache.indexer.dynSsz, entry.Root, entry.Slot)
			}

			recentActivity = append(recentActivity, ValidatorActivity{
				VoteBlock: block,
				VoteDelay: entry.Delay,
			})
		}

	} else {
		cache.activityMutex.RLock()
		defer cache.activityMutex.RUnlock()

		cachedActivity := cache.activityMap[validatorIndex]
		recentActivity = make([]ValidatorActivity, 0, len(cachedActivity))
		for _, activity := range cachedActivity {
			if activity.VoteBlock != nil {
				recentActivity = append(recentActivity, activity)
			}
		}
	}

	sort.Slice(recentActivity, func(i, j int) bool {
		return recentActivity[i].VoteBlock.Slot > recentActivity[j].VoteBlock.Slot
	})

	return recentActivity
}
