package inclusionlists

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/indexer/beacon"
)

var testChainID = big.NewInt(1337)

// testTx builds a signed EIP-1559 transfer and returns its raw encoding.
func testTx(t *testing.T, key *ecdsa.PrivateKey, nonce uint64, gas uint64, feeCap int64, value int64) []byte {
	t.Helper()

	to := common.HexToAddress("0x00000000000000000000000000000000000000ff")
	tx, err := types.SignNewTx(key, types.LatestSignerForChainID(testChainID), &types.DynamicFeeTx{
		ChainID:   testChainID,
		Nonce:     nonce,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(feeCap),
		Gas:       gas,
		To:        &to,
		Value:     big.NewInt(value),
	})
	require.NoError(t, err)

	raw, err := tx.MarshalBinary()
	require.NoError(t, err)

	return raw
}

// testList builds an inclusion list observation.
func testList(validator uint64, signature byte, dependentRoot phase0.Root, firstSeen int32, txs ...[]byte) *beacon.InclusionListObservation {
	list := &v1.SignedInclusionList{
		Message: &v1.InclusionList{
			Slot:           100,
			ValidatorIndex: phase0.ValidatorIndex(validator),
			DependentRoot:  dependentRoot,
			Transactions:   make([]bellatrix.Transaction, 0, len(txs)),
		},
	}
	list.Signature[0] = signature
	for _, tx := range txs {
		list.Message.Transactions = append(list.Message.Transactions, tx)
	}

	return &beacon.InclusionListObservation{InclusionList: list, FirstSeen: firstSeen, HasSeen: true}
}

// statusOf returns the outcome of the given raw transaction.
func statusOf(t *testing.T, eval *evaluation, rawTx []byte) btypes.SlotInclusionListTx {
	t.Helper()

	for idx, tx := range eval.fragment.Transactions {
		if string(tx) == string(rawTx) {
			return eval.result.Txs[idx]
		}
	}

	t.Fatalf("transaction not in evaluation")

	return btypes.SlotInclusionListTx{}
}

func TestEvaluation(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	sender := crypto.PubkeyToAddress(key.PublicKey)

	otherKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	other := crypto.PubkeyToAddress(otherKey.PublicKey)

	const (
		dueMs   = 8000
		baseFee = 10
		gasLeft = 100_000
	)
	branch := phase0.Root{0xaa}

	included := testTx(t, key, 1, 21000, 100, 0)
	includedEarlier := testTx(t, key, 0, 21000, 100, 0)
	lateOnly := testTx(t, key, 20, 21000, 100, 0)
	lateAndTimely := testTx(t, otherKey, 7, 21000, 100, 0)
	equivocated := testTx(t, key, 21, 21000, 100, 0)
	wrongBranch := testTx(t, key, 22, 21000, 100, 0)
	tooMuchGas := testTx(t, key, 5, gasLeft+1, 100, 0)
	feeTooLow := testTx(t, key, 5, 21000, baseFee-1, 0)
	malformed := []byte{0x02, 0xff, 0x00}
	nonceTooLow := testTx(t, key, 4, 21000, 100, 0)
	nonceTooHigh := testTx(t, key, 6, 21000, 100, 0)
	tooExpensive := testTx(t, key, 5, 21000, 100, 5_000_000)
	unsatisfied := testTx(t, key, 5, 21000, 100, 0)

	input := &evaluationInput{
		lists: []*beacon.InclusionListObservation{
			testList(1, 1, branch, 2000, included, includedEarlier, tooMuchGas, feeTooLow, malformed,
				nonceTooLow, nonceTooHigh, tooExpensive, unsatisfied, lateAndTimely),
			testList(2, 1, branch, dueMs, lateOnly, lateAndTimely, included),
			testList(3, 1, branch, 1000, equivocated),
			testList(3, 2, branch, 1500, equivocated),
			testList(4, 1, phase0.Root{0xbb}, 1000, wrongBranch),
		},
		dueMs:            dueMs,
		dependentRoot:    branch,
		hasDependentRoot: true,
		blockRoot:        phase0.Root{0x01},
		payload: &all.ExecutionPayload{
			BlockNumber:   50,
			BlockHash:     phase0.Hash32{0x02},
			GasLimit:      1_000_000,
			GasUsed:       1_000_000 - gasLeft,
			BaseFeePerGas: uint256.NewInt(baseFee),
			Timestamp:     1_700_000_000,
			Transactions:  []bellatrix.Transaction{included},
			Withdrawals: []*capella.Withdrawal{
				{Address: bellatrix.ExecutionAddress(sender), Amount: 1},
			},
		},
		parentTransactions: map[string]bool{string(includedEarlier): true},
	}

	eval := newEvaluation(input)

	assert.Equal(t, uint64(baseFee), eval.result.BaseFee)
	assert.Equal(t, []uint8{
		btypes.ILListFlagEvaluated,
		btypes.ILListFlagEvaluated | btypes.ILListFlagLate,
		btypes.ILListFlagEvaluated | btypes.ILListFlagEquivocation,
		btypes.ILListFlagEvaluated | btypes.ILListFlagEquivocation,
		btypes.ILListFlagEvaluated | btypes.ILListFlagWrongBranch,
	}, eval.result.ListFlags)

	// Outcomes that need no sender state.
	stateless := []struct {
		name   string
		rawTx  []byte
		status uint8
	}{
		{"included, also carried by a late list", included, btypes.ILTxStatusIncluded},
		{"included in the parent payload", includedEarlier, btypes.ILTxStatusIncludedEarlier},
		{"only in a late list", lateOnly, btypes.ILTxStatusListLate},
		{"only in lists of an equivocating member", equivocated, btypes.ILTxStatusListEquivocation},
		{"only in a list for another shuffling", wrongBranch, btypes.ILTxStatusListWrongBranch},
		{"gas limit exceeds the gas left", tooMuchGas, btypes.ILTxStatusGasLimit},
		{"fee cap below the base fee", feeTooLow, btypes.ILTxStatusFeeCapTooLow},
		{"not decodable", malformed, btypes.ILTxStatusMalformed},
	}
	for _, tt := range stateless {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.status, statusOf(t, eval, tt.rawTx).Status)
		})
	}

	// The remaining transactions depend on the state of their two senders.
	assert.ElementsMatch(t, []common.Address{sender, other}, eval.senders())
	assert.Equal(t, btypes.ILTxStatusUnknown, statusOf(t, eval, unsatisfied).Status)

	// The sender's balance covers the plain transfers (21000 gas * 100 wei)
	// but not the one with a value. One gwei of it is a withdrawal of this
	// payload, which is credited after the transactions and not spendable.
	states := map[common.Address]*senderState{
		sender: {nonce: 5, balance: big.NewInt(2_100_000 + 1_000_000_000)},
	}
	assert.Equal(t, []common.Address{sender}, eval.codeCheckSenders(states))
	states[sender].codeKnown = true

	eval.applySenderStates(states)

	stateful := []struct {
		name   string
		rawTx  []byte
		status uint8
	}{
		{"nonce already consumed", nonceTooLow, btypes.ILTxStatusNonceTooLow},
		{"nonce gap", nonceTooHigh, btypes.ILTxStatusNonceTooHigh},
		{"balance does not cover the cost", tooExpensive, btypes.ILTxStatusInsufficientFunds},
		{"valid and fitting", unsatisfied, btypes.ILTxStatusUnsatisfied},
		{"sender state not available", lateAndTimely, btypes.ILTxStatusUnknown},
	}
	for _, tt := range stateful {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.status, statusOf(t, eval, tt.rawTx).Status)
		})
	}

	outcome := statusOf(t, eval, unsatisfied)
	assert.Equal(t, btypes.ILTxFlagHasState, outcome.Flags)
	assert.Equal(t, uint64(5), outcome.Nonce)
	assert.Equal(t, big.NewInt(2_100_000+1_000_000_000).Bytes(), outcome.Balance)

	unsatisfiedCount, complete := eval.result.CountUnsatisfied()
	assert.Equal(t, 1, unsatisfiedCount)
	assert.False(t, complete)

	// A withdrawal of this payload to the sender is not spendable, so a
	// balance that only covers the cost with it is insufficient.
	states[sender].balance = big.NewInt(2_100_000 + 1_000_000_000 - 1)
	eval.applySenderStates(states)
	assert.Equal(t, btypes.ILTxStatusInsufficientFunds, statusOf(t, eval, unsatisfied).Status)

	// A sender with non-delegated code cannot send transactions (EIP-3607).
	states[sender].balance = big.NewInt(2_100_000 + 1_000_000_000)
	states[sender].hasCode = true
	eval.applySenderStates(states)
	outcome = statusOf(t, eval, unsatisfied)
	assert.Equal(t, btypes.ILTxStatusSenderHasCode, outcome.Status)
	assert.Equal(t, btypes.ILTxFlagHasState|btypes.ILTxFlagSenderHasCode, outcome.Flags)

	// The fragment is self-contained and encodable with the lists' object.
	meta := &btypes.SlotMeta{Slot: 100, Bids: []*btypes.SlotMetaBid{}, InclusionLists: eval.fragment}
	_, err = btypes.EncodeSlotMeta(meta)
	require.NoError(t, err)
}

func TestPayloadStateProbe(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	sender := crypto.PubkeyToAddress(key.PublicKey)

	otherKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	first := testTx(t, otherKey, 3, 21000, 100, 0)
	last := testTx(t, key, 41, 21000, 100, 0)

	// The probe is the sender of the last decodable transaction, with the
	// nonce the account has after it.
	probe := payloadStateProbe([]bellatrix.Transaction{first, last, {0x02, 0xff}})
	require.NotNil(t, probe)
	assert.Equal(t, sender, probe.address)
	assert.Equal(t, uint64(42), probe.nonce)

	assert.Nil(t, payloadStateProbe(nil))
	assert.Nil(t, payloadStateProbe([]bellatrix.Transaction{{0x02, 0xff}}))
}
