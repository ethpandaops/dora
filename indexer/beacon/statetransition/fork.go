package statetransition

import (
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/indexer/beacon/depositsig"
)

// builderWithdrawalPrefix is BUILDER_WITHDRAWAL_PREFIX (Gloas/EIP-8282): the 0xB0
// withdrawal-credential prefix that marks a deposit as a builder deposit.
const builderWithdrawalPrefix byte = 0xB0

// isBuilderWithdrawalCredential reports whether the credentials use the builder prefix (0xB0).
func isBuilderWithdrawalCredential(wc []byte) bool {
	return len(wc) > 0 && wc[0] == builderWithdrawalPrefix
}

// maybeUpgradeToGloas applies the upgrade_to_gloas irregular state change at the first slot of
// GLOAS_FORK_EPOCH, mirroring the spec trigger
// (state.slot % SLOTS_PER_EPOCH == 0 and compute_epoch_at_slot(state.slot) == GLOAS_FORK_EPOCH).
// It is a no-op for every other slot, for chains without a configured Gloas fork, and for states
// that are already Gloas. The onboarded builder deposits are reported via info (if provided).
func maybeUpgradeToGloas(s *stateAccessor, info *TransitionInfo) {
	if s.specs.GloasForkEpoch == nil || s.Version >= spec.DataVersionGloas {
		return
	}
	if uint64(s.Slot)%s.specs.SlotsPerEpoch != 0 {
		return
	}
	if uint64(s.currentEpoch()) != *s.specs.GloasForkEpoch {
		return
	}

	onboarded := upgradeToGloas(s)
	if info != nil {
		info.GloasOnboardedDeposits = onboarded
	}
}

// upgradeToGloas implements upgrade_to_gloas: it mutates the embedded state in place from Fulu to
// Gloas (EIP-7732/EIP-8282), initializing the new builder-related fields and the PTC window, then
// onboards builders from the pending_deposits queue. It returns the deposits that were onboarded.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/fork.md
func upgradeToGloas(s *stateAccessor) []*electra.PendingDeposit {
	epoch := s.currentEpoch()
	header := s.LatestExecutionPayloadHeader

	// [Modified in Gloas] fork versions
	s.Fork = &phase0.Fork{
		PreviousVersion: s.Fork.CurrentVersion,
		CurrentVersion:  s.specs.GloasForkVersion,
		Epoch:           epoch,
	}

	// From here the state is treated as Gloas for HTR and all fork-conditional logic.
	s.Version = spec.DataVersionGloas

	// [New in Gloas:EIP7732] latest_block_hash carries over the (removed) execution payload header hash.
	if header != nil {
		s.LatestBlockHash = header.BlockHash
	}

	// [New in Gloas:EIP7732] empty builder registry and withdrawal cursor.
	s.Builders = make([]*gloas.Builder, 0)
	s.NextWithdrawalBuilderIndex = 0

	// [New in Gloas:EIP7732] execution_payload_availability: a Bitvector[SLOTS_PER_HISTORICAL_ROOT]
	// with every bit set (all historical payloads are considered available at the fork).
	availability := make([]uint8, s.specs.SlotsPerHistoricalRoot/8)
	for i := range availability {
		availability[i] = 0xFF
	}
	s.ExecutionPayloadAvailability = availability

	// [New in Gloas:EIP7732] 2 * SLOTS_PER_EPOCH empty builder pending payments, no pending withdrawals.
	payments := make([]*gloas.BuilderPendingPayment, 2*s.specs.SlotsPerEpoch)
	for i := range payments {
		payments[i] = &gloas.BuilderPendingPayment{}
	}
	s.BuilderPendingPayments = payments
	s.BuilderPendingWithdrawals = make([]*gloas.BuilderPendingWithdrawal, 0)

	// [New in Gloas:EIP7732] latest_execution_payload_bid seeded from the header, with the root of
	// an empty ExecutionRequests. A HTR failure leaves a zero root (best effort; only affects the
	// state root used to short-circuit later replays, never the extracted field values).
	var execRequestsRoot phase0.Root
	if root, err := s.dynSsz.HashTreeRoot(&all.ExecutionRequests{Version: spec.DataVersionGloas}); err == nil {
		execRequestsRoot = phase0.Root(root)
	}
	bid := &all.ExecutionPayloadBid{
		Version:               spec.DataVersionGloas,
		ExecutionRequestsRoot: execRequestsRoot,
	}
	if header != nil {
		bid.BlockHash = header.BlockHash
		bid.GasLimit = header.GasLimit
	}
	s.LatestExecutionPayloadBid = bid
	s.PayloadExpectedWithdrawals = make([]*capella.Withdrawal, 0)

	// [New in Gloas:EIP7732] PTC assignment window.
	s.PTCWindow = initializePtcWindow(s)

	// [Removed in Gloas:EIP7732] latest_execution_payload_header (replaced by the bid above).
	s.LatestExecutionPayloadHeader = nil

	// [New in Gloas:EIP7732] one-time onboarding of builders from the pending_deposits queue.
	return onboardBuildersFromPendingDeposits(s)
}

// initializePtcWindow implements initialize_ptc_window: an empty previous epoch (SLOTS_PER_EPOCH
// vectors of PTC_SIZE zero indices) followed by the computed PTCs for the current epoch and the
// next MIN_SEED_LOOKAHEAD epochs.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-initialize_ptc_window
func initializePtcWindow(s *stateAccessor) [][]phase0.ValidatorIndex {
	slotsPerEpoch := s.specs.SlotsPerEpoch
	window := make([][]phase0.ValidatorIndex, 0, (2+s.specs.MinSeedLookahead)*slotsPerEpoch)

	// empty_previous_epoch
	for i := uint64(0); i < slotsPerEpoch; i++ {
		window = append(window, make([]phase0.ValidatorIndex, s.specs.PtcSize))
	}

	currentEpoch := s.currentEpoch()
	for e := uint64(0); e < 1+s.specs.MinSeedLookahead; e++ {
		epoch := currentEpoch + phase0.Epoch(e)
		startSlot := uint64(epoch) * slotsPerEpoch
		for i := uint64(0); i < slotsPerEpoch; i++ {
			window = append(window, computePtc(s, phase0.Slot(startSlot+i)))
		}
	}

	return window
}

// onboardBuildersFromPendingDeposits implements onboard_builders_from_pending_deposits: it walks
// the pending_deposits queue once, onboarding builder-credential deposits (new registrations and
// top-ups) into the builder registry and dropping them from the queue, while leaving validator
// deposits in place. It returns the deposits that were onboarded as builders.
//
// The proof-of-possession signatures are verified under the regular deposit domain (these deposits
// came through the validator deposit contract) in a single batch up front — the queue can hold
// thousands of deposits, so per-deposit verification at the fork would be slow. The onboarding pass
// itself must stay sequential: each registration mutates the builder set, turning later same-pubkey
// deposits into top-ups, and the pending-validator check depends on the kept queue built so far.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#new-onboard_builders_from_pending_deposits
func onboardBuildersFromPendingDeposits(s *stateAccessor) []*electra.PendingDeposit {
	onboarded := make([]*electra.PendingDeposit, 0)

	validatorPubkeys := make(map[phase0.BLSPubKey]bool, len(s.Validators))
	for _, v := range s.Validators {
		validatorPubkeys[v.PublicKey] = true
	}

	sigValid := batchVerifyOnboardingSignatures(s, validatorPubkeys)

	// The builder registry is empty pre-fork and only this loop grows it, so a pubkey→index map
	// mirrors state.builders exactly (used to detect top-ups). keptPubkeys mirrors the deposits
	// that stay in the queue, so a builder deposit sharing a pubkey with a pending validator
	// deposit is kept rather than onboarded.
	kept := make([]*electra.PendingDeposit, 0, len(s.PendingDeposits))
	keptPubkeys := make(map[phase0.BLSPubKey]bool, len(s.PendingDeposits))
	builderIndexByPubkey := make(map[phase0.BLSPubKey]uint64)

	for i, deposit := range s.PendingDeposits {
		// deposits for existing validators always stay in the pending queue
		if validatorPubkeys[deposit.Pubkey] {
			kept = append(kept, deposit)
			keptPubkeys[deposit.Pubkey] = true
			continue
		}

		if builderIndex, isBuilder := builderIndexByPubkey[deposit.Pubkey]; isBuilder {
			// top up an already-onboarded builder
			s.Builders[builderIndex].Balance += deposit.Amount
		} else {
			// non-builder deposits, and builder deposits for a pubkey that already has a pending
			// validator deposit, stay in the queue (applied to that validator later).
			if !isBuilderWithdrawalCredential(deposit.WithdrawalCredentials) {
				kept = append(kept, deposit)
				keptPubkeys[deposit.Pubkey] = true
				continue
			}
			if keptPubkeys[deposit.Pubkey] {
				kept = append(kept, deposit)
				continue
			}
			// new builder: onboarded only with a valid proof-of-possession; an invalid signature is
			// dropped entirely (neither kept nor onboarded).
			if !sigValid[i] {
				continue
			}
			var execAddr bellatrix.ExecutionAddress
			copy(execAddr[:], deposit.WithdrawalCredentials[12:])
			addBuilderToRegistry(s, deposit.Pubkey, deposit.WithdrawalCredentials[0], execAddr, deposit.Amount, deposit.Slot)
			builderIndexByPubkey[deposit.Pubkey] = uint64(len(s.Builders) - 1)
		}

		// onboarded as a builder (new registration or top-up)
		onboarded = append(onboarded, deposit)
	}

	s.PendingDeposits = kept
	return onboarded
}

// batchVerifyOnboardingSignatures batch-verifies the proof-of-possession of every builder-credential
// pending deposit for a pubkey that is not already a validator (a superset of the deposits whose
// signature actually gates a new builder registration), returning per-deposit validity indexed by
// position in state.pending_deposits. Verification uses the regular deposit domain.
func batchVerifyOnboardingSignatures(s *stateAccessor, validatorPubkeys map[phase0.BLSPubKey]bool) []bool {
	sigValid := make([]bool, len(s.PendingDeposits))

	inputs := make([]depositsig.Input, 0, len(s.PendingDeposits))
	indexes := make([]int, 0, len(s.PendingDeposits))
	for i, deposit := range s.PendingDeposits {
		if validatorPubkeys[deposit.Pubkey] || !isBuilderWithdrawalCredential(deposit.WithdrawalCredentials) {
			continue
		}
		inputs = append(inputs, depositsig.Input{
			Pubkey:                deposit.Pubkey,
			WithdrawalCredentials: deposit.WithdrawalCredentials,
			Amount:                deposit.Amount,
			Signature:             deposit.Signature,
		})
		indexes = append(indexes, i)
	}

	if len(inputs) > 0 {
		results := depositsig.VerifyBatch(inputs, depositsig.Domain(s.specs.GenesisForkVersion))
		for j, idx := range indexes {
			sigValid[idx] = results[j]
		}
	}

	return sigValid
}
