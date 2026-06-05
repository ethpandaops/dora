package depositsig

import (
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	blsu "github.com/protolambda/bls12-381-util"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
	"github.com/protolambda/ztyp/tree"
)

// keyPair deterministically derives a BLS key pair from a single seed byte.
func keyPair(t *testing.T, seed byte) (*blsu.SecretKey, phase0.BLSPubKey) {
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

// signDeposit produces a valid deposit signature over the given message.
func signDeposit(sk *blsu.SecretKey, pubkey phase0.BLSPubKey, wc []byte, amount phase0.Gwei, domain zrnt_common.BLSDomain) phase0.BLSSignature {
	msg := &zrnt_common.DepositMessage{
		Pubkey:                zrnt_common.BLSPubkey(pubkey),
		WithdrawalCredentials: tree.Root(wc),
		Amount:                zrnt_common.Gwei(amount),
	}
	root := zrnt_common.ComputeSigningRoot(msg.HashTreeRoot(tree.GetHashFn()), domain)
	return phase0.BLSSignature(blsu.Sign(sk, root[:]).Serialize())
}

func infinitySignature() phase0.BLSSignature {
	var sig phase0.BLSSignature
	sig[0] = 0xc0
	return sig
}

func TestValid(t *testing.T) {
	domain := Domain(phase0.Version{})

	sk, pubkey := keyPair(t, 0x42)
	wc := make([]byte, 32)
	wc[0] = 0x01
	amount := phase0.Gwei(32_000_000_000)
	validSig := signDeposit(sk, pubkey, wc, amount, domain)

	sk2, _ := keyPair(t, 0x43)
	otherSig := signDeposit(sk2, pubkey, wc, amount, domain)

	tamperedWc := make([]byte, 32)
	copy(tamperedWc, wc)
	tamperedWc[0] = 0x02

	tests := []struct {
		name   string
		wc     []byte
		amount phase0.Gwei
		sig    phase0.BLSSignature
		want   bool
	}{
		{"valid signature", wc, amount, validSig, true},
		{"tampered amount", wc, amount + 1, validSig, false},
		{"tampered withdrawal credentials", tamperedWc, amount, validSig, false},
		{"signature from a different key", wc, amount, otherSig, false},
		{"zero signature", wc, amount, phase0.BLSSignature{}, false},
		{"point-at-infinity signature", wc, amount, infinitySignature(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Valid(pubkey, tt.wc, tt.amount, tt.sig, domain); got != tt.want {
				t.Errorf("Valid = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestVerifyBatch checks that aggregated verification returns per-input validity
// in order, including when valid and invalid inputs are interleaved (forcing the
// bisection fallback) and when inputs are malformed.
func TestVerifyBatch(t *testing.T) {
	domain := Domain(phase0.Version{})
	wc := make([]byte, 32)
	wc[0] = 0x01
	amount := phase0.Gwei(32_000_000_000)

	// Six valid deposits from distinct keys.
	const n = 6
	inputs := make([]Input, 0, n+3)
	want := make([]bool, 0, n+3)
	for i := range n {
		sk, pubkey := keyPair(t, byte(0x10+i))
		inputs = append(inputs, Input{Pubkey: pubkey, WithdrawalCredentials: wc, Amount: amount, Signature: signDeposit(sk, pubkey, wc, amount, domain)})
		want = append(want, true)
	}

	// One valid signature re-pointed at the wrong pubkey (well-formed but invalid).
	skBad, _ := keyPair(t, 0x70)
	_, otherPubkey := keyPair(t, 0x71)
	inputs = append(inputs, Input{Pubkey: otherPubkey, WithdrawalCredentials: wc, Amount: amount, Signature: signDeposit(skBad, otherPubkey, wc, amount+1, domain)})
	want = append(want, false)

	// One zero (malformed) signature and one all-valid trailing deposit.
	_, zeroPubkey := keyPair(t, 0x72)
	inputs = append(inputs, Input{Pubkey: zeroPubkey, WithdrawalCredentials: wc, Amount: amount, Signature: phase0.BLSSignature{}})
	want = append(want, false)

	skTail, tailPubkey := keyPair(t, 0x73)
	inputs = append(inputs, Input{Pubkey: tailPubkey, WithdrawalCredentials: wc, Amount: amount, Signature: signDeposit(skTail, tailPubkey, wc, amount, domain)})
	want = append(want, true)

	got := VerifyBatch(inputs, domain)
	if len(got) != len(want) {
		t.Fatalf("VerifyBatch returned %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("result[%d] = %v, want %v", i, got[i], want[i])
		}
		// Aggregated result must agree with single verification.
		single := Valid(inputs[i].Pubkey, inputs[i].WithdrawalCredentials, inputs[i].Amount, inputs[i].Signature, domain)
		if single != got[i] {
			t.Errorf("result[%d]: batch=%v single=%v disagree", i, got[i], single)
		}
	}

	// Empty input is valid (no-op).
	if got := VerifyBatch(nil, domain); len(got) != 0 {
		t.Errorf("VerifyBatch(nil) = %v, want empty", got)
	}
}
