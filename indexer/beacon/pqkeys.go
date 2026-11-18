package beacon

import (
	"sync"

	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/eip8450"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

type pqKeyRegistry struct {
	mutex        sync.RWMutex
	slot         phase0.Slot
	material     []*eip8450.PQKeyMaterial
	changeEpochs []phase0.Epoch
}

func (r *pqKeyRegistry) update(state *all.BeaconState) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if state.Slot < r.slot {
		return
	}

	r.slot = state.Slot
	r.material = state.PostQuantumKeyMaterial
	r.changeEpochs = state.PostQuantumKeyChangeEpochs
}

// GetValidatorPQKeys returns the post-quantum keys a validator registered and the epoch of the
// last change, or nil if the validator has no registry entry with a leanXMSS key.
func (indexer *Indexer) GetValidatorPQKeys(index phase0.ValidatorIndex) (*eip8450.PQKeyMaterial, phase0.Epoch) {
	registry := indexer.pqKeyRegistry

	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	if uint64(index) >= uint64(len(registry.material)) || uint64(index) >= uint64(len(registry.changeEpochs)) {
		return nil, 0
	}

	material := registry.material[index]
	if material == nil || material.XMSSPublicKey == (eip8450.XMSSPublicKey{}) {
		return nil, 0
	}

	return material, registry.changeEpochs[index]
}
