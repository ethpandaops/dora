package system_contracts

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/indexer/execution"
)

// transactionMatcher is used to match transactions to requests in the database
type transactionMatcher[MatchType any] struct {
	indexer *execution.IndexerCtx
	logger  logrus.FieldLogger
	options *transactionMatcherOptions[MatchType]
	state   *transactionMatcherState
}

// transactionMatcherOptions are the options for the transaction matcher
type transactionMatcherOptions[MatchType any] struct {
	stateKey    string
	deployBlock uint64
	timeLimit   time.Duration

	matchBlockRange func(fromBlock uint64, toBlock uint64) ([]*MatchType, error)
	persistMatches  func(tx *sqlx.Tx, matches []*MatchType) error
}

// transactionMatcherState is the state of the transaction matcher
type transactionMatcherState struct {
	MatchHeight uint64 `json:"match_height"`
}

// newTransactionMatcher creates a new transaction matcher
func newTransactionMatcher[MatchType any](indexer *execution.IndexerCtx, logger logrus.FieldLogger, options *transactionMatcherOptions[MatchType]) *transactionMatcher[MatchType] {
	ci := &transactionMatcher[MatchType]{
		indexer: indexer,
		logger:  logger,
		options: options,
	}

	return ci
}

// GetMatcherHeight returns the current match height of the transaction matcher
func (ds *transactionMatcher[MatchType]) GetMatcherHeight() uint64 {
	if ds.state == nil {
		ds.loadState()
	}

	return ds.state.MatchHeight
}

// runTransactionMatcher runs the transaction matcher logic for the next block ranges if its fully loaded and ready to be matched.
func (ds *transactionMatcher[MatchType]) runTransactionMatcher(indexerBlock uint64) error {
	// get matcher state
	if ds.state == nil {
		ds.loadState()
	}

	finalizedBlock := ds.getFinalizedBlockNumber()

	matchTargetHeight := finalizedBlock
	if indexerBlock < matchTargetHeight {
		matchTargetHeight = indexerBlock
	}

	// check if the synchronization is running and if so, if its ahead of the block range we want to match
	_, syncEpoch := ds.indexer.BeaconIndexer.GetSynchronizerState()
	indexerFinalizedEpoch, _ := ds.indexer.BeaconIndexer.GetBlockCacheState()
	if syncEpoch < indexerFinalizedEpoch {
		// synchronization is behind head, check if our block range is synced
		syncSlot := ds.indexer.ChainState.EpochToSlot(syncEpoch)
		syncBlockRoot := db.GetHighestRootBeforeSlot(context.Background(), uint64(syncSlot), false)
		if syncBlockRoot == nil {
			// no block found, not synced at all
			return nil
		}

		syncBlock := db.GetSlotByRoot(context.Background(), syncBlockRoot)
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
	persistedHeight := ds.state.MatchHeight

	// match blocks until we reach the target height or the time limit is reached
	for ds.state.MatchHeight < matchTargetHeight && time.Since(t1) < ds.options.timeLimit {
		matchTarget := ds.state.MatchHeight + 200
		if matchTarget > matchTargetHeight {
			matchTarget = matchTargetHeight
		}

		matches, err := ds.options.matchBlockRange(ds.state.MatchHeight, matchTarget)
		if err != nil {
			return err
		}

		ds.state.MatchHeight = matchTarget

		if len(matches) > 0 {
			// persist matches to the database
			err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
				ds.options.persistMatches(tx, matches)

				persistedHeight = matchTarget
				return ds.persistState(tx)
			})

			if err != nil {
				return err
			}
		}
	}

	if ds.state.MatchHeight > persistedHeight {
		err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
			return ds.persistState(tx)
		})

		if err != nil {
			return err
		}
	}

	return nil
}

// loadState loads the state of the transaction matcher from the database
func (ds *transactionMatcher[_]) loadState() {
	syncState := transactionMatcherState{}
	db.GetExplorerState(context.Background(), ds.options.stateKey, &syncState)
	ds.state = &syncState

	if ds.state.MatchHeight == 0 {
		ds.state.MatchHeight = ds.options.deployBlock
	}
}

// persistState persists the state of the transaction matcher to the database
func (ds *transactionMatcher[_]) persistState(tx *sqlx.Tx) error {
	err := db.SetExplorerState(context.Background(), tx, ds.options.stateKey, ds.state)
	if err != nil {
		return fmt.Errorf("error while updating tx matcher state: %v", err)
	}

	return nil
}

// getFinalizedBlockNumber gets highest available finalized el block number
// available means that the block is processed by the finalization routine, so all request operations are available in the db
func (ds *transactionMatcher[_]) getFinalizedBlockNumber() uint64 {
	var finalizedBlockNumber uint64

	finalizedEpoch, finalizedRoot := ds.indexer.ChainState.GetFinalizedCheckpoint()
	if finalizedBlock := ds.indexer.BeaconIndexer.GetBlockByRoot(finalizedRoot); finalizedBlock != nil {
		if indexVals := finalizedBlock.GetBlockIndex(); indexVals != nil {
			finalizedBlockNumber = indexVals.ExecutionNumber
		}
	}

	if finalizedBlockNumber == 0 {
		// load from db
		if finalizedBlock := db.GetSlotByRoot(context.Background(), finalizedRoot[:]); finalizedBlock != nil && finalizedBlock.EthBlockNumber != nil {
			finalizedBlockNumber = *finalizedBlock.EthBlockNumber
		}
	}

	for {
		indexerFinalizedEpoch, _ := ds.indexer.BeaconIndexer.GetBlockCacheState()
		if indexerFinalizedEpoch >= finalizedEpoch {
			break
		}

		// wait for finalization routine to catch up
		time.Sleep(1 * time.Second)
	}

	return finalizedBlockNumber
}
