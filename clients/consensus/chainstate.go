package consensus

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/ethwallclock"
)

type ChainState struct {
	specMutex   sync.RWMutex
	specs       *ChainSpec
	clientSpecs map[*Client]map[string]interface{}

	genesisMutex sync.Mutex
	genesis      *v1.Genesis

	wallclockMutex sync.Mutex
	wallclock      *ethwallclock.EthereumBeaconChain

	finalityMutex sync.RWMutex
	finality      *v1.Finality

	checkpointDispatcher     utils.Dispatcher[*v1.Finality]
	wallclockEpochDispatcher utils.Dispatcher[*ethwallclock.Epoch]
	wallclockSlotDispatcher  utils.Dispatcher[*ethwallclock.Slot]
}

func newChainState() *ChainState {
	return &ChainState{
		clientSpecs: make(map[*Client]map[string]interface{}),
	}
}

func (cs *ChainState) setGenesis(genesis *v1.Genesis) error {
	cs.genesisMutex.Lock()
	defer cs.genesisMutex.Unlock()

	if cs.genesis != nil {
		if cs.genesis.GenesisTime != genesis.GenesisTime {
			return fmt.Errorf("genesis mismatch: GenesisTime")
		}

		if !bytes.Equal(cs.genesis.GenesisValidatorsRoot[:], genesis.GenesisValidatorsRoot[:]) {
			return fmt.Errorf("genesis mismatch: GenesisValidatorsRoot")
		}
	} else {
		cs.genesis = genesis
	}

	return nil
}

func (cs *ChainState) updateClientSpecs(client *Client, specValues map[string]interface{}) error {
	cs.specMutex.Lock()
	defer cs.specMutex.Unlock()

	// Store this client's specs
	cs.clientSpecs[client] = specValues

	// Compute the majority-based global specs
	majoritySpecs, err := cs.computeMajoritySpecs()
	if err != nil {
		return err
	}

	cs.specs = majoritySpecs

	// Update warnings for all clients against the new majority spec
	if majoritySpecs != nil {
		for c, specs := range cs.clientSpecs {
			warnings := cs.checkClientSpecWarnings(majoritySpecs, specs)
			c.specWarnings = warnings
			c.hasBadSpecs = len(warnings) > 0
		}
	}

	return nil
}

// checkClientSpecWarnings compares a client's specs against the majority and returns any warnings.
// Must be called with specMutex held.
func (cs *ChainState) checkClientSpecWarnings(majoritySpecs *ChainSpec, specValues map[string]interface{}) []string {
	warnings := []string{}

	clientSpec := &ChainSpec{}
	err := clientSpec.ParseAdditive(specValues)
	if err != nil {
		return []string{fmt.Sprintf("failed to parse specs: %v", err)}
	}

	// Check for mismatches: client has different values than majority
	mismatches, err := majoritySpecs.CheckMismatch(clientSpec)
	if err != nil {
		return []string{fmt.Sprintf("failed to check mismatches: %v", err)}
	}

	if len(mismatches) > 0 {
		mismatchesStr := []string{}
		for _, mismatch := range mismatches {
			if mismatch.Severity != "ignore" {
				mismatchesStr = append(mismatchesStr, mismatch.Name)
			}
		}
		if len(mismatchesStr) > 0 {
			warnings = append(warnings, fmt.Sprintf("spec mismatch with majority: %v", strings.Join(mismatchesStr, ", ")))
		}
	}

	// Check for missing specs: client doesn't have values that majority has
	// (by reversing the check - client's zero values are ignored, so we find majority values client doesn't have)
	mismatches, err = clientSpec.CheckMismatch(majoritySpecs)
	if err != nil {
		return warnings
	}
	if len(mismatches) > 0 {
		missingStr := []string{}
		for _, mismatch := range mismatches {
			missingStr = append(missingStr, mismatch.Name)
		}
		if len(missingStr) > 0 {
			warnings = append(warnings, fmt.Sprintf("spec missing: %v", strings.Join(missingStr, ", ")))
		}
	}

	return warnings
}

// computeMajoritySpecs computes the global spec by taking the majority value for each spec setting.
// Must be called with specMutex held.
func (cs *ChainState) computeMajoritySpecs() (*ChainSpec, error) {
	if len(cs.clientSpecs) == 0 {
		return nil, nil
	}

	// Build a map of spec key -> value -> count
	valueCounts := make(map[string]map[string]int)
	valueStore := make(map[string]map[string]interface{})

	for _, clientSpec := range cs.clientSpecs {
		for key, value := range clientSpec {
			if valueCounts[key] == nil {
				valueCounts[key] = make(map[string]int)
				valueStore[key] = make(map[string]interface{})
			}

			// Convert value to string for counting (handles different types)
			valueStr := fmt.Sprintf("%v", value)
			valueCounts[key][valueStr]++
			valueStore[key][valueStr] = value
		}
	}

	// Build the majority spec values
	majorityValues := make(map[string]interface{})

	for key, counts := range valueCounts {
		// Find the value with the highest count
		var maxCount int
		var maxValueStr string

		for valueStr, count := range counts {
			if count > maxCount {
				maxCount = count
				maxValueStr = valueStr
			}
		}

		// Use the actual value (not the string representation)
		majorityValues[key] = valueStore[key][maxValueStr]
	}

	// Parse into ChainSpec
	majoritySpec := &ChainSpec{}
	err := majoritySpec.ParseAdditive(majorityValues)
	if err != nil {
		return nil, err
	}

	return majoritySpec, nil
}

func (cs *ChainState) initWallclock() {
	cs.wallclockMutex.Lock()
	defer cs.wallclockMutex.Unlock()

	if cs.wallclock != nil {
		return
	}

	if cs.specs == nil || cs.genesis == nil {
		return
	}

	cs.wallclock = ethwallclock.NewEthereumBeaconChain(cs.genesis.GenesisTime, time.Duration(cs.specs.SecondsPerSlot)*time.Second, cs.specs.SlotsPerEpoch)
	cs.wallclock.OnEpochChanged(func(current ethwallclock.Epoch) {
		cs.wallclockEpochDispatcher.Fire(&current)
	})
	cs.wallclock.OnSlotChanged(func(current ethwallclock.Slot) {
		cs.wallclockSlotDispatcher.Fire(&current)
	})
}

func (cs *ChainState) setFinalizedCheckpoint(finality *v1.Finality) {
	cs.finalityMutex.Lock()
	if cs.finality != nil && finality.Justified.Epoch <= cs.finality.Justified.Epoch && finality.Finalized.Epoch <= cs.finality.Finalized.Epoch {
		cs.finalityMutex.Unlock()
		return
	}

	cs.finality = finality
	cs.finalityMutex.Unlock()

	cs.checkpointDispatcher.Fire(finality)
}

func (cs *ChainState) GetSpecs() *ChainSpec {
	return cs.specs
}

func (cs *ChainState) GetGenesis() *v1.Genesis {
	return cs.genesis
}

func (cs *ChainState) GetFinalizedCheckpoint() (phase0.Epoch, phase0.Root) {
	cs.finalityMutex.RLock()
	defer cs.finalityMutex.RUnlock()

	if cs.finality == nil {
		return 0, NullRoot
	}

	return cs.finality.Finalized.Epoch, cs.finality.Finalized.Root
}

func (cs *ChainState) GetJustifiedCheckpoint() (phase0.Epoch, phase0.Root) {
	cs.finalityMutex.RLock()
	defer cs.finalityMutex.RUnlock()

	if cs.finality == nil {
		return 0, NullRoot
	}

	return cs.finality.Justified.Epoch, cs.finality.Justified.Root
}

func (cs *ChainState) GetFinalizedSlot() phase0.Slot {
	if cs.specs == nil {
		return 0
	}

	cs.finalityMutex.RLock()
	defer cs.finalityMutex.RUnlock()

	if cs.finality == nil {
		return 0
	}

	return phase0.Slot(cs.finality.Finalized.Epoch) * phase0.Slot(cs.specs.SlotsPerEpoch)
}

func (cs *ChainState) CurrentSlot() phase0.Slot {
	return cs.TimeToSlot(time.Now())
}

func (cs *ChainState) CurrentEpoch() phase0.Epoch {
	return cs.EpochOfSlot(cs.CurrentSlot())
}

func (cs *ChainState) EpochOfSlot(slot phase0.Slot) phase0.Epoch {
	if cs.specs == nil {
		return 0
	}

	return phase0.Epoch(slot / phase0.Slot(cs.specs.SlotsPerEpoch))
}

func (cs *ChainState) EpochToSlot(epoch phase0.Epoch) phase0.Slot {
	if cs.specs == nil {
		return 0
	}

	return phase0.Slot(epoch) * phase0.Slot(cs.specs.SlotsPerEpoch)
}

func (cs *ChainState) SlotToTime(slot phase0.Slot) time.Time {
	if cs.specs == nil || cs.genesis == nil {
		return time.Time{}
	}

	return cs.genesis.GenesisTime.Add(time.Duration(uint64(slot)*cs.specs.SecondsPerSlot) * time.Second)
}

func (cs *ChainState) EpochToTime(epoch phase0.Epoch) time.Time {
	if cs.specs == nil || cs.genesis == nil {
		return time.Time{}
	}

	return cs.genesis.GenesisTime.Add(time.Duration(uint64(cs.EpochToSlot(epoch))*cs.specs.SecondsPerSlot) * time.Second)
}

func (cs *ChainState) TimeToSlot(timestamp time.Time) phase0.Slot {
	if cs.specs == nil || cs.genesis == nil {
		return 0
	}

	if cs.genesis.GenesisTime.Compare(timestamp) > 0 {
		return 0
	}

	return phase0.Slot(uint64((timestamp.Sub(cs.genesis.GenesisTime)).Seconds()) / cs.specs.SecondsPerSlot)
}

func (cs *ChainState) SlotToSlotIndex(slot phase0.Slot) phase0.Slot {
	if cs.specs == nil {
		return 0
	}

	return slot % phase0.Slot(cs.specs.SlotsPerEpoch)
}

func (cs *ChainState) EpochStartSlot(epoch phase0.Epoch) phase0.Slot {
	if cs.specs == nil {
		return 0
	}

	return phase0.Slot(epoch) * phase0.Slot(cs.specs.SlotsPerEpoch)
}

func (cs *ChainState) GetForkDigestForEpoch(epoch phase0.Epoch) phase0.ForkDigest {
	if cs.specs == nil || cs.genesis == nil {
		return phase0.ForkDigest{}
	}

	var currentBlobParams *BlobScheduleEntry

	if cs.specs.FuluForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.FuluForkEpoch) {
		currentBlobParams = &BlobScheduleEntry{
			Epoch:            *cs.specs.ElectraForkEpoch,
			MaxBlobsPerBlock: cs.specs.MaxBlobsPerBlockElectra,
		}

		for i, blobScheduleEntry := range cs.specs.BlobSchedule {
			if blobScheduleEntry.Epoch <= uint64(epoch) {
				currentBlobParams = &cs.specs.BlobSchedule[i]
			} else {
				break
			}
		}
	}

	currentForkVersion := cs.GetForkVersionAtEpoch(epoch)

	return cs.GetForkDigest(currentForkVersion, currentBlobParams)
}

func (cs *ChainState) GetBlobScheduleForEpoch(epoch phase0.Epoch) *BlobScheduleEntry {
	if cs.specs == nil {
		return nil
	}

	var blobSchedule *BlobScheduleEntry

	if cs.specs.ElectraForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.ElectraForkEpoch) {
		blobSchedule = &BlobScheduleEntry{
			Epoch:            *cs.specs.ElectraForkEpoch,
			MaxBlobsPerBlock: cs.specs.MaxBlobsPerBlockElectra,
		}
	} else if cs.specs.DenebForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.DenebForkEpoch) {
		blobSchedule = &BlobScheduleEntry{
			Epoch:            *cs.specs.DenebForkEpoch,
			MaxBlobsPerBlock: cs.specs.MaxBlobsPerBlock,
		}
	}

	for i, blobScheduleEntry := range cs.specs.BlobSchedule {
		if blobScheduleEntry.Epoch <= uint64(epoch) {
			blobSchedule = &cs.specs.BlobSchedule[i]
		}
	}

	return blobSchedule
}

func (cs *ChainState) GetForkDigest(forkVersion phase0.Version, blobParams *BlobScheduleEntry) phase0.ForkDigest {
	if cs.specs == nil || cs.genesis == nil {
		return phase0.ForkDigest{}
	}

	forkData := phase0.ForkData{
		CurrentVersion:        forkVersion,
		GenesisValidatorsRoot: cs.genesis.GenesisValidatorsRoot,
	}

	forkDataRoot, _ := forkData.HashTreeRoot()

	// For Fulu fork and later, modify the fork digest with blob parameters
	if blobParams != nil {
		// serialize epoch and max_blobs_per_block as uint64 little-endian
		epochBytes := make([]byte, 8)
		maxBlobsBytes := make([]byte, 8)
		for i := 0; i < 8; i++ {
			epochBytes[i] = byte((blobParams.Epoch >> (8 * i)) & 0xff)
			maxBlobsBytes[i] = byte((blobParams.MaxBlobsPerBlock >> (8 * i)) & 0xff)
		}
		blobParamBytes := append(epochBytes, maxBlobsBytes...)

		blobParamHash := [32]byte{}
		{
			h := sha256.New()
			h.Write(blobParamBytes)
			copy(blobParamHash[:], h.Sum(nil))
		}

		// xor baseDigest with first 4 bytes of blobParamHash
		forkDigest := make([]byte, 4)
		for i := 0; i < 4; i++ {
			forkDigest[i] = forkDataRoot[i] ^ blobParamHash[i]
		}

		return phase0.ForkDigest(forkDigest)
	}

	return phase0.ForkDigest(forkDataRoot[:4])
}

func (cs *ChainState) GetForkVersionAtEpoch(epoch phase0.Epoch) phase0.Version {
	if cs.specs == nil {
		return phase0.Version{}
	}

	switch {
	case cs.specs.FuluForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.FuluForkEpoch):
		return cs.specs.FuluForkVersion
	case cs.specs.ElectraForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.ElectraForkEpoch):
		return cs.specs.ElectraForkVersion
	case cs.specs.DenebForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.DenebForkEpoch):
		return cs.specs.DenebForkVersion
	case cs.specs.CapellaForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.CapellaForkEpoch):
		return cs.specs.CapellaForkVersion
	case cs.specs.BellatrixForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.BellatrixForkEpoch):
		return cs.specs.BellatrixForkVersion
	case cs.specs.AltairForkEpoch != nil && epoch >= phase0.Epoch(*cs.specs.AltairForkEpoch):
		return cs.specs.AltairForkVersion
	default:
		return cs.specs.GenesisForkVersion
	}
}

func (cs *ChainState) GetValidatorChurnLimit(validatorCount uint64) uint64 {
	if cs.specs == nil {
		return 0
	}

	adaptable := uint64(0)
	if validatorCount > 0 {
		adaptable = validatorCount / cs.specs.ChurnLimitQuotient
	}

	min := cs.specs.MinPerEpochChurnLimit
	if min > adaptable {
		return min
	}

	return adaptable
}

func (cs *ChainState) GetBalanceChurnLimit(totalActiveBalance uint64) uint64 {
	if cs.specs == nil {
		return 0
	}

	balanceChurnLimit := totalActiveBalance / cs.specs.ChurnLimitQuotient
	if balanceChurnLimit < cs.specs.MinPerEpochChurnLimitElectra {
		balanceChurnLimit = cs.specs.MinPerEpochChurnLimitElectra
	}

	return balanceChurnLimit - (balanceChurnLimit % cs.specs.EffectiveBalanceIncrement)
}

func (cs *ChainState) GetActivationExitChurnLimit(totalActiveBalance uint64) uint64 {
	if cs.specs == nil {
		return 0
	}

	balanceChurnLimit := cs.GetBalanceChurnLimit(totalActiveBalance)
	if balanceChurnLimit > cs.specs.MaxPerEpochActivationExitChurnLimit {
		return cs.specs.MaxPerEpochActivationExitChurnLimit
	}

	return balanceChurnLimit
}
