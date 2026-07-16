package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/config"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
	"gopkg.in/yaml.v3"
)

var logger_vn = logrus.StandardLogger().WithField("module", "validator_names")

// maxValidatorNameRangeSize caps per-index expansion of inventory ranges so a
// malformed range (e.g. "0-18446744073709551615") can't hang the loader.
const maxValidatorNameRangeSize = 10_000_000

type ValidatorNames struct {
	ctx                   context.Context
	beaconIndexer         *beacon.Indexer
	chainState            *consensus.ChainState
	loadingMutex          sync.Mutex
	loading               chan bool
	lastResolvedMapUpdate time.Time
	lastInventoryRefresh  time.Time
	updaterRunning        bool
	namesMutex            sync.RWMutex
	namesByIndex          map[uint64]*validatorNameEntry
	namesByWithdrawal     map[common.Address]*validatorNameEntry
	namesByDepositOrigin  map[common.Address]*validatorNameEntry
	namesByDepositTarget  map[common.Address]*validatorNameEntry
	resolvedNamesByIndex  map[uint64]*validatorNameEntry
	nameHistoryRaw        []validatorNamesHistoryEntry
	nameHistoryBuilt      bool
	nameHistory           []validatorNameSnapshot
	nameSourceOk          bool
	dictMutex             sync.RWMutex
	dictByName            map[string]uint64
	dictById              map[uint64]string
	dictLoaded            bool
	dictSynced            bool
}

type validatorNameEntry struct {
	name string
}

type validatorNameRange struct {
	startIndex uint64
	endIndex   uint64 // inclusive
	name       string
}

// validatorNameSnapshot holds the full range assignment valid for [startSlot, endSlot).
// Snapshots are immutable once published and sorted by startSlot; ranges are disjoint and sorted by startIndex.
type validatorNameSnapshot struct {
	startSlot phase0.Slot
	endSlot   phase0.Slot // exclusive; math.MaxInt64 for the open interval
	ranges    []validatorNameRange
}

func NewValidatorNames(ctx context.Context, beaconIndexer *beacon.Indexer, chainState *consensus.ChainState) *ValidatorNames {
	validatorNames := &ValidatorNames{
		ctx:           ctx,
		beaconIndexer: beaconIndexer,
		chainState:    chainState,
	}
	return validatorNames
}

func (vn *ValidatorNames) StartUpdater() {
	if vn.updaterRunning {
		return
	}
	if utils.Config.Frontend.ValidatorNamesRefreshInterval == 0 {
		utils.Config.Frontend.ValidatorNamesRefreshInterval = 2 * time.Hour
	}
	if utils.Config.Frontend.ValidatorNamesResolveInterval == 0 {
		utils.Config.Frontend.ValidatorNamesResolveInterval = 6 * time.Hour
	}

	vn.updaterRunning = true
	go vn.runUpdaterLoop()
}

func (vn *ValidatorNames) runUpdaterLoop() {
	defer utils.HandleSubroutinePanic("ValidatorNames.runUpdaterLoop", vn.runUpdaterLoop)

	for {
		time.Sleep(30 * time.Second)

		err := vn.runUpdater()
		if err != nil {
			logger_vn.Errorf("validator names update error: %v, retrying in 30 sec...", err)
		}
	}
}

func (vn *ValidatorNames) runUpdater() error {
	needUpdate := false

	if utils.Config.Frontend.ValidatorNamesRefreshInterval > 0 && time.Since(vn.lastInventoryRefresh) > utils.Config.Frontend.ValidatorNamesRefreshInterval {
		logger_vn.Infof("refreshing validator inventory")
		loadingChan := vn.LoadValidatorNames()
		<-loadingChan
		needUpdate = true
	}

	// retried on the 30s updater tick until chain specs & genesis are available
	if err := vn.ensureNameHistory(); err != nil {
		logger_vn.Debugf("validator name history pending: %v", err)
	}

	if !vn.dictLoaded {
		if err := vn.loadNameDict(); err != nil {
			logger_vn.WithError(err).Warnf("could not load validator name dictionary")
		}
	}

	if err := vn.syncNameDict(); err != nil {
		logger_vn.WithError(err).Warnf("name dictionary sync failed")
	}

	if err := vn.reconcileNameHistoryState(); err != nil {
		logger_vn.WithError(err).Warnf("name history reconciliation failed")
	}

	if err := vn.repairSlotNameIds(); err != nil {
		logger_vn.WithError(err).Warnf("slot name id repair failed")
	}

	if time.Since(vn.lastResolvedMapUpdate) > utils.Config.Frontend.ValidatorNamesResolveInterval {
		changes, err := vn.resolveNames()
		if err != nil {
			return err
		}

		vn.lastResolvedMapUpdate = time.Now()
		if changes {
			needUpdate = true
		}
	}

	if needUpdate {
		err := vn.UpdateDb(vn.ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

func (vn *ValidatorNames) getDefaultValidatorNames() string {
	specs := vn.chainState.GetSpecs()
	chainName := ""
	if specs != nil {
		chainName = specs.ConfigName
	}

	// default validator names
	switch chainName {
	case "sepolia":
		return "~internal/sepolia.names.yml"
	case "holesky":
		return "~internal/holesky.names.yml"
	case "hoodi":
		return "~internal/hoodi.names.yml"
	}

	return ""
}

func (vn *ValidatorNames) resolveNames() (bool, error) {
	logger_vn.Debugf("resolve validator names")

	vn.namesMutex.RLock()
	var namesByWithdrawalCopy map[common.Address]*validatorNameEntry
	if vn.namesByWithdrawal != nil {
		namesByWithdrawalCopy = maps.Clone(vn.namesByWithdrawal)
	}
	var namesByDepositOriginCopy map[common.Address]*validatorNameEntry
	if vn.namesByDepositOrigin != nil {
		namesByDepositOriginCopy = maps.Clone(vn.namesByDepositOrigin)
	}
	var namesByDepositTargetCopy map[common.Address]*validatorNameEntry
	if vn.namesByDepositTarget != nil {
		namesByDepositTargetCopy = maps.Clone(vn.namesByDepositTarget)
	}
	var resolvedNamesByIndexCopy map[uint64]*validatorNameEntry
	if vn.resolvedNamesByIndex != nil {
		resolvedNamesByIndexCopy = maps.Clone(vn.resolvedNamesByIndex)
	}
	vn.namesMutex.RUnlock()

	newResolvedNames := map[uint64]*validatorNameEntry{}
	hasUpdates := false
	addResolved := func(index uint64, name *validatorNameEntry) {
		newResolvedNames[index] = name
		if resolvedNamesByIndexCopy[index] != name {
			hasUpdates = true
		}
	}

	canonicalForkIds := GlobalBeaconService.GetCanonicalForkIds()

	// resolve names by withdrawal address
	for wdAddr, name := range namesByWithdrawalCopy {
		if name == nil {
			continue
		}

		validators, _ := GlobalBeaconService.GetFilteredValidatorSet(vn.ctx, &dbtypes.ValidatorFilter{
			WithdrawalAddress: wdAddr[:],
		}, false)
		for _, validator := range validators {
			addResolved(uint64(validator.Index), name)
		}
	}

	// resolve names by depositor address
	for address := range namesByDepositOriginCopy {
		offset := uint64(0)
		pageSize := uint64(5000)

		for {
			deposits, depositCount, _ := db.GetDepositTxsFiltered(vn.ctx, offset, uint32(pageSize), canonicalForkIds, &dbtypes.DepositTxFilter{
				Address: address[:],
			})
			for _, deposit := range deposits {
				validatorIndex, found := vn.beaconIndexer.GetValidatorIndexByPubkey(phase0.BLSPubKey(deposit.PublicKey))
				if found {
					addResolved(uint64(validatorIndex), namesByDepositOriginCopy[address])
				}
			}

			offset += pageSize
			if offset > depositCount {
				break
			}
		}
	}

	// resolve names by deposit target address
	for address := range namesByDepositTargetCopy {
		offset := uint64(0)
		pageSize := uint64(5000)

		for {
			deposits, depositCount, _ := db.GetDepositTxsFiltered(vn.ctx, offset, uint32(pageSize), canonicalForkIds, &dbtypes.DepositTxFilter{
				TargetAddress: address[:],
			})
			for _, deposit := range deposits {
				validatorIndex, found := vn.beaconIndexer.GetValidatorIndexByPubkey(phase0.BLSPubKey(deposit.PublicKey))
				if found {
					addResolved(uint64(validatorIndex), namesByDepositTargetCopy[address])
				}
			}

			offset += pageSize
			if offset > depositCount {
				break
			}
		}
	}

	if !hasUpdates {
		// check for removed names
		for index := range resolvedNamesByIndexCopy {
			if newResolvedNames[index] == nil {
				hasUpdates = true
			}
		}
	}

	if hasUpdates {
		vn.namesMutex.Lock()
		vn.resolvedNamesByIndex = newResolvedNames
		vn.namesMutex.Unlock()

		vn.dictMutex.Lock()
		vn.dictSynced = false
		vn.dictMutex.Unlock()
	}

	return hasUpdates, nil
}

func (vn *ValidatorNames) GetValidatorName(index uint64) string {
	if !vn.namesMutex.TryRLock() {
		return ""
	}
	defer vn.namesMutex.RUnlock()
	if vn.namesByIndex == nil {
		return ""
	}

	name := vn.namesByIndex[index]
	if name != nil {
		return name.name
	}

	if vn.resolvedNamesByIndex == nil {
		return ""
	}

	name = vn.resolvedNamesByIndex[index]
	if name != nil {
		return name.name
	}

	return ""
}

// lookupHistoryName finds the name assignment covering (index, slot) in the snapshots.
func lookupHistoryName(history []validatorNameSnapshot, index uint64, slot phase0.Slot) (string, bool) {
	snapIdx := sort.Search(len(history), func(i int) bool { return history[i].startSlot > slot }) - 1
	if snapIdx >= 0 && slot < history[snapIdx].endSlot {
		ranges := history[snapIdx].ranges
		rangeIdx := sort.Search(len(ranges), func(i int) bool { return ranges[i].startIndex > index }) - 1
		if rangeIdx >= 0 && index <= ranges[rangeIdx].endIndex {
			return ranges[rangeIdx].name, true
		}
	}
	return "", false
}

// GetValidatorNameAt returns the name assignment valid at the given slot.
// Falls back to the current name when no history snapshot covers the slot or index,
// so networks without name history behave identically to GetValidatorName.
func (vn *ValidatorNames) GetValidatorNameAt(index uint64, slot phase0.Slot) string {
	if !vn.namesMutex.TryRLock() {
		return ""
	}
	history := vn.nameHistory
	vn.namesMutex.RUnlock()

	// snapshots are immutable once published, searching outside the lock is safe
	if name, found := lookupHistoryName(history, index, slot); found {
		return name
	}

	return vn.GetValidatorName(index)
}

// resolveNameAt is the blocking variant of GetValidatorNameAt for the stamping path:
// it must never return "" due to lock contention, only when there really is no name.
func (vn *ValidatorNames) resolveNameAt(index uint64, slot phase0.Slot) string {
	vn.namesMutex.RLock()
	defer vn.namesMutex.RUnlock()

	if name, found := lookupHistoryName(vn.nameHistory, index, slot); found {
		return name
	}
	if entry := vn.namesByIndex[index]; entry != nil {
		return entry.name
	}
	if entry := vn.resolvedNamesByIndex[index]; entry != nil {
		return entry.name
	}
	return ""
}

// nameSourceIsReady reports whether name sources are loaded well enough to stamp
// rows authoritatively: the last inventory/yaml load succeeded and the history
// (when served) has been converted to slot-bounded snapshots.
func (vn *ValidatorNames) nameSourceIsReady() bool {
	vn.namesMutex.RLock()
	defer vn.namesMutex.RUnlock()
	if !vn.nameSourceOk {
		return false
	}
	return len(vn.nameHistoryRaw) == 0 || vn.nameHistoryBuilt
}

// stampingActive reports whether name-id stamping is enabled: only networks whose
// inventory serves an assignment history get stamped rows. On yaml-based networks
// names are identity labels that may be renamed retroactively - freezing them into
// rows would be wrong, so those networks keep plain current-name resolution.
func (vn *ValidatorNames) stampingActive() bool {
	vn.namesMutex.RLock()
	defer vn.namesMutex.RUnlock()
	return vn.nameSourceOk && vn.nameHistoryBuilt && len(vn.nameHistory) > 0
}

// GetValidatorNameIdAt resolves the dictionary id of the name valid at the given slot.
// Returns nil while name sources are unavailable (rows get repaired later); 0 means
// "resolved, no name". Cache-only: it is called from within indexer write transactions,
// so it must never open a DB transaction itself (nested write tx deadlocks on sqlite).
// Dictionary inserts happen in syncNameDict on the updater tick; unknown names resolve
// to nil until then and are repaired afterwards.
func (vn *ValidatorNames) GetValidatorNameIdAt(index phase0.ValidatorIndex, slot phase0.Slot) *uint64 {
	if !vn.stampingActive() {
		return nil
	}

	name := vn.resolveNameAt(uint64(index), slot)
	if name == "" {
		zero := uint64(0)
		return &zero
	}

	vn.dictMutex.RLock()
	id, found := vn.dictByName[name]
	vn.dictMutex.RUnlock()
	if !found {
		return nil
	}
	return &id
}

func (vn *ValidatorNames) GetValidatorNameById(id uint64) string {
	vn.dictMutex.RLock()
	defer vn.dictMutex.RUnlock()
	return vn.dictById[id]
}

func (vn *ValidatorNames) loadNameDict() error {
	entries, err := db.GetValidatorNameDictEntries(vn.ctx)
	if err != nil {
		return err
	}

	vn.dictMutex.Lock()
	defer vn.dictMutex.Unlock()
	vn.dictByName = make(map[string]uint64, len(entries))
	vn.dictById = make(map[uint64]string, len(entries))
	for _, entry := range entries {
		vn.dictByName[entry.Name] = entry.Id
		vn.dictById[entry.Id] = entry.Name
	}
	vn.dictLoaded = true
	return nil
}

// syncNameDict inserts dictionary entries for all currently known names. It is the
// only dictionary write path and runs on the updater tick, outside any indexer write
// transaction — the resolver itself is cache-only.
func (vn *ValidatorNames) syncNameDict() error {
	if !vn.stampingActive() {
		return nil
	}

	vn.dictMutex.RLock()
	dictLoaded := vn.dictLoaded
	dictSynced := vn.dictSynced
	vn.dictMutex.RUnlock()
	if !dictLoaded || dictSynced {
		return nil
	}

	names := map[string]bool{}
	vn.namesMutex.RLock()
	for _, entry := range vn.namesByIndex {
		names[entry.name] = true
	}
	for _, entry := range vn.resolvedNamesByIndex {
		names[entry.name] = true
	}
	for _, snapshot := range vn.nameHistory {
		for _, nameRange := range snapshot.ranges {
			names[nameRange.name] = true
		}
	}
	vn.namesMutex.RUnlock()

	for name := range names {
		if _, err := vn.getOrCreateNameId(name); err != nil {
			return err
		}
	}

	vn.dictMutex.Lock()
	vn.dictSynced = true
	vn.dictMutex.Unlock()
	return nil
}

func (vn *ValidatorNames) getOrCreateNameId(name string) (uint64, error) {
	vn.dictMutex.RLock()
	id, found := vn.dictByName[name]
	dictLoaded := vn.dictLoaded
	vn.dictMutex.RUnlock()
	if found {
		return id, nil
	}
	if !dictLoaded {
		return 0, fmt.Errorf("name dictionary not loaded")
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		var err error
		id, err = db.InsertValidatorNameDictEntry(vn.ctx, tx, name)
		return err
	})
	if err != nil {
		return 0, err
	}

	vn.dictMutex.Lock()
	vn.dictByName[name] = id
	vn.dictById[id] = name
	vn.dictMutex.Unlock()
	return id, nil
}

// repairSlotNameIds stamps slot rows that were persisted before name sources were
// available (or before this feature existed). One batch per updater tick.
func (vn *ValidatorNames) repairSlotNameIds() error {
	if !vn.stampingActive() {
		return nil
	}
	vn.dictMutex.RLock()
	dictLoaded := vn.dictLoaded
	vn.dictMutex.RUnlock()
	if !dictLoaded {
		return nil
	}

	stamps := db.GetSlotsWithoutNameId(vn.ctx, 10000)
	if len(stamps) == 0 {
		return nil
	}

	resolved := make([]*dbtypes.SlotNameStamp, 0, len(stamps))
	for _, stamp := range stamps {
		stamp.ProposerNameId = vn.GetValidatorNameIdAt(phase0.ValidatorIndex(stamp.Proposer), phase0.Slot(stamp.Slot))
		if stamp.ProposerNameId != nil {
			resolved = append(resolved, stamp)
		}
	}
	if len(resolved) == 0 {
		return nil
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.UpdateSlotNameIds(vn.ctx, tx, resolved)
	})
	if err != nil {
		return err
	}

	logger_vn.Infof("stamped %v slot rows with proposer name ids", len(resolved))
	return nil
}

// ensureNameHistory converts the raw inventory history into slot-bounded snapshots.
// Returns an error (and stays unbuilt) while chain specs or genesis are unknown or the
// last source load failed — previous snapshots are kept, so a transient inventory
// outage never degrades resolution or churns stamped rows.
func (vn *ValidatorNames) ensureNameHistory() error {
	vn.namesMutex.RLock()
	built := vn.nameHistoryBuilt
	rawHistory := vn.nameHistoryRaw
	sourceOk := vn.nameSourceOk
	vn.namesMutex.RUnlock()

	if built {
		return nil
	}
	if !sourceOk {
		return fmt.Errorf("name sources not loaded")
	}

	var snapshots []validatorNameSnapshot
	if len(rawHistory) > 0 {
		if vn.chainState.GetSpecs() == nil || vn.chainState.GetGenesis() == nil {
			return fmt.Errorf("chain specs not initialized")
		}
		snapshots = buildNameSnapshots(rawHistory, func(ts int64) phase0.Slot {
			return vn.chainState.TimeToSlot(time.Unix(ts, 0))
		})
	}

	vn.namesMutex.Lock()
	defer vn.namesMutex.Unlock()
	vn.nameHistoryBuilt = true
	if !reflect.DeepEqual(vn.nameHistory, snapshots) {
		vn.nameHistory = snapshots
		logger_vn.Infof("built validator name history: %v snapshots", len(snapshots))
	}
	return nil
}

// nameHistoryDiffBoundary returns the first slot from which name resolution differs
// between two snapshot sets, or nil when they resolve identically. endSlot changes are
// ignored: appending a history entry re-bounds the previously open snapshot without
// changing what earlier slots resolve to.
func nameHistoryDiffBoundary(oldSnaps []validatorNameSnapshot, newSnaps []validatorNameSnapshot) *phase0.Slot {
	minLen := len(oldSnaps)
	if len(newSnaps) < minLen {
		minLen = len(newSnaps)
	}
	for i := 0; i < minLen; i++ {
		if oldSnaps[i].startSlot != newSnaps[i].startSlot || !reflect.DeepEqual(oldSnaps[i].ranges, newSnaps[i].ranges) {
			boundary := oldSnaps[i].startSlot
			if newSnaps[i].startSlot < boundary {
				boundary = newSnaps[i].startSlot
			}
			return &boundary
		}
	}
	if len(oldSnaps) > minLen {
		return &oldSnaps[minLen].startSlot
	}
	if len(newSnaps) > minLen {
		return &newSnaps[minLen].startSlot
	}
	return nil
}

type nameHistoryStateRange struct {
	Start uint64 `json:"s"`
	End   uint64 `json:"e"`
	Name  string `json:"n"`
}

type nameHistoryStateSnapshot struct {
	StartSlot uint64                  `json:"start"`
	EndSlot   uint64                  `json:"end"`
	Ranges    []nameHistoryStateRange `json:"ranges"`
}

func nameHistoryToState(snapshots []validatorNameSnapshot) []nameHistoryStateSnapshot {
	state := make([]nameHistoryStateSnapshot, len(snapshots))
	for i, snapshot := range snapshots {
		ranges := make([]nameHistoryStateRange, len(snapshot.ranges))
		for j, nameRange := range snapshot.ranges {
			ranges[j] = nameHistoryStateRange{Start: nameRange.startIndex, End: nameRange.endIndex, Name: nameRange.name}
		}
		state[i] = nameHistoryStateSnapshot{StartSlot: uint64(snapshot.startSlot), EndSlot: uint64(snapshot.endSlot), Ranges: ranges}
	}
	return state
}

func nameHistoryFromState(state []nameHistoryStateSnapshot) []validatorNameSnapshot {
	snapshots := make([]validatorNameSnapshot, len(state))
	for i, stateSnapshot := range state {
		ranges := make([]validatorNameRange, len(stateSnapshot.Ranges))
		for j, stateRange := range stateSnapshot.Ranges {
			ranges[j] = validatorNameRange{startIndex: stateRange.Start, endIndex: stateRange.End, name: stateRange.Name}
		}
		snapshots[i] = validatorNameSnapshot{startSlot: phase0.Slot(stateSnapshot.StartSlot), endSlot: phase0.Slot(stateSnapshot.EndSlot), ranges: ranges}
	}
	return snapshots
}

// reconcileNameHistoryState compares the current snapshots against the persisted
// state from the last run and resets stamped slot rows from the first slot whose
// resolution changed, so the repair pass re-stamps them with the corrected names.
// Covers slots stamped with a stale mapping before the inventory refresh caught up,
// as well as retroactive history amendments — across restarts.
func (vn *ValidatorNames) reconcileNameHistoryState() error {
	if !vn.nameSourceIsReady() {
		return nil
	}

	vn.namesMutex.RLock()
	snapshots := vn.nameHistory
	vn.namesMutex.RUnlock()

	persistedState := []nameHistoryStateSnapshot{}
	if _, err := db.GetExplorerState(vn.ctx, "validator_names.history_state", &persistedState); err != nil && len(snapshots) == 0 {
		return nil // nothing persisted, nothing built: no-history network, keep zero footprint
	}

	boundary := nameHistoryDiffBoundary(nameHistoryFromState(persistedState), snapshots)
	if boundary == nil {
		return nil
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		resetCount, err := db.ResetSlotNameIdsFromSlot(vn.ctx, tx, uint64(*boundary))
		if err != nil {
			return err
		}
		if err := db.SetExplorerState(vn.ctx, tx, "validator_names.history_state", nameHistoryToState(snapshots)); err != nil {
			return err
		}
		logger_vn.Infof("validator name history changed at slot %v, reset %v slot stamps for re-resolution", *boundary, resetCount)
		return nil
	})
}

func buildNameSnapshots(entries []validatorNamesHistoryEntry, timeToSlot func(int64) phase0.Slot) []validatorNameSnapshot {
	sorted := make([]validatorNamesHistoryEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(a, b int) bool {
		return sorted[a].EffectiveFrom < sorted[b].EffectiveFrom
	})

	var snapshots []validatorNameSnapshot
	for idx, entry := range sorted {
		startSlot := timeToSlot(entry.EffectiveFrom)
		endSlot := phase0.Slot(math.MaxInt64)
		if idx < len(sorted)-1 {
			endSlot = timeToSlot(sorted[idx+1].EffectiveFrom)
		}
		if startSlot >= endSlot {
			continue // zero-length interval, fully shadowed by the next snapshot
		}
		snapshots = append(snapshots, validatorNameSnapshot{
			startSlot: startSlot,
			endSlot:   endSlot,
			ranges:    parseValidatorNameRanges(entry.Ranges),
		})
	}
	return snapshots
}

func parseValidatorNameRanges(ranges map[string]string) []validatorNameRange {
	result := make([]validatorNameRange, 0, len(ranges))
	for key, name := range ranges {
		if strings.Contains(key, ":") {
			logger_vn.Warnf("unsupported key in validator name history: %v", key)
			continue
		}
		rangeParts := strings.Split(key, "-")
		minIdx, err := strconv.ParseUint(rangeParts[0], 10, 64)
		if err != nil {
			logger_vn.Warnf("invalid range key in validator name history: %v", key)
			continue
		}
		maxIdx := minIdx
		if len(rangeParts) > 1 {
			maxIdx, err = strconv.ParseUint(rangeParts[1], 10, 64)
			if err != nil {
				logger_vn.Warnf("invalid range key in validator name history: %v", key)
				continue
			}
		}
		if maxIdx < minIdx || maxIdx-minIdx >= maxValidatorNameRangeSize {
			logger_vn.Warnf("invalid range bounds in validator name history: %v", key)
			continue
		}
		result = append(result, validatorNameRange{startIndex: minIdx, endIndex: maxIdx, name: name})
	}
	sort.Slice(result, func(a, b int) bool { return result[a].startIndex < result[b].startIndex })

	// disjoint ranges are required for unambiguous lookups and duplicate-free SQL joins
	deduped := result[:0]
	for _, nameRange := range result {
		if len(deduped) > 0 && nameRange.startIndex <= deduped[len(deduped)-1].endIndex {
			logger_vn.Warnf("overlapping range in validator name history: %v-%v (%v)", nameRange.startIndex, nameRange.endIndex, nameRange.name)
			continue
		}
		deduped = append(deduped, nameRange)
	}
	return deduped
}

func (vn *ValidatorNames) GetValidatorNameByPubkey(pubkey []byte) string {
	validatorIndex, found := vn.beaconIndexer.GetValidatorIndexByPubkey(phase0.BLSPubKey(pubkey))
	if !found {
		return ""
	}

	return vn.GetValidatorName(uint64(validatorIndex))
}

func (vn *ValidatorNames) GetValidatorNamesCount() uint64 {
	if !vn.namesMutex.TryRLock() {
		return 0
	}
	defer vn.namesMutex.RUnlock()
	if vn.namesByIndex == nil {
		return 0
	}
	return uint64(len(maps.Keys(vn.namesByIndex)) + len(maps.Keys(vn.namesByWithdrawal)))
}

func (vn *ValidatorNames) LoadValidatorNames() chan bool {
	vn.loadingMutex.Lock()
	defer vn.loadingMutex.Unlock()
	if vn.loading != nil {
		return vn.loading
	}
	vn.loading = make(chan bool)

	go func() {
		defer func() {
			vn.loadingMutex.Lock()
			defer vn.loadingMutex.Unlock()
			close(vn.loading)
			vn.loading = nil
			vn.lastInventoryRefresh = time.Now()
		}()

		vn.namesMutex.Lock()
		vn.namesByIndex = make(map[uint64]*validatorNameEntry)
		vn.namesByWithdrawal = make(map[common.Address]*validatorNameEntry)
		vn.namesByDepositOrigin = make(map[common.Address]*validatorNameEntry)
		vn.namesByDepositTarget = make(map[common.Address]*validatorNameEntry)
		vn.nameHistoryRaw = nil
		vn.nameHistoryBuilt = false
		vn.nameSourceOk = false // stamping pauses during reload; affected rows are repaired later
		vn.namesMutex.Unlock()

		loadOk := true

		if utils.Config.Frontend.ValidatorNamesInventory != "" {
			err := vn.loadFromRangesApi(utils.Config.Frontend.ValidatorNamesInventory)
			if err != nil {
				logger_vn.WithError(err).Errorf("error while loading validator names inventory")
				loadOk = false
			}
		}

		validatorNamesYaml := utils.Config.Frontend.ValidatorNamesYaml
		if validatorNamesYaml == "" {
			validatorNamesYaml = vn.getDefaultValidatorNames()
		}

		if strings.HasPrefix(validatorNamesYaml, "~internal/") {
			err := vn.loadFromInternalYaml(validatorNamesYaml[10:])
			if err != nil {
				logger_vn.WithError(err).Errorf("error while loading validator names from internal yaml")
				loadOk = false
			}
		} else if validatorNamesYaml != "" {
			err := vn.loadFromYaml(validatorNamesYaml)
			if err != nil {
				logger_vn.WithError(err).Errorf("error while loading validator names from yaml")
				loadOk = false
			}
		}

		vn.namesMutex.Lock()
		vn.nameSourceOk = loadOk
		vn.namesMutex.Unlock()

		vn.dictMutex.Lock()
		vn.dictSynced = false
		vn.dictMutex.Unlock()
	}()

	return vn.loading
}

func (vn *ValidatorNames) loadFromYaml(fileName string) error {
	f, err := os.Open(fileName)
	if err != nil {
		return fmt.Errorf("error opening validator names file %v: %v", fileName, err)
	}
	defer f.Close()

	namesYaml := map[string]string{}
	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&namesYaml)
	if err != nil {
		return fmt.Errorf("error decoding validator names file %v: %v", fileName, err)
	}

	nameCount := vn.parseNamesMap(namesYaml)
	logger_vn.Infof("loaded %v validator names from yaml (%v)", nameCount, fileName)

	return nil
}

func (vn *ValidatorNames) loadFromInternalYaml(fileName string) error {
	f, err := config.ValidatorNamesYml.Open(fileName)
	if err != nil {
		return fmt.Errorf("could not find internal validator names file %v: %v", fileName, err)
	}

	namesYaml := map[string]string{}
	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&namesYaml)
	if err != nil {
		return fmt.Errorf("could not find internal validator names file %v: %v", fileName, err)
	}

	nameCount := vn.parseNamesMap(namesYaml)
	logger_vn.Infof("loaded %v validator names from internal yaml (%v)", nameCount, fileName)

	return nil
}

func (vn *ValidatorNames) parseNamesMap(names map[string]string) int {
	vn.namesMutex.Lock()
	defer vn.namesMutex.Unlock()
	nameCount := 0
	for idxStr, name := range names {
		rangeParts := strings.Split(idxStr, ":")
		nameEntry := &validatorNameEntry{
			name: name,
		}

		if len(rangeParts) > 1 {
			switch rangeParts[0] {
			case "withdrawal":
				withdrawal := common.HexToAddress(rangeParts[1])
				vn.namesByWithdrawal[withdrawal] = nameEntry
				nameCount++
			case "depositor", "deposit_origin":
				depositor := common.HexToAddress(rangeParts[1])
				vn.namesByDepositOrigin[depositor] = nameEntry
				nameCount++
			case "deposit_target":
				target := common.HexToAddress(rangeParts[1])
				vn.namesByDepositTarget[target] = nameEntry
				nameCount++
			}

		} else {
			rangeParts = strings.Split(idxStr, "-")
			minIdx, err := strconv.ParseUint(rangeParts[0], 10, 64)
			if err != nil {
				continue
			}
			maxIdx := minIdx
			if len(rangeParts) > 1 {
				maxIdx, err = strconv.ParseUint(rangeParts[1], 10, 64)
				if err != nil {
					continue
				}
			}
			if maxIdx < minIdx || maxIdx-minIdx >= maxValidatorNameRangeSize {
				logger_vn.Warnf("invalid validator name range bounds: %v", idxStr)
				continue
			}
			for idx := minIdx; idx <= maxIdx; idx++ {
				vn.namesByIndex[idx] = nameEntry
				nameCount++
			}
		}
	}
	return nameCount
}

type validatorNamesRangesResponse struct {
	Ranges  map[string]string            `json:"ranges"`
	History []validatorNamesHistoryEntry `json:"history"`
}

// validatorNamesHistoryEntry is a full range snapshot valid from EffectiveFrom (unix seconds)
// until the next entry. EffectiveFrom values at or before genesis mean "since genesis".
type validatorNamesHistoryEntry struct {
	EffectiveFrom int64             `json:"effective_from"`
	Ranges        map[string]string `json:"ranges"`
}

func (vn *ValidatorNames) loadFromRangesApi(apiUrl string) error {
	logger_vn.Debugf("Loading validator names from inventory: %v", apiUrl)

	client := &http.Client{Timeout: time.Second * 120}
	resp, err := client.Get(apiUrl)
	if err != nil {
		return fmt.Errorf("could not fetch inventory (%v): %v", utils.GetRedactedUrl(apiUrl), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			logger_vn.Errorf("could not fetch inventory (%v): not found", utils.GetRedactedUrl(apiUrl))
			return nil
		}
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("url: %v, error-response: %s", utils.GetRedactedUrl(apiUrl), data)
	}
	rangesResponse := &validatorNamesRangesResponse{}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&rangesResponse)
	if err != nil {
		return fmt.Errorf("error parsing validator ranges response: %v", err)
	}

	nameCount := vn.parseNamesMap(rangesResponse.Ranges)
	logger_vn.Infof("loaded %v validator names from inventory api (%v)", nameCount, utils.GetRedactedUrl(apiUrl))

	vn.namesMutex.Lock()
	vn.nameHistoryRaw = rangesResponse.History
	vn.namesMutex.Unlock()
	if len(rangesResponse.History) > 0 {
		logger_vn.Infof("loaded %v validator name history snapshots from inventory api", len(rangesResponse.History))
	}

	return nil
}

func (vn *ValidatorNames) UpdateDb(ctx context.Context) error {
	vn.namesMutex.RLock()
	nameRows := make([]*dbtypes.ValidatorName, 0)
	hasName := map[uint64]bool{}
	for index, name := range vn.namesByIndex {
		hasName[index] = true
		nameRows = append(nameRows, &dbtypes.ValidatorName{
			Index: index,
			Name:  name.name,
		})
	}
	for index, name := range vn.resolvedNamesByIndex {
		if hasName[index] {
			continue
		}
		nameRows = append(nameRows, &dbtypes.ValidatorName{
			Index: index,
			Name:  name.name,
		})
	}
	vn.namesMutex.RUnlock()

	sort.Slice(nameRows, func(a, b int) bool {
		return nameRows[a].Index < nameRows[b].Index
	})

	batchSize := 10000

	lastIndex := uint64(0)
	nameIdx := 0
	nameLen := len(nameRows)
	for nameIdx < nameLen {
		maxIdx := nameIdx + batchSize
		if maxIdx >= nameLen {
			maxIdx = nameLen
		}
		sliceLen := maxIdx - nameIdx
		namesSlice := nameRows[nameIdx:maxIdx]
		maxIndex := namesSlice[sliceLen-1].Index

		// get existing db entries
		dbNamesMap := map[uint64]string{}
		for _, dbName := range db.GetValidatorNames(ctx, lastIndex, maxIndex) {
			dbNamesMap[dbName.Index] = dbName.Name
		}

		// get diffs
		updateNames := make([]*dbtypes.ValidatorName, 0)
		for _, nameRow := range namesSlice {
			dbName := dbNamesMap[nameRow.Index]
			delete(dbNamesMap, nameRow.Index)
			if dbName == nameRow.Name {
				continue // no update
			}
			updateNames = append(updateNames, nameRow)
		}

		removeIndexes := make([]uint64, 0)
		for index := range dbNamesMap {
			removeIndexes = append(removeIndexes, index)
		}

		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			if len(updateNames) > 0 {
				err := db.InsertValidatorNames(ctx, tx, updateNames)
				if err != nil {
					logger_vn.WithError(err).Errorf("error while adding validator names to db")
				}
			}

			if len(removeIndexes) > 0 {
				err := db.DeleteValidatorNames(ctx, tx, removeIndexes)
				if err != nil {
					logger_vn.WithError(err).Errorf("error while deleting validator names from db")
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		if len(updateNames) > 0 || len(removeIndexes) > 0 {
			logger_vn.Infof("update validator names %v-%v: %v changed, %v removed", lastIndex, maxIndex, len(updateNames), len(removeIndexes))
			time.Sleep(2 * time.Second)
		} else {
			time.Sleep(100 * time.Millisecond)
		}

		lastIndex = maxIndex + 1
		nameIdx = maxIdx
	}

	return nil
}
