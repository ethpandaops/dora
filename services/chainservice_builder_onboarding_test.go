package services

import (
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func testPubkey(seed byte) phase0.BLSPubKey {
	pubkey := phase0.BLSPubKey{}
	for i := range pubkey {
		pubkey[i] = seed
	}

	return pubkey
}

func validatorCredentials() []byte {
	credentials := make([]byte, 32)
	credentials[0] = 0x02

	return credentials
}

func builderCredentials() []byte {
	credentials := make([]byte, 32)
	credentials[0] = builderWithdrawalCredType

	return credentials
}

func queueEntry(pos uint64, pubkey phase0.BLSPubKey, credentials []byte) *IndexedDepositQueueEntry {
	return &IndexedDepositQueueEntry{
		QueuePos: pos,
		PendingDeposit: &electra.PendingDeposit{
			Pubkey:                pubkey,
			WithdrawalCredentials: credentials,
		},
	}
}

func builderDeposit(pubkey phase0.BLSPubKey, queued bool, pos uint64) *ProjectedBuilderDeposit {
	return &ProjectedBuilderDeposit{
		Deposit:  &dbtypes.DepositWithTx{Deposit: dbtypes.Deposit{PublicKey: pubkey[:]}},
		IsQueued: queued,
		QueuePos: pos,
	}
}

// TestValidatorDepositAppliedBeforeForkKeepsBuilderDeposit covers the case that made the projection
// diverge from the chain: a validator deposit at the front of the queue is applied well before the
// fork, so it registers the pubkey as a validator, and onboarding then keeps every builder deposit
// for it. Being processed before the fork is exactly what makes it invisible to a check that only
// looks at deposits still queued at the fork.
func TestValidatorDepositAppliedBeforeForkKeepsBuilderDeposit(t *testing.T) {
	pubkey := testPubkey(0x11)

	validatorDeposits := newValidatorDepositsInQueue()
	validatorDeposits.add(queueEntry(0, pubkey, validatorCredentials()), false, true)

	// order is irrelevant once the validator exists: both a later and an earlier builder deposit
	// are kept
	require.True(t, validatorDeposits.keeps(builderDeposit(pubkey, true, 17)))
	require.True(t, validatorDeposits.keeps(builderDeposit(pubkey, true, 0)))
	require.True(t, validatorDeposits.keeps(builderDeposit(pubkey, false, 0)))
}

// TestQueuedValidatorDepositKeepsOnlyLaterBuilderDeposits covers the other half: a validator
// deposit that is still queued at the fork is kept as onboarding walks the queue, so it only
// affects builder deposits behind it.
func TestQueuedValidatorDepositKeepsOnlyLaterBuilderDeposits(t *testing.T) {
	pubkey := testPubkey(0x22)

	validatorDeposits := newValidatorDepositsInQueue()
	validatorDeposits.add(queueEntry(10, pubkey, validatorCredentials()), true, false)

	require.True(t, validatorDeposits.keeps(builderDeposit(pubkey, true, 11)),
		"a builder deposit behind the kept validator deposit is kept too")
	require.False(t, validatorDeposits.keeps(builderDeposit(pubkey, true, 9)),
		"a builder deposit ahead of it is onboarded before onboarding ever sees it")
	require.True(t, validatorDeposits.keeps(builderDeposit(pubkey, false, 0)),
		"a deposit included after the queue snapshot sits behind everything in it")
}

func TestValidatorDepositsInQueueTracksTheEarliestKeptPosition(t *testing.T) {
	pubkey := testPubkey(0x33)

	validatorDeposits := newValidatorDepositsInQueue()
	validatorDeposits.add(queueEntry(20, pubkey, validatorCredentials()), true, false)
	validatorDeposits.add(queueEntry(5, pubkey, validatorCredentials()), true, false)

	require.True(t, validatorDeposits.keeps(builderDeposit(pubkey, true, 10)),
		"the earliest kept validator deposit decides, not the last one seen")
}

func TestBuilderDepositsAreNotValidatorDeposits(t *testing.T) {
	pubkey := testPubkey(0x44)

	validatorDeposits := newValidatorDepositsInQueue()
	validatorDeposits.add(queueEntry(0, pubkey, builderCredentials()), false, true)
	validatorDeposits.add(queueEntry(1, pubkey, builderCredentials()), true, false)

	require.False(t, validatorDeposits.keeps(builderDeposit(pubkey, true, 5)),
		"builder deposits are onboarded, so they never keep a later builder deposit as a validator")
}

func TestUnrelatedPubkeysAreUnaffected(t *testing.T) {
	validatorDeposits := newValidatorDepositsInQueue()
	validatorDeposits.add(queueEntry(0, testPubkey(0x55), validatorCredentials()), false, true)
	validatorDeposits.add(queueEntry(1, testPubkey(0x66), validatorCredentials()), true, false)

	require.False(t, validatorDeposits.keeps(builderDeposit(testPubkey(0x77), true, 9)))
}

// TestInvalidValidatorDepositDoesNotBlockOnboarding is the case that made the projection hide a
// real builder: a pubkey with thousands of invalid validator deposits drained before the fork.
// None of them registers anything, so onboarding still sees a free pubkey and registers the
// builder from its one valid builder deposit.
func TestInvalidValidatorDepositDoesNotBlockOnboarding(t *testing.T) {
	pubkey := testPubkey(0x88)

	validatorDeposits := newValidatorDepositsInQueue()
	for pos := uint64(0); pos < 5; pos++ {
		validatorDeposits.add(queueEntry(pos, pubkey, validatorCredentials()), false, false)
	}

	require.False(t, validatorDeposits.keeps(builderDeposit(pubkey, true, 10)),
		"deposits that register nothing must not keep the builder deposit out of the registry")
}

// TestQueuedValidatorDepositKeepsRegardlessOfSignature guards the asymmetry: onboarding keeps every
// non-builder deposit it walks past without looking at the signature, and only apply_pending_deposit
// judges it later. So a still-queued validator deposit keeps the builder deposit even when its own
// proof-of-possession is invalid.
func TestQueuedValidatorDepositKeepsRegardlessOfSignature(t *testing.T) {
	pubkey := testPubkey(0x99)

	validatorDeposits := newValidatorDepositsInQueue()
	validatorDeposits.add(queueEntry(3, pubkey, validatorCredentials()), true, false)

	require.True(t, validatorDeposits.keeps(builderDeposit(pubkey, true, 4)))
}

func TestDepositSignatureVerdicts(t *testing.T) {
	verdict := func(v uint8) *uint8 { return &v }

	require.True(t, depositSignatureIsValid(verdict(1)), "a verified signature registers")
	require.True(t, depositSignatureIsValid(verdict(2)), "a top-up implies an earlier valid signature")
	require.False(t, depositSignatureIsValid(verdict(0)), "an invalid proof-of-possession registers nothing")
	require.False(t, depositSignatureIsValid(nil),
		"an unindexed deposit transaction leaves the verdict unknown, which must not be read as valid")
}
