package types

import (
	"bytes"
	"testing"
)

func TestHashPrefix(t *testing.T) {
	full := make([]byte, 32)
	for i := range full {
		full[i] = byte(i + 1)
	}

	got := HashPrefix(full)
	if len(got) != TxHashPrefixLen {
		t.Fatalf("expected length %d, got %d", TxHashPrefixLen, len(got))
	}
	if !bytes.Equal(got, full[:TxHashPrefixLen]) {
		t.Fatalf("prefix mismatch: %x vs %x", got, full[:TxHashPrefixLen])
	}

	// Must be a copy: mutating the result must not touch the input.
	got[0] = 0xFF
	if full[0] == 0xFF {
		t.Fatal("HashPrefix aliased the input slice")
	}

	// Short input is right-padded with zeroes (no panic).
	short := HashPrefix([]byte{0xAB, 0xCD})
	if len(short) != TxHashPrefixLen || short[0] != 0xAB || short[1] != 0xCD || short[2] != 0x00 {
		t.Fatalf("unexpected short prefix: %x", short)
	}
}
