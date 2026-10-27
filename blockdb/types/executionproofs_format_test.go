package types

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func testExecutionProof(root byte, proofType uint8, size int, valid bool) *SlotExecutionProof {
	proof := &SlotExecutionProof{
		ProofType: proofType,
		ProofData: bytes.Repeat([]byte{root ^ proofType}, size),
	}
	proof.BlockRoot[0] = root
	proof.ValidatorIndex = uint64(100 + proofType)
	proof.Signature[0] = 0xaa
	proof.Signature[95] = proofType
	proof.SignatureValid = valid
	proof.ProverActive = valid
	proof.SeenTime = 1_793_000_000_000 + uint64(proofType)
	if valid {
		proof.Verification = 1
		proof.Verifier = "v0.17.2"
	}
	return proof
}

func TestSlotExecutionProofsRoundTrip(t *testing.T) {
	obj := buildTestSlotMeta(77, []string{"a", "b"}, []map[int]int32{{0: 10, 1: 20}})
	obj.ExecutionProofs = []*SlotExecutionProof{
		testExecutionProof(1, 1, 3000, true),
		testExecutionProof(1, 5, 7, true),
		testExecutionProof(2, 1, 0, false), // orphaned sibling block, invalid signature
	}

	data, err := EncodeSlotMeta(obj)
	require.NoError(t, err)

	// full decode restores the proofs with their bytes
	decoded, err := DecodeSlotMeta(data)
	require.NoError(t, err)
	require.Len(t, decoded.ExecutionProofs, 3)
	require.Len(t, decoded.Bids, 1, "bids survive next to proofs")
	for i, proof := range decoded.ExecutionProofs {
		want := obj.ExecutionProofs[i]
		require.Equal(t, want.BlockRoot, proof.BlockRoot)
		require.Equal(t, want.ProofType, proof.ProofType)
		require.Equal(t, want.SignatureValid, proof.SignatureValid)
		require.Equal(t, want.ProverActive, proof.ProverActive)
		require.Equal(t, want.Verification, proof.Verification)
		require.Equal(t, want.Verifier, proof.Verifier)
		require.Equal(t, want.SeenTime, proof.SeenTime)
		require.Equal(t, want.ValidatorIndex, proof.ValidatorIndex)
		require.Equal(t, want.Signature, proof.Signature)
		require.Equal(t, uint32(len(want.ProofData)), proof.DataSize)
		require.Equal(t, want.ProofData, proof.ProofData)
	}

	// an index-only read sees sizes and signers but no bytes, and only touches the prefix
	ranges := [][2]int64{}
	indexOnly, err := ReadSlotMeta(SlotMetaFlagExecutionProofs, func(offset int64, length int64) ([]byte, error) {
		ranges = append(ranges, [2]int64{offset, length})
		end := int64(len(data))
		if length > 0 && offset+length < end {
			end = offset + length
		}
		return data[offset:end], nil
	})
	require.NoError(t, err)
	require.Len(t, indexOnly.ExecutionProofs, 3)
	require.Equal(t, uint32(3000), indexOnly.ExecutionProofs[0].DataSize)
	require.Nil(t, indexOnly.ExecutionProofs[0].ProofData)
	require.Equal(t, uint64(101), indexOnly.ExecutionProofs[0].ValidatorIndex)
	require.Len(t, ranges, 1, "index read must not fetch the data section")

	// an object loaded without the proof bytes cannot be written back
	_, err = EncodeSlotMeta(indexOnly)
	require.Error(t, err)

	// a read that selects no proof sections leaves them out
	bidsOnly, err := DecodeSlotMetaSections(data, SlotMetaFlagBids)
	require.NoError(t, err)
	require.Nil(t, bidsOnly.ExecutionProofs)
}

func TestSlotExecutionProofsMerge(t *testing.T) {
	a := buildTestSlotMeta(5, []string{"a"}, nil)
	a.ExecutionProofs = []*SlotExecutionProof{
		testExecutionProof(1, 1, 10, true),
		testExecutionProof(1, 2, 10, true),
	}
	b := buildTestSlotMeta(5, []string{"b"}, nil)
	newer := testExecutionProof(1, 2, 20, true)
	b.ExecutionProofs = []*SlotExecutionProof{
		newer,
		testExecutionProof(1, 3, 10, false),
	}

	merged := MergeSlotMeta(a, b)
	require.Len(t, merged.ExecutionProofs, 3)
	require.Equal(t, uint8(1), merged.ExecutionProofs[0].ProofType)
	require.Same(t, newer, merged.ExecutionProofs[1], "newer object wins for the same (root, type)")
	require.Equal(t, uint8(3), merged.ExecutionProofs[2].ProofType)

	// the newer proof wins but keeps the earliest known first-seen time
	seenTests := []struct {
		name         string
		older, newer uint64
		expectedSeen uint64
	}{
		{name: "older seen first", older: 100, newer: 200, expectedSeen: 100},
		{name: "newer seen first", older: 200, newer: 100, expectedSeen: 100},
		{name: "newer unknown", older: 100, newer: 0, expectedSeen: 100},
		{name: "older unknown", older: 0, newer: 200, expectedSeen: 200},
	}
	for _, tt := range seenTests {
		older, newer := testExecutionProof(1, 2, 10, true), testExecutionProof(1, 2, 20, true)
		older.SeenTime, newer.SeenTime = tt.older, tt.newer
		result := mergeSlotExecutionProofs([]*SlotExecutionProof{older}, []*SlotExecutionProof{newer})
		require.Len(t, result, 1, tt.name)
		require.Equal(t, tt.expectedSeen, result[0].SeenTime, tt.name)
		require.Len(t, result[0].ProofData, 20, tt.name)
		require.Equal(t, tt.newer, newer.SeenTime, "%s: the input is not modified", tt.name)
	}

	// merging with an object without proofs keeps them
	require.Len(t, MergeSlotMeta(merged, buildTestSlotMeta(5, nil, nil)).ExecutionProofs, 3)
	require.Nil(t, MergeSlotMeta(buildTestSlotMeta(5, nil, nil), buildTestSlotMeta(5, nil, nil)).ExecutionProofs)
}

// TestSlotExecutionProofsWithoutSeenTime reads records written before the
// first-seen time was stored.
func TestSlotExecutionProofsWithoutSeenTime(t *testing.T) {
	proofs := []*SlotExecutionProof{testExecutionProof(1, 1, 30, true), testExecutionProof(1, 5, 7, false)}
	index, data, err := encodeExecutionProofSections(proofs)
	require.NoError(t, err)

	// drop the trailing seen time of every record
	src := index.Data
	legacy := append([]byte(nil), src[0:2]...)
	pos := 2
	for range binary.BigEndian.Uint16(src[0:2]) {
		recordLen := int(binary.BigEndian.Uint16(src[pos : pos+2]))
		legacy = binary.BigEndian.AppendUint16(legacy, uint16(recordLen-8))
		legacy = append(legacy, src[pos+2:pos+2+recordLen-8]...)
		pos += 2 + recordLen
	}

	decoded, err := decodeExecutionProofSections(&SlotMetaSection{Type: MetaSectionExecutionProofs, Data: legacy}, data)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
	for i, proof := range decoded {
		require.Zero(t, proof.SeenTime)
		require.Equal(t, proofs[i].Verifier, proof.Verifier)
		require.Equal(t, proofs[i].ProofData, proof.ProofData)
	}
}
