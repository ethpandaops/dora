package db

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
)

// TestUpdateMevBlockByEpoch checks that proposal status is resolved against the
// right rows on sqlite, whose $N placeholders bind by appearance order. With the
// placeholders out of order the slot range and hash list bind to the wrong
// values, so canonical blocks are never marked proposed.
func TestUpdateMevBlockByEpoch(t *testing.T) {
	newTestDB(t)

	const slotsPerEpoch = 32
	const epoch = 1 // covers slots 32..63

	canonHash := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	insertMevBlock(t, 40, canonHash, 0)     // canonical, in range -> expect 1
	insertMevBlock(t, 41, []byte{0x01}, 0)  // not canonical, was 0  -> expect 0
	insertMevBlock(t, 42, []byte{0x02}, 1)  // not canonical, was 1  -> expect 2 (orphaned)
	insertMevBlock(t, 200, []byte{0x03}, 1) // out of epoch range    -> unchanged (1)

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		return UpdateMevBlockByEpoch(context.Background(), tx, epoch, slotsPerEpoch, [][]byte{canonHash})
	})
	if err != nil {
		t.Fatalf("UpdateMevBlockByEpoch: %v", err)
	}

	cases := []struct {
		slot uint64
		want int
	}{
		{40, 1},
		{41, 0},
		{42, 2},
		{200, 1},
	}
	for _, c := range cases {
		if got := proposedAt(t, c.slot); got != c.want {
			t.Errorf("slot %d: proposed = %d, want %d", c.slot, got, c.want)
		}
	}
}

func insertMevBlock(t *testing.T, slot uint64, hash []byte, proposed int) {
	t.Helper()
	_, err := writerDb.Exec(`INSERT INTO mev_blocks
		(slot_number, block_hash, block_number, builder_pubkey, proposed,
		 seenby_relays, fee_recipient, tx_count, gas_used, block_value, block_value_gwei)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		slot, hash, 0, []byte{}, proposed, 0, []byte{}, 0, 0, []byte{}, 0)
	if err != nil {
		t.Fatalf("insert mev_block slot %d: %v", slot, err)
	}
}

func proposedAt(t *testing.T, slot uint64) int {
	t.Helper()
	var proposed int
	if err := ReaderDb.Get(&proposed, `SELECT proposed FROM mev_blocks WHERE slot_number = ?`, slot); err != nil {
		t.Fatalf("read proposed slot %d: %v", slot, err)
	}
	return proposed
}
