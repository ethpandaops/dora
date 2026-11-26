package beacon

import (
	"sync"

	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/go-eth2-client/spec/pq"
)

type pqKeyRegistry struct {
	mutex      sync.RWMutex
	slot       phase0.Slot
	validators []*pq.Validator
}

func (r *pqKeyRegistry) update(state *all.BeaconState) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if state.Slot < r.slot && len(state.XMSSValidators) <= len(r.validators) {
		return
	}

	r.slot = state.Slot
	r.validators = state.XMSSValidators
}

// GetValidatorXMSSKeys returns the leanXMSS public key and VRF root of a validator on a pq network.
func (indexer *Indexer) GetValidatorXMSSKeys(index phase0.ValidatorIndex) (pubkey pq.XMSSPubKey, vrfRoot phase0.Root, found bool) {
	registry := indexer.pqKeyRegistry

	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	if uint64(index) >= uint64(len(registry.validators)) {
		return pubkey, vrfRoot, false
	}

	validator := registry.validators[index]

	return validator.PublicKey, validator.VRFRoot, true
}
