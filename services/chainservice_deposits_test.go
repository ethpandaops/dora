package services

import (
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// regularDeposit builds a non-synthetic pending deposit at the given slot.
func regularDeposit(slot phase0.Slot, pubkeyByte byte) *electra.PendingDeposit {
	d := &electra.PendingDeposit{Slot: slot}
	d.Pubkey[0] = pubkeyByte
	// zero signature -> signature[0] == 0x00 != 0xc0, so not synthetic
	return d
}

// syntheticDeposit builds a synthesized (0x01->0x02 compounding switch) pending deposit:
// the G2 point-at-infinity signature at GENESIS_SLOT, which is how queue_excess_active_balance
// marks a deposit that has no EL counterpart.
func syntheticDeposit(pubkeyByte byte) *electra.PendingDeposit {
	d := &electra.PendingDeposit{Slot: 0}
	d.Pubkey[0] = pubkeyByte
	d.Signature[0] = 0xc0
	return d
}

// infinitySigDeposit builds a real EL deposit that happens to carry the G2 point-at-infinity
// signature. The deposit contract accepts any 96-byte signature, so these exist on chain and
// must NOT be mistaken for synthesized deposits.
func infinitySigDeposit(slot phase0.Slot, pubkeyByte byte) *electra.PendingDeposit {
	d := &electra.PendingDeposit{Slot: slot}
	d.Pubkey[0] = pubkeyByte
	d.Signature[0] = 0xc0
	return d
}

func anchorAt(index, slot uint64) *dbtypes.Deposit {
	idx := index
	return &dbtypes.Deposit{Index: &idx, SlotNumber: slot}
}

func u64p(v uint64) *uint64 { return &v }

// queueRole describes the intended role of a queue entry when building realistic cases.
type queueRole int

const (
	roleRegular   queueRole = iota // a normal in-order deposit backed by an EL deposit index
	roleSynthetic                  // a 0x01->0x02 compounding switch (no EL deposit)
	rolePostponed                  // a deposit reordered to the tail (slot dips below the running max)
)

// buildQueue assembles a pending-deposit queue from a role list and returns it together with
// the per-entry roles. Regular entries get strictly increasing slots; postponed entries get a
// low slot so the resolver's forward pass detects the dip; synthetic entries carry the
// point-at-infinity signature and are slot-agnostic.
func buildQueue(roles []queueRole) ([]*electra.PendingDeposit, []queueRole) {
	queue := make([]*electra.PendingDeposit, len(roles))
	var nextRegularSlot phase0.Slot = 1000
	for i, role := range roles {
		pubkey := byte(i % 251)
		switch role {
		case roleSynthetic:
			queue[i] = syntheticDeposit(pubkey)
		case rolePostponed:
			// far below any regular slot so it always dips below the running max
			queue[i] = regularDeposit(10+phase0.Slot(i), pubkey)
		default:
			queue[i] = regularDeposit(nextRegularSlot, pubkey)
			nextRegularSlot++
		}
	}
	return queue, roles
}

// expectedFor computes the expected (indexes, postponed) for a role list given the anchor
// index, assigning contiguous EL deposit indexes to the in-order regular entries (the last
// regular aligns with the anchor) and leaving synthetic/postponed entries unindexed. It is
// derived directly from the roles, independent of the resolver's internals.
func expectedFor(roles []queueRole, anchorIndex uint64) (indexes []*uint64, postponed []bool) {
	indexes = make([]*uint64, len(roles))
	postponed = make([]bool, len(roles))

	regularPositions := make([]int, 0, len(roles))
	for i, role := range roles {
		switch role {
		case roleRegular:
			regularPositions = append(regularPositions, i)
		case rolePostponed:
			postponed[i] = true
		}
	}

	cand := int64(anchorIndex)
	for k := len(regularPositions) - 1; k >= 0; k-- {
		v := uint64(cand)
		indexes[regularPositions[k]] = &v
		cand--
	}
	return indexes, postponed
}

func TestResolveQueueDepositIndexes(t *testing.T) {
	tests := []struct {
		name          string
		queue         []*electra.PendingDeposit
		anchor        *dbtypes.Deposit
		wantIndexes   []*uint64
		wantPostponed []bool
		wantBySlot    []bool
	}{
		{
			name:          "contiguous regular deposits",
			queue:         []*electra.PendingDeposit{regularDeposit(10, 1), regularDeposit(11, 2), regularDeposit(12, 3)},
			anchor:        anchorAt(5, 12),
			wantIndexes:   []*uint64{u64p(3), u64p(4), u64p(5)},
			wantPostponed: []bool{false, false, false},
			wantBySlot:    []bool{false, false, false},
		},
		{
			name:          "postponed entry dips below running max slot",
			queue:         []*electra.PendingDeposit{regularDeposit(10, 1), regularDeposit(11, 2), regularDeposit(9, 3)},
			anchor:        anchorAt(5, 11),
			wantIndexes:   []*uint64{u64p(4), u64p(5), nil},
			wantPostponed: []bool{false, false, true},
			wantBySlot:    []bool{false, false, true},
		},
		{
			name:          "synthetic compounding-switch deposit is skipped, not postponed",
			queue:         []*electra.PendingDeposit{regularDeposit(10, 1), syntheticDeposit(2), regularDeposit(11, 3)},
			anchor:        anchorAt(5, 11),
			wantIndexes:   []*uint64{u64p(4), nil, u64p(5)},
			wantPostponed: []bool{false, false, false},
			wantBySlot:    []bool{false, false, false},
		},
		{
			name:          "0xB0 (builder-cred) regular deposit is indexed contiguously like any validator deposit",
			queue:         []*electra.PendingDeposit{regularDeposit(10, 1), regularDeposit(11, 2)},
			anchor:        anchorAt(8, 11),
			wantIndexes:   []*uint64{u64p(7), u64p(8)},
			wantPostponed: []bool{false, false},
			wantBySlot:    []bool{false, false},
		},
		{
			name:          "anchor slot mismatch falls back to slot resolution without claiming the entry is postponed",
			queue:         []*electra.PendingDeposit{regularDeposit(10, 1)},
			anchor:        anchorAt(5, 20),
			wantIndexes:   []*uint64{nil},
			wantPostponed: []bool{false},
			wantBySlot:    []bool{true},
		},
		{
			name:          "nil anchor leaves every entry to slot resolution",
			queue:         []*electra.PendingDeposit{regularDeposit(10, 1), regularDeposit(11, 2)},
			anchor:        nil,
			wantIndexes:   []*uint64{nil, nil},
			wantPostponed: []bool{false, false},
			wantBySlot:    []bool{true, true},
		},
		{
			name:          "real deposit with a point-at-infinity signature is indexed like any other",
			queue:         []*electra.PendingDeposit{regularDeposit(10, 1), infinitySigDeposit(11, 2), regularDeposit(12, 3)},
			anchor:        anchorAt(9, 12),
			wantIndexes:   []*uint64{u64p(7), u64p(8), u64p(9)},
			wantPostponed: []bool{false, false, false},
			wantBySlot:    []bool{false, false, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexes, postponed, bySlot := resolveQueueDepositIndexes(tt.queue, tt.anchor, true)

			for i := range tt.wantBySlot {
				if bySlot[i] != tt.wantBySlot[i] {
					t.Errorf("resolveBySlot[%d] = %v, want %v", i, bySlot[i], tt.wantBySlot[i])
				}
			}

			if len(postponed) != len(tt.wantPostponed) {
				t.Fatalf("postponed length = %d, want %d", len(postponed), len(tt.wantPostponed))
			}
			for i := range tt.wantPostponed {
				if postponed[i] != tt.wantPostponed[i] {
					t.Errorf("postponed[%d] = %v, want %v", i, postponed[i], tt.wantPostponed[i])
				}
			}

			if len(indexes) != len(tt.wantIndexes) {
				t.Fatalf("indexes length = %d, want %d", len(indexes), len(tt.wantIndexes))
			}
			for i := range tt.wantIndexes {
				switch {
				case tt.wantIndexes[i] == nil && indexes[i] != nil:
					t.Errorf("index[%d] = %d, want nil", i, *indexes[i])
				case tt.wantIndexes[i] != nil && indexes[i] == nil:
					t.Errorf("index[%d] = nil, want %d", i, *tt.wantIndexes[i])
				case tt.wantIndexes[i] != nil && indexes[i] != nil && *indexes[i] != *tt.wantIndexes[i]:
					t.Errorf("index[%d] = %d, want %d", i, *indexes[i], *tt.wantIndexes[i])
				}
			}
		})
	}
}

func countRole(roles []queueRole, want queueRole) int {
	n := 0
	for _, r := range roles {
		if r == want {
			n++
		}
	}
	return n
}

// TestResolveQueueDepositIndexesRealistic exercises large, mainnet-shaped queues: long
// contiguous runs, clusters of postponed top-ups to exiting validators appended at the tail
// (process_pending_deposits behaviour), 0x01->0x02 compounding-switch synthetics interspersed
// throughout, and the "nothing but old stuck deposits" case that falls back to slot
// resolution. These mirror the patterns seen on the live mainnet deposit queue.
func TestResolveQueueDepositIndexesRealistic(t *testing.T) {
	buildRoles := func(spec func() []queueRole) []queueRole { return spec() }

	cases := []struct {
		name        string
		roles       []queueRole
		anchorIndex uint64
	}{
		{
			name: "256 contiguous deposits",
			roles: buildRoles(func() []queueRole {
				roles := make([]queueRole, 256)
				for i := range roles {
					roles[i] = roleRegular
				}
				return roles
			}),
			anchorIndex: 5000,
		},
		{
			name: "200 in-order deposits with 8 postponed top-ups appended at the tail",
			roles: buildRoles(func() []queueRole {
				roles := make([]queueRole, 0, 208)
				for i := 0; i < 200; i++ {
					roles = append(roles, roleRegular)
				}
				for i := 0; i < 8; i++ {
					roles = append(roles, rolePostponed)
				}
				return roles
			}),
			anchorIndex: 800000,
		},
		{
			name: "150 deposits with a compounding-switch synthetic after every 25",
			roles: buildRoles(func() []queueRole {
				roles := make([]queueRole, 0, 156)
				for i := 0; i < 150; i++ {
					roles = append(roles, roleRegular)
					if (i+1)%25 == 0 {
						roles = append(roles, roleSynthetic)
					}
				}
				return roles
			}),
			anchorIndex: 1_234_567,
		},
		{
			name: "kitchen sink: 100 deposits, interspersed synthetics, postponed top-ups at the tail",
			roles: buildRoles(func() []queueRole {
				roles := make([]queueRole, 0, 120)
				for i := 0; i < 100; i++ {
					roles = append(roles, roleRegular)
					if (i+1)%30 == 0 {
						roles = append(roles, roleSynthetic)
					}
				}
				for i := 0; i < 5; i++ {
					roles = append(roles, rolePostponed)
				}
				return roles
			}),
			anchorIndex: 42_000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			queue, roles := buildQueue(tc.roles)
			// The tail-most in-order regular carries the highest assigned slot; the anchor must
			// align with it for the positional assignment to be trusted.
			regCount := countRole(roles, roleRegular)
			anchor := anchorAt(tc.anchorIndex, uint64(1000+regCount-1))

			wantIndexes, wantPostponed := expectedFor(roles, tc.anchorIndex)
			gotIndexes, gotPostponed, _ := resolveQueueDepositIndexes(queue, anchor, true)

			if len(gotIndexes) != len(queue) || len(gotPostponed) != len(queue) {
				t.Fatalf("result lengths = (%d,%d), want %d", len(gotIndexes), len(gotPostponed), len(queue))
			}
			for i := range queue {
				if gotPostponed[i] != wantPostponed[i] {
					t.Errorf("postponed[%d] = %v, want %v", i, gotPostponed[i], wantPostponed[i])
				}
				switch {
				case wantIndexes[i] == nil && gotIndexes[i] != nil:
					t.Errorf("index[%d] = %d, want nil", i, *gotIndexes[i])
				case wantIndexes[i] != nil && gotIndexes[i] == nil:
					t.Errorf("index[%d] = nil, want %d", i, *wantIndexes[i])
				case wantIndexes[i] != nil && gotIndexes[i] != nil && *gotIndexes[i] != *wantIndexes[i]:
					t.Errorf("index[%d] = %d, want %d", i, *gotIndexes[i], *wantIndexes[i])
				}
			}
		})
	}
}

// TestResolveQueueDepositIndexesStuckDepositsFallback models a queue made up entirely of old
// stuck top-up deposits to already-exited validators: their slots are in order (no dip), but
// the anchor — the most recent included deposit — is unrelated and far newer, so positional
// assignment cannot be trusted and every entry must fall back to slot resolution.
func TestResolveQueueDepositIndexesStuckDepositsFallback(t *testing.T) {
	queue := []*electra.PendingDeposit{
		regularDeposit(1000, 1),
		regularDeposit(1001, 2),
		regularDeposit(1002, 3),
	}
	anchor := anchorAt(9_000_000, 5_000_000) // recent, unrelated deposit

	indexes, postponed, bySlot := resolveQueueDepositIndexes(queue, anchor, true)
	for i := range queue {
		if !bySlot[i] {
			t.Errorf("resolveBySlot[%d] = false, want true (slot fallback)", i)
		}
		if postponed[i] {
			t.Errorf("postponed[%d] = true, want false (the entries are stuck, not reordered)", i)
		}
		if indexes[i] != nil {
			t.Errorf("index[%d] = %d, want nil (slot fallback)", i, *indexes[i])
		}
	}
}

// TestResolveQueueDepositIndexesPostGloas checks that the positional shortcut is fully
// disabled once Gloas is active: onboard_builders_from_pending_deposits and the builder
// fast path in process_deposit_request both leave holes in the deposit index sequence, so
// every non-synthetic entry must be resolved by slot even though the queue looks in-order.
func TestResolveQueueDepositIndexesPostGloas(t *testing.T) {
	queue := []*electra.PendingDeposit{
		regularDeposit(1000, 1),
		syntheticDeposit(2),
		regularDeposit(1001, 3),
	}
	anchor := anchorAt(500, 1001) // would verify fine under the positional path

	indexes, postponed, bySlot := resolveQueueDepositIndexes(queue, anchor, false)
	wantBySlot := []bool{true, false, true}
	for i := range queue {
		if bySlot[i] != wantBySlot[i] {
			t.Errorf("resolveBySlot[%d] = %v, want %v", i, bySlot[i], wantBySlot[i])
		}
		if postponed[i] {
			t.Errorf("postponed[%d] = true, want false", i)
		}
		if indexes[i] != nil {
			t.Errorf("index[%d] = %d, want nil (no positional assignment post-Gloas)", i, *indexes[i])
		}
	}
}

// depositRow builds a db deposit row as GetDepositRequestsBySlots returns them.
func depositRow(index, slot uint64, pubkeyByte byte, amount uint64) *dbtypes.Deposit {
	idx := index
	pubkey := make([]byte, 48)
	pubkey[0] = pubkeyByte

	return &dbtypes.Deposit{
		Index:                 &idx,
		SlotNumber:            slot,
		PublicKey:             pubkey,
		WithdrawalCredentials: []byte{0x01},
		Amount:                amount,
	}
}

// queueEntryAt builds a pending deposit matching depositRow's identity fields.
func queueEntryAt(slot phase0.Slot, pubkeyByte byte, amount phase0.Gwei) *electra.PendingDeposit {
	d := &electra.PendingDeposit{Slot: slot, Amount: amount}
	d.Pubkey[0] = pubkeyByte
	d.WithdrawalCredentials = []byte{0x01}

	return d
}

// TestAssignDepositIndexesBySlotUsesSlotTail covers the queue's head slot, which the chain has
// normally already drained part of. Its remaining entries are the LAST deposits of that slot,
// so they must get the highest indexes - handing out the slot's first indexes instead links
// each row to a deposit transaction that was already applied.
func TestAssignDepositIndexesBySlotUsesSlotTail(t *testing.T) {
	// slot 100 included six identical deposits; only the last three are still queued
	rows := []*dbtypes.Deposit{
		depositRow(10, 100, 1, 1_000_000_000),
		depositRow(11, 100, 1, 1_000_000_000),
		depositRow(12, 100, 1, 1_000_000_000),
		depositRow(13, 100, 1, 1_000_000_000),
		depositRow(14, 100, 1, 1_000_000_000),
		depositRow(15, 100, 1, 1_000_000_000),
		// a later, fully queued slot
		depositRow(20, 101, 2, 1_000_000_000),
		depositRow(21, 101, 2, 1_000_000_000),
	}

	queue := []*electra.PendingDeposit{
		queueEntryAt(100, 1, 1_000_000_000),
		queueEntryAt(100, 1, 1_000_000_000),
		queueEntryAt(100, 1, 1_000_000_000),
		queueEntryAt(101, 2, 1_000_000_000),
		queueEntryAt(101, 2, 1_000_000_000),
	}
	resolveBySlot := []bool{true, true, true, true, true}

	want := []uint64{13, 14, 15, 20, 21}
	got := assignDepositIndexesBySlot(queue, resolveBySlot, rows)

	if len(got) != len(want) {
		t.Fatalf("got %d indexes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] == nil {
			t.Errorf("index[%d] = nil, want %d", i, want[i])
			continue
		}
		if *got[i] != want[i] {
			t.Errorf("index[%d] = %d, want %d", i, *got[i], want[i])
		}
	}
}

// TestAssignDepositIndexesBySlotSkipsUnflagged leaves entries that were already resolved
// positionally untouched, and reports no index for a queue entry with no matching deposit.
func TestAssignDepositIndexesBySlotSkipsUnflagged(t *testing.T) {
	rows := []*dbtypes.Deposit{depositRow(7, 100, 1, 1_000_000_000)}

	queue := []*electra.PendingDeposit{
		queueEntryAt(100, 1, 1_000_000_000),
		queueEntryAt(100, 1, 1_000_000_000),
		queueEntryAt(100, 9, 1_000_000_000), // no matching deposit row
	}
	resolveBySlot := []bool{false, true, true}

	got := assignDepositIndexesBySlot(queue, resolveBySlot, rows)
	if got[0] != nil {
		t.Errorf("index[0] = %d, want nil (not flagged for slot resolution)", *got[0])
	}
	if got[1] == nil || *got[1] != 7 {
		t.Errorf("index[1] = %v, want 7", got[1])
	}
	if got[2] != nil {
		t.Errorf("index[2] = %d, want nil (no matching deposit)", *got[2])
	}
}
