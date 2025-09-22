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
	"gopkg.in/yaml.v2"
)

type ChainState struct {
	specMutex sync.RWMutex
	specs     *ChainSpec

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
	return &ChainState{}
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

func (cs *ChainState) setClientSpecs(specValues map[string]interface{}) (error, error) {
	cs.specMutex.Lock()
	defer cs.specMutex.Unlock()

	specs := cs.specs
	if specs == nil {
		specs = &ChainSpec{}
	} else {
		specs = specs.Clone()
	}

	specValuesYaml, err := yaml.Marshal(specValues)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(specValuesYaml, specs)
	if err != nil {
		return nil, err
	}

	var warning error

	if cs.specs != nil {
		mismatches, err := cs.specs.CheckMismatch(specs)
		if err != nil {
			return nil, err
		}
		if len(mismatches) > 0 {
			return nil, fmt.Errorf("spec mismatch: %v", strings.Join(mismatches, ", "))
		}

		newSpecs := &ChainSpec{}
		err = yaml.Unmarshal(specValuesYaml, newSpecs)
		if err != nil {
			return nil, err
		}

		mismatches, err = cs.specs.CheckMismatch(newSpecs)
		if err != nil {
			return nil, err
		}
		if len(mismatches) > 0 {
			warning = fmt.Errorf("spec missing: %v", strings.Join(mismatches, ", "))
		}
	}

	cs.specs = specs

	return warning, nil
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

	cs.wallclock = ethwallclock.NewEthereumBeaconChain(cs.genesis.GenesisTime, cs.specs.SecondsPerSlot, cs.specs.SlotsPerEpoch)
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

	return cs.genesis.GenesisTime.Add(time.Duration(slot) * cs.specs.SecondsPerSlot)
}

func (cs *ChainState) EpochToTime(epoch phase0.Epoch) time.Time {
	if cs.specs == nil || cs.genesis == nil {
		return time.Time{}
	}

	return cs.genesis.GenesisTime.Add(time.Duration(cs.EpochToSlot(epoch)) * cs.specs.SecondsPerSlot)
}

func (cs *ChainState) TimeToSlot(timestamp time.Time) phase0.Slot {
	if cs.specs == nil || cs.genesis == nil {
		return 0
	}

	if cs.genesis.GenesisTime.Compare(timestamp) > 0 {
		return 0
	}

	return phase0.Slot(uint64((timestamp.Sub(cs.genesis.GenesisTime)).Seconds()) / uint64(cs.specs.SecondsPerSlot.Seconds()))
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
