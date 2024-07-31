package beacon

import (
	"math"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/indexer/beacon/duties"
	dynssz "github.com/pk910/dynamic-ssz"
)

// EpochStats holds the epoch-specific information based on the underlying dependent beacon state.
type EpochStats struct {
	epoch          phase0.Epoch
	dependentRoot  phase0.Root
	dependentState *epochState

	requestedMutex sync.Mutex
	requestedBy    []*Client
	ready          bool
	values         *EpochStatsValues
}

// EpochStatsValues holds the values for the epoch-specific information.
type EpochStatsValues struct {
	ProposerDuties   []phase0.ValidatorIndex
	AttesterDuties   [][][]EpochStatsAttesterDuty
	ActiveValidators uint64
	TotalBalance     phase0.Gwei
	ActiveBalance    phase0.Gwei
	EffectiveBalance phase0.Gwei
}

// EpochStatsAttesterDuty holds the attester duty information for a validator.
type EpochStatsAttesterDuty struct {
	ValidatorIndex      phase0.ValidatorIndex
	EffectiveBalanceEth uint16
}

// newEpochStats creates a new EpochStats instance.
func newEpochStats(epoch phase0.Epoch, dependentRoot phase0.Root, dependentState *epochState) *EpochStats {
	stats := &EpochStats{
		epoch:          epoch,
		dependentRoot:  dependentRoot,
		dependentState: dependentState,
		requestedBy:    make([]*Client, 0),
	}

	return stats
}

// addRequestedBy adds a client to the list of clients that have requested this EpochStats.
func (es *EpochStats) addRequestedBy(client *Client) {
	es.requestedMutex.Lock()
	defer es.requestedMutex.Unlock()

	for _, c := range es.requestedBy {
		if c == client {
			return
		}
	}

	es.requestedBy = append(es.requestedBy, client)
}

// getRequestedBy returns a copy of the list of clients that have requested this EpochStats.
func (es *EpochStats) getRequestedBy() []*Client {
	es.requestedMutex.Lock()
	defer es.requestedMutex.Unlock()

	clients := make([]*Client, len(es.requestedBy))
	copy(clients, es.requestedBy)

	return clients
}

// marshalSSZ marshals the EpochStats values using SSZ.
func (es *EpochStats) marshalSSZ(dynSsz *dynssz.DynSsz) ([]byte, error) {
	if dynSsz == nil {
		dynSsz = dynssz.NewDynSsz(nil)
	}
	if es.values == nil {
		return []byte{}, nil
	}
	return dynSsz.MarshalSSZ(es.values)
}

// unmarshalSSZ unmarshals the EpochStats values using the provided SSZ bytes.
func (es *EpochStats) unmarshalSSZ(dynSsz *dynssz.DynSsz, ssz []byte) error {
	if dynSsz == nil {
		dynSsz = dynssz.NewDynSsz(nil)
	}

	if len(ssz) == 0 {
		es.values = nil
	} else {
		values := &EpochStatsValues{}
		if err := dynSsz.UnmarshalSSZ(values, ssz); err != nil {
			return err
		}

		es.values = values
	}

	return nil
}

// processState processes the epoch state and computes proposer and attester duties.
func (es *EpochStats) processState(indexer *Indexer) {
	if es.dependentState == nil || es.dependentState.loadingStatus != 2 {
		return
	}

	chainState := indexer.consensusPool.GetChainState()
	values := &EpochStatsValues{
		TotalBalance:     0,
		ActiveBalance:    0,
		EffectiveBalance: 0,
	}

	// get active validator indices & aggregate balances
	activeIndices := []phase0.ValidatorIndex{}
	for index, validator := range es.dependentState.validatorList {
		values.TotalBalance += es.dependentState.validatorBalances[index]
		if es.epoch >= validator.ActivationEpoch && es.epoch < validator.ExitEpoch {
			activeIndices = append(activeIndices, phase0.ValidatorIndex(index))
			values.EffectiveBalance += validator.EffectiveBalance
			values.ActiveBalance += es.dependentState.validatorBalances[index]
		}
	}

	values.ActiveValidators = uint64(len(activeIndices))
	beaconState := &duties.BeaconState{
		GetRandaoMixes: func() []phase0.Root {
			return es.dependentState.randaoMixes
		},
		GetActiveIndices: func() []phase0.ValidatorIndex {
			return activeIndices
		},
		GetEffectiveBalance: func(index phase0.ValidatorIndex) phase0.Gwei {
			return es.dependentState.validatorList[index].EffectiveBalance
		},
	}

	indexer.logger.Infof("processing epoch %v stats (%v / %v), validators: %v/%v", es.epoch, es.dependentRoot.String(), es.dependentState.stateRoot.String(), values.ActiveValidators, len(es.dependentState.validatorList))

	// compute proposers
	proposerDuties := []phase0.ValidatorIndex{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		proposer, err := duties.GetProposerIndex(chainState.GetSpecs(), beaconState, slot)
		if err != nil {
			indexer.logger.Warnf("failed computing proposer for slot %v: %v", slot, err)
			proposer = math.MaxInt64
		}

		proposerDuties = append(proposerDuties, proposer)
	}

	values.ProposerDuties = proposerDuties

	// compute committees
	attesterDuties := [][][]EpochStatsAttesterDuty{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		committees, err := duties.GetBeaconCommittees(chainState.GetSpecs(), beaconState, slot)
		if err != nil {
			indexer.logger.Warnf("failed computing committees for slot %v: %v", slot, err)
			committees = [][]phase0.ValidatorIndex{}
		}

		committeeDuties := [][]EpochStatsAttesterDuty{}
		for i, committee := range committees {
			committeeDuty := []EpochStatsAttesterDuty{}
			for j, validatorIndex := range committee {
				validator := es.dependentState.validatorList[validatorIndex]
				effectiveBalance := uint16(0)
				if validator == nil {
					indexer.logger.Warnf("validator %v not found for committee duty %v:%v:%v", validatorIndex, slot, i, j)
				} else {
					effectiveBalance = uint16(validator.EffectiveBalance / 1000000000)
				}

				committeeDuty = append(committeeDuty, EpochStatsAttesterDuty{
					ValidatorIndex:      validatorIndex,
					EffectiveBalanceEth: effectiveBalance,
				})
			}
			committeeDuties = append(committeeDuties, committeeDuty)
		}
		attesterDuties = append(attesterDuties, committeeDuties)
	}

	values.AttesterDuties = attesterDuties

	es.ready = true
	es.values = values

	ssz, _ := es.marshalSSZ(indexer.dynSsz)
	indexer.logger.Infof("epoch %v stats (%v / %v) ready, %v bytes", es.epoch, es.dependentRoot.String(), es.dependentState.stateRoot.String(), len(ssz))
}

// GetValues returns the EpochStats values.
func (es *EpochStats) GetValues() *EpochStatsValues {
	if es == nil {
		return nil
	}

	return es.values
}
