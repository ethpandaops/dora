package indexer

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

func persistEpochData(epoch uint64, blockMap map[uint64]*CacheBlock, epochStats *EpochStats, epochVotes *EpochVotes, tx *sqlx.Tx) error {
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

	dbEpoch := buildDbEpoch(epoch, blockMap, epochStats, epochVotes, func(block *CacheBlock) {
		// insert block
		dbBlock := buildDbBlock(block, epochStats)
		db.InsertBlock(dbBlock, tx)
	})

	// insert slot assignments
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	if epochStats.proposerAssignments != nil {
		slotAssignments := make([]*dbtypes.SlotAssignment, utils.Config.Chain.Config.SlotsPerEpoch)
		for slotIdx := uint64(0); slotIdx < utils.Config.Chain.Config.SlotsPerEpoch; slotIdx++ {
			slot := firstSlot + slotIdx
			slotAssignments[slotIdx] = &dbtypes.SlotAssignment{
				Slot:     slot,
				Proposer: epochStats.proposerAssignments[slot],
			}
		}
		db.InsertSlotAssignments(slotAssignments, tx)
	}

	// insert epoch
	db.InsertEpoch(dbEpoch, tx)

	if commitTx {
		logger.Infof("commit transaction")
		if err := tx.Commit(); err != nil {
			logger.Errorf("error committing db transaction: %v", err)
			return fmt.Errorf("error committing db transaction: %w", err)
		}
	}
	return nil
}

func buildDbBlock(block *CacheBlock, epochStats *EpochStats) *dbtypes.Block {
	blockBody := block.GetBlockBody()
	if blockBody == nil {
		logger.Errorf("Error while aggregating epoch blocks: canonical block body not found: %v", block.Slot)
		return nil
	}

	dbBlock := dbtypes.Block{
		Root:                  block.Root,
		Slot:                  uint64(block.header.Message.Slot),
		ParentRoot:            block.header.Message.ParentRoot,
		StateRoot:             block.header.Message.StateRoot,
		Proposer:              uint64(blockBody.Message.ProposerIndex),
		Graffiti:              blockBody.Message.Body.Graffiti,
		GraffitiText:          utils.GraffitiToString(blockBody.Message.Body.Graffiti),
		AttestationCount:      uint64(len(blockBody.Message.Body.Attestations)),
		DepositCount:          uint64(len(blockBody.Message.Body.Deposits)),
		ExitCount:             uint64(len(blockBody.Message.Body.VoluntaryExits)),
		AttesterSlashingCount: uint64(len(blockBody.Message.Body.AttesterSlashings)),
		ProposerSlashingCount: uint64(len(blockBody.Message.Body.ProposerSlashings)),
		BLSChangeCount:        uint64(len(blockBody.Message.Body.SignedBLSToExecutionChange)),
	}

	syncAggregate := blockBody.Message.Body.SyncAggregate
	if syncAggregate != nil {
		var assignedCount int
		if epochStats != nil && epochStats.syncAssignments != nil {
			assignedCount = len(epochStats.syncAssignments)
		} else {
			// this is not accurate, but best we can get without epoch assignments
			assignedCount = len(syncAggregate.SyncCommitteeBits) * 8
		}
		votedCount := 0
		for i := 0; i < assignedCount; i++ {
			if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
				votedCount++
			}
		}
		dbBlock.SyncParticipation = float32(votedCount) / float32(assignedCount)
	}

	if executionPayload := blockBody.Message.Body.ExecutionPayload; executionPayload != nil {
		dbBlock.EthTransactionCount = uint64(len(executionPayload.Transactions))
		dbBlock.EthBlockNumber = uint64(executionPayload.BlockNumber)
		dbBlock.EthBlockHash = executionPayload.BlockHash

		if executionPayload.Withdrawals != nil {
			dbBlock.WithdrawCount = uint64(len(executionPayload.Withdrawals))
			for _, withdrawal := range executionPayload.Withdrawals {
				dbBlock.WithdrawAmount += uint64(withdrawal.Amount)
			}
		}
	}

	return &dbBlock
}

func buildDbEpoch(epoch uint64, blockMap map[uint64]*CacheBlock, epochStats *EpochStats, epochVotes *EpochVotes, blockFn func(block *CacheBlock)) *dbtypes.Epoch {
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch) - 1

	totalSyncAssigned := 0
	totalSyncVoted := 0
	dbEpoch := dbtypes.Epoch{
		Epoch:       epoch,
		VotedTarget: epochVotes.currentEpoch.targetVoteAmount + epochVotes.nextEpoch.targetVoteAmount,
		VotedHead:   epochVotes.currentEpoch.headVoteAmount + epochVotes.nextEpoch.headVoteAmount,
		VotedTotal:  epochVotes.currentEpoch.totalVoteAmount + epochVotes.nextEpoch.totalVoteAmount,
	}
	if epochStats.validatorStats != nil {
		dbEpoch.ValidatorCount = epochStats.validatorStats.ValidatorCount
		dbEpoch.ValidatorBalance = epochStats.validatorStats.ValidatorBalance
		dbEpoch.Eligible = epochStats.validatorStats.EligibleAmount
	}

	// aggregate blocks
	for slot := firstSlot; slot <= lastSlot; slot++ {
		block := blockMap[slot]

		if block != nil {
			dbEpoch.BlockCount++
			blockBody := block.GetBlockBody()
			if blockBody == nil {
				logger.Errorf("Error while aggregating epoch blocks: canonical block body not found: %v", block.Slot)
				continue
			}
			if blockFn != nil {
				blockFn(block)
			}

			dbEpoch.AttestationCount += uint64(len(blockBody.Message.Body.Attestations))
			dbEpoch.DepositCount += uint64(len(blockBody.Message.Body.Deposits))
			dbEpoch.ExitCount += uint64(len(blockBody.Message.Body.VoluntaryExits))
			dbEpoch.AttesterSlashingCount += uint64(len(blockBody.Message.Body.AttesterSlashings))
			dbEpoch.ProposerSlashingCount += uint64(len(blockBody.Message.Body.ProposerSlashings))
			dbEpoch.BLSChangeCount += uint64(len(blockBody.Message.Body.SignedBLSToExecutionChange))

			syncAggregate := blockBody.Message.Body.SyncAggregate
			if syncAggregate != nil && epochStats.syncAssignments != nil {
				votedCount := 0
				assignedCount := len(epochStats.syncAssignments)
				for i := 0; i < assignedCount; i++ {
					if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
						votedCount++
					}
				}
				totalSyncAssigned += assignedCount
				totalSyncVoted += votedCount
			}

			if executionPayload := blockBody.Message.Body.ExecutionPayload; executionPayload != nil {
				dbEpoch.EthTransactionCount += uint64(len(executionPayload.Transactions))
				if executionPayload.Withdrawals != nil {
					dbEpoch.WithdrawCount += uint64(len(executionPayload.Withdrawals))
					for _, withdrawal := range executionPayload.Withdrawals {
						dbEpoch.WithdrawAmount += uint64(withdrawal.Amount)
					}
				}
			}
		}
	}

	if totalSyncAssigned > 0 {
		dbEpoch.SyncParticipation = float32(totalSyncVoted) / float32(totalSyncAssigned)
	}

	return &dbEpoch
}
