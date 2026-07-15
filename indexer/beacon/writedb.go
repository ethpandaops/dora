package beacon

import (
	"bytes"
	"fmt"
	"math"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"
)

// clampInt64 caps a uint64 value at math.MaxInt64 to fit PostgreSQL BIGINT.
func clampInt64(v uint64) uint64 {
	if v >= math.MaxInt64 {
		return math.MaxInt64
	}
	return v
}

// getBlockBid extracts the execution payload bid from a Gloas/Heze signed
// beacon block and converts it to a dbtypes.BlockBid. Returns nil for
// pre-Gloas blocks or blocks without a bid.
func getBlockBid(v *all.SignedBeaconBlock) *dbtypes.BlockBid {
	if v == nil || v.Version < spec.DataVersionGloas || v.Message == nil || v.Message.Body == nil {
		return nil
	}

	signedBid := v.Message.Body.SignedExecutionPayloadBid
	if signedBid == nil || signedBid.Message == nil {
		return nil
	}

	bid := signedBid.Message
	return &dbtypes.BlockBid{
		ParentRoot:   bid.ParentBlockRoot[:],
		ParentHash:   bid.ParentBlockHash[:],
		BlockHash:    bid.BlockHash[:],
		FeeRecipient: bid.FeeRecipient[:],
		GasLimit:     bid.GasLimit,
		BuilderIndex: int64(bid.BuilderIndex),
		Slot:         uint64(bid.Slot),
		Value:        uint64(bid.Value),
		ElPayment:    uint64(bid.ExecutionPayment),
	}
}

type dbWriter struct {
	indexer *Indexer
}

func newDbWriter(indexer *Indexer) *dbWriter {
	return &dbWriter{
		indexer: indexer,
	}
}

func (dbw *dbWriter) resolveProposerNameId(proposer phase0.ValidatorIndex, slot phase0.Slot) *uint64 {
	if dbw.indexer.validatorNameIdResolver == nil {
		return nil
	}
	return dbw.indexer.validatorNameIdResolver(proposer, slot)
}

func (dbw *dbWriter) persistMissedSlots(tx *sqlx.Tx, epoch phase0.Epoch, blocks []*Block, epochStats *EpochStats) error {
	chainState := dbw.indexer.consensusPool.GetChainState()
	epochStatsValues := epochStats.GetValues(true)

	// insert missed slots
	firstSlot := chainState.EpochStartSlot(epoch)
	lastSlot := firstSlot + phase0.Slot(chainState.GetSpecs().SlotsPerEpoch)
	blockIdx := 0

	for slot := firstSlot; slot < lastSlot; slot++ {
		if blockIdx < len(blocks) && blocks[blockIdx].Slot == slot {
			blockIdx++
			continue
		}

		proposer := phase0.ValidatorIndex(math.MaxInt64)
		if epochStatsValues != nil {
			proposer = epochStatsValues.ProposerDuties[int(slot-firstSlot)]
		}

		missedSlot := &dbtypes.SlotHeader{
			Slot:           uint64(slot),
			Proposer:       uint64(proposer),
			Status:         dbtypes.Missing,
			ProposerNameId: dbw.resolveProposerNameId(proposer, slot),
		}

		err := db.InsertMissingSlot(dbw.indexer.ctx, tx, missedSlot)
		if err != nil {
			return fmt.Errorf("error while adding missed slot to db: %w", err)
		}
	}
	return nil
}

func (dbw *dbWriter) persistBlockData(tx *sqlx.Tx, block *Block, epochStats *EpochStats, payment *builderPaymentInfo, depositIndex *uint64, orphaned bool, overrideForkId *ForkKey, sim *stateSimulator) (*dbtypes.Slot, error) {
	// insert block
	dbBlock := dbw.buildDbBlock(block, epochStats, overrideForkId, payment)
	if dbBlock == nil {
		return nil, fmt.Errorf("error while building db block: %v", block.Slot)
	}

	if orphaned {
		dbBlock.Status = dbtypes.Orphaned
	}

	// Apply payload orphaned status from block flag (set during finalization/sync)
	if block.isPayloadOrphaned && dbBlock.PayloadStatus == dbtypes.PayloadStatusCanonical {
		dbBlock.PayloadStatus = dbtypes.PayloadStatusOrphaned
	}

	err := db.InsertSlot(dbw.indexer.ctx, tx, dbBlock)
	if err != nil {
		return nil, fmt.Errorf("error inserting slot: %v", err)
	}

	block.isInFinalizedDb = true

	// insert child objects
	if block.Slot > 0 {
		err = dbw.persistBlockChildObjects(tx, block, depositIndex, orphaned, overrideForkId, sim)
		if err != nil {
			return nil, err
		}
	}

	return dbBlock, nil
}

func (dbw *dbWriter) persistBlockChildObjects(tx *sqlx.Tx, block *Block, depositIndex *uint64, orphaned bool, overrideForkId *ForkKey, sim *stateSimulator) error {
	var err error

	// insert deposits (pre/early electra)
	err = dbw.persistBlockDeposits(tx, block, depositIndex, orphaned, overrideForkId)
	if err != nil {
		return err
	}

	// insert deposit requests (post electra)
	err = dbw.persistBlockDepositRequests(tx, block, orphaned, overrideForkId)
	if err != nil {
		return err
	}

	// insert voluntary exits
	err = dbw.persistBlockVoluntaryExits(tx, block, orphaned, overrideForkId)
	if err != nil {
		return err
	}

	// insert slashings
	err = dbw.persistBlockSlashings(tx, block, orphaned, overrideForkId)
	if err != nil {
		return err
	}

	// insert consolidation requests
	err = dbw.persistBlockConsolidationRequests(tx, block, orphaned, overrideForkId, sim)
	if err != nil {
		return err
	}

	// insert withdrawal requests
	err = dbw.persistBlockWithdrawalRequests(tx, block, orphaned, overrideForkId, sim)
	if err != nil {
		return err
	}

	// insert builder deposit requests (gloas)
	err = dbw.persistBlockBuilderDeposits(tx, block, orphaned, overrideForkId)
	if err != nil {
		return err
	}

	// insert builder exit requests (gloas)
	err = dbw.persistBlockBuilderExits(tx, block, orphaned, overrideForkId)
	if err != nil {
		return err
	}

	// insert withdrawals
	err = dbw.persistBlockWithdrawals(tx, block, orphaned, overrideForkId, sim)
	if err != nil {
		return err
	}

	return nil
}

func (dbw *dbWriter) persistEpochData(tx *sqlx.Tx, epoch phase0.Epoch, blocks []*Block, epochStats *EpochStats, epochVotes *EpochVotes, sim *stateSimulator) error {
	if tx == nil {
		return db.RunDBTransaction(func(tx *sqlx.Tx) error {
			return dbw.persistEpochData(tx, epoch, blocks, epochStats, epochVotes, sim)
		})
	}
	canonicalForkId := ForkKey(0)

	if sim == nil {
		sim = newStateSimulator(dbw.indexer, epochStats)
	}

	paymentBase := dbw.resolveBuilderPaymentBase(epoch, epochStats)

	dbEpoch := dbw.buildDbEpoch(epoch, blocks, epochStats, epochVotes, func(block *Block, depositIndex *uint64) {
		payment := dbw.builderPaymentForSlot(block.Slot, epochVotes, paymentBase)
		_, err := dbw.persistBlockData(tx, block, epochStats, payment, depositIndex, false, &canonicalForkId, sim)
		if err != nil {
			dbw.indexer.logger.Errorf("error persisting slot: %v", err)
		}
	})

	// insert missing slots
	err := dbw.persistMissedSlots(tx, epoch, blocks, epochStats)
	if err != nil {
		return err
	}

	// insert epoch
	err = db.InsertEpoch(dbw.indexer.ctx, tx, dbEpoch)
	if err != nil {
		return fmt.Errorf("error while saving epoch to db: %w", err)
	}

	return nil
}

func (dbw *dbWriter) persistSyncAssignments(tx *sqlx.Tx, epoch phase0.Epoch, epochStats *EpochStats) error {
	chainState := dbw.indexer.consensusPool.GetChainState()
	specs := chainState.GetSpecs()

	if specs.AltairForkEpoch == nil || epoch < phase0.Epoch(*specs.AltairForkEpoch) {
		// no sync committees before altair
		return nil
	}

	var epochStatsValues *EpochStatsValues
	if epochStats != nil {
		epochStatsValues = epochStats.GetValues(true)
	}
	if epochStatsValues == nil {
		return nil
	}

	period := epoch / phase0.Epoch(specs.EpochsPerSyncCommitteePeriod)
	isStartOfPeriod := epoch == period*phase0.Epoch(specs.EpochsPerSyncCommitteePeriod)
	if !isStartOfPeriod && db.IsSyncCommitteeSynchronized(dbw.indexer.ctx, uint64(period)) {
		// already synchronized
		return nil
	}

	syncAssignments := make([]*dbtypes.SyncAssignment, 0)
	for idx, val := range epochStatsValues.SyncCommitteeDuties {
		syncAssignments = append(syncAssignments, &dbtypes.SyncAssignment{
			Period:    uint64(period),
			Index:     uint32(idx),
			Validator: uint64(val),
		})
	}
	return db.InsertSyncAssignments(dbw.indexer.ctx, tx, syncAssignments)
}

// builderPaymentInfo carries the resolved Gloas builder-payment quorum figures for a single slot.
type builderPaymentInfo struct {
	Weight  uint64  // same-slot attester balance backing the payment (Gwei)
	Percent float32 // Weight as a percentage of the per-slot quorum base
}

// resolveBuilderPaymentBase returns the per-slot quorum base for an epoch's builder payments:
// get_total_active_balance / SLOTS_PER_EPOCH. Settlement uses the *next* epoch's active balance,
// so prefer epoch+1's stats for an exact base and fall back to the current epoch when they are not
// available yet (e.g. live sync at the head). Returns 0 for pre-Gloas epochs (no builder payments).
func (dbw *dbWriter) resolveBuilderPaymentBase(epoch phase0.Epoch, epochStats *EpochStats) phase0.Gwei {
	chainState := dbw.indexer.consensusPool.GetChainState()
	if !chainState.IsEip7732Enabled(epoch) {
		return 0
	}
	slotsPerEpoch := phase0.Gwei(chainState.GetSpecs().SlotsPerEpoch)
	if slotsPerEpoch == 0 {
		return 0
	}

	totalActive := phase0.Gwei(0)
	for _, nextStats := range dbw.indexer.epochCache.getEpochStatsByEpoch(epoch + 1) {
		if values := nextStats.GetValues(false); values != nil && values.EffectiveBalance > 0 {
			totalActive = values.EffectiveBalance
			break
		}
	}
	if totalActive == 0 && epochStats != nil {
		if values := epochStats.GetValues(true); values != nil {
			totalActive = values.EffectiveBalance
		}
	}
	if totalActive == 0 {
		return 0
	}
	return totalActive / slotsPerEpoch
}

// builderPaymentForSlot resolves the per-slot builder-payment figures from the aggregated epoch
// votes and the quorum base. Returns nil when unavailable (pre-Gloas, no aggregation, or out of range).
func (dbw *dbWriter) builderPaymentForSlot(slot phase0.Slot, epochVotes *EpochVotes, base phase0.Gwei) *builderPaymentInfo {
	if epochVotes == nil || epochVotes.SlotWeights == nil {
		return nil
	}
	slotIndex := dbw.indexer.consensusPool.GetChainState().SlotToSlotIndex(slot)
	if int(slotIndex) >= len(epochVotes.SlotWeights) {
		return nil
	}
	weight := epochVotes.SlotWeights[slotIndex]
	percent := float32(0)
	if base > 0 {
		percent = float32(float64(weight) / float64(base) * 100)
	}
	return &builderPaymentInfo{Weight: uint64(weight), Percent: percent}
}

func (dbw *dbWriter) buildDbBlock(block *Block, epochStats *EpochStats, overrideForkId *ForkKey, payment *builderPaymentInfo) *dbtypes.Slot {
	if block.Slot == 0 {
		// genesis block
		header := block.GetHeader()
		if header == nil {
			// some clients do not serve the genesis block header, so add a fallback here
			header = &phase0.SignedBeaconBlockHeader{
				Message: &phase0.BeaconBlockHeader{
					Slot:          0,
					ProposerIndex: math.MaxInt64,
					ParentRoot:    consensus.NullRoot,
					StateRoot:     consensus.NullRoot,
				},
			}
		}

		return &dbtypes.Slot{
			Slot:           0,
			Proposer:       math.MaxInt64,
			Status:         dbtypes.Canonical,
			Root:           block.Root[:],
			ParentRoot:     header.Message.ParentRoot[:],
			StateRoot:      header.Message.StateRoot[:],
			ProposerNameId: dbw.resolveProposerNameId(math.MaxInt64, 0),
		}
	}

	blockBody := block.GetBlock(dbw.indexer.ctx)
	if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
		dbw.indexer.logger.Warnf("error while building db blocks: block body not found: %v", block.Slot)
		return nil
	}

	var epochStatsValues *EpochStatsValues
	if epochStats != nil {
		epochStatsValues = epochStats.GetValues(true)
	}

	chainState := dbw.indexer.consensusPool.GetChainState()

	body := blockBody.Message.Body
	graffiti := body.Graffiti
	attestations := body.Attestations
	deposits := body.Deposits
	voluntaryExits := body.VoluntaryExits
	attesterSlashings := body.AttesterSlashings
	proposerSlashings := body.ProposerSlashings
	blsToExecChanges := body.BLSToExecutionChanges
	syncAggregate := body.SyncAggregate
	blobKzgCommitments := utils.BlockBodyBlobCommitments(body)
	var executionBlockHash phase0.Hash32

	var executionBlockNumber uint64
	var executionBlockParentHash []byte
	var executionExtraData []byte
	var executionTransactions []bellatrix.Transaction
	var executionWithdrawals []*capella.Withdrawal
	var depositRequests []*electra.DepositRequest
	var payloadStatus dbtypes.PayloadStatus

	if chainState.IsEip7732Enabled(chainState.EpochOfSlot(block.Slot)) {
		blockPayload := block.GetExecutionPayload(dbw.indexer.ctx)
		if blockPayload != nil {
			executionBlockHash = blockPayload.Message.Payload.BlockHash
			executionBlockNumber = blockPayload.Message.Payload.BlockNumber
			executionBlockParentHash = blockPayload.Message.Payload.ParentHash[:]
			executionExtraData = blockPayload.Message.Payload.ExtraData
			executionTransactions = blockPayload.Message.Payload.Transactions
			executionWithdrawals = blockPayload.Message.Payload.Withdrawals
			payloadStatus = dbtypes.PayloadStatusCanonical
		} else {
			payloadStatus = dbtypes.PayloadStatusMissing
		}
	} else {
		payloadStatus = dbtypes.PayloadStatusCanonical
		if body.ExecutionPayload != nil {
			ep := body.ExecutionPayload
			executionBlockHash = ep.BlockHash
			executionBlockNumber = ep.BlockNumber
			executionExtraData = ep.ExtraData
			executionTransactions = ep.Transactions
			executionWithdrawals = ep.Withdrawals
			executionBlockParentHash = ep.ParentHash[:]
		}
	}

	// DepositCount/ExitCount count the deposits and exits this block processes; in Gloas
	// these come from the parent payload (parent_execution_requests), consistent with the
	// deposits table and the slot detail page, which attribute requests to the block that
	// processes them. Gloas builder deposits/exits (EIP-8282) are folded into the combined
	// deposit/exit counts.
	var builderDepositCount, builderExitCount int
	if processedRequests, _ := dbw.getProcessedExecutionRequests(block); processedRequests != nil {
		depositRequests = processedRequests.Deposits
		builderDepositCount = len(processedRequests.BuilderDeposits)
		builderExitCount = len(processedRequests.BuilderExits)
	}

	// Get builder index from block, default to -1 (self-built/MaxUint64)
	var builderIndexInt64 int64 = -1
	var bidValue uint64
	if blockIndex := block.GetBlockIndex(dbw.indexer.ctx); blockIndex != nil {
		if blockIndex.BuilderIndex == math.MaxUint64 {
			builderIndexInt64 = -1
		} else {
			builderIndexInt64 = int64(blockIndex.BuilderIndex)
		}
		bidValue = blockIndex.BidValue
	}

	// Extract execution payload bid from Gloas/Heze blocks and add to bid cache.
	// This ensures bids are persisted even when syncing from blocks (not just SSE events).
	blockBid := getBlockBid(blockBody)
	if blockBid != nil {
		dbw.indexer.blockBidCache.AddBid(blockBid)
	}

	dbBlock := dbtypes.Slot{
		Slot:                  uint64(block.header.Message.Slot),
		Proposer:              uint64(block.header.Message.ProposerIndex),
		ProposerNameId:        dbw.resolveProposerNameId(block.header.Message.ProposerIndex, block.Slot),
		Status:                dbtypes.Canonical,
		ForkId:                uint64(block.forkId),
		Root:                  block.Root[:],
		ParentRoot:            block.header.Message.ParentRoot[:],
		StateRoot:             block.header.Message.StateRoot[:],
		Graffiti:              graffiti[:],
		GraffitiText:          utils.GraffitiToString(graffiti[:]),
		AttestationCount:      uint64(len(attestations)),
		DepositCount:          uint64(len(deposits) + len(depositRequests) + builderDepositCount),
		ExitCount:             uint64(len(voluntaryExits) + builderExitCount),
		AttesterSlashingCount: uint64(len(attesterSlashings)),
		ProposerSlashingCount: uint64(len(proposerSlashings)),
		BLSChangeCount:        uint64(len(blsToExecChanges)),
		BlobCount:             uint64(len(blobKzgCommitments)),
		RecvDelay:             block.recvDelay,
		PayloadStatus:         payloadStatus,
		BlockUid:              block.BlockUID,
		BuilderIndex:          builderIndexInt64,
		EthBidValue:           bidValue,
	}

	blockSize, err := getBlockSize(block.dynSsz, blockBody)
	if err != nil {
		dbw.indexer.logger.Warnf("error while building db blocks: failed to get block size: %v", err)
	} else {
		dbBlock.BlockSize = uint64(blockSize)
	}

	if overrideForkId != nil {
		dbBlock.ForkId = uint64(*overrideForkId)
	}

	if syncAggregate != nil {
		var assignedCount int
		if epochStatsValues != nil {
			assignedCount = len(epochStatsValues.SyncCommitteeDuties)
		} else {
			// this is not accurate, but best we can get without epoch assignments
			assignedCount = len(syncAggregate.SyncCommitteeBits) * 8
		}

		// clamp to bits actually present in the block to avoid out-of-range access on mismatch
		if maxBits := len(syncAggregate.SyncCommitteeBits) * 8; assignedCount > maxBits {
			assignedCount = maxBits
		}

		if assignedCount > 0 {
			votedCount := 0
			for i := 0; i < assignedCount; i++ {
				if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
					votedCount++
				}
			}
			dbBlock.SyncParticipation = float32(votedCount) / float32(assignedCount)
		}
	}

	if executionBlockNumber > 0 {
		dbBlock.EthTransactionCount = uint64(len(executionTransactions))
		dbBlock.EthBlockNumber = &executionBlockNumber
		dbBlock.EthBlockHash = executionBlockHash[:]
		dbBlock.EthBlockParentHash = executionBlockParentHash
		dbBlock.EthBlockExtra = executionExtraData
		dbBlock.EthBlockExtraText = utils.GraffitiToString(executionExtraData[:])
		dbBlock.WithdrawCount = uint64(len(executionWithdrawals))

		// Get execution times from the block
		if execTimes := block.GetExecutionTimes(); len(execTimes) > 0 {
			// Calculate min/max times for quick queries
			minTime, maxTime := CalculateMinMaxTimesForStorage(execTimes)
			if minTime > 0 {
				dbBlock.MinExecTime = minTime
				dbBlock.MaxExecTime = maxTime

				execTimesSSZ, err := block.dynSsz.MarshalSSZ(execTimes)
				if err != nil {
					dbw.indexer.logger.Warnf("error while building db blocks: failed to marshal execution times: %v", err)
				} else {
					dbBlock.ExecTimes = execTimesSSZ
				}
			}
		}

		withdrawalAmountOverflow := false
		for _, withdrawal := range executionWithdrawals {
			dbBlock.WithdrawAmount += uint64(withdrawal.Amount)
			if dbBlock.WithdrawAmount < uint64(withdrawal.Amount) {
				withdrawalAmountOverflow = true
			}
		}
		if withdrawalAmountOverflow || dbBlock.WithdrawAmount >= math.MaxInt64 {
			dbBlock.WithdrawAmount = math.MaxInt64
		}

		switch {
		case blockBody.Version >= spec.DataVersionBellatrix && blockBody.Version < spec.DataVersionGloas:
			if body.ExecutionPayload != nil {
				payload := body.ExecutionPayload
				dbBlock.EthGasUsed = payload.GasUsed
				dbBlock.EthGasLimit = payload.GasLimit
				if payload.BaseFeePerGas != nil {
					dbBlock.EthBaseFee = utils.GetBaseFeeAsUint64(payload.BaseFeePerGas)
				} else {
					dbBlock.EthBaseFee = utils.GetBaseFeeAsUint64(payload.BaseFeePerGasLE)
				}
				dbBlock.EthFeeRecipient = payload.FeeRecipient[:]
			}
		case blockBody.Version >= spec.DataVersionGloas:
			blockPayload := block.GetExecutionPayload(dbw.indexer.ctx)
			if blockPayload != nil {
				payload := blockPayload.Message.Payload
				dbBlock.EthGasUsed = payload.GasUsed
				dbBlock.EthGasLimit = payload.GasLimit
				dbBlock.EthBaseFee = utils.GetBaseFeeAsUint64(payload.BaseFeePerGas)
				dbBlock.EthFeeRecipient = payload.FeeRecipient[:]
			}
		}
	}

	// Missed payload (Gloas): the execution payload was never revealed, so the eth_* fields above
	// were not written (they depend on executionBlockNumber, which is unknown here). The block still
	// commits to an execution payload bid that references the execution block hash (plus parent hash,
	// fee recipient and gas limit), so populate those from the bid - a missed-payload block must show
	// its committed block hash, not nothing. The block number stays unset (the bid does not carry it).
	if payloadStatus == dbtypes.PayloadStatusMissing && blockBid != nil && len(dbBlock.EthBlockHash) == 0 {
		dbBlock.EthBlockHash = blockBid.BlockHash
		dbBlock.EthBlockParentHash = blockBid.ParentHash
		dbBlock.EthFeeRecipient = blockBid.FeeRecipient
		dbBlock.EthGasLimit = blockBid.GasLimit
	}

	// Gloas builder-payment quorum weight for this slot (same-slot attester balance), resolved by
	// the epoch-vote aggregation. Only set on canonical persistence paths that pass it through.
	if payment != nil {
		dbBlock.BuilderPaymentWeight = payment.Weight
		dbBlock.BuilderPaymentPercent = payment.Percent
	}

	return &dbBlock
}

func (dbw *dbWriter) buildDbEpoch(epoch phase0.Epoch, blocks []*Block, epochStats *EpochStats, epochVotes *EpochVotes, blockFn func(block *Block, depositIndex *uint64)) *dbtypes.Epoch {
	chainState := dbw.indexer.consensusPool.GetChainState()

	var epochStatsValues *EpochStatsValues
	if epochStats != nil {
		epochStatsValues = epochStats.GetValues(true)
	}

	// insert missed slots
	firstSlot := chainState.EpochStartSlot(epoch)
	lastSlot := firstSlot + phase0.Slot(chainState.GetSpecs().SlotsPerEpoch) - 1

	totalSyncAssigned := 0
	totalSyncVoted := 0
	var depositIndex *uint64
	dbEpoch := dbtypes.Epoch{
		Epoch: uint64(epoch),
	}
	if epochVotes != nil {
		dbEpoch.VotedTarget = clampInt64(uint64(epochVotes.CurrentEpoch.TargetVoteAmount + epochVotes.NextEpoch.TargetVoteAmount))
		dbEpoch.VotedTargetSlashed = clampInt64(uint64(epochVotes.CurrentEpoch.TargetVoteAmountSlashed + epochVotes.NextEpoch.TargetVoteAmountSlashed))
		dbEpoch.VotedHead = clampInt64(uint64(epochVotes.CurrentEpoch.HeadVoteAmount + epochVotes.NextEpoch.HeadVoteAmount))
		dbEpoch.VotedTotal = clampInt64(uint64(epochVotes.CurrentEpoch.TotalVoteAmount + epochVotes.NextEpoch.TotalVoteAmount))
	}
	if epochStatsValues != nil {
		dbEpoch.ValidatorCount = epochStatsValues.ActiveValidators
		dbEpoch.ValidatorBalance = clampInt64(uint64(epochStatsValues.ActiveBalance))
		dbEpoch.Eligible = clampInt64(uint64(epochStatsValues.EffectiveBalance))
		depositIndexField := epochStatsValues.FirstDepositIndex
		depositIndex = &depositIndexField
	}

	// aggregate blocks
	blockIdx := 0
	for slot := firstSlot; slot <= lastSlot; slot++ {
		var block *Block

		if blockIdx < len(blocks) && blocks[blockIdx].Slot == slot {
			block = blocks[blockIdx]
			blockIdx++
		}

		if block != nil {
			dbEpoch.BlockCount++
			if block.Slot == 0 {
				if blockFn != nil {
					blockFn(block, depositIndex)
				}

				continue
			}

			blockBody := block.GetBlock(dbw.indexer.ctx)
			if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
				dbw.indexer.logger.Warnf("error while building db epoch: block body not found for aggregation: %v", block.Slot)
				continue
			}
			if blockFn != nil {
				blockFn(block, depositIndex)
			}

			body := blockBody.Message.Body
			attestations := body.Attestations
			deposits := body.Deposits
			voluntaryExits := body.VoluntaryExits
			attesterSlashings := body.AttesterSlashings
			proposerSlashings := body.ProposerSlashings
			blsToExecChanges := body.BLSToExecutionChanges
			syncAggregate := body.SyncAggregate
			blobKzgCommitments := utils.BlockBodyBlobCommitments(body)

			var executionTransactions []bellatrix.Transaction
			var executionWithdrawals []*capella.Withdrawal
			var depositRequests []*electra.DepositRequest

			if chainState.IsEip7732Enabled(chainState.EpochOfSlot(block.Slot)) {
				blockPayload := block.GetExecutionPayload(dbw.indexer.ctx)
				if blockPayload != nil {
					dbEpoch.PayloadCount++
					executionTransactions = blockPayload.Message.Payload.Transactions
					executionWithdrawals = blockPayload.Message.Payload.Withdrawals
				}
			} else {
				if body.ExecutionPayload != nil {
					executionTransactions = body.ExecutionPayload.Transactions
					executionWithdrawals = body.ExecutionPayload.Withdrawals
				}
			}

			// Count the deposits/exits each block processes; in Gloas these come from the
			// parent payload (parent_execution_requests), consistent with the deposits table.
			// Gloas builder deposits/exits (EIP-8282) are folded into the combined counts.
			var builderDepositCount, builderExitCount int
			if processedRequests, _ := dbw.getProcessedExecutionRequests(block); processedRequests != nil {
				depositRequests = processedRequests.Deposits
				builderDepositCount = len(processedRequests.BuilderDeposits)
				builderExitCount = len(processedRequests.BuilderExits)
			}

			dbEpoch.AttestationCount += uint64(len(attestations))
			dbEpoch.DepositCount += uint64(len(deposits) + len(depositRequests) + builderDepositCount)
			dbEpoch.ExitCount += uint64(len(voluntaryExits) + builderExitCount)
			dbEpoch.AttesterSlashingCount += uint64(len(attesterSlashings))
			dbEpoch.ProposerSlashingCount += uint64(len(proposerSlashings))
			dbEpoch.BLSChangeCount += uint64(len(blsToExecChanges))

			if syncAggregate != nil && epochStatsValues != nil {
				assignedCount := len(epochStatsValues.SyncCommitteeDuties)
				if maxBits := len(syncAggregate.SyncCommitteeBits) * 8; assignedCount > maxBits {
					assignedCount = maxBits
				}

				votedCount := 0
				for i := 0; i < assignedCount; i++ {
					if utils.BitAtVector(syncAggregate.SyncCommitteeBits, i) {
						votedCount++
					}
				}
				totalSyncAssigned += assignedCount
				totalSyncVoted += votedCount
			}

			dbEpoch.EthTransactionCount += uint64(len(executionTransactions))
			dbEpoch.BlobCount += uint64(len(blobKzgCommitments))
			dbEpoch.WithdrawCount += uint64(len(executionWithdrawals))

			withdrawalAmountOverflow := false
			for _, withdrawal := range executionWithdrawals {
				dbEpoch.WithdrawAmount += uint64(withdrawal.Amount)
				if dbEpoch.WithdrawAmount < uint64(withdrawal.Amount) {
					withdrawalAmountOverflow = true
				}
			}
			if withdrawalAmountOverflow || dbEpoch.WithdrawAmount >= math.MaxInt64 {
				dbEpoch.WithdrawAmount = math.MaxInt64
			}

			// Aggregate gas used and gas limit
			switch {
			case blockBody.Version >= spec.DataVersionBellatrix && blockBody.Version < spec.DataVersionGloas:
				if body.ExecutionPayload != nil {
					dbEpoch.EthGasUsed += body.ExecutionPayload.GasUsed
					dbEpoch.EthGasLimit += body.ExecutionPayload.GasLimit
				}
			case blockBody.Version >= spec.DataVersionGloas:
				blockPayload := block.GetExecutionPayload(dbw.indexer.ctx)
				if blockPayload != nil {
					payload := blockPayload.Message.Payload
					dbEpoch.EthGasUsed += payload.GasUsed
					dbEpoch.EthGasLimit += payload.GasLimit
				}
			}
		}
	}

	if totalSyncAssigned > 0 {
		dbEpoch.SyncParticipation = float32(totalSyncVoted) / float32(totalSyncAssigned)
	}

	return &dbEpoch
}

func withdrawalCredType(withdrawalCredentials []byte) uint8 {
	if len(withdrawalCredentials) == 0 {
		return 0
	}
	return withdrawalCredentials[0]
}

func (dbw *dbWriter) persistBlockDeposits(tx *sqlx.Tx, block *Block, depositIndex *uint64, orphaned bool, overrideForkId *ForkKey) error {
	// insert deposits
	dbDeposits := dbw.buildDbDeposits(block, depositIndex, orphaned, overrideForkId)
	if orphaned {
		for idx := range dbDeposits {
			dbDeposits[idx].Orphaned = true
		}
	}

	if len(dbDeposits) > 0 {
		err := db.InsertDeposits(dbw.indexer.ctx, tx, dbDeposits)
		if err != nil {
			return fmt.Errorf("error inserting deposits: %v", err)
		}

		if overrideForkId != nil {
			if err := dbw.reconcileOnboardedBuilderDeposits(tx, dbDeposits, *overrideForkId); err != nil {
				return err
			}
		}
	}

	return nil
}

// reconcileOnboardedBuilderDeposits keeps the upgrade_to_gloas onboarded builder deposit copies in
// sync with their source validator deposits. The copies are written once at the fork boundary with
// the then-unfinalized fork id and are not re-persisted per block, so when a source deposit (a
// pre-gloas builder 0xB0 deposit) is persisted canonically its matching copy is moved onto the same
// fork id; otherwise the copy keeps its unfinalized fork id and later shows up as orphaned. It only
// runs once the fork has activated (i.e. the copy exists).
func (dbw *dbWriter) reconcileOnboardedBuilderDeposits(tx *sqlx.Tx, deposits []*dbtypes.Deposit, forkId ForkKey) error {
	chainState := dbw.indexer.consensusPool.GetChainState()
	gloasForkEpoch := chainState.GetSpecs().GloasForkEpoch
	if gloasForkEpoch == nil || chainState.CurrentEpoch() < phase0.Epoch(*gloasForkEpoch) {
		return nil
	}

	onboardingSlot := uint64(chainState.EpochToSlot(phase0.Epoch(*gloasForkEpoch)))
	for _, deposit := range deposits {
		// only pre-gloas builder (0xB0) deposits are onboarded into builder_deposits by the fork
		// transition, so only those have a copy to reconcile.
		if deposit.CredType != 0xB0 || uint64(chainState.EpochOfSlot(phase0.Slot(deposit.SlotNumber))) >= *gloasForkEpoch {
			continue
		}

		if err := db.UpdateOnboardedBuilderDepositForkId(dbw.indexer.ctx, tx, deposit.PublicKey, onboardingSlot, uint64(forkId)); err != nil {
			return err
		}
	}

	return nil
}

func (dbw *dbWriter) buildDbDeposits(block *Block, depositIndex *uint64, orphaned bool, overrideForkId *ForkKey) []*dbtypes.Deposit {
	blockBody := block.GetBlock(dbw.indexer.ctx)
	if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
		return nil
	}

	deposits := blockBody.Message.Body.Deposits

	dbDeposits := make([]*dbtypes.Deposit, len(deposits))
	for idx, deposit := range deposits {
		dbDeposit := &dbtypes.Deposit{
			SlotNumber:            uint64(block.Slot),
			SlotIndex:             uint64(idx),
			SlotRoot:              block.Root[:],
			Orphaned:              orphaned,
			ForkId:                uint64(block.forkId),
			PublicKey:             deposit.Data.PublicKey[:],
			WithdrawalCredentials: deposit.Data.WithdrawalCredentials,
			Amount:                uint64(deposit.Data.Amount),
			CredType:              withdrawalCredType(deposit.Data.WithdrawalCredentials),
		}
		if depositIndex != nil {
			cDepIdx := *depositIndex
			dbDeposit.Index = &cDepIdx
			*depositIndex++
		}
		if overrideForkId != nil {
			dbDeposit.ForkId = uint64(*overrideForkId)
		}

		dbDeposits[idx] = dbDeposit
	}

	return dbDeposits
}

func (dbw *dbWriter) persistBlockDepositRequests(tx *sqlx.Tx, block *Block, orphaned bool, overrideForkId *ForkKey) error {
	// insert deposits
	dbDeposits := dbw.buildDbDepositRequests(block, orphaned, overrideForkId)
	if orphaned {
		for idx := range dbDeposits {
			dbDeposits[idx].Orphaned = true
		}
	}

	if len(dbDeposits) > 0 {
		err := db.InsertDeposits(dbw.indexer.ctx, tx, dbDeposits)
		if err != nil {
			return fmt.Errorf("error inserting deposit requests: %v", err)
		}

		if overrideForkId != nil {
			if err := dbw.reconcileOnboardedBuilderDeposits(tx, dbDeposits, *overrideForkId); err != nil {
				return err
			}
		}
	}

	return nil
}

// getProcessedExecutionRequests returns the execution requests this block processes,
// together with the EL block number they were included in.
//
// In Gloas/EIP-7732 a block processes its parent's payload requests
// (parent_execution_requests) at this block's slot, so the requests are attributed to this
// block's slot (matching the beacon state's PendingDeposit.slot). They were included in the
// parent payload, so the EL block number they were dequeued in is the parent block's execution
// block number. When this block reveals a payload that equals its own payload number minus one;
// when this block is payload-less (the builder did not reveal a payload for this slot) its own
// payload is absent, so the parent block's execution number is used directly. Reading the
// requests from the block body keeps them available even when the payload envelope is missing.
// The first Gloas block carries an empty parent_execution_requests, so the last pre-Gloas
// requests are not counted twice. In earlier forks the requests live in the block body and were
// included in the block's own payload.
func (dbw *dbWriter) getProcessedExecutionRequests(block *Block) (*all.ExecutionRequests, uint64) {
	chainState := dbw.indexer.consensusPool.GetChainState()

	blockBody := block.GetBlock(dbw.indexer.ctx)
	if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
		return nil, 0
	}
	body := blockBody.Message.Body

	if chainState.IsEip7732Enabled(chainState.EpochOfSlot(block.Slot)) {
		var blockNumber uint64
		if payload := block.GetExecutionPayload(dbw.indexer.ctx); payload != nil && payload.Message.Payload.BlockNumber > 0 {
			blockNumber = payload.Message.Payload.BlockNumber - 1
		} else if parentRoot := block.GetParentRoot(); parentRoot != nil {
			// payload-less block: the processed requests come from the parent block's payload,
			// so use the parent's execution block number as the dequeue block.
			if parentBlock := dbw.indexer.GetBlockByRoot(*parentRoot); parentBlock != nil {
				if parentIndex := parentBlock.GetBlockIndex(dbw.indexer.ctx); parentIndex != nil {
					blockNumber = parentIndex.ExecutionNumber
				}
			}
			if blockNumber == 0 {
				// the parent may be pruned from the block cache (e.g. first slot of a finalized
				// epoch), so fall back to the database.
				if parentSlot := db.GetSlotByRoot(dbw.indexer.ctx, parentRoot[:]); parentSlot != nil && parentSlot.EthBlockNumber != nil {
					blockNumber = *parentSlot.EthBlockNumber
				}
			}
		}
		return body.ParentExecutionRequests, blockNumber
	}

	var blockNumber uint64
	if body.ExecutionPayload != nil {
		blockNumber = body.ExecutionPayload.BlockNumber
	}
	return body.ExecutionRequests, blockNumber
}

func (dbw *dbWriter) buildDbDepositRequests(block *Block, orphaned bool, overrideForkId *ForkKey) []*dbtypes.Deposit {
	requests, _ := dbw.getProcessedExecutionRequests(block)
	if requests == nil {
		return []*dbtypes.Deposit{}
	}

	deposits := requests.Deposits

	dbDeposits := make([]*dbtypes.Deposit, len(deposits))
	for idx, deposit := range deposits {
		dbDeposit := &dbtypes.Deposit{
			Index:                 &deposit.Index,
			SlotNumber:            uint64(block.Slot),
			SlotIndex:             uint64(idx),
			SlotRoot:              block.Root[:],
			Orphaned:              orphaned,
			ForkId:                uint64(block.forkId),
			PublicKey:             deposit.Pubkey[:],
			WithdrawalCredentials: deposit.WithdrawalCredentials,
			Amount:                uint64(deposit.Amount),
			CredType:              withdrawalCredType(deposit.WithdrawalCredentials),
		}
		if overrideForkId != nil {
			dbDeposit.ForkId = uint64(*overrideForkId)
		}

		dbDeposits[idx] = dbDeposit
	}

	return dbDeposits
}

// persistBlockBuilderDeposits persists the block's builder deposit requests
// (Gloas/EIP-8282) to the builder_deposits table. Pre-Gloas blocks carry none.
func (dbw *dbWriter) persistBlockBuilderDeposits(tx *sqlx.Tx, block *Block, orphaned bool, overrideForkId *ForkKey) error {
	dbDeposits := dbw.buildDbBuilderDeposits(block, orphaned, overrideForkId)
	if len(dbDeposits) > 0 {
		err := db.InsertBuilderDeposits(dbw.indexer.ctx, tx, dbDeposits)
		if err != nil {
			return fmt.Errorf("error inserting builder deposits: %v", err)
		}
	}

	return nil
}

func (dbw *dbWriter) buildDbBuilderDeposits(block *Block, orphaned bool, overrideForkId *ForkKey) []*dbtypes.BuilderDeposit {
	requests, blockNumber := dbw.getProcessedExecutionRequests(block)
	if requests == nil || len(requests.BuilderDeposits) == 0 {
		return []*dbtypes.BuilderDeposit{}
	}

	dbDeposits := make([]*dbtypes.BuilderDeposit, len(requests.BuilderDeposits))
	for idx, deposit := range requests.BuilderDeposits {
		dbDeposit := &dbtypes.BuilderDeposit{
			SlotNumber:            uint64(block.Slot),
			SlotRoot:              block.Root[:],
			SlotIndex:             uint64(idx),
			Orphaned:              orphaned,
			ForkId:                uint64(block.forkId),
			PublicKey:             deposit.Pubkey[:],
			WithdrawalCredentials: deposit.WithdrawalCredentials,
			Amount:                uint64(deposit.Amount),
			Signature:             deposit.Signature[:],
			BlockNumber:           blockNumber,
		}
		if builderIdx, found := dbw.indexer.builderPubkeyCache.Get(deposit.Pubkey); found {
			resolvedIdx := uint64(builderIdx)
			dbDeposit.BuilderIndex = &resolvedIdx
		}
		if overrideForkId != nil {
			dbDeposit.ForkId = uint64(*overrideForkId)
		}

		dbDeposits[idx] = dbDeposit
	}

	return dbDeposits
}

// persistGloasOnboardedBuilderDeposits copies the builder deposits that the one-time
// upgrade_to_gloas fork transition onboarded from the pending_deposits queue into the
// builder_deposits (+ builder_deposit_request_txs) tables. These deposits arrived through
// the validator deposit contract, so they are absent from the dedicated builder deposit
// tables; without this copy they never surface on the builder deposit / builder detail pages.
//
// The deposits are attributed to the first slot of the Gloas fork epoch (where onboarding
// happens) and keyed by the epoch's dependent root, so reloads upsert and sibling branches
// stay distinct. Execution-layer tx details are sourced from the regular deposit_txs table
// (matched by pubkey+amount+signature); the contract indexer persists those rows for recent
// unfinalized blocks too, so the lookup is reliable even shortly after the fork.
func (dbw *dbWriter) persistGloasOnboardedBuilderDeposits(tx *sqlx.Tx, epoch phase0.Epoch, dependentRoot phase0.Root, forkId ForkKey, onboarded []*electra.PendingDeposit) error {
	if len(onboarded) == 0 {
		return nil
	}

	chainState := dbw.indexer.consensusPool.GetChainState()
	slotNumber := uint64(epoch) * chainState.GetSpecs().SlotsPerEpoch

	// Index the deposit txs of every onboarded pubkey for matching. Top-ups share a pubkey, so
	// candidates are consumed in order as they are paired to keep the mapping 1:1.
	pubkeys := make([][]byte, 0, len(onboarded))
	seenPubkey := make(map[phase0.BLSPubKey]bool, len(onboarded))
	for _, deposit := range onboarded {
		if !seenPubkey[deposit.Pubkey] {
			seenPubkey[deposit.Pubkey] = true
			pubkeys = append(pubkeys, deposit.Pubkey[:])
		}
	}

	txsByPubkey := make(map[phase0.BLSPubKey][]*dbtypes.DepositTx, len(pubkeys))
	for _, depositTx := range db.GetDepositTxsByPublicKeys(dbw.indexer.ctx, pubkeys) {
		key := phase0.BLSPubKey(depositTx.PublicKey)
		txsByPubkey[key] = append(txsByPubkey[key], depositTx)
	}

	dbDeposits := make([]*dbtypes.BuilderDeposit, 0, len(onboarded))
	dbDepositTxs := make([]*dbtypes.BuilderDepositTx, 0, len(onboarded))
	firstSeen := make(map[phase0.BLSPubKey]bool, len(pubkeys))

	for idx, deposit := range onboarded {
		// The first deposit of a pubkey registers a new builder, later ones top it up.
		result := dbtypes.BuilderDepositRequestResultTopUp
		if !firstSeen[deposit.Pubkey] {
			firstSeen[deposit.Pubkey] = true
			result = dbtypes.BuilderDepositRequestResultNewBuilder
		}

		dbDeposit := &dbtypes.BuilderDeposit{
			SlotNumber:            slotNumber,
			SlotRoot:              dependentRoot[:],
			SlotIndex:             uint64(idx),
			Orphaned:              false,
			ForkId:                uint64(forkId),
			PublicKey:             deposit.Pubkey[:],
			WithdrawalCredentials: deposit.WithdrawalCredentials,
			Amount:                uint64(deposit.Amount),
			Signature:             deposit.Signature[:],
			Result:                result,
		}
		if builderIdx, found := dbw.indexer.builderPubkeyCache.Get(deposit.Pubkey); found {
			resolvedIdx := uint64(builderIdx)
			dbDeposit.BuilderIndex = &resolvedIdx
		}

		if depositTx := consumeMatchingDepositTx(txsByPubkey, deposit); depositTx != nil {
			dbDeposit.TxHash = depositTx.TxHash
			dbDeposit.BlockNumber = depositTx.BlockNumber

			// dequeue_block is set to the originating EL block so the page treats the request as
			// already included (its pending bucket is dequeue_block > highest indexed EL block).
			dbDepositTxs = append(dbDepositTxs, &dbtypes.BuilderDepositTx{
				BlockNumber:           depositTx.BlockNumber,
				BlockIndex:            uint64(idx),
				BlockTime:             depositTx.BlockTime,
				BlockRoot:             depositTx.BlockRoot,
				ForkId:                depositTx.ForkId,
				PublicKey:             deposit.Pubkey[:],
				WithdrawalCredentials: deposit.WithdrawalCredentials,
				Amount:                uint64(deposit.Amount),
				Signature:             deposit.Signature[:],
				BuilderIndex:          dbDeposit.BuilderIndex,
				TxHash:                depositTx.TxHash,
				TxSender:              depositTx.TxSender,
				TxTarget:              depositTx.TxTarget,
				DequeueBlock:          depositTx.BlockNumber,
			})
		}

		dbDeposits = append(dbDeposits, dbDeposit)
	}

	if err := db.InsertBuilderDeposits(dbw.indexer.ctx, tx, dbDeposits); err != nil {
		return fmt.Errorf("error inserting onboarded builder deposits: %v", err)
	}
	if len(dbDepositTxs) > 0 {
		if err := db.InsertBuilderDepositTxs(dbw.indexer.ctx, tx, dbDepositTxs); err != nil {
			return fmt.Errorf("error inserting onboarded builder deposit txs: %v", err)
		}
	}

	return nil
}

// consumeMatchingDepositTx returns and removes the first deposit tx that matches the pending
// deposit by amount and signature, so repeated top-ups of the same pubkey pair to distinct txs.
func consumeMatchingDepositTx(txsByPubkey map[phase0.BLSPubKey][]*dbtypes.DepositTx, deposit *electra.PendingDeposit) *dbtypes.DepositTx {
	candidates := txsByPubkey[deposit.Pubkey]
	for i, candidate := range candidates {
		if candidate.Amount == uint64(deposit.Amount) && bytes.Equal(candidate.Signature, deposit.Signature[:]) {
			txsByPubkey[deposit.Pubkey] = append(candidates[:i], candidates[i+1:]...)
			return candidate
		}
	}

	return nil
}

// persistBlockBuilderExits persists the block's builder exit requests
// (Gloas/EIP-8282) to the builder_exits table. Pre-Gloas blocks carry none.
func (dbw *dbWriter) persistBlockBuilderExits(tx *sqlx.Tx, block *Block, orphaned bool, overrideForkId *ForkKey) error {
	dbExits := dbw.buildDbBuilderExits(block, orphaned, overrideForkId)
	if len(dbExits) > 0 {
		err := db.InsertBuilderExits(dbw.indexer.ctx, tx, dbExits)
		if err != nil {
			return fmt.Errorf("error inserting builder exits: %v", err)
		}
	}

	return nil
}

func (dbw *dbWriter) buildDbBuilderExits(block *Block, orphaned bool, overrideForkId *ForkKey) []*dbtypes.BuilderExit {
	requests, blockNumber := dbw.getProcessedExecutionRequests(block)
	if requests == nil || len(requests.BuilderExits) == 0 {
		return []*dbtypes.BuilderExit{}
	}

	dbExits := make([]*dbtypes.BuilderExit, len(requests.BuilderExits))
	for idx, exit := range requests.BuilderExits {
		dbExit := &dbtypes.BuilderExit{
			SlotNumber:    uint64(block.Slot),
			SlotRoot:      block.Root[:],
			SlotIndex:     uint64(idx),
			Orphaned:      orphaned,
			ForkId:        uint64(block.forkId),
			SourceAddress: exit.SourceAddress[:],
			PublicKey:     exit.Pubkey[:],
			BlockNumber:   blockNumber,
		}
		if builderIdx, found := dbw.indexer.builderPubkeyCache.Get(exit.Pubkey); found {
			resolvedIdx := uint64(builderIdx)
			dbExit.BuilderIndex = &resolvedIdx
		}
		if overrideForkId != nil {
			dbExit.ForkId = uint64(*overrideForkId)
		}

		dbExits[idx] = dbExit
	}

	return dbExits
}

func (dbw *dbWriter) persistBlockVoluntaryExits(tx *sqlx.Tx, block *Block, orphaned bool, overrideForkId *ForkKey) error {
	// insert voluntary exits
	dbVoluntaryExits := dbw.buildDbVoluntaryExits(block, orphaned, overrideForkId)
	if len(dbVoluntaryExits) > 0 {
		err := db.InsertVoluntaryExits(dbw.indexer.ctx, tx, dbVoluntaryExits)
		if err != nil {
			return fmt.Errorf("error inserting voluntary exits: %v", err)
		}
	}

	return nil
}

func (dbw *dbWriter) buildDbVoluntaryExits(block *Block, orphaned bool, overrideForkId *ForkKey) []*dbtypes.VoluntaryExit {
	blockBody := block.GetBlock(dbw.indexer.ctx)
	if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
		return nil
	}

	voluntaryExits := blockBody.Message.Body.VoluntaryExits

	dbVoluntaryExits := make([]*dbtypes.VoluntaryExit, len(voluntaryExits))
	for idx, voluntaryExit := range voluntaryExits {
		dbVoluntaryExit := &dbtypes.VoluntaryExit{
			SlotNumber:     uint64(block.Slot),
			SlotIndex:      uint64(idx),
			SlotRoot:       block.Root[:],
			Orphaned:       orphaned,
			ForkId:         uint64(block.forkId),
			ValidatorIndex: uint64(voluntaryExit.Message.ValidatorIndex),
		}
		if overrideForkId != nil {
			dbVoluntaryExit.ForkId = uint64(*overrideForkId)
		}

		dbVoluntaryExits[idx] = dbVoluntaryExit
	}

	return dbVoluntaryExits
}

func (dbw *dbWriter) persistBlockWithdrawals(tx *sqlx.Tx, block *Block, orphaned bool, overrideForkId *ForkKey, sim *stateSimulator) error {
	dbWithdrawals := dbw.buildDbWithdrawals(block, orphaned, overrideForkId, tx, sim)
	if len(dbWithdrawals) > 0 {
		err := db.InsertWithdrawals(dbw.indexer.ctx, tx, dbWithdrawals)
		if err != nil {
			return fmt.Errorf("error inserting withdrawals: %v", err)
		}
	}

	// Update fork_id/orphaned for all entries of this block_uid (including fee recipient entries written by EL tx indexer)
	forkId := uint64(block.forkId)
	if overrideForkId != nil {
		forkId = uint64(*overrideForkId)
	}

	err := db.UpdateWithdrawalsForkId(dbw.indexer.ctx, tx, block.BlockUID, forkId, orphaned)
	if err != nil {
		return fmt.Errorf("error updating withdrawals fork id: %v", err)
	}

	return nil
}

// buildDbWithdrawals extracts CL withdrawals from the block and classifies their types.
// If tx is non-nil (persist path), missing accounts are created using that transaction.
// If tx is nil (read path), only existing accounts are looked up.
// If sim is non-nil, it's used to determine pending partial withdrawal count for type classification.
func (dbw *dbWriter) buildDbWithdrawals(block *Block, orphaned bool, overrideForkId *ForkKey, tx *sqlx.Tx, sim *stateSimulator) []*dbtypes.Withdrawal {
	chainState := dbw.indexer.consensusPool.GetChainState()

	var executionWithdrawals []*capella.Withdrawal
	if chainState.IsEip7732Enabled(chainState.EpochOfSlot(block.Slot)) {
		blockPayload := block.GetExecutionPayload(dbw.indexer.ctx)
		if blockPayload != nil {
			executionWithdrawals = blockPayload.Message.Payload.Withdrawals
		}
	} else {
		blockBody := block.GetBlock(dbw.indexer.ctx)
		if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
			return nil
		}

		executionPayload := blockBody.Message.Body.ExecutionPayload
		if executionPayload == nil || len(executionPayload.Withdrawals) == 0 {
			return nil
		}

		executionWithdrawals = executionPayload.Withdrawals
	}

	forkId := uint64(block.forkId)
	if overrideForkId != nil {
		forkId = uint64(*overrideForkId)
	}

	// Simulate pending withdrawal state to get classifications
	simResult := dbw.getWithdrawalSimResult(block, sim)

	blockEpoch := dbw.indexer.consensusPool.GetChainState().EpochOfSlot(block.Slot)

	dbWithdrawals := make([]*dbtypes.Withdrawal, len(executionWithdrawals))
	for idx, withdrawal := range executionWithdrawals {
		// Classify withdrawal type and resolve reference slot
		withdrawalType, refSlot := dbw.classifyWithdrawalType(idx, simResult, withdrawal.ValidatorIndex, phase0.Gwei(withdrawal.Amount), blockEpoch)

		dbWithdrawals[idx] = &dbtypes.Withdrawal{
			BlockUid:  block.BlockUID,
			BlockIdx:  int16(idx),
			Type:      withdrawalType,
			Orphaned:  orphaned,
			ForkId:    forkId,
			Validator: uint64(withdrawal.ValidatorIndex),
			Address:   withdrawal.Address[:],
			RefSlot:   refSlot,
			Amount:    uint64(withdrawal.Amount),
		}
	}

	// Resolve account IDs for withdrawal addresses
	dbw.resolveWithdrawalAccounts(executionWithdrawals, dbWithdrawals, tx)

	return dbWithdrawals
}

// getWithdrawalSimResult gets the withdrawal classification from the state simulator.
func (dbw *dbWriter) getWithdrawalSimResult(block *Block, sim *stateSimulator) *withdrawalSimResult {
	if sim == nil {
		// Reconstruct sim from epoch stats (read path)
		chainState := dbw.indexer.consensusPool.GetChainState()
		epochStats := dbw.indexer.epochCache.getEpochStatsByEpochAndRoot(chainState.EpochOfSlot(block.Slot), block.Root)
		if epochStats != nil {
			sim = newStateSimulator(dbw.indexer, epochStats)
		}
	}
	if sim == nil {
		return &withdrawalSimResult{}
	}

	return sim.replayWithdrawalState(block)
}

// classifyWithdrawalType determines the withdrawal type and reference slot for a single withdrawal.
// Uses the sim result for builder payment and partial withdrawal counts/classifications.
// The spec order in the execution payload is:
//
//	[0..BuilderPaymentCount-1]                                    = builder payments (from sim)
//	[BuilderPaymentCount..BuilderPaymentCount+PartialCount-1]     = requested withdrawals (type 3)
//	[remaining with builder flag]                                 = builder full withdrawal (type 5)
//	[remaining without builder flag, exited+withdrawable]         = full withdrawal (type 1)
//	[remaining without builder flag]                              = sweep withdrawal (type 2)
func (dbw *dbWriter) classifyWithdrawalType(idx int, simResult *withdrawalSimResult, validatorIndex phase0.ValidatorIndex, amount phase0.Gwei, blockEpoch phase0.Epoch) (uint8, *uint64) {
	isBuilder := uint64(validatorIndex)&BuilderIndexFlag != 0

	// First N withdrawals are builder payments — type and ref slot from sim
	if uint64(idx) < simResult.BuilderPaymentCount {
		if idx < len(simResult.BuilderPayments) {
			bp := simResult.BuilderPayments[idx]
			return bp.Type, bp.RefSlot
		}
		return dbtypes.WithdrawalTypeBuilderPayment, nil
	}

	// Next M withdrawals are from pending partial withdrawals (EIP-7002 requested)
	if uint64(idx) < simResult.BuilderPaymentCount+uint64(simResult.PartialCount) {
		return dbtypes.WithdrawalTypeRequestedWithdrawal, nil
	}

	// Remaining withdrawals with builder flag are builder sweep (full balance withdrawal)
	if isBuilder {
		return dbtypes.WithdrawalTypeBuilderFullWithdrawal, nil
	}

	// Check if this is a full withdrawal (validator exited and withdrawable, with significant amount)
	validator := dbw.indexer.GetValidatorByIndex(validatorIndex, nil)
	if validator != nil && validator.ExitEpoch != FarFutureEpoch && validator.WithdrawableEpoch <= blockEpoch {
		chainSpec := dbw.indexer.consensusPool.GetChainState().GetSpecs()
		fullWithdrawalThreshold := phase0.Gwei(0)
		if chainSpec.EjectionBalance > 500000000 { // 0.5 ETH in Gwei
			fullWithdrawalThreshold = phase0.Gwei(chainSpec.EjectionBalance - 500000000)
		}
		if amount >= fullWithdrawalThreshold {
			return dbtypes.WithdrawalTypeFullWithdrawal, nil
		}
	}

	// Default: sweep withdrawal (excess balance or small dust after exit)
	return dbtypes.WithdrawalTypeSweepWithdrawal, nil
}

// resolveWithdrawalAccounts looks up account IDs for withdrawal addresses.
// If tx is non-nil, missing accounts are created using that transaction.
// If tx is nil, only existing accounts are resolved.
func (dbw *dbWriter) resolveWithdrawalAccounts(withdrawals []*capella.Withdrawal, dbWithdrawals []*dbtypes.Withdrawal, tx *sqlx.Tx) {
	// Collect unique withdrawal addresses
	addrSet := make(map[bellatrix.ExecutionAddress]bool, len(withdrawals))
	addrList := make([][]byte, 0, len(withdrawals))
	for _, w := range withdrawals {
		if !addrSet[w.Address] {
			addrSet[w.Address] = true
			addrList = append(addrList, w.Address[:])
		}
	}

	// Batch lookup existing accounts
	accountMap, err := db.GetElAccountsByAddresses(dbw.indexer.ctx, addrList)
	if err != nil {
		dbw.indexer.logger.Warnf("error looking up withdrawal accounts: %v", err)
		return
	}

	// Create missing accounts only when we have a transaction (persist path)
	if tx != nil {
		for _, w := range withdrawals {
			addrHex := fmt.Sprintf("0x%x", w.Address[:])
			if _, ok := accountMap[addrHex]; !ok {
				newAccount := &dbtypes.ElAccount{
					Address: w.Address[:],
				}

				id, insertErr := db.InsertElAccount(dbw.indexer.ctx, tx, newAccount)
				if insertErr != nil {
					dbw.indexer.logger.Warnf("error inserting withdrawal account: %v", insertErr)
					continue
				}
				newAccount.ID = id
				accountMap[addrHex] = newAccount
			}
		}
	}

	// Set account IDs on withdrawal entries
	for idx, w := range withdrawals {
		addrHex := fmt.Sprintf("0x%x", w.Address[:])
		if acct, ok := accountMap[addrHex]; ok && acct.ID > 0 {
			dbWithdrawals[idx].AccountID = acct.ID
		}
	}
}

func (dbw *dbWriter) persistBlockSlashings(tx *sqlx.Tx, block *Block, orphaned bool, overrideForkId *ForkKey) error {
	// insert slashings
	dbSlashings := dbw.buildDbSlashings(block, orphaned, overrideForkId)
	if len(dbSlashings) > 0 {
		err := db.InsertSlashings(dbw.indexer.ctx, tx, dbSlashings)
		if err != nil {
			return fmt.Errorf("error inserting slashings: %v", err)
		}
	}

	return nil
}

func (dbw *dbWriter) buildDbSlashings(block *Block, orphaned bool, overrideForkId *ForkKey) []*dbtypes.Slashing {
	blockBody := block.GetBlock(dbw.indexer.ctx)
	if blockBody == nil || blockBody.Message == nil || blockBody.Message.Body == nil {
		return nil
	}

	proposerSlashings := blockBody.Message.Body.ProposerSlashings
	attesterSlashings := blockBody.Message.Body.AttesterSlashings
	proposerIndex := blockBody.Message.ProposerIndex

	dbSlashings := []*dbtypes.Slashing{}
	slashingIndex := 0

	for _, proposerSlashing := range proposerSlashings {
		dbSlashing := &dbtypes.Slashing{
			SlotNumber:     uint64(block.Slot),
			SlotIndex:      uint64(slashingIndex),
			SlotRoot:       block.Root[:],
			Orphaned:       orphaned,
			ForkId:         uint64(block.forkId),
			ValidatorIndex: uint64(proposerSlashing.SignedHeader1.Message.ProposerIndex),
			SlasherIndex:   uint64(proposerIndex),
			Reason:         dbtypes.ProposerSlashing,
		}
		if overrideForkId != nil {
			dbSlashing.ForkId = uint64(*overrideForkId)
		}

		slashingIndex++
		dbSlashings = append(dbSlashings, dbSlashing)
	}

	for _, attesterSlashing := range attesterSlashings {
		if attesterSlashing == nil || attesterSlashing.Attestation1 == nil || attesterSlashing.Attestation2 == nil {
			continue
		}

		att1AttestingIndices := attesterSlashing.Attestation1.AttestingIndices
		att2AttestingIndices := attesterSlashing.Attestation2.AttestingIndices
		if att1AttestingIndices == nil || att2AttestingIndices == nil {
			continue
		}

		for _, valIdx := range utils.FindMatchingIndices(att1AttestingIndices, att2AttestingIndices) {
			dbSlashing := &dbtypes.Slashing{
				SlotNumber:     uint64(block.Slot),
				SlotIndex:      uint64(slashingIndex),
				SlotRoot:       block.Root[:],
				Orphaned:       orphaned,
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

func (dbw *dbWriter) persistBlockConsolidationRequests(tx *sqlx.Tx, block *Block, orphaned bool, overrideForkId *ForkKey, sim *stateSimulator) error {
	// insert consolidation requests
	dbConsolidations := dbw.buildDbConsolidationRequests(block, orphaned, overrideForkId, sim)
	if orphaned {
		for idx := range dbConsolidations {
			dbConsolidations[idx].Orphaned = true
		}
	}

	if len(dbConsolidations) > 0 {
		err := db.InsertConsolidationRequests(dbw.indexer.ctx, tx, dbConsolidations)
		if err != nil {
			return fmt.Errorf("error inserting consolidation requests: %v", err)
		}
	}

	return nil
}

func (dbw *dbWriter) buildDbConsolidationRequests(block *Block, orphaned bool, overrideForkId *ForkKey, sim *stateSimulator) []*dbtypes.ConsolidationRequest {
	requests, blockNumber := dbw.getProcessedExecutionRequests(block)
	if requests == nil {
		return []*dbtypes.ConsolidationRequest{}
	}

	if sim == nil {
		chainState := dbw.indexer.consensusPool.GetChainState()
		epochStats := dbw.indexer.epochCache.getEpochStatsByEpochAndRoot(chainState.EpochOfSlot(block.Slot), block.Root)
		if epochStats != nil {
			sim = newStateSimulator(dbw.indexer, epochStats)
		}
	}

	consolidations := requests.Consolidations

	if len(consolidations) == 0 {
		return []*dbtypes.ConsolidationRequest{}
	}

	var blockResults [][]uint8
	if sim != nil {
		blockResults = sim.replayBlockResults(block)
	}

	dbConsolidations := make([]*dbtypes.ConsolidationRequest, len(consolidations))
	for idx, consolidation := range consolidations {
		dbConsolidation := &dbtypes.ConsolidationRequest{
			SlotNumber:    uint64(block.Slot),
			SlotRoot:      block.Root[:],
			SlotIndex:     uint64(idx),
			Orphaned:      orphaned,
			ForkId:        uint64(block.forkId),
			SourceAddress: consolidation.SourceAddress[:],
			SourcePubkey:  consolidation.SourcePubkey[:],
			TargetPubkey:  consolidation.TargetPubkey[:],
			BlockNumber:   blockNumber,
		}
		if overrideForkId != nil {
			dbConsolidation.ForkId = uint64(*overrideForkId)
		}
		if sourceIdx, found := dbw.indexer.pubkeyCache.Get(consolidation.SourcePubkey); found {
			sourceIdx := uint64(sourceIdx)
			dbConsolidation.SourceIndex = &sourceIdx
		}
		if targetIdx, found := dbw.indexer.pubkeyCache.Get(consolidation.TargetPubkey); found {
			targetIdx := uint64(targetIdx)
			dbConsolidation.TargetIndex = &targetIdx
		}

		if blockResults != nil {
			dbConsolidation.Result = blockResults[1][idx]
		}

		dbConsolidations[idx] = dbConsolidation
	}

	return dbConsolidations
}

func (dbw *dbWriter) persistBlockWithdrawalRequests(tx *sqlx.Tx, block *Block, orphaned bool, overrideForkId *ForkKey, sim *stateSimulator) error {
	// insert deposits
	dbWithdrawalRequests := dbw.buildDbWithdrawalRequests(block, orphaned, overrideForkId, sim)

	if len(dbWithdrawalRequests) > 0 {
		err := db.InsertWithdrawalRequests(dbw.indexer.ctx, tx, dbWithdrawalRequests)
		if err != nil {
			return fmt.Errorf("error inserting withdrawal requests: %v", err)
		}
	}

	return nil
}

func (dbw *dbWriter) buildDbWithdrawalRequests(block *Block, orphaned bool, overrideForkId *ForkKey, sim *stateSimulator) []*dbtypes.WithdrawalRequest {
	requests, blockNumber := dbw.getProcessedExecutionRequests(block)
	if requests == nil {
		return []*dbtypes.WithdrawalRequest{}
	}

	if sim == nil {
		chainState := dbw.indexer.consensusPool.GetChainState()
		epochStats := dbw.indexer.epochCache.getEpochStatsByEpochAndRoot(chainState.EpochOfSlot(block.Slot), block.Root)
		if epochStats != nil {
			sim = newStateSimulator(dbw.indexer, epochStats)
		}
	}

	withdrawalRequests := requests.Withdrawals

	if len(withdrawalRequests) == 0 {
		return []*dbtypes.WithdrawalRequest{}
	}

	var blockResults [][]uint8
	if sim != nil {
		blockResults = sim.replayBlockResults(block)
	}

	dbWithdrawalRequests := make([]*dbtypes.WithdrawalRequest, len(withdrawalRequests))
	for idx, withdrawalRequest := range withdrawalRequests {
		dbWithdrawalRequest := &dbtypes.WithdrawalRequest{
			SlotNumber:      uint64(block.Slot),
			SlotRoot:        block.Root[:],
			SlotIndex:       uint64(idx),
			Orphaned:        orphaned,
			ForkId:          uint64(block.forkId),
			SourceAddress:   withdrawalRequest.SourceAddress[:],
			ValidatorPubkey: withdrawalRequest.ValidatorPubkey[:],
			Amount:          db.ConvertUint64ToInt64(uint64(withdrawalRequest.Amount)),
			BlockNumber:     blockNumber,
		}
		if overrideForkId != nil {
			dbWithdrawalRequest.ForkId = uint64(*overrideForkId)
		}
		if validatorIdx, found := dbw.indexer.pubkeyCache.Get(withdrawalRequest.ValidatorPubkey); found {
			validatorIdx := uint64(validatorIdx)
			dbWithdrawalRequest.ValidatorIndex = &validatorIdx
		}

		if blockResults != nil {
			dbWithdrawalRequest.Result = blockResults[0][idx]
		}

		dbWithdrawalRequests[idx] = dbWithdrawalRequest
	}

	return dbWithdrawalRequests
}
