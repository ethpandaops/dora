package beacon

import (
	"bytes"
	"encoding/binary"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/dbtypes"
)

// epochStatsKey is the primary key for EpochStats entries in cache.
// consists of dependendRoot (32 byte) and epoch (8 byte).
type epochStatsKey [32 + 8]byte

// generate epochStatsKey from epoch and dependentRoot
func getEpochStatsKey(epoch phase0.Epoch, dependentRoot phase0.Root) epochStatsKey {
	var key epochStatsKey

	copy(key[0:], dependentRoot[:])
	binary.LittleEndian.PutUint64(key[32:], uint64(epoch))

	return key
}

// epochCache is the cache for EpochStats (epoch status) and epochState (beacon state) structures.
type epochCache struct {
	indexer     *Indexer
	cacheMutex  sync.RWMutex                                // mutex to protect statsMap & stateMap for concurrent read/write
	statsMap    map[epochStatsKey]*EpochStats               // epoch status cache by epochStatsKey
	stateMap    map[phase0.Root]*epochState                 // beacon state cache by dependentRoot
	loadingChan chan bool                                   // limits concurrent state calls by channel capacity
	valsetMutex sync.Mutex                                  // mutex to protect valsetCache for concurrent access
	valsetCache map[phase0.ValidatorIndex]*phase0.Validator // global validator set cache for reuse of matching validator entries
	syncMutex   sync.Mutex                                  // mutex to protect syncCache for concurrent access
	syncCache   []phase0.ValidatorIndex                     // global sync committee cache for reuse if matching
}

// newEpochCache creates & returns a new instance of epochCache.
// initializes the cache & starts the beacon state loader subroutine.
func newEpochCache(indexer *Indexer) *epochCache {
	cache := &epochCache{
		indexer:     indexer,
		statsMap:    map[epochStatsKey]*EpochStats{},
		stateMap:    map[phase0.Root]*epochState{},
		loadingChan: make(chan bool, indexer.maxParallelStateCalls),
		valsetCache: map[phase0.ValidatorIndex]*phase0.Validator{},
	}

	// start beacon state loader subroutine
	go cache.startLoaderLoop()

	return cache
}

// createOrGetEpochStats gets an existing EpochStats entry for the given epoch and dependentRoot or creates a new instance if not found.
func (cache *epochCache) createOrGetEpochStats(epoch phase0.Epoch, dependentRoot phase0.Root) (*EpochStats, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	statsKey := getEpochStatsKey(epoch, dependentRoot)

	if cache.statsMap[statsKey] != nil {
		// found in cache
		return cache.statsMap[statsKey], false
	}

	cache.indexer.logger.Infof("created epoch stats for epoch %v (%v)", epoch, dependentRoot.String())

	// get or create beacon state which the epoch status depends on (dependentRoot beacon state)
	epochState := cache.stateMap[dependentRoot]
	if epochState == nil {
		epochState = newEpochState(dependentRoot)
		cache.stateMap[dependentRoot] = epochState
	}

	epochStats := newEpochStats(epoch, dependentRoot, epochState)
	cache.statsMap[statsKey] = epochStats

	if epochState.loadingStatus == 2 {
		// dependent state is already loaded, process it
		epochStats.processState(cache.indexer)
	}

	return epochStats, true
}

func (cache *epochCache) restoreEpochStats(dbDuty *dbtypes.UnfinalizedDuty) (*EpochStats, error) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	statsKey := getEpochStatsKey(phase0.Epoch(dbDuty.Epoch), phase0.Root(dbDuty.DependentRoot))

	epochStats := cache.statsMap[statsKey]
	isNew := false
	if epochStats == nil {
		epochStats = newEpochStats(phase0.Epoch(dbDuty.Epoch), phase0.Root(dbDuty.DependentRoot), nil)
		isNew = true
	}

	if err := epochStats.unmarshalSSZ(cache.indexer.dynSsz, dbDuty.DutiesSSZ); err != nil {
		return nil, err
	}

	if isNew {
		cache.statsMap[statsKey] = epochStats
	}

	return epochStats, nil
}

func (cache *epochCache) getEpochStats(epoch phase0.Epoch, dependentRoot phase0.Root) *EpochStats {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	statsKey := getEpochStatsKey(epoch, dependentRoot)

	return cache.statsMap[statsKey]
}

// getPendingEpochStats gets all EpochStats with unloaded epochStates.
func (cache *epochCache) getPendingEpochStats() []*EpochStats {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	pendingStats := make([]*EpochStats, 0)
	for _, stats := range cache.statsMap {
		if stats.dependentState != nil && stats.dependentState.loadingStatus == 0 {
			pendingStats = append(pendingStats, stats)
		}
	}

	return pendingStats
}

func (cache *epochCache) getEpochStatsByEpoch(epoch phase0.Epoch) []*EpochStats {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	statsList := []*EpochStats{}
	for _, stats := range cache.statsMap {
		if stats.epoch == epoch {
			statsList = append(statsList, stats)
		}
	}

	return statsList
}

// removeEpochStats removes an EpochStats struct from cache.
// stops loading state call if not referenced by another epoch status.
func (cache *epochCache) removeEpochStats(epochStats *EpochStats) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	statsKey := getEpochStatsKey(epochStats.epoch, epochStats.dependentRoot)

	if cache.statsMap[statsKey] == nil {
		return
	}

	delete(cache.statsMap, statsKey)

	if epochStats.dependentState != nil {
		foundOtherStats := false
		for _, stats := range cache.statsMap {
			if bytes.Equal(stats.dependentRoot[:], epochStats.dependentRoot[:]) {
				foundOtherStats = true
				break
			}
		}

		if !foundOtherStats {
			// no other epoch status depends on this beacon state
			epochStats.dependentState.dispose()
			delete(cache.stateMap, epochStats.dependentRoot)
		}
	}
}

func (cache *epochCache) removeEpochStatsByEpoch(epoch phase0.Epoch) {
	for _, stats := range cache.getEpochStatsByEpoch(epoch) {
		cache.removeEpochStats(stats)
	}
}

// getOrCreateValidator replaces the supplied validator with an older Validator object from cache if all properties match.
// heavily reduces memory consumption as validator objects are not duplicated for each validator set request.
func (cache *epochCache) getOrCreateValidator(index phase0.ValidatorIndex, validator *phase0.Validator) *phase0.Validator {
	cache.valsetMutex.Lock()
	defer cache.valsetMutex.Unlock()

	existingValidator := cache.valsetCache[index]
	if existingValidator != nil &&
		bytes.Equal(existingValidator.WithdrawalCredentials[:], validator.WithdrawalCredentials[:]) &&
		existingValidator.EffectiveBalance == validator.EffectiveBalance &&
		existingValidator.Slashed == validator.Slashed &&
		existingValidator.ActivationEligibilityEpoch == validator.ActivationEligibilityEpoch &&
		existingValidator.ActivationEpoch == validator.ActivationEpoch &&
		existingValidator.ExitEpoch == validator.ExitEpoch &&
		existingValidator.WithdrawableEpoch == validator.WithdrawableEpoch {
		// all properties match, return reference to old cached entry
		return existingValidator
	}

	cache.valsetCache[index] = validator
	return validator
}

// getOrUpdateSyncCommittee replaces the supplied sync committee with an older sync committee from cache if all properties match.
// heavily reduces memory consumption as sync committee objects are not duplicated for each sync committee request.
func (cache *epochCache) getOrUpdateSyncCommittee(syncCommittee []phase0.ValidatorIndex) []phase0.ValidatorIndex {
	cache.syncMutex.Lock()
	defer cache.syncMutex.Unlock()

	isEqual := false
	if len(syncCommittee) == len(cache.syncCache) {
		isEqual = true
		for i, index := range syncCommittee {
			if cache.syncCache[i] != index {
				isEqual = false
				break
			}
		}
	}

	if isEqual {
		// all properties match, return reference to old cached entry
		return cache.syncCache
	}

	cache.syncCache = syncCommittee
	return syncCommittee
}

// startLoaderLoop is the entrypoint for the beacon state loader subroutine.
// contains the main loop & crash handler of the subroutine.
func (cache *epochCache) startLoaderLoop() {
	defer func() {
		if err := recover(); err != nil {
			cache.indexer.logger.WithError(err.(error)).Errorf("uncaught panic in indexer.beacon.epochCache.startLoaderLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go cache.startLoaderLoop()
		}
	}()

	for {
		cache.runLoaderLoop()
		time.Sleep(2 * time.Second)
	}
}

// runLoaderLoop checks the cache for unloaded epoch states.
// loads the next unloaded state in a subroutine if needed.
// blocks if too many loader subroutines are already running.
func (cache *epochCache) runLoaderLoop() {
	// load next epoch stats
	pendingStats := cache.getPendingEpochStats()
	if len(pendingStats) == 0 {
		return
	}

	// sort by loading priority
	// 1. retry count (prefer lower)
	// 2. requested by clients count (prefer higher)
	// 3. epoch number (prefer higher)
	sort.Slice(pendingStats, func(a, b int) bool {
		if pendingStats[a].dependentState.retryCount != pendingStats[b].dependentState.retryCount {
			return pendingStats[a].dependentState.retryCount < pendingStats[b].dependentState.retryCount
		}

		reqCountA := len(pendingStats[a].requestedBy)
		reqCountB := len(pendingStats[b].requestedBy)
		if reqCountA != reqCountB {
			return reqCountA > reqCountB
		}

		return pendingStats[a].epoch > pendingStats[b].epoch
	})

	if cache.indexer.maxParallelStateCalls > 0 {
		cache.loadingChan <- true
	}

	go cache.loadEpochStats(pendingStats[0])
}

// loadEpochStats loads the supplied unloaded epoch status (the dependent epoch state).
// retires loading from multiple clients, ordered by priority.
func (cache *epochCache) loadEpochStats(epochStats *EpochStats) {
	defer func() {
		if cache.indexer.maxParallelStateCalls > 0 {
			<-cache.loadingChan
		}
	}()

	clients := []*Client{}
	for _, client := range cache.indexer.GetReadyClientsByBlockRoot(epochStats.dependentRoot) {
		if client.skipValidators {
			continue
		}

		if client.client.GetStatus() != consensus.ClientStatusOnline && client.client.GetStatus() != consensus.ClientStatusOptimistic {
			continue
		}

		if !cache.indexer.blockCache.isCanonicalBlock(epochStats.dependentRoot, client.headRoot) {
			continue
		}

		clients = append(clients, client)
	}

	if len(clients) == 0 {
		for _, client := range epochStats.getRequestedBy() {
			if client.skipValidators {
				continue
			}

			if client.client.GetStatus() != consensus.ClientStatusOnline && client.client.GetStatus() != consensus.ClientStatusOptimistic {
				continue
			}

			clients = append(clients, client)
		}
	}

	sort.Slice(clients, func(a, b int) bool {
		cliA := clients[a]
		cliB := clients[b]

		if cliA.archive != cliB.archive {
			if cliA.archive {
				return false
			} else {
				return true
			}
		}

		if cliA.priority != cliB.priority {
			if cliA.priority > cliB.priority {
				return false
			} else {
				return true
			}
		}

		return false
	})

	for _, client := range clients {
		err := epochStats.dependentState.loadState(client, cache)
		if err != nil {
			client.logger.Warnf("failed loading epoch %v stats (dep: %v): %v", epochStats.epoch, epochStats.dependentRoot.String(), err)
		} else {
			break
		}
	}

	if epochStats.dependentState.loadingStatus != 2 {
		// epoch state could not be loaded
		return
	}

	dependentStats := []*EpochStats{}
	cache.cacheMutex.Lock()
	for _, stats := range cache.statsMap {
		if stats.dependentState == epochStats.dependentState {
			dependentStats = append(dependentStats, stats)
		}
	}
	cache.cacheMutex.Unlock()

	for _, stats := range dependentStats {
		stats.processState(cache.indexer)
	}
}
