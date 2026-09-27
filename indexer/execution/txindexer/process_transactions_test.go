package txindexer

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/sirupsen/logrus"
)

// gethStyleFrameTxJSON is a frame transaction as a client that does not render the type
// reports it: the generic transaction fields, the type byte, and none of the frame
// content. EIP-8141 specifies no JSON-RPC encoding, so this is a well-formed answer.
const gethStyleFrameTxJSON = `{
	"type": "0x6",
	"hash": "0xb837926d70d96cbb37ddc4cdd66ccfbfdaa332cb67a464bd83aa9b4e1dfbe5a7",
	"from": "0x846c1aa48ca796975ebffde156a076c520b356ea",
	"to": null,
	"nonce": "0x12ab",
	"gas": "0xca4e",
	"gasPrice": "0x4a817c800",
	"value": "0x0",
	"input": "0x"
}`

// newTxTestContext builds a processing context that performs no I/O: accounts are only
// recorded for batch resolution later, and no transaction here carries logs.
func newTxTestContext() *txProcessingContext {
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	return &txProcessingContext{
		ctx:              context.Background(),
		indexer:          &TxIndexer{logger: logger, mode: ModeFull},
		block:            &BlockRef{BlockUID: 1},
		blockData:        &blockData{Stats: &blockStats{}},
		accounts:         make(map[common.Address]*pendingAccount, 8),
		tokens:           make(map[common.Address]*pendingToken, 4),
		senderNonces:     make(map[common.Address]uint64, 4),
		balanceDeltas:    make(map[uint64]*balanceDelta, 8),
		pendingTransfers: make([]*pendingBalanceTransfer, 0, 4),
		systemDeposits:   make([]*pendingSystemDeposit, 0, 4),
	}
}

// frameTxReceipt builds a minimal receipt for a frame transaction that succeeded.
func frameTxReceipt(txHash common.Hash) *txtypes.Receipt {
	return &txtypes.Receipt{
		Type:              txtypes.FrameTxType,
		TxHash:            txHash,
		Status:            1,
		GasUsed:           19910,
		CumulativeGasUsed: 19910,
		BlockNumber:       big.NewInt(11654),
		EffectiveGasPrice: big.NewInt(0x77359407),
	}
}

// A frame transaction addresses each of its frames separately and has no recipient of its
// own, so the recipient it does not report is not a contract creation. Reading it as one
// derives a CREATE address for a contract that was never deployed, files the transaction
// against it, and marks the address a contract on every page that lists it.
func TestProcessTransactionNeverCreatesAContractForAFrameTx(t *testing.T) {
	tx, _, err := decodeBlockTransaction(json.RawMessage(gethStyleFrameTxJSON))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if _, unknown := tx.Inner().(*txtypes.UnknownTx); !unknown {
		t.Fatalf("inner type = %T, want *txtypes.UnknownTx for this fixture", tx.Inner())
	}

	ctx := newTxTestContext()
	if _, err := ctx.processTransaction(tx, frameTxReceipt(tx.Hash()), nil, nil); err != nil {
		t.Fatalf("processTransaction failed: %v", err)
	}

	if len(ctx.txResults) != 1 {
		t.Fatalf("results = %d, want 1", len(ctx.txResults))
	}

	result := ctx.txResults[0]

	if result.transaction.TxType&dbtypes.ElTxFlagCreate != 0 {
		t.Errorf("tx type = %d, a frame transaction must not be flagged as a creation", result.transaction.TxType)
	}

	if result.toAccount != nil {
		t.Errorf("toAccount = %v, a frame transaction has no recipient of its own", result.toAccount.account.Address)
	}

	sender := common.HexToAddress("0x846c1aa48ca796975ebffde156a076c520b356ea")
	for address := range ctx.accounts {
		if address != sender {
			t.Errorf("account %s was registered, only the sender took part in this transaction", address.Hex())
		}
	}
}

// A frame transaction sequences itself by nonce key, and only the zero key aliases the
// sender's account nonce. One that arrived without its frames does not say which key it
// used, so its sequence must not be recorded as an account nonce.
func TestProcessTransactionSkipsTheNonceOfAFrameTxWithoutFrames(t *testing.T) {
	tx, _, err := decodeBlockTransaction(json.RawMessage(gethStyleFrameTxJSON))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	ctx := newTxTestContext()
	if _, err := ctx.processTransaction(tx, frameTxReceipt(tx.Hash()), nil, nil); err != nil {
		t.Fatalf("processTransaction failed: %v", err)
	}

	if len(ctx.senderNonces) != 0 {
		t.Errorf("senderNonces = %v, want none recorded", ctx.senderNonces)
	}
}

// The frames of a transaction that arrived with them are still resolved, and an ordinary
// transaction with no recipient is still the contract creation it has always been.
func TestProcessTransactionStillReadsFramesAndCreations(t *testing.T) {
	frameTx := txtypes.NewTx(sampleFrameTx())

	ctx := newTxTestContext()
	if _, err := ctx.processTransaction(frameTx, frameTxReceipt(frameTx.Hash()), nil, nil); err != nil {
		t.Fatalf("processTransaction failed: %v", err)
	}

	if got := len(ctx.txResults[0].frames); got != len(sampleFrameTx().Frames) {
		t.Errorf("frames = %d, want %d", got, len(sampleFrameTx().Frames))
	}

	creation, _, err := decodeBlockTransaction(creationTx("null"))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	ctx = newTxTestContext()

	receipt := frameTxReceipt(creation.Hash())
	receipt.Type = creation.Type()

	if _, err := ctx.processTransaction(creation, receipt, nil, nil); err != nil {
		t.Fatalf("processTransaction failed: %v", err)
	}

	result := ctx.txResults[0]

	if result.transaction.TxType&dbtypes.ElTxFlagCreate == 0 {
		t.Errorf("tx type = %d, a transaction with no recipient is a creation", result.transaction.TxType)
	}

	if result.toAccount == nil {
		t.Fatal("a contract creation must register the address it deploys to")
	}

	if !result.toAccount.isContract {
		t.Error("the deployed address must be registered as a contract")
	}
}
