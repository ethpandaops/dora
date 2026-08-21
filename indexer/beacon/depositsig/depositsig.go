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
// (https://ethresear.ch/t/fast-verification-of-multiple-bls-signatures/5407): an
// aggregate check confirms a whole group at roughly half the cost of verifying its
// signatures one by one. The check is all-or-nothing, so a group that fails is
// re-verified individually. Inputs with a malformed pubkey or signature are
// rejected up front without entering a group.
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

// batchGroupSize bounds how many signatures share one aggregate check. The group is
// the unit of wasted work when a check fails, so it trades the best case (fewer, larger
// aggregate checks) against the cost of an invalid signature (re-verifying its group).
const batchGroupSize = 128

// maxAggregateFailures is how many groups may fail before aggregate checking is given
// up for the rest of the batch. Invalid deposit signatures arrive in runs — a spammer
// repeating the same bad proof-of-possession — and once they are this dense the
// aggregate check is pure overhead on top of verifying individually.
const maxAggregateFailures = 3

// verifyBatchItems verifies items in groups, marking results[idx]=true for each valid
// one.
//
// A failing group is verified item by item rather than bisected. Bisecting sounds
// cheaper — O(log n) checks to isolate one bad signature — but every level re-runs an
// aggregate check over half the remaining items, so the constant is large: measured
// against verifying one by one, bisection is ~2.6x slower at 1% invalid and ~7x slower
// when most signatures are bad. Verifying a failed group directly bounds the worst case
// at roughly 1.5x the one-by-one cost while keeping the ~0.5x best case.
func verifyBatchItems(items []batchItem, results []bool) {
	useAggregate := true
	failures := 0

	for start := 0; start < len(items); start += batchGroupSize {
		end := start + batchGroupSize
		if end > len(items) {
			end = len(items)
		}

		group := items[start:end]

		if useAggregate && aggregateVerify(group) {
			for _, it := range group {
				results[it.idx] = true
			}

			continue
		}

		if useAggregate {
			failures++
			if failures >= maxAggregateFailures {
				useAggregate = false
			}
		}

		for _, it := range group {
			results[it.idx] = blsu.Verify(it.pubkey, it.message, it.signature)
		}
	}
}

// aggregateVerify reports whether every signature in the group is valid.
func aggregateVerify(group []batchItem) bool {
	if len(group) == 0 {
		return true
	}

	pubkeys := make([]*blsu.Pubkey, len(group))
	messages := make([][]byte, len(group))
	signatures := make([]*blsu.Signature, len(group))

	for i, it := range group {
		pubkeys[i] = it.pubkey
		messages[i] = it.message
		signatures[i] = it.signature
	}

	ok, err := blsu.SignatureSetVerify(pubkeys, messages, signatures)

	return err == nil && ok
}
