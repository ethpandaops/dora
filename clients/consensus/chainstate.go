package consensus

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/ethwallclock"
	"github.com/mashingan/smapping"
)

type FinalizedCheckpoint struct {
	Epoch phase0.Epoch
	Root  phase0.Root
}

type chainState struct {
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

func newChainState() *chainState {
	return &chainState{}
}

func (cs *chainState) setGenesis(genesis *v1.Genesis) error {
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

func (cs *chainState) setClientSpecs(specValues map[string]interface{}) error {
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

func (cs *chainState) getSpecs() *ChainSpec {
	cs.specMutex.RLock()
	defer cs.specMutex.RUnlock()

	return cs.specs
}

func (cs *chainState) initWallclock() {
	cs.wallclockMutex.Lock()
	defer cs.wallclockMutex.Unlock()

	if cs.wallclock != nil {
		return
	}

	specs := cs.getSpecs()
	if specs == nil || cs.genesis == nil {
		return
	}

	cs.wallclock = ethwallclock.NewEthereumBeaconChain(cs.genesis.GenesisTime, specs.SecondsPerSlot, specs.SlotsPerEpoch)
	cs.wallclock.OnEpochChanged(func(current ethwallclock.Epoch) {
		cs.wallclockEpochDispatcher.Fire(&current)
	})
	cs.wallclock.OnSlotChanged(func(current ethwallclock.Slot) {
		cs.wallclockSlotDispatcher.Fire(&current)
	})
}

func (cs *chainState) setFinalizedCheckpoint(finalizedEpoch phase0.Epoch, finalizedRoot phase0.Root) {
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

func (cs *chainState) getFinalizedCheckpoint() (phase0.Epoch, phase0.Root) {
	cs.finalizedMutex.RLock()
	defer cs.finalizedMutex.RUnlock()

	return cs.finalizedEpoch, cs.finalizedRoot
}
