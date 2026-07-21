package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"sync/atomic"
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
	nameHistorySynced     bool
	nameSourceOk          bool

	// nameHistory holds the published slot-bounded snapshots. Snapshots are immutable
	// once published, so lookups load the pointer and search without any locking.
	nameHistory atomic.Pointer[[]validatorNameSnapshot]
}

type validatorNameEntry struct {
	name string
}

type validatorNameRange struct {
	startIndex uint64
	endIndex   uint64 // inclusive
	name       string
}

// validatorNameSnapshot holds the full range assignment valid for [startSlot, endSlot)
// resp. [startTime, endTime). Snapshots are immutable once published and sorted by
// startSlot; ranges are disjoint and sorted by startIndex.
type validatorNameSnapshot struct {
	startSlot  phase0.Slot
	endSlot    phase0.Slot // exclusive; math.MaxInt64 for the open interval
	startTime  int64       // unix seconds
	endTime    int64       // exclusive; math.MaxInt64 for the open interval
	rangesHash string      // fingerprint of ranges for cheap db sync diffing
	ranges     []validatorNameRange
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

	if err := vn.syncNameHistoryDb(); err != nil {
		logger_vn.WithError(err).Warnf("validator name history db sync failed")
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

// lookupHistoryNameByTime finds the name assignment covering (index, unix time) in the
// snapshots. Snapshots are sorted by startSlot, which is equivalent to startTime order.
func lookupHistoryNameByTime(history []validatorNameSnapshot, index uint64, ts int64) (string, bool) {
	snapIdx := sort.Search(len(history), func(i int) bool { return history[i].startTime > ts }) - 1
	if snapIdx >= 0 && ts < history[snapIdx].endTime {
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
	if history := vn.nameHistory.Load(); history != nil {
		if name, found := lookupHistoryName(*history, index, slot); found {
			return name
		}
	}

	return vn.GetValidatorName(index)
}

// GetValidatorNameAtTime returns the name assignment valid at the given unix time.
// Used for rows that only carry an execution layer block time instead of a slot.
func (vn *ValidatorNames) GetValidatorNameAtTime(index uint64, ts int64) string {
	if history := vn.nameHistory.Load(); history != nil {
		if name, found := lookupHistoryNameByTime(*history, index, ts); found {
			return name
		}
	}

	return vn.GetValidatorName(index)
}

// ensureNameHistory converts the raw inventory history into slot-bounded snapshots.
// Returns an error (and stays unbuilt) while chain specs or genesis are unknown or the
// last source load failed — previous snapshots are kept, so a transient inventory
// outage never degrades resolution.
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
	current := vn.nameHistory.Load()
	if current == nil || !reflect.DeepEqual(*current, snapshots) {
		vn.nameHistory.Store(&snapshots)
		vn.nameHistorySynced = false
		if len(snapshots) > 0 {
			logger_vn.Infof("built validator name history: %v snapshots", len(snapshots))
		}
	}
	return nil
}

// syncNameHistoryDb mirrors the published snapshots into the validator_name_snapshots
// and validator_name_ranges tables, which back the SQL-side name filters. The mapping
// itself is persisted instead of denormalized per-row name ids, so a history change
// (including retroactive amendments) is applied consistently with one small atomic
// rewrite of the affected snapshots — no per-row repair is ever needed.
func (vn *ValidatorNames) syncNameHistoryDb() error {
	vn.namesMutex.RLock()
	built := vn.nameHistoryBuilt
	synced := vn.nameHistorySynced
	vn.namesMutex.RUnlock()
	if !built || synced {
		return nil
	}

	var snapshots []validatorNameSnapshot
	if historyPtr := vn.nameHistory.Load(); historyPtr != nil {
		snapshots = *historyPtr
	}

	dbSnapshots, err := db.GetValidatorNameSnapshots(vn.ctx)
	if err != nil {
		return fmt.Errorf("could not load persisted name snapshots: %w", err)
	}
	staleDbSlots := make(map[uint64]*dbtypes.ValidatorNameSnapshot, len(dbSnapshots))
	for _, dbSnapshot := range dbSnapshots {
		staleDbSlots[dbSnapshot.StartSlot] = dbSnapshot
	}

	changedCount := 0
	err = db.RunDBTransaction(func(tx *sqlx.Tx) error {
		for i := range snapshots {
			snapshot := &snapshots[i]
			meta := &dbtypes.ValidatorNameSnapshot{
				StartSlot:  uint64(snapshot.startSlot),
				EndSlot:    uint64(snapshot.endSlot),
				StartTime:  clampToUnsigned(snapshot.startTime),
				EndTime:    clampToUnsigned(snapshot.endTime),
				RangesHash: snapshot.rangesHash,
			}

			existing := staleDbSlots[meta.StartSlot]
			delete(staleDbSlots, meta.StartSlot)
			if existing != nil && *existing == *meta {
				continue
			}

			rangeRows := make([]*dbtypes.ValidatorNameRange, len(snapshot.ranges))
			for j, nameRange := range snapshot.ranges {
				rangeRows[j] = &dbtypes.ValidatorNameRange{
					SnapshotSlot: meta.StartSlot,
					StartIndex:   nameRange.startIndex,
					EndIndex:     nameRange.endIndex,
					Name:         nameRange.name,
				}
			}
			if err := db.UpsertValidatorNameSnapshot(vn.ctx, tx, meta, rangeRows); err != nil {
				return err
			}
			changedCount++
		}

		for startSlot := range staleDbSlots {
			if err := db.DeleteValidatorNameSnapshot(vn.ctx, tx, startSlot); err != nil {
				return err
			}
			changedCount++
		}
		return nil
	})
	if err != nil {
		return err
	}

	vn.namesMutex.Lock()
	vn.nameHistorySynced = true
	vn.namesMutex.Unlock()

	if changedCount > 0 {
		logger_vn.Infof("synced validator name history to db: %v snapshots written", changedCount)
	}
	return nil
}

func clampToUnsigned(value int64) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value)
}

func hashNameRanges(ranges []validatorNameRange) string {
	hasher := sha256.New()
	for _, nameRange := range ranges {
		fmt.Fprintf(hasher, "%d-%d:%s\n", nameRange.startIndex, nameRange.endIndex, nameRange.name)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func buildNameSnapshots(entries []validatorNamesHistoryEntry, timeToSlot func(int64) phase0.Slot) []validatorNameSnapshot {
	sorted := make([]validatorNamesHistoryEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(a, b int) bool {
		return sorted[a].EffectiveFrom < sorted[b].EffectiveFrom
	})

	// drop entries fully shadowed by the next entry within the same slot, so the kept
	// entries produce non-empty slot windows and consistent time windows
	kept := make([]validatorNamesHistoryEntry, 0, len(sorted))
	for idx, entry := range sorted {
		if idx < len(sorted)-1 && timeToSlot(entry.EffectiveFrom) >= timeToSlot(sorted[idx+1].EffectiveFrom) {
			continue
		}
		kept = append(kept, entry)
	}

	snapshots := make([]validatorNameSnapshot, 0, len(kept))
	for idx, entry := range kept {
		startSlot := timeToSlot(entry.EffectiveFrom)
		endSlot := phase0.Slot(math.MaxInt64)
		endTime := int64(math.MaxInt64)
		if idx < len(kept)-1 {
			endSlot = timeToSlot(kept[idx+1].EffectiveFrom)
			endTime = kept[idx+1].EffectiveFrom
		}
		ranges := parseValidatorNameRanges(entry.Ranges)
		snapshots = append(snapshots, validatorNameSnapshot{
			startSlot:  startSlot,
			endSlot:    endSlot,
			startTime:  entry.EffectiveFrom,
			endTime:    endTime,
			rangesHash: hashNameRanges(ranges),
			ranges:     ranges,
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
		vn.nameSourceOk = false // published snapshots stay live during reload
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
