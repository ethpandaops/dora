package beacon

import (
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/indexer/beacon/beaconsim"
)

type EpochStats struct {
	epoch          phase0.Epoch
	dependentRoot  phase0.Root
	dependentState *EpochState

	requestedMutex sync.Mutex
	requestedBy    []*Client
	ready          bool

	proposerDuties []phase0.ValidatorIndex
	attesterDuties [][][]phase0.ValidatorIndex
}

func newEpochStats(epoch phase0.Epoch, dependentRoot phase0.Root, dependentState *EpochState) *EpochStats {
	stats := &EpochStats{
		epoch:          epoch,
		dependentRoot:  dependentRoot,
		dependentState: dependentState,
		requestedBy:    make([]*Client, 0),
	}

	return stats
}

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

func (es *EpochStats) getRequestedBy() []*Client {
	es.requestedMutex.Lock()
	defer es.requestedMutex.Unlock()

	clients := make([]*Client, len(es.requestedBy))
	copy(clients, es.requestedBy)

	return clients
}

func (es *EpochStats) processState(indexer *Indexer) {
	if es.dependentState.loadingStatus != 2 {
		return
	}

	chainState := indexer.consensusPool.GetChainState()

	// get active validator indicees
	activeIndices := []phase0.ValidatorIndex{}
	for index, validator := range es.dependentState.validatorList {
		if es.epoch >= validator.ActivationEpoch && es.epoch < validator.ExitEpoch {
			activeIndices = append(activeIndices, phase0.ValidatorIndex(index))
		}
	}

	indexer.logger.Infof("processing epoch %v stats (%v / %v)", es.epoch, es.dependentRoot.String(), es.dependentState.stateRoot.String())

	// compute proposers
	proposerDuties := []phase0.ValidatorIndex{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		proposer, err := beaconsim.GetProposerIndex(chainState.GetSpecs(), es, activeIndices, slot)
		if err != nil {
			indexer.logger.Warnf("failed computing proposer for slot %v: %v", slot, err)
		}

		proposerDuties = append(proposerDuties, proposer)
	}

	es.proposerDuties = proposerDuties

	// compute committees
	attesterDuties := [][][]phase0.ValidatorIndex{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		committees, err := beaconsim.GetBeaconCommittees(chainState.GetSpecs(), es, activeIndices, slot)
		if err != nil {
			indexer.logger.Warnf("failed computing committees for slot %v: %v", slot, err)
		}

		attesterDuties = append(attesterDuties, committees)
	}

	es.attesterDuties = attesterDuties

	es.ready = true
}

func (es *EpochStats) GetRandaoMixes() []phase0.Root {
	return es.dependentState.randaoMixes
}

func (es *EpochStats) GetEffectiveBalance(index phase0.ValidatorIndex) phase0.Gwei {
	return es.dependentState.validatorList[index].EffectiveBalance
}
