package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/clients/sshtunnel"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/rpc"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

type ChainService struct {
	logger         logrus.FieldLogger
	consensusPool  *consensus.Pool
	executionPool  *execution.Pool
	beaconIndexer  *beacon.Indexer
	validatorNames *ValidatorNames

	indexer *indexer.Indexer

	validatorActivityMutex sync.Mutex
	validatorActivityStats struct {
		cacheEpoch uint64
		epochLimit uint64
		activity   map[uint64]uint8
	}

	assignmentsCacheMux sync.Mutex
	assignmentsCache    *lru.Cache[uint64, *rpc.EpochAssignments]
}

var GlobalBeaconService *ChainService

// StartChainService is used to start the global beaconchain service
func StartChainService(ctx context.Context, logger logrus.FieldLogger) error {
	if GlobalBeaconService != nil {
		return nil
	}

	// initialize client pools & indexers
	consensusPool := consensus.NewPool(ctx, logger)
	executionPool := execution.NewPool(ctx, logger)
	beaconIndexer := beacon.NewIndexer(logger, consensusPool)

	// add consensus clients
	for index, endpoint := range utils.Config.BeaconApi.Endpoints {
		endpointConfig := &consensus.ClientConfig{
			URL:     endpoint.Url,
			Name:    endpoint.Name,
			Headers: endpoint.Headers,
		}

		if endpoint.Ssh != nil {
			endpointConfig.SshConfig = &sshtunnel.SshConfig{
				Host:     endpoint.Ssh.Host,
				Port:     endpoint.Ssh.Port,
				User:     endpoint.Ssh.User,
				Password: endpoint.Ssh.Password,
				Keyfile:  endpoint.Ssh.Keyfile,
			}
		}

		client, err := consensusPool.AddEndpoint(endpointConfig)
		if err != nil {
			logger.Errorf("could not add beacon client '%v' to pool: %v", endpoint.Name, err)
			continue
		}

		beaconIndexer.AddClient(uint16(index), client, endpoint.Priority, endpoint.Archive, endpoint.SkipValidators)
	}

	if len(consensusPool.GetAllEndpoints()) == 0 {
		return fmt.Errorf("no beacon clients configured")
	}

	// add execution clients
	for _, endpoint := range utils.Config.ExecutionApi.Endpoints {
		endpointConfig := &execution.ClientConfig{
			URL:     endpoint.Url,
			Name:    endpoint.Name,
			Headers: endpoint.Headers,
		}

		if endpoint.Ssh != nil {
			endpointConfig.SshConfig = &sshtunnel.SshConfig{
				Host:     endpoint.Ssh.Host,
				Port:     endpoint.Ssh.Port,
				User:     endpoint.Ssh.User,
				Password: endpoint.Ssh.Password,
				Keyfile:  endpoint.Ssh.Keyfile,
			}
		}

		_, err := executionPool.AddEndpoint(endpointConfig)
		if err != nil {
			logger.Errorf("could not add execution client '%v' to pool: %v", endpoint.Name, err)
			continue
		}
	}

	// init validator names & load inventory
	validatorNames := NewValidatorNames()
	validatorNamesLoading := validatorNames.LoadValidatorNames()

	// reset sync state if configured
	if utils.Config.Indexer.ResyncFromEpoch != nil {
		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			syncState := &dbtypes.IndexerSyncState{
				Epoch: *utils.Config.Indexer.ResyncFromEpoch,
			}
			return db.SetExplorerState("indexer.syncstate", syncState, tx)
		})
		if err != nil {
			return fmt.Errorf("failed resetting sync state: %v", err)
		}
		logger.Warnf("Reset explorer synchronization status to epoch %v as configured! Please remove this setting again.", *utils.Config.Indexer.ResyncFromEpoch)
	}

	// await validator names & beacon pool readiness
	<-validatorNamesLoading
	lastLog := time.Now()
	for {
		specs := consensusPool.GetChainState().GetSpecs()
		if specs != nil {
			break
		}

		if time.Since(lastLog) > 10*time.Second {
			logger.Warnf("still waiting for chain specs... need at least 1 consensus client to load chain specs from.")
		}

		time.Sleep(1 * time.Second)
	}

	// start chain indexer
	beaconIndexer.StartIndexer()

	// legacy indexer
	/*
		indexer, err := indexer.NewIndexer()
		if err != nil {
			return err
		}

		for idx, endpoint := range utils.Config.BeaconApi.Endpoints {
			indexer.AddConsensusClient(uint16(idx), &endpoint)
		}

		for idx, endpoint := range utils.Config.ExecutionApi.Endpoints {
			indexer.AddExecutionClient(uint16(idx), &endpoint)
		}

		// start validator names updater
		validatorNames.StartUpdater(indexer)

		// start mev index updater
		mevIndexer := NewMevIndexer()
		mevIndexer.StartUpdater(indexer)
	*/

	GlobalBeaconService = &ChainService{
		logger:           logger,
		consensusPool:    consensusPool,
		executionPool:    executionPool,
		beaconIndexer:    beaconIndexer,
		validatorNames:   validatorNames,
		assignmentsCache: lru.NewCache[uint64, *rpc.EpochAssignments](10),
	}
	return nil
}

func (bs *ChainService) GetIndexer() *indexer.Indexer {
	return bs.indexer
}

func (bs *ChainService) GetBeaconIndexer() *beacon.Indexer {
	return bs.beaconIndexer
}

func (bs *ChainService) GetConsensusClients() []*consensus.Client {
	return bs.consensusPool.GetAllEndpoints()
}

func (bs *ChainService) GetExecutionClients() []*execution.Client {
	return bs.executionPool.GetAllEndpoints()
}

func (bs *ChainService) GetHeadForks(readyOnly bool) []*indexer.HeadFork {
	return bs.indexer.GetHeadForks(readyOnly)
}

func (bs *ChainService) GetValidatorName(index uint64) string {
	return bs.validatorNames.GetValidatorName(index)
}

func (bs *ChainService) GetValidatorNamesCount() uint64 {
	return bs.validatorNames.GetValidatorNamesCount()
}

func (bs *ChainService) GetCachedValidatorSet() map[phase0.ValidatorIndex]*v1.Validator {
	return bs.indexer.GetCachedValidatorSet()
}

func (bs *ChainService) GetCachedValidatorPubkeyMap() map[phase0.BLSPubKey]*v1.Validator {
	return bs.indexer.GetCachedValidatorPubkeyMap()
}

func (bs *ChainService) GetFinalizedEpoch() (int64, []byte) {
	finalizedEpoch, finalizedRoot, _, _ := bs.indexer.GetFinalizationCheckpoints()
	return finalizedEpoch, finalizedRoot
}

func (bs *ChainService) GetCachedEpochStats(epoch uint64) *indexer.EpochStats {
	return bs.indexer.GetCachedEpochStats(epoch)
}

func (bs *ChainService) GetGenesis() (*v1.Genesis, error) {
	return bs.indexer.GetCachedGenesis(), nil
}

func (bs *ChainService) GetValidatorActivity() (map[uint64]uint8, uint64) {
	activityMap := map[uint64]uint8{}
	epochLimit := uint64(3)

	idxHeadSlot := bs.indexer.GetHighestSlot()
	idxHeadEpoch := utils.EpochOfSlot(idxHeadSlot)
	if idxHeadEpoch < 1 {
		return activityMap, 0
	}
	idxHeadEpoch--
	finalizedEpoch, _ := bs.GetFinalizedEpoch()
	var idxMinEpoch uint64
	if finalizedEpoch < 2 {
		idxMinEpoch = 0
	} else {
		idxMinEpoch = uint64(finalizedEpoch - 1)
	}

	activityEpoch := utils.EpochOfSlot(idxHeadSlot - 1)
	bs.validatorActivityMutex.Lock()
	defer bs.validatorActivityMutex.Unlock()
	if bs.validatorActivityStats.activity != nil && bs.validatorActivityStats.cacheEpoch == activityEpoch {
		return bs.validatorActivityStats.activity, bs.validatorActivityStats.epochLimit
	}

	actualEpochCount := idxHeadEpoch - idxMinEpoch + 1
	if actualEpochCount > epochLimit {
		idxMinEpoch = idxHeadEpoch - epochLimit + 1
	} else if actualEpochCount < epochLimit {
		epochLimit = actualEpochCount
	}

	for epochIdx := int64(idxHeadEpoch); epochIdx >= int64(idxMinEpoch); epochIdx-- {
		epoch := uint64(epochIdx)
		_, epochVotes := bs.indexer.GetEpochVotes(epoch)
		if epochVotes == nil {
			epochLimit--
		} else {
			for valIdx := range epochVotes.ActivityMap {
				activityMap[valIdx]++
			}
		}
	}

	bs.validatorActivityStats.cacheEpoch = activityEpoch
	bs.validatorActivityStats.epochLimit = epochLimit
	bs.validatorActivityStats.activity = activityMap
	return activityMap, epochLimit
}
