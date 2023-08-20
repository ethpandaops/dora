package indexer

/*
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

	dbEpoch := buildDbEpoch(epoch, blockMap, epochStats, epochVotes, func(block *BlockInfo) {
		// insert block
		dbBlock := buildDbBlock(block, epochStats)
		db.InsertBlock(dbBlock, tx)

		// insert orphaned block
		if dbBlock.Orphaned {
			db.InsertOrphanedBlock(BuildOrphanedBlock(block), tx)
		}
	})

	// insert slot assignments
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	if epochStats.Assignments != nil {
		slotAssignments := make([]*dbtypes.SlotAssignment, utils.Config.Chain.Config.SlotsPerEpoch)
		for slotIdx := uint64(0); slotIdx < utils.Config.Chain.Config.SlotsPerEpoch; slotIdx++ {
			slot := firstSlot + slotIdx
			slotAssignments[slotIdx] = &dbtypes.SlotAssignment{
				Slot:     slot,
				Proposer: epochStats.Assignments.ProposerAssignments[slot],
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

func buildDbBlock(block *BlockInfo, epochStats *EpochStats) *dbtypes.Block {
	dbBlock := dbtypes.Block{
		Root:                  block.Header.Data.Root,
		Slot:                  uint64(block.Header.Data.Header.Message.Slot),
		ParentRoot:            block.Header.Data.Header.Message.ParentRoot,
		StateRoot:             block.Header.Data.Header.Message.StateRoot,
		Orphaned:              block.Orphaned,
		Proposer:              uint64(block.Block.Data.Message.ProposerIndex),
		Graffiti:              block.Block.Data.Message.Body.Graffiti,
		GraffitiText:          utils.GraffitiToString(block.Block.Data.Message.Body.Graffiti),
		AttestationCount:      uint64(len(block.Block.Data.Message.Body.Attestations)),
		DepositCount:          uint64(len(block.Block.Data.Message.Body.Deposits)),
		ExitCount:             uint64(len(block.Block.Data.Message.Body.VoluntaryExits)),
		AttesterSlashingCount: uint64(len(block.Block.Data.Message.Body.AttesterSlashings)),
		ProposerSlashingCount: uint64(len(block.Block.Data.Message.Body.ProposerSlashings)),
		BLSChangeCount:        uint64(len(block.Block.Data.Message.Body.SignedBLSToExecutionChange)),
	}

	syncAggregate := block.Block.Data.Message.Body.SyncAggregate
	if syncAggregate != nil && epochStats != nil && epochStats.Assignments != nil && epochStats.Assignments.SyncAssignments != nil {
		votedCount := 0
		assignedCount := len(epochStats.Assignments.SyncAssignments)
		for i := 0; i < assignedCount; i++ {
			if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
				votedCount++
			}
		}
		dbBlock.SyncParticipation = float32(votedCount) / float32(assignedCount)
	}

	if executionPayload := block.Block.Data.Message.Body.ExecutionPayload; executionPayload != nil {
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

func buildDbEpoch(epoch uint64, blockMap map[uint64][]*BlockInfo, epochStats *EpochStats, epochVotes *EpochVotes, blockFn func(block *BlockInfo)) *dbtypes.Epoch {
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch) - 1

	totalSyncAssigned := 0
	totalSyncVoted := 0
	dbEpoch := dbtypes.Epoch{
		Epoch:            epoch,
		ValidatorCount:   epochStats.Validators.ValidatorCount,
		ValidatorBalance: epochStats.Validators.ValidatorBalance,
		Eligible:         epochStats.Validators.EligibleAmount,
		VotedTarget:      epochVotes.currentEpoch.targetVoteAmount + epochVotes.nextEpoch.targetVoteAmount,
		VotedHead:        epochVotes.currentEpoch.headVoteAmount + epochVotes.nextEpoch.headVoteAmount,
		VotedTotal:       epochVotes.currentEpoch.totalVoteAmount + epochVotes.nextEpoch.totalVoteAmount,
	}

	// aggregate blocks
	for slot := firstSlot; slot <= lastSlot; slot++ {
		blocks := blockMap[slot]
		if blocks != nil {
			for bidx := 0; bidx < len(blocks); bidx++ {
				block := blocks[bidx]
				if blockFn != nil {
					blockFn(block)
				}

				if block.Orphaned {
					dbEpoch.OrphanedCount++
					continue
				}

				dbEpoch.BlockCount++
				dbEpoch.AttestationCount += uint64(len(block.Block.Data.Message.Body.Attestations))
				dbEpoch.DepositCount += uint64(len(block.Block.Data.Message.Body.Deposits))
				dbEpoch.ExitCount += uint64(len(block.Block.Data.Message.Body.VoluntaryExits))
				dbEpoch.AttesterSlashingCount += uint64(len(block.Block.Data.Message.Body.AttesterSlashings))
				dbEpoch.ProposerSlashingCount += uint64(len(block.Block.Data.Message.Body.ProposerSlashings))
				dbEpoch.BLSChangeCount += uint64(len(block.Block.Data.Message.Body.SignedBLSToExecutionChange))

				syncAggregate := block.Block.Data.Message.Body.SyncAggregate
				if syncAggregate != nil && epochStats.Assignments != nil && epochStats.Assignments.SyncAssignments != nil {
					votedCount := 0
					assignedCount := len(epochStats.Assignments.SyncAssignments)
					for i := 0; i < assignedCount; i++ {
						if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
							votedCount++
						}
					}
					totalSyncAssigned += assignedCount
					totalSyncVoted += votedCount
				}

				if executionPayload := block.Block.Data.Message.Body.ExecutionPayload; executionPayload != nil {
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
	}

	if totalSyncAssigned > 0 {
		dbEpoch.SyncParticipation = float32(totalSyncVoted) / float32(totalSyncAssigned)
	}

	return &dbEpoch
}
*/
