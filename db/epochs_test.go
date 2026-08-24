package db

import (
	"context"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func TestRepairEpochPayloadCounts(t *testing.T) {
	newTestDB(t)

	ctx := context.Background()
	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		for _, epoch := range []*dbtypes.Epoch{
			{Epoch: 0, PayloadCount: 7},  // pre-Gloas: must remain untouched
			{Epoch: 1, PayloadCount: 32}, // stale: counted a late envelope
			{Epoch: 2, PayloadCount: 1},  // already correct
		} {
			if err := InsertEpoch(ctx, tx, epoch); err != nil {
				return err
			}
		}

		slots := []*dbtypes.Slot{
			{Slot: 32, Status: dbtypes.Canonical, Root: []byte{0x20}, PayloadStatus: dbtypes.PayloadStatusCanonical},
			{Slot: 33, Status: dbtypes.Canonical, Root: []byte{0x21}, PayloadStatus: dbtypes.PayloadStatusOrphaned},
			{Slot: 34, Status: dbtypes.Canonical, Root: []byte{0x22}, PayloadStatus: dbtypes.PayloadStatusMissing},
			{Slot: 35, Status: dbtypes.Orphaned, Root: []byte{0x23}, PayloadStatus: dbtypes.PayloadStatusCanonical},
			{Slot: 64, Status: dbtypes.Canonical, Root: []byte{0x40}, PayloadStatus: dbtypes.PayloadStatusCanonical},
		}
		for _, slot := range slots {
			if err := InsertSlot(ctx, tx, slot); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("seed database: %v", err)
	}

	var repaired uint64
	err = RunDBTransaction(func(tx *sqlx.Tx) error {
		var repairErr error
		repaired, repairErr = RepairEpochPayloadCounts(ctx, tx, 32, 1)
		return repairErr
	})
	if err != nil {
		t.Fatalf("repair payload counts: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired epochs = %d, want 1", repaired)
	}

	tests := []struct {
		epoch uint64
		want  uint64
	}{
		{epoch: 0, want: 7},
		{epoch: 1, want: 1},
		{epoch: 2, want: 1},
	}
	for _, tt := range tests {
		var got uint64
		if err := ReaderDb.GetContext(ctx, &got, `SELECT payload_count FROM epochs WHERE epoch = $1`, tt.epoch); err != nil {
			t.Fatalf("read epoch %d: %v", tt.epoch, err)
		}
		if got != tt.want {
			t.Errorf("epoch %d payload count = %d, want %d", tt.epoch, got, tt.want)
		}
	}

	err = RunDBTransaction(func(tx *sqlx.Tx) error {
		var repairErr error
		repaired, repairErr = RepairEpochPayloadCounts(ctx, tx, 32, 1)
		return repairErr
	})
	if err != nil {
		t.Fatalf("repeat payload count repair: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repeated repair changed %d epochs, want 0", repaired)
	}
}
