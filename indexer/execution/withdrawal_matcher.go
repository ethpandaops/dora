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

type WithdrawalMatcher struct {
	indexer           *IndexerCtx
	withdrawalIndexer *WithdrawalIndexer
	logger            logrus.FieldLogger
	state             *withdrawalMatcherState
}

type withdrawalMatcherState struct {
	MatchHeight uint64 `json:"match_height"`
}

func NewWithdrawalMatcher(indexer *IndexerCtx, withdrawalIndexer *WithdrawalIndexer) *WithdrawalMatcher {
	ci := &WithdrawalMatcher{
		indexer:           indexer,
		withdrawalIndexer: withdrawalIndexer,
		logger:            indexer.logger.WithField("indexer", "withdrawal_matcher"),
	}
	return ci
}

// runWithdrawalMatcher runs the withdrawal matcher logic.
// It tries to match withdrawal transactions with withdrawal requests on the beacon chain.
func (ds *WithdrawalMatcher) runWithdrawalMatcher() error {
	// get matcher state
	if ds.state == nil {
		ds.loadState()
	}

	finalizedBlock := ds.getFinalizedBlockNumber()
	indexerBlock := ds.withdrawalIndexer.indexer.state.FinalBlock

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

func (ds *WithdrawalMatcher) loadState() {
	syncState := withdrawalMatcherState{}
	db.GetExplorerState("matcher.withdrawalmatcher", &syncState)
	ds.state = &syncState

	if ds.state.MatchHeight == 0 {
		ds.state.MatchHeight = uint64(utils.Config.ExecutionApi.ElectraDeployBlock)
	}
}

func (ds *WithdrawalMatcher) persistState(tx *sqlx.Tx) error {
	err := db.SetExplorerState("matcher.withdrawalmatcher", ds.state, tx)
	if err != nil {
		return fmt.Errorf("error while updating withdrawal tx matcher state: %v", err)
	}

	return nil
}

func (ds *WithdrawalMatcher) getFinalizedBlockNumber() uint64 {
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

type withdrawalRequestMatch struct {
	slotRoot  []byte
	slotIndex uint64
	txHash    []byte
}

func (ds *WithdrawalMatcher) matchBlockRange(fromBlock uint64, toBlock uint64) error {
	requestMatches := []*withdrawalRequestMatch{}

	dequeueWithdrawalTxs := db.GetWithdrawalRequestTxsByDequeueRange(fromBlock, toBlock)
	if len(dequeueWithdrawalTxs) > 0 {
		firstBlock := dequeueWithdrawalTxs[0].DequeueBlock
		lastBlock := dequeueWithdrawalTxs[len(dequeueWithdrawalTxs)-1].DequeueBlock

		for _, withdrawalRequest := range db.GetWithdrawalRequestsByElBlockRange(firstBlock, lastBlock) {
			if len(withdrawalRequest.TxHash) > 0 {
				continue
			}

			parentForkIds := ds.indexer.beaconIndexer.GetParentForkIds(beacon.ForkKey(withdrawalRequest.ForkId))
			isParentFork := func(forkId uint64) bool {
				if forkId == withdrawalRequest.ForkId {
					return true
				}
				for _, parentForkId := range parentForkIds {
					if uint64(parentForkId) == forkId {
						return true
					}
				}
				return false
			}

			matchingTxs := []*dbtypes.WithdrawalRequestTx{}
			for _, tx := range dequeueWithdrawalTxs {
				if tx.DequeueBlock == withdrawalRequest.BlockNumber && isParentFork(tx.ForkId) {
					matchingTxs = append(matchingTxs, tx)
				}
			}

			if len(matchingTxs) == 0 {
				for _, tx := range dequeueWithdrawalTxs {
					if tx.DequeueBlock == withdrawalRequest.BlockNumber {
						matchingTxs = append(matchingTxs, tx)
					}
				}
			}

			if len(matchingTxs) < int(withdrawalRequest.SlotIndex)+1 {
				continue
			}

			txHash := matchingTxs[withdrawalRequest.SlotIndex].TxHash
			ds.logger.Debugf("Matched withdrawal request %d:%v with tx 0x%x", withdrawalRequest.SlotNumber, withdrawalRequest.SlotIndex, txHash)

			requestMatches = append(requestMatches, &withdrawalRequestMatch{
				slotRoot:  withdrawalRequest.SlotRoot,
				slotIndex: withdrawalRequest.SlotIndex,
				txHash:    txHash,
			})
		}
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		for _, match := range requestMatches {
			err := db.UpdateWithdrawalRequestTxHash(match.slotRoot, match.slotIndex, match.txHash, tx)
			if err != nil {
				return err
			}
		}

		ds.state.MatchHeight = toBlock
		return ds.persistState(tx)
	})
}
