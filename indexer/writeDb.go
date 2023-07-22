package indexer

import (
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

func persistEpochData(epoch uint64, blockMap map[uint64][]*BlockInfo, epochStats *EpochStats, epochVotes *EpochVotes, tx *sqlx.Tx) error {
	commitTx := false
	if tx == nil {
		var err error
		tx, err = db.WriterDb.Beginx()
		if err != nil {
			logger.Errorf("error starting db transactions: %v", err)
			return err
		}
		defer tx.Rollback()
		commitTx = true
	}

	totalSyncAssigned := 0
	totalSyncVoted := 0
	dbEpoch := dbtypes.Epoch{
		Epoch:          epoch,
		ValidatorCount: epochStats.ValidatorCount,
		Eligible:       epochStats.EligibleAmount,
		VotedTarget:    epochVotes.currentEpoch.targetVoteAmount + epochVotes.nextEpoch.targetVoteAmount,
		VotedHead:      epochVotes.currentEpoch.headVoteAmount + epochVotes.nextEpoch.headVoteAmount,
		VotedTotal:     epochVotes.currentEpoch.totalVoteAmount + epochVotes.nextEpoch.totalVoteAmount,
	}

	// insert blocks
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	for slot := firstSlot; slot <= lastSlot; slot++ {
		blocks := blockMap[slot]
		if blocks == nil {
			continue
		}
		for bidx := 0; bidx < len(blocks); bidx++ {
			block := blocks[bidx]
			dbBlock := dbtypes.Block{
				Root:                  block.Header.Data.Root,
				Slot:                  slot,
				ParentRoot:            block.Header.Data.Header.Message.ParentRoot,
				StateRoot:             block.Header.Data.Header.Message.StateRoot,
				Orphaned:              block.Orphaned,
				Proposer:              uint64(block.Block.Data.Message.ProposerIndex),
				Graffiti:              block.Block.Data.Message.Body.Graffiti,
				AttestationCount:      uint64(len(block.Block.Data.Message.Body.Attestations)),
				DepositCount:          uint64(len(block.Block.Data.Message.Body.Deposits)),
				ExitCount:             uint64(len(block.Block.Data.Message.Body.VoluntaryExits)),
				AttesterSlashingCount: uint64(len(block.Block.Data.Message.Body.AttesterSlashings)),
				ProposerSlashingCount: uint64(len(block.Block.Data.Message.Body.ProposerSlashings)),
				BLSChangeCount:        uint64(len(block.Block.Data.Message.Body.SignedBLSToExecutionChange)),
			}
			dbEpoch.AttestationCount += dbBlock.AttestationCount
			dbEpoch.DepositCount += dbBlock.DepositCount
			dbEpoch.ExitCount += dbBlock.ExitCount
			dbEpoch.AttesterSlashingCount += dbBlock.AttesterSlashingCount
			dbEpoch.ProposerSlashingCount += dbBlock.ProposerSlashingCount
			dbEpoch.BLSChangeCount += dbBlock.BLSChangeCount

			syncAggregate := block.Block.Data.Message.Body.SyncAggregate
			syncAssignments := epochStats.Assignments.SyncAssignments
			if syncAggregate != nil && syncAssignments != nil {
				votedCount := 0
				assignedCount := len(syncAssignments)
				for i := 0; i < assignedCount; i++ {
					if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
						votedCount++
					}
				}
				dbBlock.SyncParticipation = float32(votedCount) / float32(assignedCount)
				if !block.Orphaned {
					totalSyncAssigned += assignedCount
					totalSyncVoted += votedCount
				}
			}

			if executionPayload := block.Block.Data.Message.Body.ExecutionPayload; executionPayload != nil {
				dbBlock.EthTransactionCount = uint64(len(executionPayload.Transactions))
				dbBlock.EthBlockNumber = uint64(executionPayload.BlockNumber)
				dbBlock.EthBlockHash = executionPayload.BlockHash
				dbEpoch.EthTransactionCount += dbBlock.EthTransactionCount
			}

			db.InsertBlock(&dbBlock, tx)

			if block.Orphaned {
				dbEpoch.OrphanedCount++
				headerJson, err := json.Marshal(block.Header)
				if err != nil {
					return err
				}
				blockJson, err := json.Marshal(block.Block)
				if err != nil {
					return err
				}
				db.InsertOrphanedBlock(&dbtypes.OrphanedBlock{
					Root:   block.Header.Data.Root,
					Header: string(headerJson),
					Block:  string(blockJson),
				}, tx)
			} else {
				dbEpoch.BlockCount++
			}
		}
	}

	// insert epoch
	if totalSyncAssigned > 0 {
		dbEpoch.SyncParticipation = float32(totalSyncVoted) / float32(totalSyncAssigned)
	}
	db.InsertEpoch(&dbEpoch, tx)

	if commitTx {
		logger.Infof("commit transaction")
		if err := tx.Commit(); err != nil {
			logger.Errorf("error committing db transaction: %v", err)
			return fmt.Errorf("error committing db transaction: %w", err)
		}
	}
	return nil
}
