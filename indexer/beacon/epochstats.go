package beacon

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon/duties"
	"github.com/jmoiron/sqlx"
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
	isInDb         bool

	precalcBaseRoot phase0.Root
	precalcValues   *EpochStatsValues
	values          *EpochStatsValues
	prunedValues    *EpochStatsValues

	prunedEpochAggregations []*dbtypes.UnfinalizedEpoch
}

// EpochStatsValues holds the values for the epoch-specific information.
type EpochStatsValues struct {
	RandaoMix           phase0.Hash32
	NextRandaoMix       phase0.Hash32
	EffectiveBalances   map[phase0.ValidatorIndex]phase0.Gwei
	ProposerDuties      []phase0.ValidatorIndex
	AttesterDuties      [][][]phase0.ValidatorIndex
	SyncCommitteeDuties []phase0.ValidatorIndex
	ActiveValidators    uint64
	TotalBalance        phase0.Gwei
	ActiveBalance       phase0.Gwei
	EffectiveBalance    phase0.Gwei
	FirstDepositIndex   uint64
}

// EpochStatsPacked holds the packed values for the epoch-specific information.
type EpochStatsPacked struct {
	ActiveValidators    []EpochStatsPackedValidator
	SyncCommitteeDuties []phase0.ValidatorIndex
	RandaoMix           phase0.Hash32
	NextRandaoMix       phase0.Hash32
	TotalBalance        phase0.Gwei
	ActiveBalance       phase0.Gwei
	FirstDepositIndex   uint64
}

// EpochStatsPackedValidator holds the packed values for an active validator.
type EpochStatsPackedValidator struct {
	ValidatorIndexOffset uint32 // offset to the previous index in the list (this is smaller than storing the full validator index)
	EffectiveBalanceEth  uint16 // effective balance in full ETH
}

// newEpochStats creates a new EpochStats instance.
func newEpochStats(epoch phase0.Epoch, dependentRoot phase0.Root) *EpochStats {
	stats := &EpochStats{
		epoch:         epoch,
		dependentRoot: dependentRoot,
		requestedBy:   make([]*Client, 0),
	}

	return stats
}

// addRequestedBy adds a client to the list of clients that have requested this EpochStats.
func (es *EpochStats) addRequestedBy(client *Client) bool {
	es.requestedMutex.Lock()
	defer es.requestedMutex.Unlock()

	for _, c := range es.requestedBy {
		if c == client {
			return false
		}
	}

	es.requestedBy = append(es.requestedBy, client)
	return true
}

// getRequestedBy returns a copy of the list of clients that have requested this EpochStats.
func (es *EpochStats) getRequestedBy() []*Client {
	es.requestedMutex.Lock()
	defer es.requestedMutex.Unlock()

	clients := make([]*Client, len(es.requestedBy))
	copy(clients, es.requestedBy)

	return clients
}

func (es *EpochStats) restoreFromDb(dbDuty *dbtypes.UnfinalizedDuty, dynSsz *dynssz.DynSsz, chainState *consensus.ChainState) error {
	if es.ready {
		return nil
	}

	values, err := es.parsePackedSSZ(dynSsz, chainState, dbDuty.DutiesSSZ)
	if err != nil {
		return err
	}

	es.values = values
	es.ready = true

	return nil
}

// marshalSSZ marshals the EpochStats values using SSZ.
func (es *EpochStats) buildPackedSSZ(dynSsz *dynssz.DynSsz) ([]byte, error) {
	if es.values == nil {
		return nil, fmt.Errorf("no values to marshal")
	}

	if dynSsz == nil {
		dynSsz = dynssz.NewDynSsz(nil)
	}

	packedValues := &EpochStatsPacked{
		ActiveValidators:    make([]EpochStatsPackedValidator, es.values.ActiveValidators),
		SyncCommitteeDuties: es.values.SyncCommitteeDuties,
		RandaoMix:           es.values.RandaoMix,
		NextRandaoMix:       es.values.NextRandaoMix,
		TotalBalance:        es.values.TotalBalance,
		ActiveBalance:       es.values.ActiveBalance,
		FirstDepositIndex:   es.values.FirstDepositIndex,
	}

	activeIndices := make([]phase0.ValidatorIndex, len(es.values.EffectiveBalances))
	i := 0
	for index := range es.values.EffectiveBalances {
		activeIndices[i] = index
		i++
	}

	sort.Slice(activeIndices, func(i, j int) bool {
		return activeIndices[i] < activeIndices[j]
	})

	lastValidatorIndex := phase0.ValidatorIndex(0)
	for i, validatorIndex := range activeIndices {
		effectiveBalance := es.values.EffectiveBalances[validatorIndex]
		packedBalance := uint16(effectiveBalance / EtherGweiFactor)

		validatorOffset := uint32(validatorIndex - lastValidatorIndex)
		lastValidatorIndex = validatorIndex

		packedValues.ActiveValidators[i] = EpochStatsPackedValidator{
			ValidatorIndexOffset: validatorOffset,
			EffectiveBalanceEth:  packedBalance,
		}
	}

	rawSsz, err := dynSsz.MarshalSSZ(packedValues)
	if err != nil {
		return nil, err
	}

	return compressBytes(rawSsz), nil
}

// unmarshalSSZ unmarshals the EpochStats values using the provided SSZ bytes.
func (es *EpochStats) parsePackedSSZ(dynSsz *dynssz.DynSsz, chainState *consensus.ChainState, ssz []byte) (*EpochStatsValues, error) {
	if dynSsz == nil {
		dynSsz = dynssz.NewDynSsz(nil)
	}

	if len(ssz) == 0 {
		return nil, nil
	}

	if d, err := decompressBytes(ssz); err != nil {
		return nil, err
	} else {
		ssz = d
	}

	packedValues := &EpochStatsPacked{}
	if err := dynSsz.UnmarshalSSZ(packedValues, ssz); err != nil {
		return nil, err
	}

	values := &EpochStatsValues{
		RandaoMix:           packedValues.RandaoMix,
		NextRandaoMix:       packedValues.NextRandaoMix,
		EffectiveBalances:   map[phase0.ValidatorIndex]phase0.Gwei{},
		SyncCommitteeDuties: packedValues.SyncCommitteeDuties,
		TotalBalance:        packedValues.TotalBalance,
		ActiveBalance:       packedValues.ActiveBalance,
		EffectiveBalance:    0,
		FirstDepositIndex:   packedValues.FirstDepositIndex,
	}

	activeIndices := make([]phase0.ValidatorIndex, len(packedValues.ActiveValidators))
	lastValidatorIndex := phase0.ValidatorIndex(0)
	for i, packedValidator := range packedValues.ActiveValidators {
		validatorIndex := lastValidatorIndex + phase0.ValidatorIndex(packedValidator.ValidatorIndexOffset)
		lastValidatorIndex = validatorIndex

		effectiveBalance := phase0.Gwei(packedValidator.EffectiveBalanceEth) * EtherGweiFactor
		values.EffectiveBalances[validatorIndex] = effectiveBalance
		values.EffectiveBalance += effectiveBalance

		activeIndices[i] = validatorIndex
	}

	values.ActiveValidators = uint64(len(activeIndices))

	beaconState := &duties.BeaconState{
		RandaoMix: &values.RandaoMix,
		GetActiveIndices: func() []phase0.ValidatorIndex {
			return activeIndices
		},
		GetEffectiveBalance: func(index phase0.ValidatorIndex) phase0.Gwei {
			return values.EffectiveBalances[index]
		},
	}

	// compute proposers
	proposerDuties := []phase0.ValidatorIndex{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		proposer, err := duties.GetProposerIndex(chainState.GetSpecs(), beaconState, slot)
		if err != nil {
			proposer = math.MaxInt64
		}

		proposerDuties = append(proposerDuties, proposer)
	}

	values.ProposerDuties = proposerDuties
	if beaconState.RandaoMix != nil {
		values.RandaoMix = *beaconState.RandaoMix
	}

	// compute committees
	attesterDuties := [][][]phase0.ValidatorIndex{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		committees, err := duties.GetBeaconCommittees(chainState.GetSpecs(), beaconState, slot)
		if err != nil {
			committees = [][]phase0.ValidatorIndex{}
		}

		attesterDuties = append(attesterDuties, committees)
	}

	values.AttesterDuties = attesterDuties

	return values, nil
}

// packValues packs the EpochStats values.
func (es *EpochStats) pruneValues() {
	if es.values == nil {
		return
	}

	es.prunedValues = &EpochStatsValues{
		RandaoMix:           es.values.RandaoMix,
		NextRandaoMix:       es.values.NextRandaoMix,
		EffectiveBalances:   nil, // prune
		ProposerDuties:      es.values.ProposerDuties,
		AttesterDuties:      nil, // prune
		SyncCommitteeDuties: es.values.SyncCommitteeDuties,
		ActiveValidators:    es.values.ActiveValidators,
		TotalBalance:        es.values.TotalBalance,
		ActiveBalance:       es.values.ActiveBalance,
		EffectiveBalance:    es.values.EffectiveBalance,
		FirstDepositIndex:   es.values.FirstDepositIndex,
	}

	es.values = nil
}

func (es *EpochStats) loadValuesFromDb(dynSsz *dynssz.DynSsz, chainState *consensus.ChainState) *EpochStatsValues {
	if !es.isInDb {
		return nil
	}

	dbDuty := db.GetUnfinalizedDuty(uint64(es.epoch), es.dependentRoot[:])
	if dbDuty == nil {
		return nil
	}

	values, err := es.parsePackedSSZ(dynSsz, chainState, dbDuty.DutiesSSZ)
	if err != nil {
		return nil
	}

	return values
}

// processState processes the epoch state and computes proposer and attester duties.
func (es *EpochStats) processState(indexer *Indexer) {
	if es.dependentState == nil || es.dependentState.loadingStatus != 2 {
		return
	}
	t1 := time.Now()

	chainState := indexer.consensusPool.GetChainState()
	values := &EpochStatsValues{
		EffectiveBalances:   map[phase0.ValidatorIndex]phase0.Gwei{},
		SyncCommitteeDuties: es.dependentState.syncCommittee,
		TotalBalance:        0,
		ActiveBalance:       0,
		EffectiveBalance:    0,
		FirstDepositIndex:   es.dependentState.depositIndex,
	}

	// get active validator indices & aggregate balances
	activeIndices := []phase0.ValidatorIndex{}
	for index, validator := range es.dependentState.validatorList {
		values.TotalBalance += es.dependentState.validatorBalances[index]
		if es.epoch >= validator.ActivationEpoch && es.epoch < validator.ExitEpoch {
			activeIndices = append(activeIndices, phase0.ValidatorIndex(index))
			values.EffectiveBalances[phase0.ValidatorIndex(index)] = validator.EffectiveBalance
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

	indexer.logger.Debugf("processing epoch %v stats (root: %v / state: %v), validators: %v/%v", es.epoch, es.dependentRoot.String(), es.dependentState.stateRoot.String(), values.ActiveValidators, len(es.dependentState.validatorList))

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
	if beaconState.RandaoMix != nil {
		values.RandaoMix = *beaconState.RandaoMix
		values.NextRandaoMix = *beaconState.NextRandaoMix
	}

	// compute committees
	attesterDuties := [][][]phase0.ValidatorIndex{}
	for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
		committees, err := duties.GetBeaconCommittees(chainState.GetSpecs(), beaconState, slot)
		if err != nil {
			indexer.logger.Warnf("failed computing committees for slot %v: %v", slot, err)
			committees = [][]phase0.ValidatorIndex{}
		}
		attesterDuties = append(attesterDuties, committees)
	}

	values.AttesterDuties = attesterDuties

	es.ready = true
	es.values = values
	es.precalcValues = nil

	packedSsz, _ := es.buildPackedSSZ(indexer.dynSsz)
	dbDuty := &dbtypes.UnfinalizedDuty{
		Epoch:         uint64(es.epoch),
		DependentRoot: es.dependentRoot[:],
		DutiesSSZ:     packedSsz,
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertUnfinalizedDuty(dbDuty, tx)
	})
	if err != nil {
		indexer.logger.WithError(err).Errorf("failed storing epoch %v stats (%v / %v) to unfinalized duties", es.epoch, es.dependentRoot.String(), es.dependentState.stateRoot.String())
	}

	es.isInDb = true

	indexer.logger.Infof(
		"processed epoch %v stats (root: %v / state: %v, validators: %v/%v, %v ms), %v bytes",
		es.epoch,
		es.dependentRoot.String(),
		es.dependentState.stateRoot.String(),
		values.ActiveValidators,
		len(es.dependentState.validatorList),
		time.Since(t1).Milliseconds(),
		len(packedSsz),
	)
}

func (es *EpochStats) precomputeFromParentState(indexer *Indexer, parentState *EpochStats) error {
	es.precalcBaseRoot = parentState.dependentRoot

	return indexer.epochCache.withPrecomputeLock(func() error {
		if es.precalcValues != nil {
			return nil
		}
		t1 := time.Now()

		otherEpochStats := indexer.epochCache.getEpochStatsByEpoch(es.epoch)
		for _, other := range otherEpochStats {
			if other == es {
				continue
			}

			if other.precalcValues != nil && bytes.Equal(other.precalcBaseRoot[:], parentState.dependentRoot[:]) {
				es.precalcValues = other.precalcValues
				return nil
			}
		}

		parentStatsValues := parentState.GetValues(false)
		if parentStatsValues == nil {
			return fmt.Errorf("parent stats values not available")
		}

		values := &EpochStatsValues{
			RandaoMix:           parentStatsValues.NextRandaoMix,
			EffectiveBalances:   parentStatsValues.EffectiveBalances,
			SyncCommitteeDuties: parentStatsValues.SyncCommitteeDuties,
			TotalBalance:        parentStatsValues.TotalBalance,
			ActiveBalance:       parentStatsValues.ActiveBalance,
			EffectiveBalance:    parentStatsValues.EffectiveBalance,
		}

		activeIndices := make([]phase0.ValidatorIndex, len(parentStatsValues.EffectiveBalances))
		i := 0
		for index := range parentStatsValues.EffectiveBalances {
			activeIndices[i] = index
			i++
		}

		sort.Slice(activeIndices, func(i, j int) bool {
			return activeIndices[i] < activeIndices[j]
		})

		values.ActiveValidators = uint64(len(activeIndices))

		beaconState := &duties.BeaconState{
			RandaoMix: &values.RandaoMix,
			GetActiveIndices: func() []phase0.ValidatorIndex {
				return activeIndices
			},
			GetEffectiveBalance: func(index phase0.ValidatorIndex) phase0.Gwei {
				return values.EffectiveBalances[index]
			},
		}
		chainState := indexer.consensusPool.GetChainState()

		// compute proposers
		proposerDuties := []phase0.ValidatorIndex{}
		for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
			proposer, err := duties.GetProposerIndex(chainState.GetSpecs(), beaconState, slot)
			if err != nil {
				proposer = math.MaxInt64
			}

			proposerDuties = append(proposerDuties, proposer)
		}

		values.ProposerDuties = proposerDuties

		// compute committees
		attesterDuties := [][][]phase0.ValidatorIndex{}
		for slot := chainState.EpochToSlot(es.epoch); slot < chainState.EpochToSlot(es.epoch+1); slot++ {
			committees, err := duties.GetBeaconCommittees(chainState.GetSpecs(), beaconState, slot)
			if err != nil {
				committees = [][]phase0.ValidatorIndex{}
			}

			attesterDuties = append(attesterDuties, committees)
		}

		values.AttesterDuties = attesterDuties

		es.precalcValues = values

		indexer.logger.Infof(
			"precomputed epoch %v stats (root: %v), validators: %v/%v (%v ms)",
			es.epoch,
			es.dependentRoot.String(),
			values.ActiveValidators,
			len(parentStatsValues.EffectiveBalances),
			time.Since(t1).Milliseconds(),
		)

		return nil
	})
}

// GetValues returns the EpochStats values.
func (es *EpochStats) GetValues(withPrecalc bool) *EpochStatsValues {
	if es == nil {
		return nil
	}

	if es.values != nil {
		return es.values
	}

	if es.prunedValues != nil {
		return es.prunedValues
	}

	if es.precalcValues != nil && withPrecalc {
		return es.precalcValues
	}

	return nil
}

func (es *EpochStats) GetOrLoadValues(indexer *Indexer, withPrecalc bool) *EpochStatsValues {
	if es == nil {
		return nil
	}

	if es.values != nil {
		return es.values
	}

	if es.isInDb {
		values := es.loadValuesFromDb(indexer.dynSsz, indexer.consensusPool.GetChainState())
		if values != nil {
			es.values = values
			return values
		}
	}

	if es.precalcValues != nil && withPrecalc {
		return es.precalcValues
	}

	return nil
}
