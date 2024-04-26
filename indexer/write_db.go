package indexer

import (
	"fmt"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/jmoiron/sqlx"
)

func persistSlotAssignments(epochStats *EpochStats, tx *sqlx.Tx) error {
	// insert slot assignments
	firstSlot := epochStats.Epoch * utils.Config.Chain.Config.SlotsPerEpoch
	if epochStats.proposerAssignments != nil {
		slotAssignments := make([]*dbtypes.SlotAssignment, utils.Config.Chain.Config.SlotsPerEpoch)
		for slotIdx := uint64(0); slotIdx < utils.Config.Chain.Config.SlotsPerEpoch; slotIdx++ {
			slot := firstSlot + slotIdx
			slotAssignments[slotIdx] = &dbtypes.SlotAssignment{
				Slot:     slot,
				Proposer: epochStats.proposerAssignments[slot],
			}
		}
		err := db.InsertSlotAssignments(slotAssignments, tx)
		if err != nil {
			return fmt.Errorf("error while adding proposer assignments to db: %w", err)
		}
	}
	return nil
}

func persistMissedSlots(epoch uint64, blockMap map[uint64]*CacheBlock, epochStats *EpochStats, tx *sqlx.Tx) error {
	// insert slot assignments
	firstSlot := epochStats.Epoch * utils.Config.Chain.Config.SlotsPerEpoch
	if epochStats.proposerAssignments != nil {
		for slotIdx := uint64(0); slotIdx < utils.Config.Chain.Config.SlotsPerEpoch; slotIdx++ {
			slot := firstSlot + slotIdx
			if blockMap[slot] != nil {
				continue
			}

			missedSlot := &dbtypes.SlotHeader{
				Slot:     slot,
				Proposer: epochStats.proposerAssignments[slot],
				Status:   dbtypes.Missing,
			}
			err := db.InsertMissingSlot(missedSlot, tx)
			if err != nil {
				return fmt.Errorf("error while adding missed slot to db: %w", err)
			}
		}
	}
	return nil
}

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
		err := db.InsertSlot(dbBlock, tx)
		if err != nil {
			logger.Errorf("error inserting slot: %v", err)
		}
	})

	// insert slot assignments
	err := persistSlotAssignments(epochStats, tx)
	if err != nil {
		return err
	}

	// insert missing slots
	err = persistMissedSlots(epoch, blockMap, epochStats, tx)
	if err != nil {
		return err
	}

	// insert epoch
	db.InsertEpoch(dbEpoch, tx)
	if err != nil {
		return fmt.Errorf("error while saving epoch to db: %w", err)
	}

	if commitTx {
		logger.Infof("commit transaction")
		if err := tx.Commit(); err != nil {
			logger.Errorf("error committing db transaction: %v", err)
			return fmt.Errorf("error committing db transaction: %w", err)
		}
	}
	return nil
}

func persistSyncAssignments(epoch uint64, epochStats *EpochStats, tx *sqlx.Tx) error {
	if epoch < utils.Config.Chain.Config.AltairForkEpoch {
		// no sync committees before altair
		return nil
	}

	period := epoch / utils.Config.Chain.Config.EpochsPerSyncCommitteePeriod
	isStartOfPeriod := epoch == period*utils.Config.Chain.Config.EpochsPerSyncCommitteePeriod
	if !isStartOfPeriod && db.IsSyncCommitteeSynchronized(period) {
		// already synchronized
		return nil
	}

	syncAssignments := make([]*dbtypes.SyncAssignment, 0)
	for idx, val := range epochStats.syncAssignments {
		syncAssignments = append(syncAssignments, &dbtypes.SyncAssignment{
			Period:    period,
			Index:     uint32(idx),
			Validator: val,
		})
	}
	return db.InsertSyncAssignments(syncAssignments, tx)
}

func buildDbBlock(block *CacheBlock, epochStats *EpochStats) *dbtypes.Slot {
	blockBody := block.GetBlockBody()
	if blockBody == nil {
		logger.Errorf("Error while aggregating epoch blocks: canonical block body not found: %v", block.Slot)
		return nil
	}

	graffiti, _ := blockBody.Graffiti()
	attestations, _ := blockBody.Attestations()
	deposits, _ := blockBody.Deposits()
	voluntaryExits, _ := blockBody.VoluntaryExits()
	attesterSlashings, _ := blockBody.AttesterSlashings()
	proposerSlashings, _ := blockBody.ProposerSlashings()
	blsToExecChanges, _ := blockBody.BLSToExecutionChanges()
	syncAggregate, _ := blockBody.SyncAggregate()
	executionBlockNumber, _ := blockBody.ExecutionBlockNumber()
	executionBlockHash, _ := blockBody.ExecutionBlockHash()
	executionExtraData, _ := GetExecutionExtraData(blockBody)
	executionTransactions, _ := blockBody.ExecutionTransactions()
	executionWithdrawals, _ := blockBody.Withdrawals()

	dbBlock := dbtypes.Slot{
		Slot:                  uint64(block.header.Message.Slot),
		Proposer:              uint64(block.header.Message.ProposerIndex),
		Status:                dbtypes.Canonical,
		Root:                  block.Root,
		ParentRoot:            block.header.Message.ParentRoot[:],
		StateRoot:             block.header.Message.StateRoot[:],
		Graffiti:              graffiti[:],
		GraffitiText:          utils.GraffitiToString(graffiti[:]),
		AttestationCount:      uint64(len(attestations)),
		DepositCount:          uint64(len(deposits)),
		ExitCount:             uint64(len(voluntaryExits)),
		AttesterSlashingCount: uint64(len(attesterSlashings)),
		ProposerSlashingCount: uint64(len(proposerSlashings)),
		BLSChangeCount:        uint64(len(blsToExecChanges)),
	}

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

	if executionBlockNumber > 0 {
		dbBlock.EthTransactionCount = uint64(len(executionTransactions))
		dbBlock.EthBlockNumber = &executionBlockNumber
		dbBlock.EthBlockHash = executionBlockHash[:]
		dbBlock.EthBlockExtra = executionExtraData
		dbBlock.EthBlockExtraText = utils.GraffitiToString(executionExtraData[:])
		dbBlock.WithdrawCount = uint64(len(executionWithdrawals))
		for _, withdrawal := range executionWithdrawals {
			dbBlock.WithdrawAmount += uint64(withdrawal.Amount)
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
		Epoch: epoch,
	}
	if epochVotes != nil {
		dbEpoch.VotedTarget = epochVotes.currentEpoch.targetVoteAmount + epochVotes.nextEpoch.targetVoteAmount
		dbEpoch.VotedHead = epochVotes.currentEpoch.headVoteAmount + epochVotes.nextEpoch.headVoteAmount
		dbEpoch.VotedTotal = epochVotes.currentEpoch.totalVoteAmount + epochVotes.nextEpoch.totalVoteAmount
	}
	if epochStats != nil && epochStats.validatorStats != nil {
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

			attestations, _ := blockBody.Attestations()
			deposits, _ := blockBody.Deposits()
			voluntaryExits, _ := blockBody.VoluntaryExits()
			attesterSlashings, _ := blockBody.AttesterSlashings()
			proposerSlashings, _ := blockBody.ProposerSlashings()
			blsToExecChanges, _ := blockBody.BLSToExecutionChanges()
			syncAggregate, _ := blockBody.SyncAggregate()
			executionTransactions, _ := blockBody.ExecutionTransactions()
			executionWithdrawals, _ := blockBody.Withdrawals()

			dbEpoch.AttestationCount += uint64(len(attestations))
			dbEpoch.DepositCount += uint64(len(deposits))
			dbEpoch.ExitCount += uint64(len(voluntaryExits))
			dbEpoch.AttesterSlashingCount += uint64(len(attesterSlashings))
			dbEpoch.ProposerSlashingCount += uint64(len(proposerSlashings))
			dbEpoch.BLSChangeCount += uint64(len(blsToExecChanges))

			if syncAggregate != nil && epochStats != nil && epochStats.syncAssignments != nil {
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

			dbEpoch.EthTransactionCount += uint64(len(executionTransactions))
			dbEpoch.WithdrawCount += uint64(len(executionWithdrawals))
			for _, withdrawal := range executionWithdrawals {
				dbEpoch.WithdrawAmount += uint64(withdrawal.Amount)
			}
		}
	}

	if totalSyncAssigned > 0 {
		dbEpoch.SyncParticipation = float32(totalSyncVoted) / float32(totalSyncAssigned)
	}

	return &dbEpoch
}
