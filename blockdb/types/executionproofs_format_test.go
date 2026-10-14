package types

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func testExecutionProof(root byte, proofType uint8, size int, signed bool) *SlotExecutionProof {
	proof := &SlotExecutionProof{
		ProofType: proofType,
		Signed:    signed,
		ProofData: bytes.Repeat([]byte{root ^ proofType}, size),
	}
	proof.BlockRoot[0] = root
	if signed {
		proof.ValidatorIndex = uint64(100 + proofType)
		proof.Signature[0] = 0xaa
		proof.Signature[95] = proofType
	}
	return proof
}

func TestSlotExecutionProofsRoundTrip(t *testing.T) {
	obj := buildTestSlotMeta(77, []string{"a", "b"}, []map[int]int32{{0: 10, 1: 20}})
	obj.ExecutionProofs = []*SlotExecutionProof{
		testExecutionProof(1, 1, 3000, true),
		testExecutionProof(1, 5, 7, true),
		testExecutionProof(2, 1, 0, false), // orphaned sibling block, unsigned
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
		require.Equal(t, want.Signed, proof.Signed)
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

	// merging with an object without proofs keeps them
	require.Len(t, MergeSlotMeta(merged, buildTestSlotMeta(5, nil, nil)).ExecutionProofs, 3)
	require.Nil(t, MergeSlotMeta(buildTestSlotMeta(5, nil, nil), buildTestSlotMeta(5, nil, nil)).ExecutionProofs)
}
