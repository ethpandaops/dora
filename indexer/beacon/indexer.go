package beacon

import (
	"runtime/debug"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/ethwallclock"
)

// Indexer is responsible for indexing the ethereum beacon chain.
type Indexer struct {
	logger        logrus.FieldLogger
	consensusPool *consensus.Pool

	// configuration
	writeDb               bool
	disableSync           bool
	inMemoryEpochs        uint16
	maxParallelStateCalls uint16
	cachePersistenceDelay uint16

	// state
	running    bool
	dynSsz     *dynssz.DynSsz
	clients    []*Client
	blockCache *blockCache
	epochCache *epochCache

	// worker state
	lastFinalizedEpoch    phase0.Epoch
	lastFinalizedRoot     phase0.Root
	lastPruningEpoch      phase0.Epoch
	finalitySubscription  *consensus.Subscription[*v1.Finality]
	wallclockSubscription *consensus.Subscription[*ethwallclock.Slot]
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

	// initialize dynamic SSZ encoder
	staticSpec := map[string]any{}
	specYaml, err := yaml.Marshal(consensusPool.GetChainState().GetSpecs())
	if err != nil {
		yaml.Unmarshal(specYaml, staticSpec)
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

		dynSsz:  dynssz.NewDynSsz(staticSpec),
		clients: make([]*Client, 0),
	}

	indexer.blockCache = newBlockCache(indexer)
	indexer.epochCache = newEpochCache(indexer)

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

	err := db.StreamUnfinalizedBlocks(func(dbBlock *dbtypes.UnfinalizedBlock) {
		if dbBlock.Slot < uint64(finalizedSlot) {
			return
		}

		block, _ := indexer.blockCache.createOrGetBlock(phase0.Root(dbBlock.Root), phase0.Slot(dbBlock.Slot))
		block.isInUnfinalizedDb = true

		if dbBlock.HeaderVer != 1 {
			indexer.logger.Warnf("failed unmarshal unfinalized block header %v [%x] from db: unsupported header version", dbBlock.Slot, dbBlock.Root)
			return
		}

		header := &phase0.SignedBeaconBlockHeader{}
		err := header.UnmarshalSSZ(dbBlock.HeaderSSZ)
		if err != nil {
			indexer.logger.Warnf("failed unmarshal unfinalized block header %v [%x] from db: %v", dbBlock.Slot, dbBlock.Root, err)
			return
		}

		block.SetHeader(header)

		blockBody, err := unmarshalVersionedSignedBeaconBlockSSZ(indexer.dynSsz, dbBlock.BlockVer, dbBlock.BlockSSZ)
		if err != nil {
			indexer.logger.Warnf("could not restore unfinalized block body %v [%x] from db: %v", dbBlock.Slot, dbBlock.Root, err)
		} else if dbBlock.Status == 0 {
			block.SetBlock(blockBody)
			restoredBodyCount++
		} else {
			block.setBlockIndex(blockBody)
			block.isInFinalizedDb = true
		}

		restoredBlockCount++
	})
	if err != nil {
		indexer.logger.WithError(err).Errorf("failed restoring unfinalized blocks from DB")
	} else {
		indexer.logger.Infof("restored %v unfinalized blocks from DB (%v with bodies)", restoredBlockCount, restoredBodyCount)
	}

	// start indexing for all clients
	for _, client := range indexer.clients {
		client.startIndexing()
	}

	// add indexer event handlers
	indexer.finalitySubscription = indexer.consensusPool.SubscribeFinalizedEvent(10)
	indexer.wallclockSubscription = indexer.consensusPool.SubscribeWallclockSlotEvent(1)
	finalizedEpoch, finalizedRoot := chainState.GetFinalizedCheckpoint()
	indexer.lastFinalizedEpoch = finalizedEpoch
	indexer.lastFinalizedRoot = finalizedRoot
	indexer.lastPruningEpoch = chainState.CurrentEpoch()

	go indexer.runIndexerLoop()
}

func (indexer *Indexer) runIndexerLoop() {
	defer func() {
		if err := recover(); err != nil {
			indexer.logger.WithError(err.(error)).Errorf("uncaught panic in indexer.beacon.Indexer.runIndexerLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go indexer.runIndexerLoop()
		}
	}()

	chainState := indexer.consensusPool.GetChainState()

	for {
		select {
		case finalityEvent := <-indexer.finalitySubscription.Channel():
			err := indexer.processFinalityEvent(finalityEvent)
			if err != nil {
				indexer.logger.WithError(err).Errorf("error processing finality event (epoch: %v, root: %v)", finalityEvent.Finalized.Epoch, finalityEvent.Finalized.Root.String())
			}
		case slotEvent := <-indexer.wallclockSubscription.Channel():
			epoch := chainState.EpochOfSlot(phase0.Slot(slotEvent.Number()))
			slotIndex := chainState.SlotToSlotIndex(phase0.Slot(slotEvent.Number()))
			slotProgress := uint8(100 / chainState.GetSpecs().SlotsPerEpoch * uint64(slotIndex))

			// prune cache if last pruning epoch is outdated and we are at least 50% into the current
			if epoch > indexer.lastPruningEpoch && slotProgress >= 50 {
				err := indexer.processCachePruning()
				if err != nil {
					indexer.logger.WithError(err).Errorf("failed pruning cache")
				}

				indexer.lastPruningEpoch = epoch
			}

		}
	}
}
