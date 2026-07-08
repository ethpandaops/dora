package beacon

import (
	"testing"

	"github.com/ethpandaops/dora/indexer/beacon/depositsig"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	blsu "github.com/protolambda/bls12-381-util"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
	"github.com/protolambda/ztyp/tree"
)

func testKeyPair(t *testing.T, seed byte) (*blsu.SecretKey, phase0.BLSPubKey) {
	t.Helper()
	var skBytes [32]byte
	skBytes[31] = seed
	sk := new(blsu.SecretKey)
	if err := sk.Deserialize(&skBytes); err != nil {
		t.Fatalf("deserialize secret key: %v", err)
	}
	pk, err := blsu.SkToPk(sk)
	if err != nil {
		t.Fatalf("derive public key: %v", err)
	}
	return sk, phase0.BLSPubKey(pk.Serialize())
}

func signedDeposit(sk *blsu.SecretKey, pubkey phase0.BLSPubKey, wc []byte, amount phase0.Gwei, domain zrnt_common.BLSDomain) *electra.PendingDeposit {
	msg := &zrnt_common.DepositMessage{
		Pubkey:                zrnt_common.BLSPubkey(pubkey),
		WithdrawalCredentials: tree.Root(wc),
		Amount:                zrnt_common.Gwei(amount),
	}
	root := zrnt_common.ComputeSigningRoot(msg.HashTreeRoot(tree.GetHashFn()), domain)
	return &electra.PendingDeposit{
		Pubkey:                pubkey,
		WithdrawalCredentials: wc,
		Amount:                amount,
		Signature:             phase0.BLSSignature(blsu.Sign(sk, root[:]).Serialize()),
		Slot:                  1,
	}
}

// TestProjectOrderingAndGating verifies the projection produces exactly the
// validators the chain would create, in queue order: top-ups are skipped, invalid
// signatures create nothing, and a new pubkey is created only at its first
// valid-signature occurrence.
// baseProjectionInput returns an input with no Gloas fork scheduled, so the
// builder-deposit filter is inert.
func baseProjectionInput() pendingProjectionInput {
	return pendingProjectionInput{
		genesisForkVersion:         phase0.Version{},
		currentEpoch:               100,
		maxPendingDepositsPerEpoch: 16,
		churnLimit:                 func(uint64) uint64 { return 256_000_000_000 },
	}
}

func TestProjectOrderingAndGating(t *testing.T) {
	domain := depositsig.Domain(phase0.Version{})

	wc := make([]byte, 32)
	wc[0] = 0x01
	const amount = phase0.Gwei(32_000_000_000)

	// One validator already on chain.
	skExisting, pkExisting := testKeyPair(t, 0x01)
	realValidators := []*phase0.Validator{{PublicKey: pkExisting}}

	skA, pkA := testKeyPair(t, 0x0A)
	_, pkB := testKeyPair(t, 0x0B)
	skC, pkC := testKeyPair(t, 0x0C)

	// Deposit whose signature is invalid (signed by the wrong key).
	skWrong, _ := testKeyPair(t, 0xFF)
	invalidB := signedDeposit(skWrong, pkB, wc, amount, domain)

	deposits := []*electra.PendingDeposit{
		signedDeposit(skExisting, pkExisting, wc, amount, domain), // top-up to existing -> no new validator
		signedDeposit(skA, pkA, wc, amount, domain),               // new A (valid) -> index 1
		invalidB, // new B (invalid) -> dropped
		signedDeposit(skA, pkA, wc, amount, domain), // A again -> top-up to projected A
		signedDeposit(skC, pkC, wc, amount, domain), // new C (valid) -> index 2
	}

	p := &pendingValidatorProjector{sigCache: make(map[[32]byte]bool)}
	validators, balances := p.project(realValidators, deposits, baseProjectionInput())

	wantPubkeys := []phase0.BLSPubKey{pkA, pkC}
	if len(validators) != len(wantPubkeys) {
		t.Fatalf("projected %d validators, want %d", len(validators), len(wantPubkeys))
	}
	for i, want := range wantPubkeys {
		if validators[i].PublicKey != want {
			t.Errorf("validator[%d] pubkey = %x, want %x", i, validators[i].PublicKey, want)
		}
		if validators[i].EffectiveBalance != 0 {
			t.Errorf("validator[%d] effective balance = %d, want 0 (projected marker)", i, validators[i].EffectiveBalance)
		}
		if validators[i].ActivationEligibilityEpoch != FarFutureEpoch {
			t.Errorf("validator[%d] eligibility = %d, want FarFuture (projected marker)", i, validators[i].ActivationEligibilityEpoch)
		}
		// Activation epoch holds the estimated processing epoch (>= currentEpoch+1),
		// never FarFuture — that combination with FarFuture eligibility marks it projected.
		if validators[i].ActivationEpoch != 101 {
			t.Errorf("validator[%d] activation epoch = %d, want 101 (estimated processing epoch)", i, validators[i].ActivationEpoch)
		}
		if balances[i] != 0 {
			t.Errorf("balance[%d] = %d, want 0", i, balances[i])
		}
	}

	// The cache must let a second projection return the identical result.
	validators2, _ := p.project(realValidators, deposits, baseProjectionInput())
	if len(validators2) != len(wantPubkeys) {
		t.Fatalf("second projection returned %d validators, want %d", len(validators2), len(wantPubkeys))
	}
}

// TestProjectGloasBuilderFilter verifies that, when a Gloas fork is scheduled, new
// 0xB0 (builder) deposits whose estimated processing epoch is at or after the fork
// are dropped (they are onboarded as builders at the fork), while those processed
// before the fork — and all non-builder deposits — still project.
func TestProjectGloasBuilderFilter(t *testing.T) {
	domain := depositsig.Domain(phase0.Version{})
	const amount = phase0.Gwei(32_000_000_000)

	builderWc := make([]byte, 32)
	builderWc[0] = 0xB0
	execWc := make([]byte, 32)
	execWc[0] = 0x01

	// Churn = one deposit per epoch; processing starts at currentEpoch+1 = 101.
	// So queue position k is processed at epoch 101+k.
	skA, pkA := testKeyPair(t, 0x0A) // builder, epoch 101 (< fork) -> kept (validator)
	skB, pkB := testKeyPair(t, 0x0B) // builder, epoch 102 (< fork) -> kept (validator)
	skC, pkC := testKeyPair(t, 0x0C) // builder, reaches fork epoch 103 -> onboarded as builder, dropped
	skD, pkD := testKeyPair(t, 0x0D) // builder, reaches fork epoch 103 -> onboarded as builder, dropped
	skE, pkE := testKeyPair(t, 0x0E) // exec, processes right after the fork (epoch 103) -> kept (validator)

	deposits := []*electra.PendingDeposit{
		signedDeposit(skA, pkA, builderWc, amount, domain),
		signedDeposit(skB, pkB, builderWc, amount, domain),
		signedDeposit(skC, pkC, builderWc, amount, domain),
		signedDeposit(skD, pkD, builderWc, amount, domain),
		signedDeposit(skE, pkE, execWc, amount, domain),
	}

	gloasEpoch := phase0.Epoch(103)
	in := pendingProjectionInput{
		genesisForkVersion:         phase0.Version{},
		currentEpoch:               100,
		maxPendingDepositsPerEpoch: 100, // count is not the limiting factor here
		gloasForkEpoch:             &gloasEpoch,
		churnLimit:                 func(uint64) uint64 { return uint64(amount) }, // one deposit per epoch
	}

	p := &pendingValidatorProjector{sigCache: make(map[[32]byte]bool)}
	validators, _ := p.project(nil, deposits, in)

	wantPubkeys := []phase0.BLSPubKey{pkA, pkB, pkE}
	// E processes right after the fork: the dropped builders C/D are removed at the
	// fork and consume no churn, so E is not pushed out to epoch 105.
	wantActivation := []phase0.Epoch{101, 102, 103}
	if len(validators) != len(wantPubkeys) {
		t.Fatalf("projected %d validators, want %d", len(validators), len(wantPubkeys))
	}
	for i, want := range wantPubkeys {
		if validators[i].PublicKey != want {
			t.Errorf("validator[%d] pubkey = %x, want %x", i, validators[i].PublicKey, want)
		}
		if validators[i].ActivationEpoch != wantActivation[i] {
			t.Errorf("validator[%d] activation epoch = %d, want %d", i, validators[i].ActivationEpoch, wantActivation[i])
		}
		if validators[i].ActivationEligibilityEpoch != FarFutureEpoch {
			t.Errorf("validator[%d] eligibility = %d, want FarFuture (projected marker)", i, validators[i].ActivationEligibilityEpoch)
		}
	}
}
