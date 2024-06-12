package indexer

import (
	"fmt"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/jmoiron/sqlx"
	"github.com/juliangruber/go-intersect"
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
	// insert missed slots
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

func persistBlockData(block *CacheBlock, epochStats *EpochStats, depositIndex *uint64, orphaned bool, tx *sqlx.Tx) error {
	// insert block
	dbBlock := buildDbBlock(block, epochStats)
	if orphaned {
		dbBlock.Status = dbtypes.Orphaned
	}

	err := db.InsertSlot(dbBlock, tx)
	if err != nil {
		return fmt.Errorf("error inserting slot: %v", err)
	}

	block.isInFinalizedDb = true

	// insert deposits
	err = persistBlockDeposits(block, depositIndex, orphaned, tx)
	if err != nil {
		return err
	}

	// insert voluntary exits
	err = persistBlockVoluntaryExits(block, orphaned, tx)
	if err != nil {
		return err
	}

	// insert slashings
	err = persistBlockSlashings(block, orphaned, tx)
	if err != nil {
		return err
	}

	return nil
}

func persistEpochData(epoch uint64, blockMap map[uint64]*CacheBlock, epochStats *EpochStats, epochVotes *EpochVotes, tx *sqlx.Tx) error {
	if tx == nil {
		return db.RunDBTransaction(func(tx *sqlx.Tx) error {
			return persistEpochData(epoch, blockMap, epochStats, epochVotes, tx)
		})
	}

	dbEpoch := buildDbEpoch(epoch, blockMap, epochStats, epochVotes, func(block *CacheBlock, depositIndex *uint64) {
		err := persistBlockData(block, epochStats, depositIndex, false, tx)
		if err != nil {
			logger.Errorf("error persisting slot: %v", err)
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

func buildDbEpoch(epoch uint64, blockMap map[uint64]*CacheBlock, epochStats *EpochStats, epochVotes *EpochVotes, blockFn func(block *CacheBlock, depositIndex *uint64)) *dbtypes.Epoch {
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch) - 1

	totalSyncAssigned := 0
	totalSyncVoted := 0
	var depositIndex *uint64
	dbEpoch := dbtypes.Epoch{
		Epoch: epoch,
	}
	if epochVotes != nil {
		dbEpoch.VotedTarget = epochVotes.currentEpoch.targetVoteAmount + epochVotes.nextEpoch.targetVoteAmount
		dbEpoch.VotedHead = epochVotes.currentEpoch.headVoteAmount + epochVotes.nextEpoch.headVoteAmount
		dbEpoch.VotedTotal = epochVotes.currentEpoch.totalVoteAmount + epochVotes.nextEpoch.totalVoteAmount
	}
	if epochStats != nil && epochStats.stateStats != nil {
		dbEpoch.ValidatorCount = epochStats.stateStats.ValidatorCount
		dbEpoch.ValidatorBalance = epochStats.stateStats.ValidatorBalance
		dbEpoch.Eligible = epochStats.stateStats.EligibleAmount
		depositIndexField := epochStats.stateStats.DepositIndex
		depositIndex = &depositIndexField
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
				blockFn(block, depositIndex)
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

func persistBlockDeposits(block *CacheBlock, depositIndex *uint64, orphaned bool, tx *sqlx.Tx) error {
	// insert deposits
	dbDeposits := buildDbDeposits(block, depositIndex)
	if orphaned {
		for idx := range dbDeposits {
			dbDeposits[idx].Orphaned = true
		}
	}

	if len(dbDeposits) > 0 {
		err := db.InsertDeposits(dbDeposits, tx)
		if err != nil {
			return fmt.Errorf("error inserting deposits: %v", err)
		}
	}

	return nil
}

func buildDbDeposits(block *CacheBlock, depositIndex *uint64) []*dbtypes.Deposit {
	blockBody := block.GetBlockBody()
	if blockBody == nil {
		return nil
	}

	deposits, err := blockBody.Deposits()
	if err != nil {
		return nil
	}

	dbDeposits := make([]*dbtypes.Deposit, len(deposits))
	for idx, deposit := range deposits {
		dbDeposit := &dbtypes.Deposit{
			SlotNumber:            block.Slot,
			SlotIndex:             uint64(idx),
			SlotRoot:              block.Root,
			Orphaned:              false,
			PublicKey:             deposit.Data.PublicKey[:],
			WithdrawalCredentials: deposit.Data.WithdrawalCredentials,
			Amount:                uint64(deposit.Data.Amount),
		}
		if depositIndex != nil {
			cDepIdx := *depositIndex
			dbDeposit.Index = &cDepIdx
			*depositIndex++
		}

		dbDeposits[idx] = dbDeposit
	}

	return dbDeposits
}

func persistBlockVoluntaryExits(block *CacheBlock, orphaned bool, tx *sqlx.Tx) error {
	// insert voluntary exits
	dbVoluntaryExits := buildDbVoluntaryExits(block)
	if orphaned {
		for idx := range dbVoluntaryExits {
			dbVoluntaryExits[idx].Orphaned = true
		}
	}

	if len(dbVoluntaryExits) > 0 {
		err := db.InsertVoluntaryExits(dbVoluntaryExits, tx)
		if err != nil {
			return fmt.Errorf("error inserting voluntary exits: %v", err)
		}
	}

	return nil
}

func buildDbVoluntaryExits(block *CacheBlock) []*dbtypes.VoluntaryExit {
	blockBody := block.GetBlockBody()
	if blockBody == nil {
		return nil
	}

	voluntaryExits, err := blockBody.VoluntaryExits()
	if err != nil {
		return nil
	}

	dbVoluntaryExits := make([]*dbtypes.VoluntaryExit, len(voluntaryExits))
	for idx, voluntaryExit := range voluntaryExits {
		dbVoluntaryExit := &dbtypes.VoluntaryExit{
			SlotNumber:     block.Slot,
			SlotIndex:      uint64(idx),
			SlotRoot:       block.Root,
			Orphaned:       false,
			ValidatorIndex: uint64(voluntaryExit.Message.ValidatorIndex),
		}

		dbVoluntaryExits[idx] = dbVoluntaryExit
	}

	return dbVoluntaryExits
}

func persistBlockSlashings(block *CacheBlock, orphaned bool, tx *sqlx.Tx) error {
	// insert slashings
	dbSlashings := buildDbSlashings(block)
	if orphaned {
		for idx := range dbSlashings {
			dbSlashings[idx].Orphaned = true
		}
	}

	if len(dbSlashings) > 0 {
		err := db.InsertSlashings(dbSlashings, tx)
		if err != nil {
			return fmt.Errorf("error inserting slashings: %v", err)
		}
	}

	return nil
}

func buildDbSlashings(block *CacheBlock) []*dbtypes.Slashing {
	blockBody := block.GetBlockBody()
	if blockBody == nil {
		return nil
	}

	proposerSlashings, err := blockBody.ProposerSlashings()
	if err != nil {
		return nil
	}

	attesterSlashings, err := blockBody.AttesterSlashings()
	if err != nil {
		return nil
	}

	proposerIndex, err := blockBody.ProposerIndex()
	if err != nil {
		return nil
	}

	dbSlashings := []*dbtypes.Slashing{}
	slashingIndex := 0

	for _, proposerSlashing := range proposerSlashings {
		dbSlashing := &dbtypes.Slashing{
			SlotNumber:     block.Slot,
			SlotIndex:      uint64(slashingIndex),
			SlotRoot:       block.Root,
			Orphaned:       false,
			ValidatorIndex: uint64(proposerSlashing.SignedHeader1.Message.ProposerIndex),
			SlasherIndex:   uint64(proposerIndex),
			Reason:         dbtypes.ProposerSlashing,
		}
		slashingIndex++
		dbSlashings = append(dbSlashings, dbSlashing)
	}

	for _, attesterSlashing := range attesterSlashings {
		inter := intersect.Simple(attesterSlashing.Attestation1.AttestingIndices, attesterSlashing.Attestation2.AttestingIndices)
		for _, j := range inter {
			valIdx := j.(uint64)

			dbSlashing := &dbtypes.Slashing{
				SlotNumber:     block.Slot,
				SlotIndex:      uint64(slashingIndex),
				SlotRoot:       block.Root,
				Orphaned:       false,
				ValidatorIndex: uint64(valIdx),
				SlasherIndex:   uint64(proposerIndex),
				Reason:         dbtypes.AttesterSlashing,
			}
			dbSlashings = append(dbSlashings, dbSlashing)
		}

		slashingIndex++
	}

	return dbSlashings
}
