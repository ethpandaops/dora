package txindexer

import (
	"bytes"
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/spamoor/txtypes"
)

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
