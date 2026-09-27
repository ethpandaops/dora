package txindexer

import (
	"bytes"
	"context"
	"encoding/binary"
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

// word encodes v as a 32-byte big-endian ABI word.
func word(v uint64) []byte {
	b := make([]byte, 32)
	binary.BigEndian.PutUint64(b[24:], v)

	return b
}

// hugeWord encodes a 32-byte ABI word whose value does not fit in a uint64.
// Decoders must reject such words rather than act on their truncated low bits.
func hugeWord(low uint64) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = 0xff
	}

	binary.BigEndian.PutUint64(b[24:], low)

	return b
}

func newTestCtx() *txProcessingContext {
	return &txProcessingContext{
		accounts: map[common.Address]*pendingAccount{},
		tokens:   map[common.Address]*pendingToken{},
		block:    &BlockRef{BlockUID: 1},
	}
}

func batchLog(data []byte) *txtypes.Log {
	return &txtypes.Log{
		Address: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Topics: []common.Hash{
			topicTransferBatch,
			common.HexToHash("0x02"), // operator
			common.HexToHash("0x03"), // from
			common.HexToHash("0x04"), // to
		},
		Data: data,
	}
}

// encodeBatch builds the standard solidity ABI encoding of
// TransferBatch(..., uint256[] ids, uint256[] values) for the given pairs.
func encodeBatch(ids, values []uint64) []byte {
	idsOffset := uint64(64)
	valuesOffset := idsOffset + 32 + uint64(len(ids))*32

	data := append([]byte{}, word(idsOffset)...)
	data = append(data, word(valuesOffset)...)

	data = append(data, word(uint64(len(ids)))...)
	for _, id := range ids {
		data = append(data, word(id)...)
	}

	data = append(data, word(uint64(len(values)))...)
	for _, v := range values {
		data = append(data, word(v)...)
	}

	return data
}

// The ids/values array offsets and lengths come straight out of an event log,
// which any contract can populate with arbitrary bytes. The bounds checks must
// hold for every possible word value: this parser runs on the block-processing
// worker goroutine, where a panic takes the whole process down and the block is
// then re-processed on every restart.
func TestTransferBatchRejectsMalformed(t *testing.T) {
	// arrays that claim to start past the end of the data
	offsetOverflow := append(append([]byte{}, hugeWord(^uint64(0)-31)...), word(64)...)
	offsetOverflow = append(offsetOverflow, make([]byte, 128)...)

	// array offsets that do not fit a uint64 at all
	offsetNotUint64 := append(append([]byte{}, hugeWord(64)...), word(64)...)
	offsetNotUint64 = append(offsetNotUint64, make([]byte, 128)...)

	// length * 32 wraps to zero, so a naive "required end" check passes
	lengthMulOverflow := append(append([]byte{}, word(64)...), word(128)...)
	lengthMulOverflow = append(lengthMulOverflow, word(1<<59)...)
	lengthMulOverflow = append(lengthMulOverflow, make([]byte, 32)...)
	lengthMulOverflow = append(lengthMulOverflow, word(1<<59)...)
	lengthMulOverflow = append(lengthMulOverflow, make([]byte, 64)...)

	// length that does not fit a uint64
	lengthNotUint64 := append(append([]byte{}, word(64)...), word(128)...)
	lengthNotUint64 = append(lengthNotUint64, hugeWord(2)...)
	lengthNotUint64 = append(lengthNotUint64, make([]byte, 32)...)
	lengthNotUint64 = append(lengthNotUint64, hugeWord(2)...)
	lengthNotUint64 = append(lengthNotUint64, make([]byte, 64)...)

	// honestly truncated: declares 4 elements but only carries 1
	truncated := append(append([]byte{}, word(64)...), word(160)...)
	truncated = append(truncated, word(4)...)
	truncated = append(truncated, word(1)...)
	truncated = append(truncated, word(4)...)
	truncated = append(truncated, word(1)...)

	// array head pointing exactly at the last word, leaving no room for elements
	headAtEnd := append(append([]byte{}, word(96)...), word(96)...)
	headAtEnd = append(headAtEnd, make([]byte, 32)...)
	headAtEnd = append(headAtEnd, word(1)...)

	cases := map[string][]byte{
		"offset overflows":          offsetOverflow,
		"offset above uint64":       offsetNotUint64,
		"length times 32 overflows": lengthMulOverflow,
		"length above uint64":       lengthNotUint64,
		"length beyond payload":     truncated,
		"head at payload end":       headAtEnd,
		"all zero words":            make([]byte, 128),
		"all 0xff words":            bytes.Repeat([]byte{0xff}, 128),
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on malformed log data: %v", r)
				}
			}()

			if got := newTestCtx().parseERC1155TransferBatch(1, 0, batchLog(data), nil); len(got) != 0 {
				t.Fatalf("expected no transfers for malformed data, got %d", len(got))
			}
		})
	}
}

// The hardened bounds checks must not reject or alter correctly encoded batches.
func TestTransferBatchParsesValid(t *testing.T) {
	ids := []uint64{7, 8, 9}
	values := []uint64{100, 0, 300}

	ctx := newTestCtx()

	transfers := ctx.parseERC1155TransferBatch(1, 0, batchLog(encodeBatch(ids, values)), nil)
	if len(transfers) != len(ids) {
		t.Fatalf("expected %d transfers, got %d", len(ids), len(transfers))
	}

	for i, transfer := range transfers {
		if got, want := transfer.transfer.TxIdx, uint32(1)<<16|uint32(i); got != want { //nolint:gosec // i < len(ids)
			t.Errorf("transfer %d: TxIdx = %d, want %d", i, got, want)
		}

		if got, want := transfer.transfer.TokenIndex, word(ids[i]); !bytes.Equal(got, want) {
			t.Errorf("transfer %d: TokenIndex = %x, want %x", i, got, want)
		}

		if got, want := transfer.transfer.AmountRaw, new(big.Int).SetUint64(values[i]).Bytes(); !bytes.Equal(got, want) {
			t.Errorf("transfer %d: AmountRaw = %x, want %x", i, got, want)
		}

		if transfer.transfer.TokenType != dbtypes.TokenTypeERC1155 {
			t.Errorf("transfer %d: TokenType = %d, want %d", i, transfer.transfer.TokenType, dbtypes.TokenTypeERC1155)
		}
	}
}

// A single-element batch is the tightest well-formed encoding (exactly 192
// bytes), so it pins down the off-by-one behaviour of the bounds checks.
func TestTransferBatchMinimalEncoding(t *testing.T) {
	data := encodeBatch([]uint64{1}, []uint64{2})
	if len(data) != 192 {
		t.Fatalf("unexpected minimal encoding length %d", len(data))
	}

	transfers := newTestCtx().parseERC1155TransferBatch(0, 0, batchLog(data), nil)
	if len(transfers) != 1 {
		t.Fatalf("expected 1 transfer, got %d", len(transfers))
	}
}

// Mismatched or empty arrays are not transfers and must be dropped.
func TestTransferBatchRejectsMismatch(t *testing.T) {
	for name, data := range map[string][]byte{
		"length mismatch": encodeBatch([]uint64{1, 2}, []uint64{1}),
		"empty arrays":    encodeBatch(nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			if got := newTestCtx().parseERC1155TransferBatch(1, 0, batchLog(data), nil); len(got) != 0 {
				t.Fatalf("expected no transfers, got %d", len(got))
			}
		})
	}
}

// Short logs must be dropped before any array head is read.
func TestTransferBatchRejectsShortLog(t *testing.T) {
	for _, size := range []int{0, 1, 63, 64, 127} {
		if got := newTestCtx().parseERC1155TransferBatch(1, 0, batchLog(make([]byte, size)), nil); got != nil {
			t.Fatalf("data length %d: expected nil, got %d transfers", size, len(got))
		}
	}

	log := batchLog(make([]byte, 192))
	log.Topics = log.Topics[:3]

	if got := newTestCtx().parseERC1155TransferBatch(1, 0, log, nil); got != nil {
		t.Fatalf("expected nil for a log with too few topics, got %d transfers", len(got))
	}
}

func FuzzTransferBatch(f *testing.F) {
	f.Add(encodeBatch([]uint64{1}, []uint64{2}))
	f.Add(encodeBatch([]uint64{1, 2, 3}, []uint64{4, 5, 6}))
	f.Add(make([]byte, 128))
	f.Add(bytes.Repeat([]byte{0xff}, 192))

	f.Fuzz(func(t *testing.T, data []byte) {
		// must never panic, whatever the contract emitted
		_ = newTestCtx().parseERC1155TransferBatch(1, 0, batchLog(data), nil)
	})
}
