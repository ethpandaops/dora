package beacon

import (
	"math"
	"runtime/debug"
	"sync"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// processedActivityPtcBit is the bit on Block.processedActivity used to mark a
// block as having had its PTC payload attestations folded into the cache, so
// repeated canonical aggregations do not double-count.
const processedActivityPtcBit uint8 = 0x04

// validatorPtcCache caches per-validator PTC duty/vote counts for recent epochs.
// Entries are populated when blocks are processed and pruned together with the
// validator activity history window so the data lives in memory only and never
// hits the database.
type validatorPtcCache struct {
	indexer     *Indexer
	statsMap    map[phase0.ValidatorIndex]map[phase0.Epoch]*validatorPtcEpochStat
	statsMutex  sync.RWMutex
	oldestEpoch phase0.Epoch
}

type validatorPtcEpochStat struct {
	expected uint16
	included uint16
}

// newValidatorPtcCache creates and returns a new validatorPtcCache instance.
func newValidatorPtcCache(indexer *Indexer) *validatorPtcCache {
	cache := &validatorPtcCache{
		indexer:     indexer,
		statsMap:    make(map[phase0.ValidatorIndex]map[phase0.Epoch]*validatorPtcEpochStat),
		oldestEpoch: math.MaxInt64,
	}

	go cache.cleanupLoop()

	return cache
}

func (cache *validatorPtcCache) getCutOffEpoch() phase0.Epoch {
	chainState := cache.indexer.consensusPool.GetChainState()
	currentEpoch := chainState.CurrentEpoch()
	cutOffEpoch := phase0.Epoch(0)
	if currentEpoch > phase0.Epoch(cache.indexer.activityHistoryLength) {
		cutOffEpoch = currentEpoch - phase0.Epoch(cache.indexer.activityHistoryLength)
	}

	return cutOffEpoch
}

// processBlock records PTC duty/vote stats from a Gloas+ block. Payload
// attestations in a block at slot S vote on the block at slot S-1, so duties
// for slot S-1 are credited per validator.
//
// Returns true if the cache was successfully updated (stats available and
// recorded). The caller is expected to gate the call so a single block is
// processed at most once on the success path.
func (cache *validatorPtcCache) processBlock(block *Block, blockBody *spec.VersionedSignedBeaconBlock) bool {
	if block == nil || blockBody == nil || block.Slot == 0 {
		return false
	}
	if blockBody.Version < spec.DataVersionGloas {
		return true // not applicable, treat as processed so we don't keep retrying
	}

	var payloadAttestations []*gloas.PayloadAttestation
	switch {
	case blockBody.Gloas != nil:
		payloadAttestations = blockBody.Gloas.Message.Body.PayloadAttestations
	case blockBody.Heze != nil:
		payloadAttestations = blockBody.Heze.Message.Body.PayloadAttestations
	default:
		return true
	}

	chainState := cache.indexer.consensusPool.GetChainState()
	specs := chainState.GetSpecs()
	if specs == nil {
		return false
	}

	votedSlot := block.Slot - 1
	votedEpoch := chainState.EpochOfSlot(votedSlot)

	cutOffEpoch := cache.getCutOffEpoch()
	if votedEpoch < cutOffEpoch {
		return true
	}

	epochStats := cache.indexer.GetEpochStats(votedEpoch, nil)
	if epochStats == nil {
		return false
	}
	values := epochStats.GetValues(true)
	if values == nil || len(values.PtcDuties) == 0 || len(values.ActiveIndices) == 0 {
		return false
	}

	slotInEpoch := uint64(votedSlot) % specs.SlotsPerEpoch
	if int(slotInEpoch) >= len(values.PtcDuties) {
		return true
	}
	slotDuties := values.PtcDuties[slotInEpoch]
	if len(slotDuties) == 0 {
		return true
	}

	// Determine voted positions across all aggregates. AggregationBits is a fixed
	// PTC_SIZE bitvector, but only positions < len(slotDuties) are meaningful.
	voted := make([]bool, len(slotDuties))
	for _, pa := range payloadAttestations {
		if pa == nil {
			continue
		}
		bitCount := len(pa.AggregationBits) * 8
		if bitCount > len(slotDuties) {
			bitCount = len(slotDuties)
		}
		for i := 0; i < bitCount; i++ {
			if (pa.AggregationBits[i/8]>>(i%8))&1 == 1 {
				voted[i] = true
			}
		}
	}

	// On small validator sets the same validator may occupy multiple PTC positions
	// via balance-weighted selection. Mirror the slot page semantics: one expected
	// vote per unique validator, and one inclusion if any of their positions voted.
	expected := make(map[phase0.ValidatorIndex]bool, len(slotDuties))
	included := make(map[phase0.ValidatorIndex]bool, len(slotDuties))
	for pos, activeIdx := range slotDuties {
		if int(activeIdx) >= len(values.ActiveIndices) {
			continue
		}
		vidx := values.ActiveIndices[activeIdx]
		expected[vidx] = true
		if voted[pos] {
			included[vidx] = true
		}
	}

	cache.statsMutex.Lock()
	defer cache.statsMutex.Unlock()

	if votedEpoch < cache.oldestEpoch {
		cache.oldestEpoch = votedEpoch
	} else if cache.oldestEpoch < cutOffEpoch+1 {
		cache.oldestEpoch = cutOffEpoch + 1
	}

	for vidx := range expected {
		epochs := cache.statsMap[vidx]
		if epochs == nil {
			epochs = make(map[phase0.Epoch]*validatorPtcEpochStat)
			cache.statsMap[vidx] = epochs
		}
		stat := epochs[votedEpoch]
		if stat == nil {
			stat = &validatorPtcEpochStat{}
			epochs[votedEpoch] = stat
		}
		stat.expected++
		if included[vidx] {
			stat.included++
		}
	}

	return true
}

// getValidatorPtcStats returns expected vs included PTC votes for a validator
// over the last lookbackEpochs epochs, using cached values only.
func (cache *validatorPtcCache) getValidatorPtcStats(validatorIndex phase0.ValidatorIndex, lookbackEpochs phase0.Epoch) (expected uint64, included uint64) {
	chainState := cache.indexer.consensusPool.GetChainState()
	currentEpoch := chainState.CurrentEpoch()

	startEpoch := phase0.Epoch(0)
	if currentEpoch > lookbackEpochs {
		startEpoch = currentEpoch - lookbackEpochs
	}

	cache.statsMutex.RLock()
	defer cache.statsMutex.RUnlock()

	for epoch, stat := range cache.statsMap[validatorIndex] {
		if epoch < startEpoch {
			continue
		}
		expected += uint64(stat.expected)
		included += uint64(stat.included)
	}
	return
}

func (cache *validatorPtcCache) cleanupLoop() {
	defer func() {
		if err := recover(); err != nil {
			cache.indexer.logger.Errorf("uncaught panic in indexer.beacon.validatorPtcCache.cleanupLoop: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go cache.cleanupLoop()
		}
	}()

	for {
		time.Sleep(30 * time.Minute)
		cache.cleanupCache()
	}
}

func (cache *validatorPtcCache) cleanupCache() {
	cutOffEpoch := cache.getCutOffEpoch()

	cache.statsMutex.Lock()
	defer cache.statsMutex.Unlock()

	deleted := 0
	for vidx, epochs := range cache.statsMap {
		for epoch := range epochs {
			if epoch < cutOffEpoch {
				delete(epochs, epoch)
				deleted++
			}
		}
		if len(epochs) == 0 {
			delete(cache.statsMap, vidx)
		}
	}

	if cache.oldestEpoch < cutOffEpoch {
		cache.oldestEpoch = cutOffEpoch
	}

	cache.indexer.logger.Infof("cleaned up ptc cache, deleted %d entries", deleted)
}
