package statetransition

import (
	"crypto/sha256"

	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/go-eth2-client/spec/pq"
)

const (
	legacyXMSSPubkeyDomain = 0x00
	legacyVRFRootDomain    = 0x01
)

func hasHeze(version spec.DataVersion) bool {
	return version >= spec.DataVersionHeze && version != spec.DataVersionPQ
}

func isLegacyPubkey(pubkey phase0.BLSPubKey) bool {
	for _, b := range pubkey[:pq.DepositPrefixSize] {
		if b != 0 {
			return true
		}
	}

	return false
}

func legacyHash(pubkey phase0.BLSPubKey, domain byte) [32]byte {
	return sha256.Sum256(append(append([]byte(nil), pubkey[:]...), domain))
}

func pqDepositPubkey(pubkey phase0.BLSPubKey) phase0.BLSPubKey {
	if !isLegacyPubkey(pubkey) {
		return pubkey
	}

	return pq.XMSSPubKey(legacyHash(pubkey, legacyXMSSPubkeyDomain)).DepositPubKey()
}

func pqDepositVRFRoot(deposit *electra.PendingDeposit) phase0.Root {
	if isLegacyPubkey(deposit.Pubkey) {
		return legacyHash(deposit.Pubkey, legacyVRFRootDomain)
	}

	var root phase0.Root

	copy(root[:], deposit.Signature[:32])

	return root
}

func applyPQPendingDeposit(s *stateAccessor, deposit *electra.PendingDeposit, pubkeyIndex map[phase0.BLSPubKey]phase0.ValidatorIndex) {
	pubkey := pqDepositPubkey(deposit.Pubkey)

	if existingIdx, ok := pubkeyIndex[pubkey]; ok {
		s.increaseBalance(existingIdx, phase0.Gwei(deposit.Amount))
		return
	}

	addValidatorToRegistry(s, pubkey, deposit.WithdrawalCredentials, phase0.Gwei(deposit.Amount))

	index := phase0.ValidatorIndex(len(s.Validators) - 1)
	pubkeyIndex[pubkey] = index
	validator := s.Validators[index]

	if isLegacyPubkey(deposit.Pubkey) {
		epoch := s.currentEpoch()
		validator.ExitEpoch = epoch
		validator.WithdrawableEpoch = epoch + phase0.Epoch(s.specs.MinValidatorWithdrawbilityDelay)
	}

	xmssValidator := &pq.Validator{VRFRoot: pqDepositVRFRoot(deposit)}
	copy(xmssValidator.PublicKey[:], pubkey[pq.DepositPrefixSize:])
	s.XMSSValidators = append(s.XMSSValidators, xmssValidator)
}

func syncPQRegistries(state *all.BeaconState) {
	if state.Version != spec.DataVersionPQ {
		return
	}

	if len(state.XMSSValidators) == len(state.Validators) {
		for i, validator := range state.Validators {
			xmssValidator := state.XMSSValidators[i]
			xmssValidator.WithdrawalCredentials = validator.WithdrawalCredentials
			xmssValidator.EffectiveBalance = validator.EffectiveBalance
			xmssValidator.Slashed = validator.Slashed
			xmssValidator.ActivationEligibilityEpoch = validator.ActivationEligibilityEpoch
			xmssValidator.ActivationEpoch = validator.ActivationEpoch
			xmssValidator.ExitEpoch = validator.ExitEpoch
			xmssValidator.WithdrawableEpoch = validator.WithdrawableEpoch
		}
	}

	if len(state.XMSSBuilders) == len(state.Builders) {
		for i, builder := range state.Builders {
			xmssBuilder := state.XMSSBuilders[i]
			xmssBuilder.Version = builder.Version
			xmssBuilder.ExecutionAddress = builder.ExecutionAddress
			xmssBuilder.Balance = builder.Balance
			xmssBuilder.DepositEpoch = builder.DepositEpoch
			xmssBuilder.WithdrawableEpoch = builder.WithdrawableEpoch
		}
	}
}
