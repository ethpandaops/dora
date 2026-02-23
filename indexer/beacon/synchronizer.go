package beacon

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/blockdb"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

type synchronizer struct {
	indexer *Indexer
	logger  logrus.FieldLogger

	syncCtx       context.Context
	syncCtxCancel context.CancelFunc
	runMutex      sync.Mutex

	stateMutex   sync.Mutex
	running      bool
	currentEpoch phase0.Epoch

	cachedSlot   phase0.Slot
	cachedBlocks map[phase0.Slot]*Block
}

func (indexer *Indexer) startSynchronizer(startEpoch phase0.Epoch) {
	if indexer.disableSync {
		return
	}
	if !indexer.synchronizer.isEpochAhead(startEpoch) || !indexer.synchronizer.running {
		indexer.synchronizer.startSync(startEpoch)
	}
}

func newSynchronizer(indexer *Indexer, logger logrus.FieldLogger) *synchronizer {
	sync := &synchronizer{
		indexer: indexer,
		logger:  logger,
	}

	// restore sync state
	syncState := &dbtypes.IndexerSyncState{}
	if _, err := db.GetExplorerState(indexer.ctx, "indexer.syncstate", syncState); err == nil {
		sync.currentEpoch = phase0.Epoch(syncState.Epoch)
	}

	return sync
}

func (s *synchronizer) isEpochAhead(epoch phase0.Epoch) bool {
	s.stateMutex.Lock()
	defer s.stateMutex.Unlock()
	if s.running {
		if s.currentEpoch < epoch {
			return true
		}
	}
	return false
}

func (s *synchronizer) startSync(startEpoch phase0.Epoch) {
	s.stopSync()

	// start synchronizer
	s.stateMutex.Lock()
	defer s.stateMutex.Unlock()
	if s.running {
		s.logger.Errorf("cannot start synchronizer: already running")
		return
	}
	if startEpoch < s.currentEpoch {
		s.currentEpoch = startEpoch
	}
	s.running = true

	ctx, cancel := context.WithCancel(s.indexer.ctx)
	s.syncCtx = ctx
	s.syncCtxCancel = cancel

	go s.runSync()
}

func (s *synchronizer) stopSync() {
	var lockedMutex *sync.Mutex
	defer func() {
		if lockedMutex != nil {
			lockedMutex.Unlock()
		}
	}()

	s.stateMutex.Lock()
	lockedMutex = &s.stateMutex
	if s.running {
		s.syncCtxCancel()
	} else {
		return
	}
	s.stateMutex.Unlock()
	lockedMutex = nil

	s.runMutex.Lock()
	lockedMutex = &s.runMutex
}

func (s *synchronizer) runSync() {
	defer utils.HandleSubroutinePanic("runSync", nil)

	s.runMutex.Lock()
	defer s.runMutex.Unlock()

	defer func() {
		s.running = false
		s.syncCtxCancel()
	}()

	s.cachedBlocks = make(map[phase0.Slot]*Block)
	s.cachedSlot = 0
	isComplete := false
	retryCount := 0

	s.logger.Infof("synchronization started. head epoch: %v", s.currentEpoch)

	for {
		// synchronize next epoch
		syncEpoch := s.currentEpoch
		syncClients := s.getSyncClients(syncEpoch)
		if len(syncClients) == 0 {
			s.logger.Warnf("no clients available for synchronization of epoch %v", syncEpoch)

			// wait for 10 seconds before retrying
			time.Sleep(10 * time.Second)
			continue
		}

		if syncEpoch >= s.indexer.lastFinalizedEpoch {
			isComplete = true
			break
		}

		retryLimit := len(syncClients)
		if retryLimit < 30 {
			retryLimit = 30
		}
		lastRetry := retryCount >= retryLimit
		syncClient := syncClients[retryCount%len(syncClients)]

		synclogger := s.logger.WithFields(logrus.Fields{
			"epoch":  syncEpoch,
			"client": syncClient.client.GetName(),
		})

		if lastRetry {
			synclogger.Infof("synchronizing epoch %v (retry: %v, last retry!)", syncEpoch, retryCount)
		} else if retryCount > 0 {
			synclogger.Infof("synchronizing epoch %v (retry: %v)", syncEpoch, retryCount)
		} else {
			synclogger.Infof("synchronizing epoch %v", syncEpoch)
		}

		done, err := s.syncEpoch(syncEpoch, syncClient, lastRetry)
		if done || lastRetry {
			if err != nil {
				s.logger.Errorf("synchronization of epoch %v failed: %v - skipping epoch", syncEpoch, err)
			}
			retryCount = 0
			s.stateMutex.Lock()
			syncEpoch++
			s.currentEpoch = syncEpoch
			s.stateMutex.Unlock()
			if syncEpoch >= s.indexer.lastFinalizedEpoch {
				isComplete = true
				break
			}
		} else if err != nil {
			synclogger.Warnf("synchronization of epoch %v failed: %v - Retrying in 10 sec...", syncEpoch, err)
			retryCount++
			time.Sleep(10 * time.Second)
		}

		if s.syncCtx.Err() != nil {
			break
		}
	}

	if isComplete {
		s.logger.Infof("synchronization complete. Head epoch: %v", s.currentEpoch)
		db.RunDBTransaction(func(tx *sqlx.Tx) error {
			return db.SetExplorerState(s.syncCtx, tx, "indexer.syncstate", &dbtypes.IndexerSyncState{
				Epoch: uint64(s.currentEpoch),
			})
		})
	} else {
		s.logger.Infof("synchronization aborted. Head epoch: %v", s.currentEpoch)
	}

	s.running = false
}

func (s *synchronizer) getSyncClients(epoch phase0.Epoch) []*Client {
	archiveClients := make([]*Client, 0)
	normalClients := make([]*Client, 0)

	for _, client := range s.indexer.clients {
		if client.client.GetStatus() != consensus.ClientStatusOnline {
			continue
		}

		if client.skipValidators {
			continue
		}

		finalizedEpoch, _, _, _ := client.client.GetFinalityCheckpoint()
		if finalizedEpoch < epoch {
			continue
		}

		if client.archive {
			archiveClients = append(archiveClients, client)
		} else {
			normalClients = append(normalClients, client)
		}
	}

	sort.Slice(archiveClients, func(i, j int) bool {
		if archiveClients[i].priority == archiveClients[j].priority {
			return rand.UintN(2) == 0
		}
		return archiveClients[i].priority > archiveClients[j].priority
	})

	sort.Slice(normalClients, func(i, j int) bool {
		if normalClients[i].priority == normalClients[j].priority {
			return rand.UintN(2) == 0
		}
		return normalClients[i].priority > normalClients[j].priority
	})

	return append(archiveClients, normalClients...)
}

func (s *synchronizer) loadBlockHeader(client *Client, slot phase0.Slot) (*phase0.SignedBeaconBlockHeader, phase0.Root, error) {
	ctx, cancel := context.WithTimeout(s.syncCtx, beaconHeaderRequestTimeout)
	defer cancel()

	header, root, orphaned, err := LoadBeaconHeaderBySlot(ctx, client, slot)
	if orphaned {
		return nil, root, nil
	}

	return header, root, err
}

func (s *synchronizer) loadBlockBody(client *Client, root phase0.Root) (*spec.VersionedSignedBeaconBlock, error) {
	ctx, cancel := context.WithTimeout(s.syncCtx, beaconHeaderRequestTimeout)
	defer cancel()
	return LoadBeaconBlock(ctx, client, root)
}

func (s *synchronizer) syncEpoch(syncEpoch phase0.Epoch, client *Client, lastTry bool) (bool, error) {
	if !utils.Config.Indexer.ResyncForceUpdate && db.IsEpochSynchronized(s.syncCtx, uint64(syncEpoch)) {
		return true, nil
	}

	chainState := s.indexer.consensusPool.GetChainState()
	specs := chainState.GetSpecs()

	// load headers & blocks from this & next epoch
	firstSlot := chainState.EpochStartSlot(syncEpoch)
	lastSlot := chainState.EpochStartSlot(syncEpoch+2) - 1
	canonicalBlocks := []*Block{}
	canonicalBlockRoots := [][]byte{}
	canonicalBlockHashes := [][]byte{}
	nextEpochCanonicalBlocks := []*Block{}

	blockHeads := db.GetBlockHeadBySlotRange(s.syncCtx, uint64(firstSlot), uint64(lastSlot))

	var firstBlock *Block
	for slot := firstSlot; slot <= lastSlot; slot++ {
		if s.cachedSlot < slot || s.cachedBlocks[slot] == nil {
			blockHeader, blockRoot, err := s.loadBlockHeader(client, slot)
			if err != nil {
				return false, fmt.Errorf("error fetching slot %v header: %v", slot, err)
			}
			if blockHeader == nil {
				continue
			}
			if s.syncCtx.Err() != nil {
				return false, nil
			}

			blockUid := uint64(slot) << 16
			for _, blockHead := range blockHeads {
				if bytes.Equal(blockHead.Root, blockRoot[:]) {
					blockUid = blockHead.BlockUid
					break
				}
				if blockHead.BlockUid >= blockUid {
					blockUid = blockHead.BlockUid + 1
				}
			}

			block := newBlock(s.indexer.dynSsz, blockRoot, slot, blockUid)
			block.SetHeader(blockHeader)

			if slot > 0 {
				blockBody, err := s.loadBlockBody(client, phase0.Root(blockRoot))
				if err != nil {
					return false, fmt.Errorf("error fetching slot %v block: %v", slot, err)
				}
				if blockBody == nil {
					return false, fmt.Errorf("error fetching slot %v block: not found", slot)
				}

				block.SetBlock(blockBody)
			}

			s.cachedBlocks[slot] = block
		}

		if firstBlock == nil && s.cachedBlocks[slot] != nil {
			firstBlock = s.cachedBlocks[slot]
		}

		if chainState.EpochOfSlot(slot) == syncEpoch {
			canonicalBlocks = append(canonicalBlocks, s.cachedBlocks[slot])
			canonicalBlockRoots = append(canonicalBlockRoots, s.cachedBlocks[slot].Root[:])
			if blockIndex := s.cachedBlocks[slot].GetBlockIndex(s.indexer.ctx); blockIndex != nil {
				canonicalBlockHashes = append(canonicalBlockHashes, blockIndex.ExecutionHash[:])
			}
		} else {
			nextEpochCanonicalBlocks = append(nextEpochCanonicalBlocks, s.cachedBlocks[slot])
		}
	}
	s.cachedSlot = lastSlot

	if s.syncCtx.Err() != nil {
		return false, nil
	}

	// load epoch state
	var dependentRoot phase0.Root
	if firstBlock != nil {
		if firstBlock.Slot == 0 { // epoch 0 dependent root is the genesis block
			dependentRoot = firstBlock.Root
		} else {
			dependentRoot = firstBlock.header.Message.ParentRoot
		}
	} else {
		// get from db
		depRoot := db.GetHighestRootBeforeSlot(s.syncCtx, uint64(firstSlot), false)
		dependentRoot = phase0.Root(depRoot)
	}

	epochState := newEpochState(dependentRoot)
	state, err := epochState.loadState(s.syncCtx, client, nil)
	if (err != nil || epochState.loadingStatus != 2) && !lastTry {
		return false, fmt.Errorf("error fetching epoch %v state: %v", syncEpoch, err)
	}

	var validatorSet []*phase0.Validator
	if state == nil {
		s.logger.Warnf("state for epoch %v not found", syncEpoch)
	} else {
		validatorSet, err = state.Validators()
		if err != nil {
			s.logger.Warnf("error getting validator set from state %v: %v", dependentRoot.String(), err)
		}
	}

	var epochStats *EpochStats
	var epochStatsValues *EpochStatsValues
	if epochState != nil && epochState.loadingStatus == 2 {
		epochStats = newEpochStats(syncEpoch, dependentRoot)
		epochStats.dependentState = epochState
		epochStats.processState(s.indexer, validatorSet)
		epochStatsValues = epochStats.GetValues(false)
	}

	if s.syncCtx.Err() != nil {
		return false, nil
	}

	// process epoch vote aggregations
	var epochVotes *EpochVotes
	if epochStatsValues != nil {
		votingBlocks := make([]*Block, len(canonicalBlocks)+len(nextEpochCanonicalBlocks))
		copy(votingBlocks, canonicalBlocks)
		copy(votingBlocks[len(canonicalBlocks):], nextEpochCanonicalBlocks)
		epochVotes = s.indexer.aggregateEpochVotes(syncEpoch, chainState, votingBlocks, epochStats)
		if epochVotes == nil && !lastTry {
			return false, fmt.Errorf("failed computing votes for epoch %v", syncEpoch)
		}
	}

	sim := newStateSimulator(s.indexer, epochStats)
	if sim != nil {
		sim.validatorSet = validatorSet
	}

	// save blocks
	err = db.RunDBTransaction(func(tx *sqlx.Tx) error {
		err = s.indexer.dbWriter.persistEpochData(tx, syncEpoch, canonicalBlocks, epochStats, epochVotes, sim)
		if err != nil {
			return fmt.Errorf("error persisting epoch data to db: %v", err)
		}

		// persist sync committee assignments
		if err := s.indexer.dbWriter.persistSyncAssignments(tx, syncEpoch, epochStats); err != nil {
			return fmt.Errorf("error persisting sync committee assignments to db: %v", err)
		}

		if err := db.UpdateMevBlockByEpoch(s.syncCtx, tx, uint64(syncEpoch), specs.SlotsPerEpoch, canonicalBlockHashes); err != nil {
			return fmt.Errorf("error while updating mev block proposal state: %v", err)
		}

		// delete unfinalized epoch aggregations in epoch
		if err := db.DeleteUnfinalizedEpochsBefore(s.syncCtx, tx, uint64(syncEpoch+1)); err != nil {
			return fmt.Errorf("failed deleting unfinalized epoch aggregations <= epoch %v: %v", syncEpoch, err)
		}

		// delete unfinalized forks for canonical roots
		if len(canonicalBlockRoots) > 0 {
			if err := db.UpdateFinalizedForkParents(s.syncCtx, tx, canonicalBlockRoots); err != nil {
				return fmt.Errorf("failed updating finalized fork parents: %v", err)
			}
			if err := db.DeleteFinalizedForks(s.syncCtx, tx, canonicalBlockRoots); err != nil {
				return fmt.Errorf("failed deleting finalized forks: %v", err)
			}
		}

		err = db.SetExplorerState(s.syncCtx, tx, "indexer.syncstate", &dbtypes.IndexerSyncState{
			Epoch: uint64(syncEpoch),
		})
		if err != nil {
			return fmt.Errorf("error while updating sync state: %v", err)
		}

		return nil
	})
	if err != nil {
		return false, err
	}

	// save block bodies to blockdb
	if blockdb.GlobalBlockDb != nil && !s.indexer.disableBlockDbWrite {
		var wg sync.WaitGroup
		for _, block := range canonicalBlocks {
			wg.Add(1)
			go func(b *Block) {
				defer wg.Done()
				if err := b.writeToBlockDb(s.indexer.ctx); err != nil {
					s.logger.Errorf("error writing block %v to blockdb: %v", b.Root.String(), err)
				}
			}(block)
		}
		wg.Wait()
	}

	// cleanup cache (remove blocks from this epoch)
	for slot := range s.cachedBlocks {
		if slot <= lastSlot {
			delete(s.cachedBlocks, slot)
		}
	}

	return true, nil
}
