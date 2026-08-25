package services

import (
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/stretchr/testify/require"
)

func depositTx(pubkey byte, amount uint64, sig byte, dequeue uint64) *dbtypes.BuilderDepositTx {
	return &dbtypes.BuilderDepositTx{
		PublicKey:    []byte{pubkey},
		Amount:       amount,
		Signature:    []byte{sig},
		DequeueBlock: dequeue,
		TxHash:       []byte{pubkey, sig},
	}
}

func includedDeposit(pubkey byte, amount uint64, sig byte, slotIndex uint64, blockNumber uint64) *CombinedBuilderDeposit {
	return &CombinedBuilderDeposit{
		Request: &dbtypes.BuilderDeposit{
			PublicKey:   []byte{pubkey},
			Amount:      amount,
			Signature:   []byte{sig},
			SlotIndex:   slotIndex,
			BlockNumber: blockNumber,
		},
	}
}

// TestBuilderDepositContentKeyIdentifiesADeposit is the property the whole pairing rests on: the
// key must be derivable identically from the consensus request and the execution transaction, and
// must separate deposits that differ in any of the three fields.
func TestBuilderDepositContentKeyIdentifiesADeposit(t *testing.T) {
	tx := depositTx(0xaa, 50, 0x11, 48364)
	request := includedDeposit(0xaa, 50, 0x11, 7, 48364).Request

	require.Equal(t,
		builderDepositContentKey(tx.PublicKey, tx.Amount, tx.Signature),
		builderDepositContentKey(request.PublicKey, request.Amount, request.Signature),
		"the same deposit must key the same from either side")

	base := builderDepositContentKey([]byte{0xaa}, 50, []byte{0x11})
	require.NotEqual(t, base, builderDepositContentKey([]byte{0xab}, 50, []byte{0x11}), "pubkey must matter")
	require.NotEqual(t, base, builderDepositContentKey([]byte{0xaa}, 51, []byte{0x11}), "amount must matter")
	require.NotEqual(t, base, builderDepositContentKey([]byte{0xaa}, 50, []byte{0x12}), "signature must matter")
}

// TestPositionalPickIsRejectedInAMixedBlock is the bug this fixes. In the blocks right after
// activation a block holds both requests enqueued before the fork (dequeue-block sentinel) and
// normally dequeued ones. SlotIndex counts every request in the block while the dequeued
// transactions are only the post-fork subset, so the positional pick lands on the wrong
// transaction — and must be refused rather than shown.
func TestPositionalPickIsRejectedInAMixedBlock(t *testing.T) {
	// block holds 4 requests; only the last two came through the dequeue
	dequeued := []*dbtypes.BuilderDepositTx{
		depositTx(0xc0, 50, 0x33, 48364),
		depositTx(0xd0, 50, 0x44, 48364),
	}

	// the third request (SlotIndex 2) is the first dequeued one
	request := includedDeposit(0xc0, 50, 0x33, 2, 48364).Request

	// positional pick at SlotIndex 2 would need 3 candidates; with 2 it is out of range, and even
	// at SlotIndex 1 it would land on 0xd0 rather than this deposit's own 0xc0
	wrong := dequeued[1]
	require.NotEqual(t,
		builderDepositContentKey(wrong.PublicKey, wrong.Amount, wrong.Signature),
		builderDepositContentKey(request.PublicKey, request.Amount, request.Signature),
		"the positionally-picked transaction is not this deposit's")
}

// TestContentMatchPairsRepeatedTopUpsOneToOne guards the consumption behaviour: identical
// top-ups share a content key, so each must take a distinct transaction rather than all
// pointing at the first one.
func TestContentMatchPairsRepeatedTopUpsOneToOne(t *testing.T) {
	candidates := map[string][]*dbtypes.BuilderDepositTx{}
	key := builderDepositContentKey([]byte{0xaa}, 50, []byte{0x11})
	candidates[key] = []*dbtypes.BuilderDepositTx{
		{PublicKey: []byte{0xaa}, Amount: 50, Signature: []byte{0x11}, TxHash: []byte{0x01}},
		{PublicKey: []byte{0xaa}, Amount: 50, Signature: []byte{0x11}, TxHash: []byte{0x02}},
	}

	results := []*CombinedBuilderDeposit{
		includedDeposit(0xaa, 50, 0x11, 0, 48364),
		includedDeposit(0xaa, 50, 0x11, 1, 48364),
	}

	// mirrors the consumption in matchBuilderDepositTxsByContent
	for _, result := range results {
		k := builderDepositContentKey(result.Request.PublicKey, result.Request.Amount, result.Request.Signature)
		matches := candidates[k]
		require.NotEmpty(t, matches)
		result.Transaction = matches[0]
		candidates[k] = matches[1:]
	}

	require.Equal(t, []byte{0x01}, results[0].Transaction.TxHash)
	require.Equal(t, []byte{0x02}, results[1].Transaction.TxHash,
		"the second identical top-up must take the second transaction, not repeat the first")
}
