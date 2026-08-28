package consensus

import (
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

const gwei = uint64(1_000_000_000)

func newChurnTestChainState() *ChainState {
	gloasForkEpoch := uint64(100)

	specs := &ChainSpec{}
	specs.GloasForkEpoch = &gloasForkEpoch
	specs.ChurnLimitQuotient = 65536
	specs.EffectiveBalanceIncrement = 1 * gwei
	specs.MinPerEpochChurnLimitElectra = 128 * gwei
	specs.MaxPerEpochActivationExitChurnLimit = 256 * gwei
	specs.ChurnLimitQuotientGloas = 32768
	specs.MaxPerEpochActivationChurnLimitGloas = 256 * gwei

	return &ChainState{specs: specs}
}

func TestActivationAndExitChurnLimits(t *testing.T) {
	cs := newChurnTestChainState()

	const preGloas, postGloas = phase0.Epoch(99), phase0.Epoch(100)

	tests := []struct {
		name               string
		epoch              phase0.Epoch
		totalActiveBalance uint64
		wantActivation     uint64
		wantExit           uint64
	}{
		// Below the floor both formulas return MIN_PER_EPOCH_CHURN_LIMIT_ELECTRA.
		{"pre-gloas floor", preGloas, 1_000_000 * gwei, 128 * gwei, 128 * gwei},
		{"gloas floor", postGloas, 1_000_000 * gwei, 128 * gwei, 128 * gwei},

		// 8M ETH: Electra gives 8M/2^16 = 122 -> floor 128; Gloas gives 8M/2^15 = 244.
		{"pre-gloas mid", preGloas, 8_000_000 * gwei, 128 * gwei, 128 * gwei},
		{"gloas mid", postGloas, 8_000_000 * gwei, 244 * gwei, 244 * gwei},

		// 35M ETH: activation churn is capped at 256 ETH; the Gloas exit churn is not.
		{"pre-gloas cap", preGloas, 35_000_000 * gwei, 256 * gwei, 256 * gwei},
		{"gloas cap", postGloas, 35_000_000 * gwei, 256 * gwei, 1068 * gwei},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cs.GetActivationChurnLimit(tt.epoch, tt.totalActiveBalance); got != tt.wantActivation {
				t.Errorf("GetActivationChurnLimit() = %d, want %d", got/gwei, tt.wantActivation/gwei)
			}
			if got := cs.GetExitChurnLimit(tt.epoch, tt.totalActiveBalance); got != tt.wantExit {
				t.Errorf("GetExitChurnLimit() = %d, want %d", got/gwei, tt.wantExit/gwei)
			}
		})
	}
}

func TestChurnLimitsWithoutSpecs(t *testing.T) {
	cs := &ChainState{}

	if got := cs.GetActivationChurnLimit(0, 1); got != 0 {
		t.Errorf("GetActivationChurnLimit() without specs = %d, want 0", got)
	}
	if got := cs.GetExitChurnLimit(0, 1); got != 0 {
		t.Errorf("GetExitChurnLimit() without specs = %d, want 0", got)
	}
}
