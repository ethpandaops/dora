package txindexer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/holiman/uint256"
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
	from, err := tx.From(tx.ChainId())
	if err != nil {
		t.Fatalf("sender recovery failed: %v", err)
	}

	if got := from.Hex(); got != creationTxSender {
		t.Errorf("sender = %s, want %s", got, creationTxSender)
	}
}

// A client that renders a contract creation as a transaction to the zero address changes
// the transaction's RLP, and with it both its hash and the sender recovered from its
// signature. The decoder adopts the hash the client reported, so the mismatch is only
// visible once the decoded fields are re-encoded - which is what the check exists for.
func TestDecodeBlockTransactionRejectsZeroAddressCreation(t *testing.T) {
	_, err := decodeBlockTransaction(creationTx(`"0x0000000000000000000000000000000000000000"`))
	if !errors.Is(err, errTxHashMismatch) {
		t.Fatalf("expected errTxHashMismatch, got %v", err)
	}
}

// A transaction whose type has no decoder keeps the generic fields the node reported
// rather than being dropped. Dropping it would leave the block short of a transaction
// and shift every receipt index behind it.
func TestDecodeBlockTransactionKeepsUnsupportedType(t *testing.T) {
	const unknownHash = "0x1111111111111111111111111111111111111111111111111111111111111111"

	tx, err := decodeBlockTransaction(json.RawMessage([]byte(`{
		"type": "0x7f",
		"hash": "` + unknownHash + `",
		"nonce": "0x642",
		"gas": "0x7245c",
		"to": "0x30592ef78d262bc79f0fe46355e07a51d685e382",
		"value": "0x2a",
		"input": "0x"
	}`)))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if tx.Type() != 0x7f {
		t.Errorf("type = %d, want %d", tx.Type(), 0x7f)
	}

	if got := tx.Hash().Hex(); got != unknownHash {
		t.Errorf("hash = %s, want %s", got, unknownHash)
	}

	if got := tx.Value().Uint64(); got != 42 {
		t.Errorf("value = %d, want 42", got)
	}
}

// The hash is what the re-encoded transaction is checked against, so a response that
// omits it cannot be verified and is rejected rather than trusted.
func TestDecodeBlockTransactionRequiresReportedHash(t *testing.T) {
	_, err := decodeBlockTransaction(json.RawMessage([]byte(`{
		"type": "0x0", "nonce": "0x642", "gasPrice": "0x4a817c800", "gas": "0x7245c",
		"to": null, "value": "0x0", "input": "0x"
	}`)))
	if err == nil {
		t.Fatal("expected an error for a transaction object with no hash")
	}
}

// frameTarget is the target of the sample frame transaction's SENDER frame.
var frameTarget = common.HexToAddress("0x30592ef78d262bc79f0fe46355e07a51d685e382")

// sampleFrameTx builds a two-frame transaction of the shape spamoor's frametx scenario
// emits: an expiry check followed by the user's operation.
func sampleFrameTx() *txtypes.FrameTx {
	expiry := common.HexToAddress("0x0000000000000000000000000000000000008141")
	target := frameTarget

	return &txtypes.FrameTx{
		ChainID:   uint256.NewInt(0x301824),
		NonceKeys: []*uint256.Int{uint256.NewInt(0)},
		NonceSeq:  7,
		Sender:    common.HexToAddress("0x6df35438a4dfcdbd25c7a364ab77e3cfdce87fc5"),
		Frames: []*txtypes.Frame{
			{
				Mode:   txtypes.FrameModeVerify,
				Target: &expiry,
				Limits: txtypes.FrameLimits{Execution: 5000},
				Value:  uint256.NewInt(0),
				Data:   []byte{0, 0, 0, 0, 0x6a, 0x8f, 0x9c, 0xff},
			},
			{
				Mode:   txtypes.FrameModeSender,
				Target: &target,
				Limits: txtypes.FrameLimits{Execution: 30000},
				Value:  uint256.NewInt(1),
				Data:   []byte{0xde, 0xad, 0xbe, 0xef},
			},
		},
		Signatures: []*txtypes.FrameSignature{
			{Scheme: txtypes.SigSchemeSecp256k1, Signature: make([]byte, 65)},
		},
		Fees: txtypes.FrameFees{
			GasTipCap:  uint256.NewInt(0x77359400),
			GasFeeCap:  uint256.NewInt(0x4a817c800),
			BlobFeeCap: uint256.NewInt(0),
		},
	}
}

// Frame transactions arrive as raw wire bytes in the beacon block's execution payload,
// which is the path that decodes every transaction the indexer sees. go-ethereum cannot
// represent type 0x06 at all, so before the switch to txtypes such a payload entry failed
// to decode and the transaction went unindexed.
func TestDecodeTxAcceptsFrameTransaction(t *testing.T) {
	frameTx := sampleFrameTx()

	encoded, err := txtypes.NewTx(frameTx).MarshalBinary()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	tx, err := txtypes.DecodeTx(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if tx.Type() != txtypes.FrameTxType {
		t.Fatalf("type = %d, want %d", tx.Type(), txtypes.FrameTxType)
	}

	decoded, ok := tx.Inner().(*txtypes.FrameTx)
	if !ok {
		t.Fatalf("inner type = %T, want *txtypes.FrameTx", tx.Inner())
	}

	if len(decoded.Frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(decoded.Frames))
	}

	// The sender is an explicit field rather than something recovered from a signature.
	from, err := tx.From(tx.ChainId())
	if err != nil {
		t.Fatalf("sender resolution failed: %v", err)
	}

	if from != frameTx.Sender {
		t.Errorf("sender = %s, want %s", from.Hex(), frameTx.Sender.Hex())
	}

	// A frame transaction has no single recipient, and dora must not read one into it.
	if decoded.Frames[1].Target == nil || *decoded.Frames[1].Target != frameTarget {
		t.Errorf("second frame target did not survive the round trip")
	}
}

// The EL client is the fallback whenever the beacon block payload cannot be decoded, and
// it reports transactions as JSON. A frame transaction has to survive that round trip
// too: the hash check re-encodes whatever the decoder produced, so a JSON representation
// that loses any part of the transaction is rejected rather than indexed.
func TestDecodeBlockTransactionAcceptsFrameTransactionJSON(t *testing.T) {
	frameTx := sampleFrameTx()

	rawTx, err := txtypes.NewTx(frameTx).MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// A frame transaction addresses each frame separately and reports no recipient of
	// its own, so the object must carry no top-level "to".
	var fields map[string]any
	if err := json.Unmarshal(rawTx, &fields); err != nil {
		t.Fatalf("unmarshal into fields failed: %v", err)
	}

	if _, ok := fields["to"]; ok {
		t.Error(`frame transaction object must not carry a top-level "to"`)
	}

	if _, ok := fields["frames"]; !ok {
		t.Error("frame transaction object is missing its frames")
	}

	tx, err := decodeBlockTransaction(rawTx)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if tx.Hash() != txtypes.NewTx(frameTx).Hash() {
		t.Errorf("hash = %s, want %s", tx.Hash().Hex(), txtypes.NewTx(frameTx).Hash().Hex())
	}

	decoded, ok := tx.Inner().(*txtypes.FrameTx)
	if !ok {
		t.Fatalf("inner type = %T, want *txtypes.FrameTx", tx.Inner())
	}

	if len(decoded.Frames) != len(frameTx.Frames) {
		t.Errorf("frames = %d, want %d", len(decoded.Frames), len(frameTx.Frames))
	}
}

// Receipts must be matched to transactions by hash. Matching them by position lets one
// unmatchable transaction consume the receipts belonging to the transactions after it.
func TestReceiptLookupIsByHashNotPosition(t *testing.T) {
	mkTx := func(nonce uint64) *txtypes.Transaction {
		return txtypes.NewTx(&txtypes.LegacyTx{
			Nonce: nonce, Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(0),
		})
	}

	txs := []*txtypes.Transaction{mkTx(0), mkTx(1), mkTx(2)}
	unmatchable := mkTx(99)

	// The block's receipts, with no receipt for the unmatchable transaction.
	receipts := make([]*txtypes.Receipt, 0, len(txs))
	for i, tx := range txs {
		receipts = append(receipts, &txtypes.Receipt{TxHash: tx.Hash(), TransactionIndex: uint(i)})
	}

	receiptMap := make(map[common.Hash]*txtypes.Receipt, len(receipts))
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
