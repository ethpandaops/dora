package system_contracts

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestTxRecipient checks the recipient lookup used by the contract-event
// callbacks. Contract-creation transactions have no recipient, so the lookup
// must fall back to the emitting contract instead of dereferencing a nil pointer.
func TestTxRecipient(t *testing.T) {
	contract := common.HexToAddress("0x00000000219ab540356cBB839Cbe05303d7705Fa")
	log := &types.Log{Address: contract}

	// Contract-creation transaction (To is nil): falls back to the emitter.
	createTx := types.NewTx(&types.DynamicFeeTx{Nonce: 0, To: nil, Value: big.NewInt(0)})
	if got := txRecipient(createTx, log); got != contract {
		t.Errorf("creation tx recipient = %x, want %x", got, contract)
	}

	// Normal call transaction: uses its recipient.
	to := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	callTx := types.NewTx(&types.DynamicFeeTx{To: &to})
	if got := txRecipient(callTx, log); got != to {
		t.Errorf("call tx recipient = %x, want %x", got, to)
	}
}
