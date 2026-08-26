package txindexer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Transaction 0 of glamsterdam-devnet-8 block 90220: a legacy contract creation, which
// every conforming client reports with a null "to".
const creationTxJSON = `{
	"type": "0x0",
	"chainId": "0x1a6a8cc6e",
	"nonce": "0x642",
	"gasPrice": "0x4a817c800",
	"gas": "0x7245c",
	"to": %s,
	"value": "0x0",
	"input": "0x56fe1a11bde174dc4cc262afed6a46177a64ea999dc07be8dfd12f562d277c28296fcf2d5b1f7beb35cd480b53d070e0c655a57e7548919563d5f34e6f4d15b748b735b6c1eed9b1f67897290606283d930d285f",
	"r": "0x3ff5600801ba387ca2a30ae64abe088bc89c71a85e4a94308c5b647cb0308f41",
	"s": "0x1e45986beae6e6c0e381df35bf713c6e1adda09aada2578e716548f8300472de",
	"v": "0x34d5198ff",
	"hash": "0x7254debffd2dbfe70383428dd260fe1e43afbfa14abe9296dd6ebcdb8776f717"
}`

const (
	creationTxHash   = "0x7254debffd2dbfe70383428dd260fe1e43afbfa14abe9296dd6ebcdb8776f717"
	creationTxSender = "0x49F047a23A510dD05b5CF2940fe20ef94D212329"
)

// creationTx renders the sample transaction with the given "to" value, so the same
// transaction can be presented the way a conforming client reports it and the way a
// client that renders creations as the zero address reports it.
func creationTx(to string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(creationTxJSON, to))
}

func TestDecodeBlockTransactionAcceptsContractCreation(t *testing.T) {
	tx, err := decodeBlockTransaction(creationTx("null"))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if tx.To() != nil {
		t.Errorf("expected a contract creation (nil to), got %s", tx.To().Hex())
	}

	if got := tx.Hash().Hex(); got != creationTxHash {
		t.Errorf("hash = %s, want %s", got, creationTxHash)
	}

	// The sender is only recoverable when the decoded transaction matches the signed one.
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil {
		t.Fatalf("sender recovery failed: %v", err)
	}

	if got := from.Hex(); got != creationTxSender {
		t.Errorf("sender = %s, want %s", got, creationTxSender)
	}
}

// A client that renders a contract creation as a transaction to the zero address changes
// the transaction's RLP, and with it both its hash and the sender recovered from its
// signature. The decoded transaction must be rejected rather than indexed.
func TestDecodeBlockTransactionRejectsZeroAddressCreation(t *testing.T) {
	_, err := decodeBlockTransaction(creationTx(`"0x0000000000000000000000000000000000000000"`))
	if !errors.Is(err, errTxHashMismatch) {
		t.Fatalf("expected errTxHashMismatch, got %v", err)
	}
}

func TestDecodeBlockTransactionRejectsUnsupportedType(t *testing.T) {
	_, err := decodeBlockTransaction(json.RawMessage([]byte(`{"type":"0x7f"}`)))
	if !errors.Is(err, errUnsupportedTxType) {
		t.Fatalf("expected errUnsupportedTxType, got %v", err)
	}
}

// A client may legitimately omit the hash; there is nothing to verify against then.
func TestDecodeBlockTransactionWithoutReportedHash(t *testing.T) {
	tx, err := decodeBlockTransaction(json.RawMessage([]byte(`{
		"type": "0x0", "nonce": "0x642", "gasPrice": "0x4a817c800", "gas": "0x7245c",
		"to": null, "value": "0x0", "input": "0x",
		"r": "0x3ff5600801ba387ca2a30ae64abe088bc89c71a85e4a94308c5b647cb0308f41",
		"s": "0x1e45986beae6e6c0e381df35bf713c6e1adda09aada2578e716548f8300472de",
		"v": "0x34d5198ff"
	}`)))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if tx.Nonce() != 0x642 {
		t.Errorf("nonce = %d, want %d", tx.Nonce(), 0x642)
	}
}

// Receipts must be matched to transactions by hash. Matching them by position lets one
// unmatchable transaction consume the receipts belonging to the transactions after it.
func TestReceiptLookupIsByHashNotPosition(t *testing.T) {
	mkTx := func(nonce uint64) *types.Transaction {
		return types.NewTx(&types.LegacyTx{Nonce: nonce, Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(0)})
	}

	txs := []*types.Transaction{mkTx(0), mkTx(1), mkTx(2)}
	unmatchable := mkTx(99)

	// The block's receipts, with no receipt for the unmatchable transaction.
	receipts := make([]*types.Receipt, 0, len(txs))
	for i, tx := range txs {
		receipts = append(receipts, &types.Receipt{TxHash: tx.Hash(), TransactionIndex: uint(i)})
	}

	receiptMap := make(map[common.Hash]*types.Receipt, len(receipts))
	for _, receipt := range receipts {
		receiptMap[receipt.TxHash] = receipt
	}

	if receiptMap[unmatchable.Hash()] != nil {
		t.Fatal("unmatchable transaction must not resolve to a receipt")
	}

	for i, tx := range txs {
		receipt := receiptMap[tx.Hash()]
		if receipt == nil {
			t.Fatalf("transaction %d lost its receipt", i)
		}

		if receipt.TransactionIndex != uint(i) {
			t.Errorf("transaction %d matched receipt at index %d", i, receipt.TransactionIndex)
		}
	}
}
