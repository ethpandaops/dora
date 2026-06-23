package types

import (
	"reflect"
	"testing"
)

// buildTestEpochDuties constructs an EpochDuties whose committee sizes follow the
// same SplitOffset layout the indexer uses, filling members with sequential
// global indices so round-trips are easy to verify.
func buildTestEpochDuties(validatorCount, slotsPerEpoch, committeesPerSlot, ptcSize uint64) *EpochDuties {
	committeesCount := committeesPerSlot * slotsPerEpoch
	committees := make([][][]uint64, slotsPerEpoch)
	next := uint64(0)
	for slotIndex := range slotsPerEpoch {
		slotCommittees := make([][]uint64, committeesPerSlot)
		for c := range committeesPerSlot {
			indexOffset := slotIndex*committeesPerSlot + c
			start := splitOffset(validatorCount, committeesCount, indexOffset)
			end := splitOffset(validatorCount, committeesCount, indexOffset+1)
			members := make([]uint64, end-start)
			for i := range members {
				members[i] = next
				next++
			}
			slotCommittees[c] = members
		}
		committees[slotIndex] = slotCommittees
	}

	var ptc [][]uint64
	if ptcSize > 0 {
		ptc = make([][]uint64, slotsPerEpoch)
		for slotIndex := range slotsPerEpoch {
			members := make([]uint64, ptcSize)
			for i := range members {
				members[i] = (slotIndex*ptcSize + uint64(i)) % validatorCount
			}
			ptc[slotIndex] = members
		}
	}

	return &EpochDuties{
		FirstSlot:         100 * slotsPerEpoch,
		Epoch:             100,
		ValidatorCount:    validatorCount,
		SlotsPerEpoch:     slotsPerEpoch,
		CommitteesPerSlot: committeesPerSlot,
		PtcSize:           ptcSize,
		Committees:        committees,
		Ptc:               ptc,
	}
}

func TestEpochDutiesRoundTrip(t *testing.T) {
	cases := []struct {
		name                                                      string
		validatorCount, slotsPerEpoch, committeesPerSlot, ptcSize uint64
	}{
		{"small-single-committee", 97, 32, 1, 0},
		{"even-split", 2048, 32, 4, 0},
		{"uneven-split", 1000003, 32, 64, 0},
		{"with-ptc", 500000, 32, 64, 512},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := buildTestEpochDuties(tc.validatorCount, tc.slotsPerEpoch, tc.committeesPerSlot, tc.ptcSize)

			encoded, err := EncodeEpochDuties(src)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}

			got, err := DecodeEpochDuties(src.FirstSlot, encoded)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}

			if !reflect.DeepEqual(got.Committees, src.Committees) {
				t.Fatalf("committees mismatch after round-trip")
			}
			if !reflect.DeepEqual(got.Ptc, src.Ptc) {
				t.Fatalf("ptc mismatch after round-trip")
			}

			// Per-slot ranged access must match the committees too.
			header, err := DecodeDutiesHeader(encoded)
			if err != nil {
				t.Fatalf("header decode failed: %v", err)
			}
			for slotIndex := range tc.slotsPerEpoch {
				off, length := header.AttesterSlotRange(slotIndex)
				committees, err := header.SplitSlotCommittees(slotIndex, encoded[off:off+length])
				if err != nil {
					t.Fatalf("slot %d split failed: %v", slotIndex, err)
				}
				if !reflect.DeepEqual(committees, src.Committees[slotIndex]) {
					t.Fatalf("slot %d committees mismatch via ranged access", slotIndex)
				}
			}
		})
	}
}

func TestSlotCommitteesRoundTrip(t *testing.T) {
	committees := [][]uint64{
		{1, 2, 3},
		{},
		{1000000, 999999, 0, 42},
	}

	blob, err := EncodeSlotCommittees(committees)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	got, err := DecodeSlotCommittees(blob)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if !reflect.DeepEqual(got, committees) {
		t.Fatalf("slot committees mismatch: got %v want %v", got, committees)
	}
}

func TestIndexListRoundTrip(t *testing.T) {
	indices := []uint64{0, 1, 255, 256, 1 << 40, (1 << 48) - 1}

	blob, err := EncodeIndexList(indices)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	got := DecodeIndexList(blob, DutiesIndexWidth)
	if !reflect.DeepEqual(got, indices) {
		t.Fatalf("index list mismatch: got %v want %v", got, indices)
	}
}
