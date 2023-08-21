package indexer

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type EpochStats struct {
	epoch               uint64
	dependendRoot       []byte
	dutiesInDb          bool
	dutiesMutex         sync.RWMutex
	validatorsMutex     sync.RWMutex
	proposerAssignments map[uint64]uint64
	attestorAssignments map[string][]uint64
	syncAssignments     []uint64
	validatorStats      *EpochValidatorStats
}

type EpochValidatorStats struct {
	ValidatorCount    uint64
	ValidatorBalance  uint64
	EligibleAmount    uint64
	ValidatorBalances map[uint64]uint64
}

func (cache *indexerCache) getEpochStats(epoch uint64, dependendRoot []byte) *EpochStats {
	cache.epochStatsMutex.RLock()
	defer cache.epochStatsMutex.RUnlock()
	if cache.epochStatsMap[epoch] != nil {
		for _, epochStats := range cache.epochStatsMap[epoch] {
			if bytes.Equal(epochStats.dependendRoot, dependendRoot) {
				return epochStats
			}
		}
	}
	return nil
}

func (cache *indexerCache) createOrGetEpochStats(epoch uint64, dependendRoot []byte) (*EpochStats, bool) {
	cache.epochStatsMutex.Lock()
	defer cache.epochStatsMutex.Unlock()
	if cache.epochStatsMap[epoch] == nil {
		cache.epochStatsMap[epoch] = make([]*EpochStats, 0)
	} else {
		for _, epochStats := range cache.epochStatsMap[epoch] {
			if bytes.Equal(epochStats.dependendRoot, dependendRoot) {
				return epochStats, false
			}
		}
	}
	epochStats := &EpochStats{
		epoch:         epoch,
		dependendRoot: dependendRoot,
	}
	cache.epochStatsMap[epoch] = append(cache.epochStatsMap[epoch], epochStats)
	return epochStats, true
}

func (client *indexerClient) ensureEpochStats(epoch uint64, head []byte) error {
	var dependendRoot []byte
	var proposerRsp *rpctypes.StandardV1ProposerDutiesResponse
	if epoch > 0 {
		firstBlock := client.indexerCache.getFirstCanonicalBlock(epoch, head)
		if firstBlock != nil {
			logger.WithField("client", client.clientName).Debugf("canonical first block for epoch %v: %v/0x%x (head: 0x%x)", epoch, firstBlock.slot, firstBlock.root, head)
			firstBlock.mutex.RLock()
			if firstBlock.header != nil {
				dependendRoot = firstBlock.header.Message.ParentRoot
			}
			firstBlock.mutex.RUnlock()
		}
		if dependendRoot == nil {
			lastBlock := client.indexerCache.getLastCanonicalBlock(epoch-1, head)
			if lastBlock != nil {
				logger.WithField("client", client.clientName).Debugf("canonical last block for epoch %v: %v/0x%x (head: 0x%x)", epoch-1, lastBlock.slot, lastBlock.root, head)
				dependendRoot = lastBlock.root
			}
		}
	}
	if dependendRoot == nil {
		var err error
		proposerRsp, err = client.rpcClient.GetProposerDuties(epoch)
		if err != nil {
			logger.WithField("client", client.clientName).Warnf("Could not load proposer duties for epoch %v: %v", epoch, err)
		}
		if proposerRsp == nil {
			return fmt.Errorf("Could not find proposer duties for epoch %v", epoch)
		}
		dependendRoot = proposerRsp.DependentRoot
	}

	epochStats, isNewStats := client.indexerCache.createOrGetEpochStats(epoch, dependendRoot)
	if isNewStats {
		logger.WithField("client", client.clientName).Infof("Load epoch stats for epoch %v (dependend: 0x%x)", epoch, dependendRoot)
	} else {
		logger.WithField("client", client.clientName).Debugf("Ensure epoch stats for epoch %v (dependend: 0x%x)", epoch, dependendRoot)
	}
	go epochStats.ensureEpochStatsLazy(client, proposerRsp)
	if int64(epoch) > client.lastEpochStats {
		client.lastEpochStats = int64(epoch)
	}
	return nil
}

func (epochStats *EpochStats) ensureEpochStatsLazy(client *indexerClient, proposerRsp *rpctypes.StandardV1ProposerDutiesResponse) {
	defer func() {
		if err := recover(); err != nil {
			logger.WithField("client", client.clientName).Errorf("Uncaught panig in ensureEpochStats subroutine: %v", err)
		}
	}()

	if epochStats.dutiesInDb {
		return
	}
	epochStats.dutiesMutex.Lock()
	defer epochStats.dutiesMutex.Unlock()

	// proposer duties
	if epochStats.proposerAssignments == nil {
		if proposerRsp == nil {
			var err error
			proposerRsp, err = client.rpcClient.GetProposerDuties(epochStats.epoch)
			if err != nil {
				logger.WithField("client", client.clientName).Warnf("Could not lazy load proposer duties for epoch %v: %v", epochStats.epoch, err)
			}
			if proposerRsp == nil {
				logger.WithField("client", client.clientName).Errorf("Could not find proposer duties for epoch %v", epochStats.epoch)
				return
			}
			if !bytes.Equal(proposerRsp.DependentRoot, epochStats.dependendRoot) {
				logger.WithField("client", client.clientName).Errorf("Got unexpected dependend root for proposer duties %v (got: %v, expected: 0x%x)", epochStats.epoch, proposerRsp.DependentRoot.String(), epochStats.dependendRoot)
				return
			}
		}
		epochStats.proposerAssignments = map[uint64]uint64{}
		for _, duty := range proposerRsp.Data {
			epochStats.proposerAssignments[uint64(duty.Slot)] = uint64(duty.ValidatorIndex)
		}
	}

	// get state root for dependend root
	var depStateRoot string
	getStateRoot := func() string {
		if depStateRoot == "" {
			if epochStats.epoch == 0 {
				depStateRoot = "genesis"
			} else if dependendBlock := client.indexerCache.getCachedBlock(epochStats.dependendRoot); dependendBlock != nil {
				dependendBlock.mutex.RLock()
				depStateRoot = dependendBlock.header.Message.StateRoot.String()
				dependendBlock.mutex.RUnlock()
			} else {
				parsedHeader, err := client.rpcClient.GetBlockHeaderByBlockroot(epochStats.dependendRoot)
				if err != nil {
					logger.WithField("client", client.clientName).Errorf("Could not get dependent block header for epoch %v (0x%x)", epochStats.epoch, epochStats.dependendRoot)
				}
				depStateRoot = parsedHeader.Data.Header.Message.StateRoot.String()
			}
		}
		return depStateRoot
	}

	// load validators
	if epochStats.validatorStats == nil {
		go epochStats.ensureValidatorStatsLazy(client, getStateRoot())
	}

	// get committee duties
	if epochStats.attestorAssignments == nil {
		if getStateRoot() == "" {
			return
		}
		parsedCommittees, err := client.rpcClient.GetCommitteeDuties(depStateRoot, epochStats.epoch)
		if err != nil {
			logger.WithField("client", client.clientName).Errorf("error retrieving committees data: %v", err)
			return
		}
		epochStats.attestorAssignments = map[string][]uint64{}
		for _, committee := range parsedCommittees.Data {
			for i, valIndex := range committee.Validators {
				valIndexU64, err := strconv.ParseUint(valIndex, 10, 64)
				if err != nil {
					logger.WithField("client", client.clientName).Warnf("epoch %d committee %d index %d has bad validator index %q", epochStats.epoch, committee.Index, i, valIndex)
					continue
				}
				k := fmt.Sprintf("%v-%v", uint64(committee.Slot), uint64(committee.Index))
				if epochStats.attestorAssignments[k] == nil {
					epochStats.attestorAssignments[k] = make([]uint64, 0)
				}
				epochStats.attestorAssignments[k] = append(epochStats.attestorAssignments[k], valIndexU64)
			}
		}
	}

	// get sync committee duties
	if epochStats.syncAssignments == nil && epochStats.epoch >= utils.Config.Chain.Config.AltairForkEpoch {
		syncCommitteeState := fmt.Sprintf("%s", getStateRoot())
		if epochStats.epoch > 0 && epochStats.epoch == utils.Config.Chain.Config.AltairForkEpoch {
			syncCommitteeState = fmt.Sprintf("%d", utils.Config.Chain.Config.AltairForkEpoch*utils.Config.Chain.Config.SlotsPerEpoch)
		}
		if syncCommitteeState == "" {
			return
		}
		parsedSyncCommittees, err := client.rpcClient.GetSyncCommitteeDuties(syncCommitteeState, epochStats.epoch)
		if err != nil {
			logger.WithField("client", client.clientName).Errorf("error retrieving sync_committees for epoch %v (state: %v): %v", epochStats.epoch, syncCommitteeState, err)
		}
		epochStats.syncAssignments = make([]uint64, len(parsedSyncCommittees.Data.Validators))
		for i, valIndexStr := range parsedSyncCommittees.Data.Validators {
			valIndexU64, err := strconv.ParseUint(valIndexStr, 10, 64)
			if err != nil {
				logger.WithField("client", client.clientName).Errorf("in sync_committee for epoch %d validator %d has bad validator index: %q", epochStats.epoch, i, valIndexStr)
				continue
			}
			epochStats.syncAssignments[i] = valIndexU64
		}
	}
}

func (epochStats *EpochStats) ensureValidatorStatsLazy(client *indexerClient, stateRef string) {
	defer func() {
		if err := recover(); err != nil {
			logger.WithField("client", client.clientName).Errorf("Uncaught panig in ensureValidatorStats subroutine: %v", err)
		}
	}()

	epochStats.validatorsMutex.Lock()
	defer epochStats.validatorsMutex.Unlock()
	if epochStats.validatorStats != nil {
		return
	}

	// `lock` concurrency limit (limit concurrent get validators calls)
	client.indexerCache.validatorLoadingLimiter <- 1

	var epochValidators *rpctypes.StandardV1StateValidatorsResponse
	var err error
	if epochStats.epoch == 0 {
		epochValidators, err = client.rpcClient.GetGenesisValidators()
	} else {
		epochValidators, err = client.rpcClient.GetStateValidators(stateRef)
	}

	// `unlock` concurrency limit
	<-client.indexerCache.validatorLoadingLimiter

	if err != nil {
		logger.Errorf("Error fetching epoch %v validators: %v", epochStats.epoch, err)
		return
	}
	client.indexerCache.setLastValidators(epochStats.epoch, epochValidators)
	validatorStats := &EpochValidatorStats{
		ValidatorBalances: make(map[uint64]uint64),
	}
	for idx := 0; idx < len(epochValidators.Data); idx++ {
		validator := epochValidators.Data[idx]
		validatorStats.ValidatorBalances[uint64(validator.Index)] = uint64(validator.Validator.EffectiveBalance)
		if !strings.HasPrefix(validator.Status, "active") {
			continue
		}
		validatorStats.ValidatorCount++
		validatorStats.ValidatorBalance += uint64(validator.Balance)
		validatorStats.EligibleAmount += uint64(validator.Validator.EffectiveBalance)
	}
	epochStats.validatorStats = validatorStats
}
