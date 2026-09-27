package txindexer

import (
	"bytes"
	"strings"
	"testing"
)

// abiString builds the standard ABI encoding of a returned string:
// offset word (32) | length word | padded data.
func abiString(s string) []byte {
	data := append([]byte{}, word(32)...)
	data = append(data, word(uint64(len(s)))...)
	data = append(data, []byte(s)...)

	if pad := len(s) % 32; pad != 0 {
		data = append(data, make([]byte, 32-pad)...)
	}

	return data
}

// abiStringLen builds an ABI string whose declared length word is forged
// independently of the payload that follows it.
func abiStringLen(length uint64, payload []byte) []byte {
	data := append([]byte{}, word(32)...)
	data = append(data, word(length)...)

	return append(data, payload...)
}

// decodeString reads an ABI length word out of a contract's return data, which
// is entirely attacker-chosen: any contract can implement name()/symbol() to
// return arbitrary bytes. It runs on the block-processing worker goroutine, so
// a panic here terminates the process and poisons every subsequent restart.
func TestDecodeStringRejectsBadLength(t *testing.T) {
	cases := map[string][]byte{
		"length 2^63":            abiStringLen(1<<63, []byte("hello")),
		"length wraps to zero":   abiStringLen(^uint64(0)-63, []byte("hello")),
		"length max uint64":      abiStringLen(^uint64(0), []byte("hello")),
		"length above uint64":    append(append([]byte{}, word(32)...), append(hugeWord(4), []byte("hello")...)...),
		"length exceeds payload": abiStringLen(64, []byte("hello")),
		"all 0xff words":         bytes.Repeat([]byte{0xff}, 96),
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on malformed ABI string: %v", r)
				}
			}()

			if got := decodeString(data); got != "" {
				t.Fatalf("expected empty string for malformed data, got %q", got)
			}
		})
	}
}

// The hardened length check must not change how valid encodings decode.
func TestDecodeStringParsesValid(t *testing.T) {
	for name, tc := range map[string]struct {
		data []byte
		want string
	}{
		"short string":      {abiString("USDC"), "USDC"},
		"exactly one word":  {abiString(strings.Repeat("a", 32)), strings.Repeat("a", 32)},
		"spans two words":   {abiString(strings.Repeat("b", 33)), strings.Repeat("b", 33)},
		"control chars":     {abiString("US\x00DC\x01"), "USDC"},
		"whitespace":        {abiString("  Dai  "), "Dai"},
		"bytes32 style":     {append([]byte("DAI"), make([]byte, 29)...), "DAI"},
		"empty":             {nil, ""},
		"zero length":       {abiString(""), ""},
		"bytes32 all zero":  {make([]byte, 32), ""},
		"non-standard head": {append(append([]byte{}, word(64)...), make([]byte, 64)...), ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := decodeString(tc.data); got != tc.want {
				t.Fatalf("decodeString = %q, want %q", got, tc.want)
			}
		})
	}
}

func FuzzDecodeString(f *testing.F) {
	f.Add(abiString("USDC"))
	f.Add(abiString(strings.Repeat("a", 100)))
	f.Add(make([]byte, 32))
	f.Add(make([]byte, 96))
	f.Add(bytes.Repeat([]byte{0xff}, 96))

	f.Fuzz(func(t *testing.T, data []byte) {
		// must never panic, whatever the contract returned
		_ = decodeString(data)
	})
}
