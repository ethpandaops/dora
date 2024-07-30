package beacon

import (
	"math"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/indexer/beacon/duties"
)

// EpochStats holds the epoch-specific information based on the underlying dependent beacon state.
type EpochStats struct {
	epoch          phase0.Epoch
	dependentRoot  phase0.Root
	dependentState *epochState

	requestedMutex sync.Mutex
	requestedBy    []*Client
	ready          bool

	proposerDuties   []phase0.ValidatorIndex
	attesterDuties   [][][]phase0.ValidatorIndex
	activeValidators uint64
	totalBalance     phase0.Gwei
	activeBalance    phase0.Gwei
	effectiveBalance phase0.Gwei
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

// processState processes the epoch state and computes proposer and attester duties.
func (es *EpochStats) processState(indexer *Indexer) {
	if es.dependentState.loadingStatus != 2 {
		return
	}

	chainState := indexer.consensusPool.GetChainState()

	// get active validator indices & aggregate balances
	es.totalBalance = 0
	es.activeBalance = 0
	es.effectiveBalance = 0

	activeIndices := []phase0.ValidatorIndex{}
	for index, validator := range es.dependentState.validatorList {
		es.totalBalance += es.dependentState.validatorBalances[index]
		if es.epoch >= validator.ActivationEpoch && es.epoch < validator.ExitEpoch {
			activeIndices = append(activeIndices, phase0.ValidatorIndex(index))
			es.effectiveBalance += validator.EffectiveBalance
			es.activeBalance += es.dependentState.validatorBalances[index]
		}
	}

	es.activeValidators = uint64(len(activeIndices))

	indexer.logger.Infof("processing epoch %v stats (%v / %v), validators: %v/%v", es.epoch, es.dependentRoot.String(), es.dependentState.stateRoot.String(), es.activeValidators, len(es.dependentState.validatorList))

	// compute proposers
	proposerDuties := []phase0.ValidatorIndex{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		proposer, err := duties.GetProposerIndex(chainState.GetSpecs(), es, activeIndices, slot)
		if err != nil {
			indexer.logger.Warnf("failed computing proposer for slot %v: %v", slot, err)
			proposer = math.MaxInt64
		}

		proposerDuties = append(proposerDuties, proposer)
	}

	es.proposerDuties = proposerDuties

	// compute committees
	attesterDuties := [][][]phase0.ValidatorIndex{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		committees, err := duties.GetBeaconCommittees(chainState.GetSpecs(), es, activeIndices, slot)
		if err != nil {
			indexer.logger.Warnf("failed computing committees for slot %v: %v", slot, err)
			committees = [][]phase0.ValidatorIndex{}
		}

		attesterDuties = append(attesterDuties, committees)
	}

	es.attesterDuties = attesterDuties

	es.ready = true
}

// GetRandaoMixes returns the RandaoMixes from the dependent epoch state.
func (es *EpochStats) GetRandaoMixes() []phase0.Root {
	return es.dependentState.randaoMixes
}

// GetEffectiveBalance returns the effective balance of the validator at the given index.
func (es *EpochStats) GetEffectiveBalance(index phase0.ValidatorIndex) phase0.Gwei {
	return es.dependentState.validatorList[index].EffectiveBalance
}
