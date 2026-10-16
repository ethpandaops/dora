package db

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/dbtypes"
)

// The slot metadata columns are only known to the live indexer; a writer that
// cannot know them (the synchronizer) must not overwrite them.
func TestInsertSlotKeepsUnknownMetadata(t *testing.T) {
	newTestDB(t)
	ctx := context.Background()
	root := []byte{0xaa, 0xbb}

	insert := func(slot *dbtypes.Slot) {
		t.Helper()
		require.NoError(t, RunDBTransaction(func(tx *sqlx.Tx) error {
			return InsertSlot(ctx, tx, slot)
		}))
	}
	read := func() *dbtypes.Slot {
		t.Helper()
		row := &dbtypes.Slot{}
		require.NoError(t, ReaderDb.GetContext(ctx, row, `SELECT slot, root, status, il_count, il_unsatisfied, proof_count FROM slots WHERE root = $1`, root))
		return row
	}

	// a first write of unknown values starts at zero
	insert(&dbtypes.Slot{Slot: 7, Root: root, Status: dbtypes.Canonical, IlCount: dbtypes.MetadataUnknown, IlUnsatisfied: dbtypes.MetadataUnknown, ProofCount: dbtypes.MetadataUnknown})
	row := read()
	require.Equal(t, int16(0), row.IlCount)
	require.Equal(t, int16(0), row.IlUnsatisfied)
	require.Equal(t, int16(0), row.ProofCount)

	// the live indexer writes the values, including the "not evaluated" verdict
	insert(&dbtypes.Slot{Slot: 7, Root: root, Status: dbtypes.Canonical, IlCount: 3, IlUnsatisfied: -1, ProofCount: 2})
	row = read()
	require.Equal(t, int16(3), row.IlCount)
	require.Equal(t, int16(-1), row.IlUnsatisfied)
	require.Equal(t, int16(2), row.ProofCount)

	// a resync keeps them while updating the rest of the row
	insert(&dbtypes.Slot{Slot: 7, Root: root, Status: dbtypes.Orphaned, IlCount: dbtypes.MetadataUnknown, IlUnsatisfied: dbtypes.MetadataUnknown, ProofCount: dbtypes.MetadataUnknown})
	row = read()
	require.Equal(t, dbtypes.Orphaned, row.Status)
	require.Equal(t, int16(3), row.IlCount)
	require.Equal(t, int16(-1), row.IlUnsatisfied)
	require.Equal(t, int16(2), row.ProofCount)

	// a missed slot row follows the same rule
	missed := func(count int16) {
		t.Helper()
		require.NoError(t, RunDBTransaction(func(tx *sqlx.Tx) error {
			return InsertMissingSlot(ctx, tx, &dbtypes.MissedSlot{Slot: 9, Proposer: 1, Status: dbtypes.Missing, IlCount: count})
		}))
	}
	readMissed := func() int16 {
		t.Helper()
		var count int16
		require.NoError(t, ReaderDb.GetContext(ctx, &count, `SELECT il_count FROM slots WHERE slot = 9 AND proposer = 1`))
		return count
	}
	missed(4)
	require.Equal(t, int16(4), readMissed())
	missed(dbtypes.MetadataUnknown)
	require.Equal(t, int16(4), readMissed())
}
