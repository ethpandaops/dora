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

	dbEpoch := buildDbEpoch(epoch, blockMap, epochStats, epochVotes, func(block *BlockInfo) {
		dbBlock := buildDbBlock(block, epochStats)
		db.InsertBlock(dbBlock, tx)

		if dbBlock.Orphaned {
			headerJson, err := json.Marshal(block.Header)
			if err != nil {
				return
			}
			blockJson, err := json.Marshal(block.Block)
			if err != nil {
				return
			}
			db.InsertOrphanedBlock(&dbtypes.OrphanedBlock{
				Root:   block.Header.Data.Root,
				Header: string(headerJson),
				Block:  string(blockJson),
			}, tx)
		}
	})

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
	}

	return &dbBlock
}

func buildDbEpoch(epoch uint64, blockMap map[uint64][]*BlockInfo, epochStats *EpochStats, epochVotes *EpochVotes, blockFn func(block *BlockInfo)) *dbtypes.Epoch {
	var firstBlock *BlockInfo
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + (utils.Config.Chain.Config.SlotsPerEpoch) - 1
slotLoop:
	for slot := firstSlot; slot <= lastSlot; slot++ {
		if blockMap[slot] != nil {
			blocks := blockMap[slot]
			for bidx := 0; bidx < len(blocks); bidx++ {
				if !blocks[bidx].Orphaned {
					firstBlock = blocks[bidx]
					break slotLoop
				}
			}
		}
	}
	if firstBlock == nil {
		return nil
	}

	totalSyncAssigned := 0
	totalSyncVoted := 0
	dbEpoch := dbtypes.Epoch{
		Epoch:          epoch,
		ValidatorCount: epochStats.Validators.ValidatorCount,
		Eligible:       epochStats.Validators.EligibleAmount,
		VotedTarget:    epochVotes.currentEpoch.targetVoteAmount + epochVotes.nextEpoch.targetVoteAmount,
		VotedHead:      epochVotes.currentEpoch.headVoteAmount + epochVotes.nextEpoch.headVoteAmount,
		VotedTotal:     epochVotes.currentEpoch.totalVoteAmount + epochVotes.nextEpoch.totalVoteAmount,
	}
	missingDuties := make(map[uint8]uint64)

	// aggregate blocks
	for slot := firstSlot; slot <= lastSlot; slot++ {
		blocks := blockMap[slot]
		if blocks == nil {
			continue
		}
		hasCanonicalBlock := false
		for bidx := 0; bidx < len(blocks); bidx++ {
			block := blocks[bidx]
			if blockFn != nil {
				blockFn(block)
			}

			if block.Orphaned {
				dbEpoch.OrphanedCount++
				continue
			}
			hasCanonicalBlock = true

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
			}
		}
		if !hasCanonicalBlock {
			slotIdx := uint8(slot - firstSlot)
			missingDuties[slotIdx] = epochStats.Assignments.ProposerAssignments[slot]
		}
	}

	dbEpoch.MissingDuties = utils.MissingDutiesToBytes(missingDuties)
	if totalSyncAssigned > 0 {
		dbEpoch.SyncParticipation = float32(totalSyncVoted) / float32(totalSyncAssigned)
	}

	return &dbEpoch
}
