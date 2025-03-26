package consensus

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/ethwallclock"
	"github.com/mashingan/smapping"
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

	checkpointDispatcher     Dispatcher[*v1.Finality]
	wallclockEpochDispatcher Dispatcher[*ethwallclock.Epoch]
	wallclockSlotDispatcher  Dispatcher[*ethwallclock.Slot]
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

	err := smapping.FillStructByTags(specs, specValues, "yaml")
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
		err = smapping.FillStructByTags(newSpecs, specValues, "yaml")
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

func (cs *ChainState) IsEip7732Enabled(epoch phase0.Epoch) bool {
	if cs.specs == nil {
		return false
	}

	return cs.specs.Eip7732ForkEpoch != nil && phase0.Epoch(*cs.specs.Eip7732ForkEpoch) <= epoch
}
