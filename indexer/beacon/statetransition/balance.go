package statetransition

import (
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// processEffectiveBalanceUpdates implements the Electra+ version of
// process_effective_balance_updates.
// Modified in Electra: https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-process_effective_balance_updates
func processEffectiveBalanceUpdates(s *stateAccessor) {
	hysteresisIncrement := s.specs.EffectiveBalanceIncrement / s.specs.HysteresisQuotient
	downwardThreshold := hysteresisIncrement * s.specs.HysteresisDownwardMultiplier
	upwardThreshold := hysteresisIncrement * s.specs.HysteresisUpwardMultiplier

	for i, v := range s.Validators {
		balance := uint64(s.Balances[i])
		maxEB := uint64(s.getMaxEffectiveBalance(v))
		eb := uint64(v.EffectiveBalance)

		if balance+downwardThreshold < eb || eb+upwardThreshold < balance {
			newEB := balance - balance%s.specs.EffectiveBalanceIncrement
			if newEB > maxEB {
				newEB = maxEB
			}
			v.EffectiveBalance = phase0.Gwei(newEB)
		}
	}
}
