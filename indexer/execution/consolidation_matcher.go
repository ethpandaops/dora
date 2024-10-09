package execution

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
)

type ConsolidationMatcher struct {
	indexer              *IndexerCtx
	consolidationIndexer *ConsolidationIndexer
	logger               logrus.FieldLogger
	state                *consolidationMatcherState
}

type consolidationMatcherState struct {
	MatchHeight uint64 `json:"match_height"`
}

func NewConsolidationMatcher(indexer *IndexerCtx, consolidationIndexer *ConsolidationIndexer) *ConsolidationMatcher {
	ci := &ConsolidationMatcher{
		indexer:              indexer,
		consolidationIndexer: consolidationIndexer,
		logger:               indexer.logger.WithField("indexer", "consolidation_matcher"),
	}
	return ci
}

// runConsolidationMatcher runs the consolidation matcher logic.
// It tries to match consolidation transactions with consolidation requests on the beacon chain.
func (ds *ConsolidationMatcher) runConsolidationMatcher() error {
	// get matcher state
	if ds.state == nil {
		ds.loadState()
	}

	finalizedBlock := ds.getFinalizedBlockNumber()
	indexerBlock := ds.consolidationIndexer.indexer.state.FinalBlock

	matchTargetHeight := finalizedBlock
	if indexerBlock < matchTargetHeight {
		matchTargetHeight = indexerBlock
	}

	// check if the synchronization is running and if so, if its ahead of the block range we want to match
	_, syncEpoch := ds.indexer.beaconIndexer.GetSynchronizerState()
	indexerFinalizedEpoch, _ := ds.indexer.beaconIndexer.GetBlockCacheState()
	if syncEpoch < indexerFinalizedEpoch {
		// synchronization is behind head, check if our block range is synced
		syncSlot := ds.indexer.chainState.EpochToSlot(syncEpoch)
		syncBlockRoot := db.GetHighestRootBeforeSlot(uint64(syncSlot), false)
		if syncBlockRoot == nil {
			// no block found, not synced at all
			return nil
		}

		syncBlock := db.GetSlotByRoot(syncBlockRoot)
		if syncBlock == nil {
			// block not found, not synced at all
			return nil
		}

		if syncBlock.EthBlockNumber == nil {
			// synced before belatrix
			return nil
		}

		syncBlockNumber := *syncBlock.EthBlockNumber
		if syncBlockNumber < matchTargetHeight {
			matchTargetHeight = syncBlockNumber
		}
	}

	t1 := time.Now()
	for ds.state.MatchHeight < matchTargetHeight && time.Since(t1) < 2*time.Second {
		matchTarget := ds.state.MatchHeight + 200
		if matchTarget > matchTargetHeight {
			matchTarget = matchTargetHeight
		}

		err := ds.matchBlockRange(ds.state.MatchHeight, matchTarget)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ds *ConsolidationMatcher) loadState() {
	syncState := consolidationMatcherState{}
	db.GetExplorerState("matcher.consolidationmatcher", &syncState)
	ds.state = &syncState

	if ds.state.MatchHeight == 0 {
		ds.state.MatchHeight = uint64(utils.Config.ExecutionApi.ElectraDeployBlock)
	}
}

func (ds *ConsolidationMatcher) persistState(tx *sqlx.Tx) error {
	err := db.SetExplorerState("matcher.consolidationmatcher", ds.state, tx)
	if err != nil {
		return fmt.Errorf("error while updating consolidation tx matcher state: %v", err)
	}

	return nil
}

func (ds *ConsolidationMatcher) getFinalizedBlockNumber() uint64 {
	var finalizedBlockNumber uint64

	finalizedEpoch, finalizedRoot := ds.indexer.chainState.GetFinalizedCheckpoint()
	if finalizedBlock := ds.indexer.beaconIndexer.GetBlockByRoot(finalizedRoot); finalizedBlock != nil {
		if indexVals := finalizedBlock.GetBlockIndex(); indexVals != nil {
			finalizedBlockNumber = indexVals.ExecutionNumber
		}
	}

	if finalizedBlockNumber == 0 {
		// load from db
		if finalizedBlock := db.GetSlotByRoot(finalizedRoot[:]); finalizedBlock != nil && finalizedBlock.EthBlockNumber != nil {
			finalizedBlockNumber = *finalizedBlock.EthBlockNumber
		}
	}

	for {
		indexerFinalizedEpoch, _ := ds.indexer.beaconIndexer.GetBlockCacheState()
		if indexerFinalizedEpoch >= finalizedEpoch {
			break
		}

		// wait for finalization routine to catch up
		time.Sleep(1 * time.Second)
	}

	return finalizedBlockNumber
}

type consolidationRequestMatch struct {
	slotRoot  []byte
	slotIndex uint64
	txHash    []byte
}

func (ds *ConsolidationMatcher) matchBlockRange(fromBlock uint64, toBlock uint64) error {
	requestMatches := []*consolidationRequestMatch{}

	dequeueConsolidationTxs := db.GetConsolidationRequestTxsByDequeueRange(fromBlock, toBlock)
	if len(dequeueConsolidationTxs) > 0 {
		firstBlock := dequeueConsolidationTxs[0].DequeueBlock
		lastBlock := dequeueConsolidationTxs[len(dequeueConsolidationTxs)-1].DequeueBlock

		for _, consolidationRequest := range db.GetConsolidationRequestsByElBlockRange(firstBlock, lastBlock) {
			if len(consolidationRequest.TxHash) > 0 {
				continue
			}

			parentForkIds := ds.indexer.beaconIndexer.GetParentForkIds(beacon.ForkKey(consolidationRequest.ForkId))
			isParentFork := func(forkId uint64) bool {
				if forkId == consolidationRequest.ForkId {
					return true
				}
				for _, parentForkId := range parentForkIds {
					if uint64(parentForkId) == forkId {
						return true
					}
				}
				return false
			}

			matchingTxs := []*dbtypes.ConsolidationRequestTx{}
			for _, tx := range dequeueConsolidationTxs {
				if tx.DequeueBlock == consolidationRequest.BlockNumber && isParentFork(tx.ForkId) {
					matchingTxs = append(matchingTxs, tx)
				}
			}

			if len(matchingTxs) == 0 {
				for _, tx := range dequeueConsolidationTxs {
					if tx.DequeueBlock == consolidationRequest.BlockNumber {
						matchingTxs = append(matchingTxs, tx)
					}
				}
			}

			if len(matchingTxs) < int(consolidationRequest.SlotIndex)+1 {
				continue
			}

			txHash := matchingTxs[consolidationRequest.SlotIndex].TxHash
			ds.logger.Debugf("Matched consolidation request %d:%v with tx 0x%x", consolidationRequest.SlotNumber, consolidationRequest.SlotIndex, txHash)

			requestMatches = append(requestMatches, &consolidationRequestMatch{
				slotRoot:  consolidationRequest.SlotRoot,
				slotIndex: consolidationRequest.SlotIndex,
				txHash:    txHash,
			})
		}
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		for _, match := range requestMatches {
			err := db.UpdateConsolidationRequestTxHash(match.slotRoot, match.slotIndex, match.txHash, tx)
			if err != nil {
				return err
			}
		}

		ds.state.MatchHeight = toBlock
		return ds.persistState(tx)
	})
}
