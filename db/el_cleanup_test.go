package db

import (
	"context"
	"testing"
	"time"
)

// TestDeleteElDataBeforeBlockUid deletes rows below the threshold and keeps the
// rest. It runs under a deadline because the cleanup opens its own per-batch
// transactions on the sqlite writer lock; if it were ever wrapped in an outer
// transaction again it would deadlock instead of returning.
func TestDeleteElDataBeforeBlockUid(t *testing.T) {
	newTestDB(t)

	for _, uid := range []uint64{10, 20, 100, 200} {
		if _, err := writerDb.Exec(`INSERT INTO el_blocks (block_uid) VALUES (?)`, uid); err != nil {
			t.Fatalf("insert el_block %d: %v", uid, err)
		}
	}

	const threshold = 50
	done := make(chan error, 1)
	go func() {
		_, err := DeleteElDataBeforeBlockUid(context.Background(), threshold)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DeleteElDataBeforeBlockUid: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DeleteElDataBeforeBlockUid did not return (writer deadlock?)")
	}

	var remaining []uint64
	if err := ReaderDb.Select(&remaining, `SELECT block_uid FROM el_blocks ORDER BY block_uid`); err != nil {
		t.Fatalf("read el_blocks: %v", err)
	}
	want := []uint64{100, 200}
	if len(remaining) != len(want) || remaining[0] != want[0] || remaining[1] != want[1] {
		t.Errorf("remaining block_uids = %v, want %v", remaining, want)
	}
}
