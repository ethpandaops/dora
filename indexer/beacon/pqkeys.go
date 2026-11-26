package beacon

import (
	"sync"

	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/go-eth2-client/spec/pq"
)

const aggregationCommitteeRetentionSlots = 4096

type pqKeyRegistry struct {
	mutex                 sync.RWMutex
	slot                  phase0.Slot
	validators            []*pq.Validator
	aggregationCommittees map[phase0.Slot][][]phase0.ValidatorIndex
}

func (r *pqKeyRegistry) update(state *all.BeaconState, slotsPerEpoch uint64) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.updateAggregationCommittees(state, slotsPerEpoch)

	if state.Slot < r.slot && len(state.XMSSValidators) <= len(r.validators) {
		return
	}

	r.slot = state.Slot
	r.validators = state.XMSSValidators
}

func (r *pqKeyRegistry) updateAggregationCommittees(state *all.BeaconState, slotsPerEpoch uint64) {
	if slotsPerEpoch == 0 || len(state.AggregationCommitteeWindow) == 0 {
		return
	}

	if r.aggregationCommittees == nil {
		r.aggregationCommittees = map[phase0.Slot][][]phase0.ValidatorIndex{}
	}

	stateEpoch := uint64(state.Slot) / slotsPerEpoch
	newest := phase0.Slot(0)

	for offset, committees := range state.AggregationCommitteeWindow {
		epoch := stateEpoch + uint64(offset)/slotsPerEpoch
		if epoch == 0 {
			continue
		}

		slot := phase0.Slot((epoch-1)*slotsPerEpoch + uint64(offset)%slotsPerEpoch)
		r.aggregationCommittees[slot] = committees

		if slot > newest {
			newest = slot
		}
	}

	for slot := range r.aggregationCommittees {
		if slot+aggregationCommitteeRetentionSlots < newest {
			delete(r.aggregationCommittees, slot)
		}
	}
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

func (indexer *Indexer) GetAggregationCommittees(slot phase0.Slot) [][]phase0.ValidatorIndex {
	registry := indexer.pqKeyRegistry

	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	return registry.aggregationCommittees[slot]
}
