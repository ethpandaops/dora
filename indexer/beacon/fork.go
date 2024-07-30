package beacon

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
)

type ForkKey uint64

func getForkKey(baseRoot phase0.Root, leafRoot phase0.Root) ForkKey {
	hashData := make([]byte, 64)
	copy(hashData[:32], baseRoot[:])
	copy(hashData[32:], leafRoot[:])
	hash := sha256.Sum256(hashData)

	keyBytes := hash[:8]
	keyBytes[0] &= 0x7F

	return ForkKey(binary.BigEndian.Uint64(keyBytes))
}

type Fork struct {
	forkId     ForkKey
	baseSlot   phase0.Slot
	baseRoot   phase0.Root
	leafSlot   phase0.Slot
	leafRoot   phase0.Root
	parentFork *Fork
}

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

func (fork *Fork) toDbFork() *dbtypes.Fork {
	return &dbtypes.Fork{
		ForkId:     uint64(fork.forkId),
		BaseSlot:   uint64(fork.baseSlot),
		BaseRoot:   fork.baseRoot[:],
		LeafSlot:   uint64(fork.leafSlot),
		LeafRoot:   fork.leafRoot[:],
		ParentFork: uint64(fork.parentFork.forkId),
	}
}
