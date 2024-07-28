package consensus

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/ethwallclock"
	"github.com/mashingan/smapping"
)

type FinalizedCheckpoint struct {
	Epoch phase0.Epoch
	Root  phase0.Root
}

type ChainState struct {
	specMutex sync.RWMutex
	specs     *ChainSpec

	genesisMutex sync.Mutex
	genesis      *v1.Genesis

	wallclockMutex sync.Mutex
	wallclock      *ethwallclock.EthereumBeaconChain

	finalizedMutex sync.RWMutex
	finalizedEpoch phase0.Epoch
	finalizedRoot  phase0.Root

	checkpointDispatcher     Dispatcher[*FinalizedCheckpoint]
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

func (cs *ChainState) setClientSpecs(specValues map[string]interface{}) error {
	cs.specMutex.Lock()
	defer cs.specMutex.Unlock()

	specs := ChainSpec{}

	err := smapping.FillStructByTags(&specs, specValues, "yaml")
	if err != nil {
		return err
	}

	if cs.specs != nil {
		mismatches := cs.specs.CheckMismatch(&specs)
		if len(mismatches) > 0 {
			return fmt.Errorf("spec mismatch: %v", strings.Join(mismatches, ", "))
		}
	}

	cs.specs = &specs

	return nil
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

func (cs *ChainState) setFinalizedCheckpoint(finalizedEpoch phase0.Epoch, finalizedRoot phase0.Root) {
	cs.finalizedMutex.Lock()
	if finalizedEpoch <= cs.finalizedEpoch {
		cs.finalizedMutex.Unlock()
		return
	}

	cs.finalizedEpoch = finalizedEpoch
	cs.finalizedRoot = finalizedRoot
	cs.finalizedMutex.Unlock()

	cs.checkpointDispatcher.Fire(&FinalizedCheckpoint{
		Epoch: finalizedEpoch,
		Root:  finalizedRoot,
	})
}

func (cs *ChainState) GetSpecs() *ChainSpec {
	return cs.specs
}

func (cs *ChainState) GetGenesis() *v1.Genesis {
	return cs.genesis
}

func (cs *ChainState) GetFinalizedCheckpoint() (phase0.Epoch, phase0.Root) {
	cs.finalizedMutex.RLock()
	defer cs.finalizedMutex.RUnlock()

	return cs.finalizedEpoch, cs.finalizedRoot
}

func (cs *ChainState) GetFinalizedSlot() phase0.Slot {
	if cs.specs == nil {
		return 0
	}

	cs.finalizedMutex.RLock()
	defer cs.finalizedMutex.RUnlock()

	return phase0.Slot(cs.finalizedEpoch) * phase0.Slot(cs.specs.SlotsPerEpoch)
}

func (cs *ChainState) CurrentSlot() phase0.Slot {
	if cs.wallclock == nil {
		return 0
	}

	slot, _, err := cs.wallclock.Now()
	if err != nil {
		return 0
	}

	if slot.Number() > uint64(math.MaxInt64) {
		return 0
	}

	return phase0.Slot(slot.Number())
}

func (cs *ChainState) CurrentEpoch() phase0.Epoch {
	if cs.wallclock == nil {
		return 0
	}

	_, epoch, err := cs.wallclock.Now()
	if err != nil {
		return 0
	}

	if epoch.Number() > uint64(math.MaxInt64) {
		return 0
	}

	return phase0.Epoch(epoch.Number())
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

func (cs *ChainState) TimeToSlot(timestamp time.Time) phase0.Slot {
	if cs.specs == nil || cs.genesis == nil {
		return 0
	}

	if cs.genesis.GenesisTime.Compare(timestamp) > 0 {
		return 0
	}

	return phase0.Slot(uint64((timestamp.Sub(cs.genesis.GenesisTime)).Seconds()) / uint64(cs.specs.SecondsPerSlot.Seconds()))
}
