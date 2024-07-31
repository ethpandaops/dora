package beacon

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
)

// ForkKey represents a key used for indexing forks.
type ForkKey uint64

// getForkKey calculates the ForkKey based on the base and leaf roots.
func getForkKey(baseRoot phase0.Root, leafRoot phase0.Root) ForkKey {
	hashData := make([]byte, 64)
	copy(hashData[:32], baseRoot[:])
	copy(hashData[32:], leafRoot[:])
	hash := sha256.Sum256(hashData)

	keyBytes := hash[:8]
	keyBytes[0] &= 0x7F

	return ForkKey(binary.BigEndian.Uint64(keyBytes))
}

// Fork represents a fork in the beacon chain.
type Fork struct {
	forkId     ForkKey     // Unique identifier for the fork.
	baseSlot   phase0.Slot // Slot of the base block.
	baseRoot   phase0.Root // Root of the base block.
	leafSlot   phase0.Slot // Slot of the leaf block.
	leafRoot   phase0.Root // Root of the leaf block.
	parentFork *Fork       // Parent fork.
}

// newFork creates a new Fork instance.
func newFork(baseBlock *Block, leafBlock *Block, parentFork *Fork) *Fork {
	fork := &Fork{
		forkId:     getForkKey(baseBlock.Root, leafBlock.Root),
		baseSlot:   baseBlock.Slot,
		baseRoot:   baseBlock.Root,
		leafSlot:   leafBlock.Slot,
		leafRoot:   leafBlock.Root,
		parentFork: parentFork,
	}

	return fork
}

// newForkFromDb creates a new Fork instance from a database record.
func newForkFromDb(dbFork *dbtypes.Fork, cache *forkCache) *Fork {
	fork := &Fork{
		forkId:   ForkKey(dbFork.ForkId),
		baseSlot: phase0.Slot(dbFork.BaseSlot),
		baseRoot: phase0.Root(dbFork.BaseRoot),
		leafSlot: phase0.Slot(dbFork.LeafSlot),
		leafRoot: phase0.Root(dbFork.LeafRoot),
	}

	if dbFork.ParentFork != 0 {
		fork.parentFork = cache.getForkById(ForkKey(dbFork.ParentFork))
	}

	return fork
}

// toDbFork converts the Fork instance to a database record.
func (fork *Fork) toDbFork() *dbtypes.Fork {
	dbFork := &dbtypes.Fork{
		ForkId:   uint64(fork.forkId),
		BaseSlot: uint64(fork.baseSlot),
		BaseRoot: fork.baseRoot[:],
		LeafSlot: uint64(fork.leafSlot),
		LeafRoot: fork.leafRoot[:],
	}

	if fork.parentFork != nil {
		dbFork.ParentFork = uint64(fork.parentFork.forkId)
	}

	return dbFork
}
