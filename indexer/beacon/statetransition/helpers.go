package statetransition

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
)

// hash256 returns the SHA-256 hash of data.
func hash256(data []byte) phase0.Root {
	return phase0.Root(sha256.Sum256(data))
}

// hashTreeRoot is a placeholder for hash_tree_root of a vector of roots.
// For historical summaries, we need the Merkle root of block_roots and state_roots.
// This is a simplified version — the actual HTR of a fixed-size vector.
func hashTreeRoot(roots []phase0.Root) phase0.Root {
	if len(roots) == 0 {
		return phase0.Root{}
	}

	// Build Merkle tree bottom-up
	leaves := make([]phase0.Root, len(roots))
	copy(leaves, roots)

	// Pad to next power of 2
	size := uint64(1)
	for size < uint64(len(leaves)) {
		size *= 2
	}
	for uint64(len(leaves)) < size {
		leaves = append(leaves, phase0.Root{})
	}

	for len(leaves) > 1 {
		next := make([]phase0.Root, len(leaves)/2)
		for i := 0; i < len(next); i++ {
			var buf [64]byte
			copy(buf[:32], leaves[2*i][:])
			copy(buf[32:], leaves[2*i+1][:])
			next[i] = hash256(buf[:])
		}
		leaves = next
	}

	return leaves[0]
}

// getSeed computes the seed for the given epoch and domain type.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#get_seed
func getSeed(s *stateAccessor, epoch phase0.Epoch, domainType phase0.DomainType) phase0.Root {
	mixEpoch := epoch + phase0.Epoch(s.specs.EpochsPerHistoricalVector) - phase0.Epoch(s.specs.MinSeedLookahead) - 1
	mix := s.RANDAOMixes[uint64(mixEpoch)%s.specs.EpochsPerHistoricalVector]

	var buf [4 + 8 + 32]byte
	copy(buf[0:4], domainType[:])
	binary.LittleEndian.PutUint64(buf[4:12], uint64(epoch))
	copy(buf[12:44], mix[:])

	result := hash256(buf[:])
	return result
}

// computeShuffledIndex implements the swap-or-not shuffle.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#compute_shuffled_index
func computeShuffledIndex(index, indexCount uint64, seed phase0.Root, specs *consensus.ChainSpec) uint64 {
	if indexCount == 0 {
		return 0
	}

	for currentRound := uint64(0); currentRound < specs.ShuffleRoundCount; currentRound++ {
		var buf [33]byte
		copy(buf[0:32], seed[:])
		buf[32] = byte(currentRound)
		pivotHash := hash256(buf[:])
		pivot := binary.LittleEndian.Uint64(pivotHash[:8]) % indexCount

		flip := (pivot + indexCount - index) % indexCount
		position := index
		if flip > index {
			position = flip
		}

		var buf2 [33 + 4]byte
		copy(buf2[0:32], seed[:])
		buf2[32] = byte(currentRound)
		binary.LittleEndian.PutUint32(buf2[33:37], uint32(position/256))
		source := hash256(buf2[:])

		byteIdx := (position % 256) / 8
		bitIdx := position % 8
		bit := (source[byteIdx] >> bitIdx) & 1

		if bit == 1 {
			index = flip
		}
	}

	return index
}

// getRandomByte extracts a pseudo-random byte from a seed.
func getRandomByte(seed phase0.Root, round, byteIndex uint64) uint8 {
	var buf [40]byte
	copy(buf[:32], seed[:])
	binary.LittleEndian.PutUint64(buf[32:40], round)
	h := hash256(buf[:])
	return h[byteIndex%32]
}

// computeActivationExitEpoch returns the epoch at which a validator activation/exit takes effect.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#compute_activation_exit_epoch
func computeActivationExitEpoch(epoch phase0.Epoch, specs *consensus.ChainSpec) phase0.Epoch {
	return epoch + 1 + phase0.Epoch(specs.MaxSeedLookahead)
}
