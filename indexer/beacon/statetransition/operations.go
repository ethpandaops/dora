package statetransition

import (
	"bytes"
	"slices"

	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/altair"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// processOperations implements process_operations.
// Processes all block body operations: slashings, attestations, deposits, exits, etc.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#operations
// Modified in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#modified-process_operations
func processOperations(s *stateAccessor, block *spec.VersionedSignedBeaconBlock) {
	proposerSlashings, _ := block.ProposerSlashings()
	for _, slashing := range proposerSlashings {
		processProposerSlashing(s, slashing)
	}

	attesterSlashings, _ := block.AttesterSlashings()
	for _, slashing := range attesterSlashings {
		processAttesterSlashing(s, slashing)
	}

	processAttestations(s, block)

	voluntaryExits, _ := block.VoluntaryExits()
	for _, exit := range voluntaryExits {
		processVoluntaryExit(s, exit)
	}

	blsChanges, _ := block.BLSToExecutionChanges()
	for _, change := range blsChanges {
		processBLSToExecutionChange(s, change)
	}

	// Process execution requests (Electra+/Fulu): deposits, withdrawals, consolidations.
	// Gloas moves these out of process_operations — the parent payload's requests
	// are processed at the start of block processing via processParentExecutionPayload.
	// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_execution_requests
	if s.version < spec.DataVersionGloas {
		requests, err := block.ExecutionRequests()
		if err == nil {
			applyExecutionRequests(s, requests)
		}
	}
}

// applyExecutionRequests processes a block/payload's execution-layer requests
// (deposits, withdrawal requests, consolidation requests) into the state.
// Shared by processOperations (pre-Gloas) and processParentExecutionPayload (Gloas+).
func applyExecutionRequests(s *stateAccessor, requests *electra.ExecutionRequests) {
	if requests == nil {
		return
	}
	for _, deposit := range requests.Deposits {
		s.PendingDeposits = append(s.PendingDeposits, &electra.PendingDeposit{
			Pubkey:                deposit.Pubkey,
			WithdrawalCredentials: deposit.WithdrawalCredentials,
			Amount:                deposit.Amount,
			Signature:             deposit.Signature,
			Slot:                  s.Slot,
		})
	}
	for _, withdrawal := range requests.Withdrawals {
		processWithdrawalRequest(s, withdrawal)
	}
	for _, consolidation := range requests.Consolidations {
		processConsolidationRequest(s, consolidation)
	}
}

// processProposerSlashing processes a proposer slashing.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#proposer-slashings
func processProposerSlashing(s *stateAccessor, slashing *phase0.ProposerSlashing) {
	if slashing == nil || slashing.SignedHeader1 == nil {
		return
	}
	proposerIndex := slashing.SignedHeader1.Message.ProposerIndex
	if int(proposerIndex) >= len(s.Validators) {
		return
	}
	slashValidator(s, proposerIndex)
}

// processAttesterSlashing processes an attester slashing.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#attester-slashings
func processAttesterSlashing(s *stateAccessor, slashing any) {
	att1, att2 := getSlashingAttestations(slashing)
	if att1 == nil || att2 == nil {
		return
	}

	att1Indices, _ := att1.AttestingIndices()
	att2Indices, _ := att2.AttestingIndices()
	if att1Indices == nil || att2Indices == nil {
		return
	}

	att2Set := make(map[uint64]bool, len(att2Indices))
	for _, idx := range att2Indices {
		att2Set[idx] = true
	}

	for _, idx := range att1Indices {
		if att2Set[idx] && phase0.ValidatorIndex(idx) < phase0.ValidatorIndex(len(s.Validators)) {
			slashValidator(s, phase0.ValidatorIndex(idx))
		}
	}
}

// processAttestations processes all attestations in the block.
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-process_attestation
func processAttestations(s *stateAccessor, block *spec.VersionedSignedBeaconBlock) {
	var attestations []*electra.Attestation

	switch block.Version {
	case spec.DataVersionFulu:
		if block.Fulu != nil && block.Fulu.Message != nil && block.Fulu.Message.Body != nil {
			attestations = block.Fulu.Message.Body.Attestations
		}
	case spec.DataVersionGloas:
		if block.Gloas != nil && block.Gloas.Message != nil && block.Gloas.Message.Body != nil {
			attestations = block.Gloas.Message.Body.Attestations
		}
	}

	for _, att := range attestations {
		processAttestation(s, att, s.caches.committeeCache)
	}
}

// processAttestation processes a single Electra+ attestation, updating participation flags.
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-process_attestation
func processAttestation(s *stateAccessor, att *electra.Attestation, cc *committeeCache) {
	if att == nil || att.Data == nil {
		return
	}

	data := att.Data
	currentEpoch := s.currentEpoch()
	previousEpoch := s.previousEpoch()

	if data.Target.Epoch != currentEpoch && data.Target.Epoch != previousEpoch {
		return
	}

	attestingIndices := s.getAttestingIndices(data.Slot, []byte(att.CommitteeBits), []byte(att.AggregationBits), cc)

	// Determine which participation flags to set based on attestation properties.
	isCurrentEpoch := data.Target.Epoch == currentEpoch
	inclusionDelay := uint64(s.Slot) - uint64(data.Slot)

	// Check correctness of source, target, head
	justifiedCheckpoint := s.PreviousJustifiedCheckpoint
	if isCurrentEpoch {
		justifiedCheckpoint = s.CurrentJustifiedCheckpoint
	}

	// Spec variable mapping (Deneb+):
	//   is_matching_source = data.source == justified_checkpoint
	//   is_matching_target = is_matching_source and target_root_matches
	//   is_matching_head   = is_matching_target and head_root_matches
	isMatchingSource := data.Source.Epoch == justifiedCheckpoint.Epoch && data.Source.Root == justifiedCheckpoint.Root
	isMatchingTarget := isMatchingSource && data.Target.Root == getBlockRoot(s, data.Target.Epoch)
	isMatchingHead := isMatchingTarget && data.BeaconBlockRoot == getBlockRootAtSlot(s, data.Slot)

	// Gloas: payload_matches check for head attestation.
	// https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#modified-process_attestation
	payloadMatches := true
	if s.version >= spec.DataVersionGloas {
		if isAttestationSameSlot(s, data) {
			payloadMatches = true
		} else {
			var payloadIndex uint64
			if s.getAvailabilityBit(data.Slot) {
				payloadIndex = 1
			}
			payloadMatches = uint64(data.Index) == payloadIndex
		}
	}

	// Determine which flags to set.
	// Modified in Deneb (EIP-7045): TIMELY_TARGET has no delay constraint.
	// https://github.com/ethereum/consensus-specs/blob/master/specs/deneb/beacon-chain.md#modified-get_attestation_participation_flag_indices
	var participationFlags [3]bool
	if isMatchingSource && inclusionDelay <= intSqrt(s.specs.SlotsPerEpoch) {
		participationFlags[TimelySourceFlagIndex] = true
	}
	if isMatchingTarget {
		participationFlags[TimelyTargetFlagIndex] = true
	}
	if isMatchingHead && payloadMatches && inclusionDelay == s.specs.MinAttestationInclusionDelay {
		participationFlags[TimelyHeadFlagIndex] = true
	}

	// Select the right participation array and (Gloas) the corresponding
	// builder pending payment slot. The Gloas spec keeps a 2-epoch sliding
	// window of payments — current epoch is the second half, previous epoch
	// the first half.
	var participation []altair.ParticipationFlags
	var builderPayment *gloas.BuilderPendingPayment
	var builderPaymentIdx uint64
	slotsPerEpoch := s.specs.SlotsPerEpoch
	if isCurrentEpoch {
		participation = s.CurrentEpochParticipation
		builderPaymentIdx = slotsPerEpoch + uint64(data.Slot)%slotsPerEpoch
	} else {
		participation = s.PreviousEpochParticipation
		builderPaymentIdx = uint64(data.Slot) % slotsPerEpoch
	}
	if s.version >= spec.DataVersionGloas && builderPaymentIdx < uint64(len(s.BuilderPendingPayments)) {
		builderPayment = s.BuilderPendingPayments[builderPaymentIdx]
	}

	sameSlot := s.version >= spec.DataVersionGloas && isAttestationSameSlot(s, data)

	// Update participation flags, compute proposer reward, and (Gloas) accumulate
	// builder payment weight from validators contributing new flags on same-slot
	// attestations.
	proposerRewardNumerator := uint64(0)
	for _, index := range attestingIndices {
		if int(index) >= len(participation) {
			continue
		}

		willSetNewFlag := false
		for fi := 0; fi < 3; fi++ {
			if !participationFlags[fi] {
				continue
			}
			if !hasFlag(participation[index], fi) {
				participation[index] |= altair.ParticipationFlags(1 << fi)
				proposerRewardNumerator += uint64(s.getBaseReward(index)) * ParticipationFlagWeights[fi]
				willSetNewFlag = true
			}
		}

		// Gloas: each validator contributes its effective balance to the
		// builder payment weight at most once per slot, when it first sets a
		// new flag on a same-slot attestation. Only counted when the slot
		// actually has a builder payment with non-zero amount.
		if willSetNewFlag && sameSlot && builderPayment != nil &&
			builderPayment.Withdrawal != nil && builderPayment.Withdrawal.Amount > 0 {
			builderPayment.Weight += s.Validators[index].EffectiveBalance
		}
	}

	// Proposer reward
	proposerRewardDenominator := uint64((WeightDenominator - ProposerWeight) * WeightDenominator / ProposerWeight)
	if proposerRewardDenominator > 0 {
		proposerReward := phase0.Gwei(proposerRewardNumerator / proposerRewardDenominator)
		proposerIndex := s.getProposerIndex()
		s.increaseBalance(proposerIndex, proposerReward)
	}
}

// processVoluntaryExit processes a voluntary exit.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#voluntary-exits
func processVoluntaryExit(s *stateAccessor, exit *phase0.SignedVoluntaryExit) {
	if exit == nil || exit.Message == nil {
		return
	}
	validatorIndex := exit.Message.ValidatorIndex
	if int(validatorIndex) >= len(s.Validators) {
		return
	}
	initiateValidatorExit(s, validatorIndex)
}

// processBLSToExecutionChange updates a validator's withdrawal credentials from BLS to execution.
// https://github.com/ethereum/consensus-specs/blob/master/specs/capella/beacon-chain.md#new-process_bls_to_execution_change
func processBLSToExecutionChange(s *stateAccessor, signed *capella.SignedBLSToExecutionChange) {
	if signed == nil || signed.Message == nil {
		return
	}

	change := signed.Message
	validatorIndex := change.ValidatorIndex
	if int(validatorIndex) >= len(s.Validators) {
		return
	}

	validator := s.Validators[validatorIndex]

	// Only apply if currently BLS credentials (0x00 prefix)
	if len(validator.WithdrawalCredentials) == 0 || validator.WithdrawalCredentials[0] != 0x00 {
		return
	}

	// Update to execution credentials (0x01 prefix)
	newCredentials := make([]byte, 32)
	newCredentials[0] = 0x01
	copy(newCredentials[12:], change.ToExecutionAddress[:])
	validator.WithdrawalCredentials = newCredentials
}

// processWithdrawalRequest processes an EL withdrawal request.
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_withdrawal_request
func processWithdrawalRequest(s *stateAccessor, request *electra.WithdrawalRequest) {
	if request == nil {
		return
	}
	isFullExitRequest := request.Amount == 0

	// If partial withdrawal queue is full, only full exits are processed.
	if uint64(len(s.PendingPartialWithdrawals)) == s.specs.PendingPartialWithdrawalsLimit && !isFullExitRequest {
		return
	}

	// Find validator by pubkey.
	validatorIndex := findValidatorByPubkey(s, request.ValidatorPubkey)
	if validatorIndex == nil {
		return
	}
	validator := s.Validators[*validatorIndex]

	// Verify withdrawal credentials: must be 0x01 or 0x02 (execution prefix) and
	// the source address must match the credential's address bytes.
	if !hasExecutionWithdrawalCredential(validator) {
		return
	}
	if !bytes.Equal(validator.WithdrawalCredentials[12:], request.SourceAddress[:]) {
		return
	}
	// Verify the validator is active and exit has not been initiated.
	if !isActiveValidator(validator, s.currentEpoch()) {
		return
	}
	if validator.ExitEpoch != FarFutureEpoch {
		return
	}
	// Validator must have been active long enough.
	if s.currentEpoch() < validator.ActivationEpoch+phase0.Epoch(s.specs.ShardCommitteePeriod) {
		return
	}

	pendingBalanceToWithdraw := getPendingBalanceToWithdraw(s, *validatorIndex)

	if isFullExitRequest {
		// Only exit if there are no pending partial withdrawals queued.
		if pendingBalanceToWithdraw == 0 {
			initiateValidatorExit(s, *validatorIndex)
		}
		return
	}

	// Partial withdrawal: only allowed for compounding (0x02) validators with
	// effective balance ≥ MIN_ACTIVATION_BALANCE and *actual* balance exceeding
	// MIN_ACTIVATION_BALANCE + already-pending withdrawals.
	if !hasCompoundingWithdrawalCredential(validator) {
		return
	}
	hasSufficientEffectiveBalance := validator.EffectiveBalance >= phase0.Gwei(s.specs.MinActivationBalance)
	hasExcessBalance := s.Balances[*validatorIndex] > phase0.Gwei(s.specs.MinActivationBalance)+pendingBalanceToWithdraw
	if !hasSufficientEffectiveBalance || !hasExcessBalance {
		return
	}

	// Withdraw at most the excess (balance - MIN - pending), capped by request.Amount.
	maxWithdrawable := s.Balances[*validatorIndex] - phase0.Gwei(s.specs.MinActivationBalance) - pendingBalanceToWithdraw
	toWithdraw := phase0.Gwei(request.Amount)
	if maxWithdrawable < toWithdraw {
		toWithdraw = maxWithdrawable
	}

	exitQueueEpoch := computeExitEpochAndUpdateChurn(s, toWithdraw)
	withdrawableEpoch := exitQueueEpoch + phase0.Epoch(s.specs.MinValidatorWithdrawbilityDelay)

	s.PendingPartialWithdrawals = append(s.PendingPartialWithdrawals, &electra.PendingPartialWithdrawal{
		ValidatorIndex:    *validatorIndex,
		Amount:            toWithdraw,
		WithdrawableEpoch: withdrawableEpoch,
	})
}

// blsG2PointAtInfinity is the canonical compressed encoding of the G2 point at
// infinity (0xc0 followed by 95 zero bytes). Used as a placeholder signature
// for synthetic pending deposits created via queueExcessActiveBalance.
var blsG2PointAtInfinity = func() phase0.BLSSignature {
	var sig phase0.BLSSignature
	sig[0] = 0xc0
	return sig
}()

// switchToCompoundingValidator switches a validator's withdrawal credentials to
// compounding (0x02) and moves any balance above MIN_ACTIVATION_BALANCE into the
// pending_deposits queue.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-switch_to_compounding_validator
func switchToCompoundingValidator(s *stateAccessor, index phase0.ValidatorIndex) {
	validator := s.Validators[index]
	validator.WithdrawalCredentials[0] = 0x02
	queueExcessActiveBalance(s, index)
}

// queueExcessActiveBalance moves any balance above MIN_ACTIVATION_BALANCE from
// the validator's balance into a synthetic pending deposit. Used when a
// validator switches to compounding credentials.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-queue_excess_active_balance
func queueExcessActiveBalance(s *stateAccessor, index phase0.ValidatorIndex) {
	balance := s.Balances[index]
	minActivation := phase0.Gwei(s.specs.MinActivationBalance)
	if balance <= minActivation {
		return
	}
	excess := balance - minActivation
	s.Balances[index] = minActivation

	validator := s.Validators[index]
	s.PendingDeposits = append(s.PendingDeposits, &electra.PendingDeposit{
		Pubkey:                validator.PublicKey,
		WithdrawalCredentials: append([]byte(nil), validator.WithdrawalCredentials...),
		Amount:                excess,
		Signature:             blsG2PointAtInfinity,
		Slot:                  0, // GENESIS_SLOT, distinguishes from real deposits
	})
}

// processConsolidationRequest processes an EL consolidation request.
// New in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_consolidation_request
func processConsolidationRequest(s *stateAccessor, request *electra.ConsolidationRequest) {
	if request == nil {
		return
	}

	sourceIndex := findValidatorByPubkey(s, request.SourcePubkey)
	targetIndex := findValidatorByPubkey(s, request.TargetPubkey)
	if sourceIndex == nil || targetIndex == nil {
		return
	}

	sourceValidator := s.Validators[*sourceIndex]
	targetValidator := s.Validators[*targetIndex]

	// Validate source credentials
	if len(sourceValidator.WithdrawalCredentials) == 0 || sourceValidator.WithdrawalCredentials[0] == 0x00 {
		return
	}
	if !bytes.Equal(sourceValidator.WithdrawalCredentials[12:], request.SourceAddress[:]) {
		return
	}

	// Self-consolidation: switch source to compounding credentials.
	// Per the spec, this also moves any excess balance (above 32 ETH) into the
	// pending_deposits queue via switch_to_compounding_validator.
	// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#new-process_consolidation_request
	if *sourceIndex == *targetIndex {
		if sourceValidator.WithdrawalCredentials[0] == 0x01 {
			switchToCompoundingValidator(s, *sourceIndex)
		}
		return
	}

	// Check queue limit
	if uint64(len(s.PendingConsolidations)) >= s.specs.PendingConsolidationsLimit {
		return
	}

	// Check target has compounding credentials
	if targetValidator.WithdrawalCredentials[0] != 0x02 {
		return
	}

	// Check both active and not exiting
	epoch := s.currentEpoch()
	if !isActiveValidator(sourceValidator, epoch) || sourceValidator.ExitEpoch != FarFutureEpoch {
		return
	}
	if !isActiveValidator(targetValidator, epoch) || targetValidator.ExitEpoch != FarFutureEpoch {
		return
	}

	// Check source age
	if epoch < sourceValidator.ActivationEpoch+phase0.Epoch(s.specs.ShardCommitteePeriod) {
		return
	}

	// Check no pending partial withdrawals for source
	for _, pw := range s.PendingPartialWithdrawals {
		if pw.ValidatorIndex == *sourceIndex {
			return
		}
	}

	s.PendingConsolidations = append(s.PendingConsolidations, &electra.PendingConsolidation{
		SourceIndex: *sourceIndex,
		TargetIndex: *targetIndex,
	})

	// Initiate exit for the source
	initiateValidatorExit(s, *sourceIndex)
}

// processExecutionPayloadBid records the builder's bid in builder_pending_payments.
// New in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-process_execution_payload_bid
func processExecutionPayloadBid(s *stateAccessor, block *spec.VersionedSignedBeaconBlock) {
	if s.version < spec.DataVersionGloas {
		return
	}
	if block.Gloas == nil || block.Gloas.Message == nil || block.Gloas.Message.Body == nil {
		return
	}

	signedBid := block.Gloas.Message.Body.SignedExecutionPayloadBid
	if signedBid == nil || signedBid.Message == nil {
		return
	}

	bid := signedBid.Message
	amount := bid.Value

	// Record the pending payment if there is some payment
	if amount > 0 {
		slotsPerEpoch := s.specs.SlotsPerEpoch
		paymentIdx := slotsPerEpoch + uint64(s.Slot)%slotsPerEpoch
		if paymentIdx < uint64(len(s.BuilderPendingPayments)) {
			s.BuilderPendingPayments[paymentIdx] = &gloas.BuilderPendingPayment{
				Weight: 0,
				Withdrawal: &gloas.BuilderPendingWithdrawal{
					FeeRecipient: bid.FeeRecipient,
					Amount:       amount,
					BuilderIndex: bid.BuilderIndex,
				},
			}
		}
	}

	// Cache the signed execution payload bid (always, regardless of amount)
	s.LatestExecutionPayloadBid = bid
}

// processSyncAggregate processes the sync committee aggregate.
// https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#sync-aggregate-processing
func processSyncAggregate(s *stateAccessor, block *spec.VersionedSignedBeaconBlock) {
	syncAggregate, err := block.SyncAggregate()
	if err != nil || syncAggregate == nil {
		return
	}

	committeeSize := s.specs.SyncCommitteeSize
	if committeeSize == 0 || s.CurrentSyncCommittee == nil {
		return
	}

	totalActiveBalance := s.getTotalActiveBalance()
	totalActiveIncrements := uint64(totalActiveBalance) / s.specs.EffectiveBalanceIncrement
	totalBaseRewards := totalActiveIncrements * uint64(s.getBaseRewardPerIncrement())
	maxParticipantRewards := totalBaseRewards * SyncRewardWeight / WeightDenominator / s.specs.SlotsPerEpoch
	participantReward := phase0.Gwei(maxParticipantRewards / committeeSize)
	proposerReward := participantReward * ProposerWeight / (WeightDenominator - ProposerWeight)

	// Build pubkey → validator index map for the sync committee
	syncCommitteePubkeys := s.CurrentSyncCommittee.Pubkeys
	proposerIndex := s.getProposerIndex()

	for i := uint64(0); i < committeeSize && i < uint64(len(syncCommitteePubkeys)); i++ {
		validatorIndex := findValidatorByPubkey(s, syncCommitteePubkeys[i])
		if validatorIndex == nil {
			continue
		}

		byteIdx := i / 8
		bitIdx := i % 8
		if byteIdx < uint64(len(syncAggregate.SyncCommitteeBits)) &&
			syncAggregate.SyncCommitteeBits[byteIdx]&(1<<bitIdx) != 0 {
			s.increaseBalance(*validatorIndex, participantReward)
			s.increaseBalance(proposerIndex, proposerReward)
		} else {
			s.decreaseBalance(*validatorIndex, participantReward)
		}
	}
}

// slashValidator applies the slashing penalty to a validator.
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-slash_validator
func slashValidator(s *stateAccessor, index phase0.ValidatorIndex) {
	validator := s.Validators[index]
	if validator.Slashed {
		return
	}

	epoch := s.currentEpoch()
	validator.Slashed = true
	validator.WithdrawableEpoch = maxEpoch(validator.WithdrawableEpoch, epoch+phase0.Epoch(s.specs.EpochsPerSlashingVector))

	if validator.ExitEpoch == FarFutureEpoch {
		initiateValidatorExit(s, index)
	}

	slashingIdx := uint64(epoch) % s.specs.EpochsPerSlashingVector
	s.Slashings[slashingIdx] += validator.EffectiveBalance

	minPenaltyQuotient := s.specs.MinSlashingPenaltyQuotientElectra
	if minPenaltyQuotient == 0 {
		minPenaltyQuotient = s.specs.MinSlashingPenaltyQuotientBellatrix
	}
	if minPenaltyQuotient == 0 {
		minPenaltyQuotient = s.specs.MinSlashingPenaltyQuotient
	}
	if minPenaltyQuotient > 0 {
		penalty := validator.EffectiveBalance / phase0.Gwei(minPenaltyQuotient)
		s.decreaseBalance(index, penalty)
	}

	// Proposer + whistleblower reward
	proposerIndex := s.getProposerIndex()
	whistleblowerRewardQuotient := s.specs.WhistleblowerRewardQuotientElectra
	if whistleblowerRewardQuotient == 0 {
		whistleblowerRewardQuotient = s.specs.WhitelistRewardQuotient
	}
	if whistleblowerRewardQuotient > 0 {
		whistleblowerReward := validator.EffectiveBalance / phase0.Gwei(whistleblowerRewardQuotient)
		proposerReward := whistleblowerReward * ProposerWeight / WeightDenominator
		s.increaseBalance(proposerIndex, proposerReward)
	}
}

// getProposerIndex returns the proposer for the current slot from the lookahead.
// Modified in Fulu: https://github.com/ethereum/consensus-specs/blob/master/specs/fulu/beacon-chain.md#modified-get_beacon_proposer_index
func (s *stateAccessor) getProposerIndex() phase0.ValidatorIndex {
	if len(s.ProposerLookahead) > 0 {
		idx := uint64(s.Slot) % s.specs.SlotsPerEpoch
		return s.ProposerLookahead[idx]
	}
	// Fallback: compute directly
	activeIndices := s.getActiveValidatorIndices(s.currentEpoch())
	return computeProposerIndex(s, activeIndices, s.currentEpoch(), s.Slot)
}

// getBlockRootAtSlot returns the block root at a specific slot.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#get_block_root_at_slot
func getBlockRootAtSlot(s *stateAccessor, slot phase0.Slot) phase0.Root {
	return s.BlockRoots[uint64(slot)%s.specs.SlotsPerHistoricalRoot]
}

// isAttestationSameSlot checks if the attestation is for the block at the attestation slot.
// New in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-is_attestation_same_slot
func isAttestationSameSlot(s *stateAccessor, data *phase0.AttestationData) bool {
	if data.Slot == 0 {
		return true
	}
	blockRoot := data.BeaconBlockRoot
	slotBlockRoot := getBlockRootAtSlot(s, data.Slot)
	prevBlockRoot := getBlockRootAtSlot(s, data.Slot-1)
	return blockRoot == slotBlockRoot && blockRoot != prevBlockRoot
}

// findValidatorByPubkey looks up a validator index by BLS public key.
// Uses a cached map for O(1) lookups after first call.
func findValidatorByPubkey(s *stateAccessor, pubkey phase0.BLSPubKey) *phase0.ValidatorIndex {
	if s.caches.pubkeyCache == nil {
		epoch := s.currentEpoch()

		// Include active validators AND recently-exited validators that could
		// still be serving on a sync committee. Sync committees are recomputed
		// every EPOCHS_PER_SYNC_COMMITTEE_PERIOD epochs; a committee chosen at
		// the start of the current period serves for its full duration, so we
		// need validators that were active at any point in the last 2 periods.
		syncPeriod := phase0.Epoch(s.specs.EpochsPerSyncCommitteePeriod)
		cutoff := phase0.Epoch(0)
		if epoch > 2*syncPeriod {
			cutoff = epoch - 2*syncPeriod
		}

		s.caches.pubkeyCache = make(map[phase0.BLSPubKey]phase0.ValidatorIndex, len(s.Validators))
		for i, v := range s.Validators {
			// Active now, or exited recently enough to still be on a sync committee.
			if isActiveValidator(v, epoch) || v.ExitEpoch >= cutoff {
				s.caches.pubkeyCache[v.PublicKey] = phase0.ValidatorIndex(i)
			}
		}
	}
	if idx, ok := s.caches.pubkeyCache[pubkey]; ok {
		return &idx
	}
	return nil
}

func maxEpoch(a, b phase0.Epoch) phase0.Epoch {
	if a > b {
		return a
	}
	return b
}

// getSlashingAttestations extracts both attestations from an attester slashing.
func getSlashingAttestations(slashing any) (att1, att2 interface {
	AttestingIndices() ([]uint64, error)
}) {
	type attestationPair interface {
		Attestation1() (interface{ AttestingIndices() ([]uint64, error) }, error)
		Attestation2() (interface{ AttestingIndices() ([]uint64, error) }, error)
	}

	if s, ok := slashing.(attestationPair); ok {
		a1, _ := s.Attestation1()
		a2, _ := s.Attestation2()
		return a1, a2
	}
	return nil, nil
}

// Unused import guards.
var (
	_ = (*electra.WithdrawalRequest)(nil)
	_ = (*electra.ConsolidationRequest)(nil)
	_ = slices.Contains[[]int]
)
