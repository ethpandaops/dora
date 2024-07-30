package beacon

import (
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
)

// Indexer is responsible for indexing the ethereum beacon chain.
type Indexer struct {
	logger        logrus.FieldLogger
	consensusPool *consensus.Pool

	writeDb               bool
	disableSync           bool
	inMemoryEpochs        uint16
	maxParallelStateCalls uint16
	cachePersistenceDelay uint16

	running            bool
	clients            []*Client
	blockCache         *blockCache
	epochCache         *epochCache
	finalizationWorker *finalizationWorker
}

// NewIndexer creates a new instance of the Indexer.
func NewIndexer(logger logrus.FieldLogger, consensusPool *consensus.Pool) *Indexer {
	// Initialize the indexer with default values from the configuration.
	inMemoryEpochs := utils.Config.Indexer.InMemoryEpochs
	if inMemoryEpochs < 2 {
		inMemoryEpochs = 2
	}
	cachePersistenceDelay := utils.Config.Indexer.CachePersistenceDelay
	if cachePersistenceDelay < 2 {
		cachePersistenceDelay = 2
	}
	maxParallelStateCalls := uint16(utils.Config.Indexer.MaxParallelValidatorSetRequests)
	if maxParallelStateCalls < 2 {
		maxParallelStateCalls = 2
	}

	// Create the indexer instance.
	indexer := &Indexer{
		logger:        logger,
		consensusPool: consensusPool,

		writeDb:               !utils.Config.Indexer.DisableIndexWriter,
		disableSync:           utils.Config.Indexer.DisableSynchronizer,
		inMemoryEpochs:        inMemoryEpochs,
		maxParallelStateCalls: maxParallelStateCalls,
		cachePersistenceDelay: cachePersistenceDelay,

		clients: make([]*Client, 0),
	}

	indexer.blockCache = newBlockCache()
	indexer.epochCache = newEpochCache(indexer)
	indexer.finalizationWorker = newFinalizationWorker(indexer)

	return indexer
}

// getMinInMemorySlot returns the minimum in-memory slot based on the indexer's configuration.
func (indexer *Indexer) getMinInMemorySlot() phase0.Slot {
	chainState := indexer.consensusPool.GetChainState()
	minInMemoryEpoch := chainState.CurrentEpoch()
	if minInMemoryEpoch > phase0.Epoch(indexer.inMemoryEpochs) {
		minInMemoryEpoch -= phase0.Epoch(indexer.inMemoryEpochs)
	} else {
		minInMemoryEpoch = 0
	}

	return chainState.EpochToSlot(minInMemoryEpoch)
}

// AddClient adds a new consensus pool client to the indexer.
func (indexer *Indexer) AddClient(index uint16, client *consensus.Client, priority int, archive bool, skipValidators bool) *Client {
	logger := indexer.logger.WithField("client", client.GetName())
	indexerClient := newClient(index, client, priority, archive, skipValidators, indexer, logger)
	indexer.clients = append(indexer.clients, indexerClient)

	return indexerClient
}

// StartIndexer starts the indexing process.
func (indexer *Indexer) StartIndexer() {
	if indexer.running {
		return
	}

	indexer.running = true

	// prefill block cache with all unfinalized blocks from db
	chainState := indexer.consensusPool.GetChainState()
	finalizedSlot := chainState.GetFinalizedSlot()
	restoredBlockCount := 0
	restoredBodyCount := 0

	loadMinSlot := indexer.getMinInMemorySlot()
	if loadMinSlot < finalizedSlot {
		loadMinSlot = finalizedSlot
	}

	dbBlocks := db.GetUnfinalizedBlocks(&dbtypes.UnfinalizedBlockFilter{
		MinSlot:  uint64(loadMinSlot),
		WithBody: true,
	})
	if loadMinSlot > finalizedSlot {
		dbBlocksWithoutBodies := db.GetUnfinalizedBlocks(&dbtypes.UnfinalizedBlockFilter{
			MinSlot:  uint64(finalizedSlot),
			MaxSlot:  uint64(loadMinSlot) - 1,
			WithBody: false,
		})
		dbBlocks = append(dbBlocks, dbBlocksWithoutBodies...)
	}

	for _, dbBlock := range dbBlocks {
		if dbBlock.Slot < uint64(finalizedSlot) {
			continue
		}

		block, _ := indexer.blockCache.createOrGetBlock(phase0.Root(dbBlock.Root), phase0.Slot(dbBlock.Slot))
		block.isInUnfinalizedDb = true

		if dbBlock.HeaderVer != 1 {
			indexer.logger.Warnf("failed unmarshal unfinalized block header %v [%x] from db: unsupported header version", dbBlock.Slot, dbBlock.Root)
			continue
		}

		header := &phase0.SignedBeaconBlockHeader{}
		err := header.UnmarshalSSZ(dbBlock.HeaderSSZ)
		if err != nil {
			indexer.logger.Warnf("failed unmarshal unfinalized block header %v [%x] from db: %v", dbBlock.Slot, dbBlock.Root, err)
			continue
		}

		block.SetHeader(header)

		if dbBlock.BlockSSZ != nil {
			blockBody, err := UnmarshalVersionedSignedBeaconBlockSSZ(chainState.GetSpecs(), dbBlock.BlockVer, dbBlock.BlockSSZ)
			if err != nil {
				indexer.logger.Warnf("could not restore unfinalized block body %v [%x] from db: %v", dbBlock.Slot, dbBlock.Root, err)
			} else {
				block.SetBlock(blockBody)
				restoredBodyCount++
			}
		}

		restoredBlockCount++
	}

	indexer.logger.Infof("restored %v unfinalized blocks from DB (%v with bodies)", restoredBlockCount, restoredBodyCount)

	// start indexing for all clients
	for _, client := range indexer.clients {
		client.startIndexing()
	}
}
