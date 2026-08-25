package db

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/dbtypes"
)

// TestInsertBuilderDepositsChunking inserts far more rows than a single statement can bind.
// The one-time Gloas builder onboarding produces one row per queued builder deposit, which on
// a spammed devnet was ~12k rows - 13 placeholders each, well past the 65535 parameters
// postgres allows and the 32766 sqlite allows.
func TestInsertBuilderDepositsChunking(t *testing.T) {
	newTestDB(t)

	const rowCount = 20000

	deposits := make([]*dbtypes.BuilderDeposit, 0, rowCount)
	for i := 0; i < rowCount; i++ {
		slotRoot := make([]byte, 32)
		slotRoot[0] = byte(i % 251)
		pubkey := make([]byte, 48)
		pubkey[0] = byte(i % 253)

		deposits = append(deposits, &dbtypes.BuilderDeposit{
			SlotNumber:            1000,
			SlotRoot:              slotRoot,
			SlotIndex:             uint64(i),
			ForkId:                1,
			PublicKey:             pubkey,
			WithdrawalCredentials: make([]byte, 32),
			Amount:                1_000_000_000,
			Signature:             make([]byte, 96),
			Result:                dbtypes.BuilderDepositRequestResultNewBuilder,
		})
	}

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		return InsertBuilderDeposits(context.Background(), tx, deposits)
	})
	if err != nil {
		t.Fatalf("insert builder deposits: %v", err)
	}

	var count int
	if err := ReaderDb.Get(&count, "SELECT count(*) FROM builder_deposits"); err != nil {
		t.Fatalf("count builder deposits: %v", err)
	}
	if count != rowCount {
		t.Errorf("stored %d builder deposits, want %d", count, rowCount)
	}
}

// TestGetBuilderDepositTxsFilteredSentinelDequeue checks that a request tx stored with the
// dequeue-block sentinel (0) only counts as pending while no builder deposit carrying the same
// content has been included. Requests enqueued before the activation fork keep that sentinel
// until the fork boundary finalizes, so without the check they would be listed as pending next
// to the very deposit they produced.
func TestGetBuilderDepositTxsFilteredSentinelDequeue(t *testing.T) {
	newTestDB(t)

	ctx := context.Background()
	includedPubkey := make([]byte, 48)
	includedPubkey[0] = 0x11
	queuedPubkey := make([]byte, 48)
	queuedPubkey[0] = 0x22
	signature := make([]byte, 96)
	signature[0] = 0x33

	depositTx := func(pubkey []byte, blockIndex uint64) *dbtypes.BuilderDepositTx {
		blockRoot := make([]byte, 32)
		blockRoot[0] = byte(blockIndex)

		return &dbtypes.BuilderDepositTx{
			BlockNumber:           100,
			BlockIndex:            blockIndex,
			BlockTime:             1000,
			BlockRoot:             blockRoot,
			PublicKey:             pubkey,
			WithdrawalCredentials: make([]byte, 32),
			Amount:                1_000_000_000,
			Signature:             signature,
			TxSender:              make([]byte, 20),
			TxTarget:              make([]byte, 20),
			DequeueBlock:          0, // sentinel: activation block not resolved yet
		}
	}

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		if err := InsertBuilderDepositTxs(ctx, tx, []*dbtypes.BuilderDepositTx{
			depositTx(includedPubkey, 1),
			depositTx(queuedPubkey, 2),
		}); err != nil {
			return err
		}

		return InsertBuilderDeposits(ctx, tx, []*dbtypes.BuilderDeposit{{
			SlotNumber:            2000,
			SlotRoot:              make([]byte, 32),
			SlotIndex:             0,
			PublicKey:             includedPubkey,
			WithdrawalCredentials: make([]byte, 32),
			Amount:                1_000_000_000,
			Signature:             signature,
		}})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, total, err := GetBuilderDepositTxsFiltered(ctx, 0, 10, &dbtypes.BuilderDepositTxFilter{MinDequeue: 500})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if total != 1 {
		t.Fatalf("pending count = %d, want 1", total)
	}
	if len(rows) != 1 {
		t.Fatalf("returned %d rows, want 1", len(rows))
	}
	if rows[0].BlockIndex != 2 {
		t.Errorf("pending row is block index %d, want 2 (the one without an included deposit)", rows[0].BlockIndex)
	}

	sentinel := GetBuilderDepositTxsWithSentinelDequeue(ctx, [][]byte{includedPubkey})
	if len(sentinel) != 1 || sentinel[0].BlockIndex != 1 {
		t.Errorf("sentinel lookup returned %d rows, want the included pubkey's request tx", len(sentinel))
	}
}
