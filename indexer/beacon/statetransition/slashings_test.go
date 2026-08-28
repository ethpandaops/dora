package statetransition

import (
	"testing"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

const gwei = phase0.Gwei(1_000_000_000)

// testSpecs returns a mainnet-shaped chain spec for the values the slashing,
// churn and deposit paths read.
func testSpecs() *consensus.ChainSpec {
	specs := &consensus.ChainSpec{
		ChainSpecPreset: consensus.ChainSpecPreset{
			SlotsPerEpoch:                      32,
			EffectiveBalanceIncrement:          uint64(gwei),
			MaxSeedLookahead:                   4,
			EpochsPerSlashingVector:            8192,
			WhitelistRewardQuotient:            512,
			MinSlashingPenaltyQuotient:         128,
			MaxEffectiveBalance:                32 * uint64(gwei),
			MinSlashingPenaltyQuotientElectra:  4096,
			WhistleblowerRewardQuotientElectra: 4096,
			MinActivationBalance:               32 * uint64(gwei),
			MaxEffectiveBalanceElectra:         2048 * uint64(gwei),
		},
	}
	specs.MinValidatorWithdrawbilityDelay = 256
	specs.ChurnLimitQuotient = 65536
	specs.MinPerEpochChurnLimitElectra = 128 * uint64(gwei)
	specs.MaxPerEpochActivationExitChurnLimit = 256 * uint64(gwei)
	specs.ChurnLimitQuotientGloas = 32768
	specs.ConsolidationChurnLimitQuotient = 65536
	specs.MaxPerEpochActivationChurnLimitGloas = 256 * uint64(gwei)

	return specs
}

// newTestState builds a Gloas state at the given epoch with validatorCount active
// 32 ETH validators, all proposal slots assigned to the last validator.
func newTestState(specs *consensus.ChainSpec, epoch phase0.Epoch, validatorCount int) *stateAccessor {
	validators := make([]*phase0.Validator, validatorCount)
	balances := make([]phase0.Gwei, validatorCount)
	for i := range validators {
		validators[i] = &phase0.Validator{
			WithdrawalCredentials:      append([]byte{0x01}, make([]byte, 31)...),
			EffectiveBalance:           32 * gwei,
			ActivationEligibilityEpoch: 0,
			ActivationEpoch:            0,
			ExitEpoch:                  FarFutureEpoch,
			WithdrawableEpoch:          FarFutureEpoch,
		}
		balances[i] = 32 * gwei
	}

	lookahead := make([]phase0.ValidatorIndex, specs.SlotsPerEpoch)
	for i := range lookahead {
		lookahead[i] = phase0.ValidatorIndex(validatorCount - 1)
	}

	return &stateAccessor{
		BeaconState: &all.BeaconState{
			Version:                   spec.DataVersionGloas,
			Slot:                      phase0.Slot(uint64(epoch) * specs.SlotsPerEpoch),
			Validators:                validators,
			Balances:                  balances,
			Slashings:                 make([]phase0.Gwei, specs.EpochsPerSlashingVector),
			ProposerLookahead:         lookahead,
			DepositRequestsStartIndex: unsetDepositRequestsStartIndex,
		},
		specs:  specs,
		caches: newStateTransitionCaches(),
	}
}

// TestSlashValidator checks slash_validator against the values the spec produces:
// the slashing delay wins over the exit's withdrawability delay, and the proposer
// collects the whistleblower reward in full because it is also the whistleblower.
func TestSlashValidator(t *testing.T) {
	specs := testSpecs()
	const epoch = phase0.Epoch(10)
	s := newTestState(specs, epoch, 3)
	proposerIndex := phase0.ValidatorIndex(2)

	slashValidator(s, 0)

	validator := s.Validators[0]
	if !validator.Slashed {
		t.Fatal("validator not marked slashed")
	}

	// compute_exit_epoch_and_update_churn: the balance fits in the first available epoch.
	wantExitEpoch := computeActivationExitEpoch(epoch, specs)
	if validator.ExitEpoch != wantExitEpoch {
		t.Errorf("exit epoch = %d, want %d", validator.ExitEpoch, wantExitEpoch)
	}

	// max(exit_epoch + MIN_VALIDATOR_WITHDRAWABILITY_DELAY, epoch + EPOCHS_PER_SLASHINGS_VECTOR)
	wantWithdrawable := epoch + phase0.Epoch(specs.EpochsPerSlashingVector)
	if validator.WithdrawableEpoch != wantWithdrawable {
		t.Errorf("withdrawable epoch = %d, want %d", validator.WithdrawableEpoch, wantWithdrawable)
	}

	if got := s.Slashings[uint64(epoch)%specs.EpochsPerSlashingVector]; got != 32*gwei {
		t.Errorf("slashings entry = %d, want %d", got, 32*gwei)
	}

	wantPenalty := 32 * gwei / phase0.Gwei(specs.MinSlashingPenaltyQuotientElectra)
	if got := s.Balances[0]; got != 32*gwei-wantPenalty {
		t.Errorf("slashed balance = %d, want %d", got, 32*gwei-wantPenalty)
	}

	// The proposer is the whistleblower, so it receives the whole reward.
	wantReward := 32 * gwei / phase0.Gwei(specs.WhistleblowerRewardQuotientElectra)
	if got := s.Balances[proposerIndex] - 32*gwei; got != wantReward {
		t.Errorf("proposer reward = %d, want %d", got, wantReward)
	}
}

// TestSlashValidatorAlreadyExiting checks that a validator that already left the
// exit queue keeps its exit epoch and still gets the slashing withdrawability delay.
func TestSlashValidatorAlreadyExiting(t *testing.T) {
	specs := testSpecs()
	const epoch = phase0.Epoch(10)
	s := newTestState(specs, epoch, 3)
	s.Validators[0].ExitEpoch = 12
	s.Validators[0].WithdrawableEpoch = 12 + phase0.Epoch(specs.MinValidatorWithdrawbilityDelay)

	slashValidator(s, 0)

	if s.Validators[0].ExitEpoch != 12 {
		t.Errorf("exit epoch = %d, want it to stay 12", s.Validators[0].ExitEpoch)
	}
	if want := epoch + phase0.Epoch(specs.EpochsPerSlashingVector); s.Validators[0].WithdrawableEpoch != want {
		t.Errorf("withdrawable epoch = %d, want %d", s.Validators[0].WithdrawableEpoch, want)
	}
}

// TestProcessAttesterSlashingSkipsUnslashable checks that only the intersecting
// indices that are slashable at the current epoch are slashed.
func TestProcessAttesterSlashingSkipsUnslashable(t *testing.T) {
	specs := testSpecs()
	const epoch = phase0.Epoch(10)
	s := newTestState(specs, epoch, 5)

	// index 1 is already slashed, index 2 is already withdrawable, index 3 is not
	// in the intersection — only index 0 must be slashed.
	s.Validators[1].Slashed = true
	s.Validators[2].ExitEpoch = 5
	s.Validators[2].WithdrawableEpoch = epoch

	slashing := &all.AttesterSlashing{
		Attestation1: &all.IndexedAttestation{AttestingIndices: []uint64{0, 1, 2, 3}},
		Attestation2: &all.IndexedAttestation{AttestingIndices: []uint64{0, 1, 2}},
	}
	processAttesterSlashing(s, slashing)

	if !s.Validators[0].Slashed {
		t.Error("slashable validator was not slashed")
	}
	for _, index := range []int{2, 3} {
		if s.Validators[index].Slashed {
			t.Errorf("validator %d was slashed but is not slashable", index)
		}
	}
	// Only the one slashing is accounted for in the slashings vector.
	if got := s.Slashings[uint64(epoch)%specs.EpochsPerSlashingVector]; got != 32*gwei {
		t.Errorf("slashings entry = %d, want %d", got, 32*gwei)
	}
}

// TestProcessDepositRequestStartIndex checks that the first deposit request sets
// deposit_requests_start_index and later ones leave it alone.
func TestProcessDepositRequestStartIndex(t *testing.T) {
	specs := testSpecs()
	s := newTestState(specs, 10, 3)

	processDepositRequest(s, &electra.DepositRequest{Index: 42, WithdrawalCredentials: make([]byte, 32)})
	if s.DepositRequestsStartIndex != 42 {
		t.Fatalf("start index = %d, want 42", s.DepositRequestsStartIndex)
	}

	processDepositRequest(s, &electra.DepositRequest{Index: 43, WithdrawalCredentials: make([]byte, 32)})
	if s.DepositRequestsStartIndex != 42 {
		t.Errorf("start index = %d, want it to stay 42", s.DepositRequestsStartIndex)
	}
	if len(s.PendingDeposits) != 2 {
		t.Errorf("pending deposits = %d, want 2", len(s.PendingDeposits))
	}
}

// TestChurnLimits checks the Gloas churn limits (EIP-8061/EIP-7521) against the
// Electra ones they replace, at a total active balance where they differ.
func TestChurnLimits(t *testing.T) {
	specs := testSpecs()
	s := newTestState(specs, 10, 8)

	// 16,777,216 ETH total active balance: 256 ETH per epoch with the Electra
	// quotient, 512 ETH with the Gloas one.
	for _, validator := range s.Validators {
		validator.EffectiveBalance = 2_097_152 * gwei
	}
	s.caches.invalidateBalanceCaches()

	tests := []struct {
		name          string
		version       spec.DataVersion
		exit          phase0.Gwei
		active        phase0.Gwei
		consolidation phase0.Gwei
	}{
		// Electra: one shared churn, capped at 256 ETH, consolidations get the leftover.
		{"electra", spec.DataVersionFulu, 256 * gwei, 256 * gwei, 0},
		// Gloas: uncapped exit churn, capped activation churn, dedicated consolidation churn.
		{"gloas", spec.DataVersionGloas, 512 * gwei, 256 * gwei, 256 * gwei},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s.Version = test.version

			if got := s.getExitChurnLimit(); got != test.exit {
				t.Errorf("exit churn limit = %d, want %d", got, test.exit)
			}
			if got := s.getActivationChurnLimit(); got != test.active {
				t.Errorf("activation churn limit = %d, want %d", got, test.active)
			}
			if got := s.getConsolidationChurnLimit(); got != test.consolidation {
				t.Errorf("consolidation churn limit = %d, want %d", got, test.consolidation)
			}
		})
	}
}
