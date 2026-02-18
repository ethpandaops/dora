package utils

import (
	"crypto/sha256"

	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/ethereum/go-ethereum/common"
)

// MatchBlobCommitments finds the KZG commitments from the block that match
// the given versioned hashes from a transaction.
// Since blob commitments in the block are ordered across all transactions,
// we need to find the ones belonging to this transaction by matching versioned hashes.
func MatchBlobCommitments(versionedHashes []common.Hash, allCommitments []deneb.KZGCommitment) [][]byte {
	result := make([][]byte, len(versionedHashes))

	// For each versioned hash, find matching commitment
	// A versioned hash is SHA256(commitment) with version byte prefix
	for i, hash := range versionedHashes {
		for _, commitment := range allCommitments {
			// Compute versioned hash from commitment
			computedHash := ComputeVersionedHash(commitment[:])
			if computedHash == hash {
				result[i] = commitment[:]
				break
			}
		}
	}

	return result
}

// ComputeVersionedHash computes the versioned hash from a KZG commitment.
// Per EIP-4844: versioned_hash = 0x01 || SHA256(commitment)[1:]
func ComputeVersionedHash(commitment []byte) common.Hash {
	hash := sha256.Sum256(commitment)
	hash[0] = 0x01 // Version byte for blob commitments
	return common.Hash(hash)
}
