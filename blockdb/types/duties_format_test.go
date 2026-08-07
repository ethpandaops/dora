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

func TestDutiesHeaderVersioning(t *testing.T) {
	base := buildTestEpochDuties(2048, 32, 4, 0)

	// v1: zero dependent root encodes a 40-byte header with version 1.
	v1Header := EncodeDutiesHeader(base)
	if len(v1Header) != DutiesHeaderSize {
		t.Fatalf("v1 header size = %d, want %d", len(v1Header), DutiesHeaderSize)
	}
	h1, err := DecodeDutiesHeader(v1Header)
	if err != nil {
		t.Fatalf("v1 header decode failed: %v", err)
	}
	if h1.Version != 1 {
		t.Fatalf("v1 header version = %d, want 1", h1.Version)
	}
	if h1.Flags&DutiesFlagDiverging != 0 {
		t.Fatalf("v1 header must not set diverging flag")
	}
	if h1.DependentRoot != ([32]byte{}) {
		t.Fatalf("v1 header dependent root = %x, want zero", h1.DependentRoot)
	}

	// v2: non-zero dependent root encodes a 72-byte header carrying the root.
	depRoot := [32]byte{}
	for i := range depRoot {
		depRoot[i] = byte(i + 1)
	}
	diverging := buildTestEpochDuties(2048, 32, 4, 0)
	diverging.DependentRoot = depRoot
	diverging.Diverging = true

	v2Header := EncodeDutiesHeader(diverging)
	if len(v2Header) != DutiesHeaderSizeV2 {
		t.Fatalf("v2 header size = %d, want %d", len(v2Header), DutiesHeaderSizeV2)
	}
	h2, err := DecodeDutiesHeader(v2Header)
	if err != nil {
		t.Fatalf("v2 header decode failed: %v", err)
	}
	if h2.Version != 2 {
		t.Fatalf("v2 header version = %d, want 2", h2.Version)
	}
	if h2.Flags&DutiesFlagDiverging == 0 {
		t.Fatalf("v2 header must set diverging flag")
	}
	if h2.DependentRoot != depRoot {
		t.Fatalf("v2 header dependent root = %x, want %x", h2.DependentRoot, depRoot)
	}
}

func TestEpochDutiesRoundTripDiverging(t *testing.T) {
	depRoot := [32]byte{}
	for i := range depRoot {
		depRoot[i] = byte(0xa0 + i)
	}
	src := buildTestEpochDuties(1000003, 32, 64, 0)
	src.DependentRoot = depRoot

	encoded, err := EncodeEpochDuties(src)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	got, err := DecodeEpochDuties(src.FirstSlot, encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.DependentRoot != depRoot {
		t.Fatalf("dependent root mismatch: got %x want %x", got.DependentRoot, depRoot)
	}
	if !reflect.DeepEqual(got.Committees, src.Committees) {
		t.Fatalf("committees mismatch after v2 round-trip")
	}

	// Ranged access must respect the larger v2 header offset.
	header, err := DecodeDutiesHeader(encoded)
	if err != nil {
		t.Fatalf("header decode failed: %v", err)
	}
	for slotIndex := range src.SlotsPerEpoch {
		off, length := header.AttesterSlotRange(slotIndex)
		committees, err := header.SplitSlotCommittees(slotIndex, encoded[off:off+length])
		if err != nil {
			t.Fatalf("slot %d split failed: %v", slotIndex, err)
		}
		if !reflect.DeepEqual(committees, src.Committees[slotIndex]) {
			t.Fatalf("slot %d committees mismatch via ranged access", slotIndex)
		}
	}
}

func TestEpochDutiesProposerRoundTrip(t *testing.T) {
	depRoot := [32]byte{}
	for i := range depRoot {
		depRoot[i] = byte(0x30 + i)
	}
	src := buildTestEpochDuties(500000, 32, 64, 512)
	src.DependentRoot = depRoot
	src.ProposerDuties = make([]uint64, src.SlotsPerEpoch)
	for i := range src.ProposerDuties {
		src.ProposerDuties[i] = uint64(1000 + i)
	}
	// An out-of-range proposer must be stored as the 0 sentinel.
	src.ProposerDuties[3] = uint64(1) << 60

	encoded, err := EncodeEpochDuties(src)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	header, err := DecodeDutiesHeader(encoded)
	if err != nil {
		t.Fatalf("header decode failed: %v", err)
	}
	if header.Flags&DutiesFlagProposers == 0 {
		t.Fatalf("proposer flag must be set when proposer duties are present")
	}

	got, err := DecodeEpochDuties(src.FirstSlot, encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if uint64(len(got.ProposerDuties)) != src.SlotsPerEpoch {
		t.Fatalf("proposer duties length = %d, want %d", len(got.ProposerDuties), src.SlotsPerEpoch)
	}
	for i := range got.ProposerDuties {
		want := src.ProposerDuties[i]
		if i == 3 {
			want = 0
		}
		if got.ProposerDuties[i] != want {
			t.Fatalf("proposer[%d] = %d, want %d", i, got.ProposerDuties[i], want)
		}
	}
	// Committees and PTC must still round-trip alongside the proposer section.
	if !reflect.DeepEqual(got.Committees, src.Committees) {
		t.Fatalf("committees mismatch after proposer round-trip")
	}
	if !reflect.DeepEqual(got.Ptc, src.Ptc) {
		t.Fatalf("ptc mismatch after proposer round-trip")
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
