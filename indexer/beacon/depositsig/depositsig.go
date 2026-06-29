// Package depositsig verifies beacon deposit BLS signatures (proof-of-possession).
//
// The verification is consensus-critical: it is the gate that decides whether a
// pending deposit for a new pubkey creates a validator (apply_pending_deposit only
// adds a validator when the signature is valid). Single and aggregated (batch)
// verification share the same signing-root construction, so they always agree, and
// both match the deposit-contract verification in indexer/execution/system_contracts.
package depositsig

import (
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	blsu "github.com/protolambda/bls12-381-util"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
	"github.com/protolambda/ztyp/tree"
)

// Domain returns the deposit signature domain
// compute_domain(DOMAIN_DEPOSIT, genesisForkVersion, Root{}).
//
// Deposit signatures are a proof-of-possession over a fork-agnostic domain (the
// genesis validators root is intentionally zero so deposits remain valid across
// forks), so the domain depends only on the genesis fork version.
func Domain(genesisForkVersion phase0.Version) zrnt_common.BLSDomain {
	return zrnt_common.ComputeDomain(
		zrnt_common.DOMAIN_DEPOSIT,
		zrnt_common.Version(genesisForkVersion),
		zrnt_common.Root{},
	)
}

// builderDepositDomainType is DOMAIN_BUILDER_DEPOSIT (Gloas/EIP-8282): a dedicated
// domain type (0x0E000000) that prevents builder-deposit signatures from being
// replayed against the regular deposit contract and vice versa.
var builderDepositDomainType = zrnt_common.BLSDomainType{0x0E, 0x00, 0x00, 0x00}

// BuilderDomain returns the builder-deposit signature domain
// compute_domain(DOMAIN_BUILDER_DEPOSIT, genesisForkVersion, Root{}).
//
// Like regular deposits, builder-deposit proofs-of-possession are signed over a
// fork-agnostic domain (zero genesis validators root), so the domain depends only
// on the genesis fork version — just with the dedicated builder domain type.
func BuilderDomain(genesisForkVersion phase0.Version) zrnt_common.BLSDomain {
	return zrnt_common.ComputeDomain(
		builderDepositDomainType,
		zrnt_common.Version(genesisForkVersion),
		zrnt_common.Root{},
	)
}

// signingRoot computes the signing root of DepositMessage{pubkey, wc, amount}.
func signingRoot(pubkey phase0.BLSPubKey, withdrawalCredentials []byte, amount phase0.Gwei, domain zrnt_common.BLSDomain) tree.Root {
	msg := &zrnt_common.DepositMessage{
		Pubkey:                zrnt_common.BLSPubkey(pubkey),
		WithdrawalCredentials: tree.Root(withdrawalCredentials),
		Amount:                zrnt_common.Gwei(amount),
	}
	return zrnt_common.ComputeSigningRoot(msg.HashTreeRoot(tree.GetHashFn()), domain)
}

// Valid reports whether signature is a valid proof-of-possession over
// DepositMessage{pubkey, withdrawalCredentials, amount} for the given domain.
//
// A malformed pubkey or signature (failing point decompression) is invalid, as is
// the G2 point-at-infinity signature used for synthetic compounding deposits.
func Valid(pubkey phase0.BLSPubKey, withdrawalCredentials []byte, amount phase0.Gwei, signature phase0.BLSSignature, domain zrnt_common.BLSDomain) bool {
	root := signingRoot(pubkey, withdrawalCredentials, amount, domain)

	pubkeyData := zrnt_common.BLSPubkey(pubkey)
	blsPubkey, err := pubkeyData.Pubkey()
	if err != nil {
		return false
	}
	sigData := zrnt_common.BLSSignature(signature)
	blsSig, err := sigData.Signature()
	if err != nil {
		return false
	}

	return blsu.Verify(blsPubkey, root[:], blsSig)
}

// Input is a single deposit to verify as part of a batch.
type Input struct {
	Pubkey                phase0.BLSPubKey
	WithdrawalCredentials []byte
	Amount                phase0.Gwei
	Signature             phase0.BLSSignature
}

// VerifyBatch verifies many deposit signatures at once and returns per-input
// validity in input order.
//
// It uses random-coefficient batch verification
// (https://ethresear.ch/t/fast-verification-of-multiple-bls-signatures/5407): in the
// common case where every signature is valid, the whole batch is confirmed with a
// single aggregate check regardless of size. Because that check is all-or-nothing,
// a failing batch is bisected to isolate the invalid inputs (O(log n) extra checks
// per invalid signature). Inputs with a malformed pubkey or signature are rejected
// up front without entering the batch.
func VerifyBatch(inputs []Input, domain zrnt_common.BLSDomain) []bool {
	results := make([]bool, len(inputs))

	items := make([]batchItem, 0, len(inputs))
	for i := range inputs {
		in := &inputs[i]

		pubkeyData := zrnt_common.BLSPubkey(in.Pubkey)
		blsPubkey, err := pubkeyData.Pubkey()
		if err != nil {
			continue // malformed pubkey -> invalid (results[i] stays false)
		}
		sigData := zrnt_common.BLSSignature(in.Signature)
		blsSig, err := sigData.Signature()
		if err != nil {
			continue // malformed signature -> invalid
		}

		root := signingRoot(in.Pubkey, in.WithdrawalCredentials, in.Amount, domain)
		items = append(items, batchItem{idx: i, pubkey: blsPubkey, message: root[:], signature: blsSig})
	}

	verifyBatchItems(items, results)
	return results
}

// batchItem is a deserialized deposit ready for aggregate verification.
type batchItem struct {
	idx       int
	pubkey    *blsu.Pubkey
	message   []byte
	signature *blsu.Signature
}

// verifyBatchItems aggregate-verifies items, marking results[idx]=true for each
// valid one. On batch failure it bisects until single items remain, which are then
// verified individually.
func verifyBatchItems(items []batchItem, results []bool) {
	if len(items) == 0 {
		return
	}

	pubkeys := make([]*blsu.Pubkey, len(items))
	messages := make([][]byte, len(items))
	signatures := make([]*blsu.Signature, len(items))
	for i, it := range items {
		pubkeys[i] = it.pubkey
		messages[i] = it.message
		signatures[i] = it.signature
	}

	if ok, err := blsu.SignatureSetVerify(pubkeys, messages, signatures); err == nil && ok {
		for _, it := range items {
			results[it.idx] = true
		}
		return
	}

	if len(items) == 1 {
		it := items[0]
		results[it.idx] = blsu.Verify(it.pubkey, it.message, it.signature)
		return
	}

	mid := len(items) / 2
	verifyBatchItems(items[:mid], results)
	verifyBatchItems(items[mid:], results)
}
