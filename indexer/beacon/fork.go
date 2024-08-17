package beacon

import (
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
)

// ForkKey represents a key used for indexing forks.
type ForkKey uint64

// Fork represents a fork in the beacon chain.
type Fork struct {
	forkId     ForkKey     // Unique identifier for the fork.
	baseSlot   phase0.Slot // Slot of the base block.
	baseRoot   phase0.Root // Root of the base block.
	leafSlot   phase0.Slot // Slot of the leaf block.
	leafRoot   phase0.Root // Root of the leaf block.
	parentFork ForkKey     // Parent fork.

	headBlock *Block // Block at the head of the fork (in case it's known).
}

// newFork creates a new Fork instance.
func newFork(forkId ForkKey, baseBlock *Block, leafBlock *Block, parentFork ForkKey) *Fork {
	fork := &Fork{
		forkId:     forkId,
		baseSlot:   baseBlock.Slot,
		baseRoot:   baseBlock.Root,
		leafSlot:   leafBlock.Slot,
		leafRoot:   leafBlock.Root,
		parentFork: parentFork,
		headBlock:  leafBlock,
	}

	return fork
}

// newForkFromDb creates a new Fork instance from a database record.
func newForkFromDb(dbFork *dbtypes.Fork) *Fork {
	fork := &Fork{
		forkId:   ForkKey(dbFork.ForkId),
		baseSlot: phase0.Slot(dbFork.BaseSlot),
		baseRoot: phase0.Root(dbFork.BaseRoot),
		leafSlot: phase0.Slot(dbFork.LeafSlot),
		leafRoot: phase0.Root(dbFork.LeafRoot),
	}

	if dbFork.ParentFork != 0 {
		fork.parentFork = ForkKey(dbFork.ParentFork)
	}

	return fork
}

// toDbFork converts the Fork instance to a database record.
func (fork *Fork) toDbFork() *dbtypes.Fork {
	dbFork := &dbtypes.Fork{
		ForkId:     uint64(fork.forkId),
		BaseSlot:   uint64(fork.baseSlot),
		BaseRoot:   fork.baseRoot[:],
		LeafSlot:   uint64(fork.leafSlot),
		LeafRoot:   fork.leafRoot[:],
		ParentFork: uint64(fork.parentFork),
	}

	return dbFork
}

func (fork *Fork) GetBase() (phase0.Slot, phase0.Root) {
	return fork.baseSlot, fork.baseRoot
}

func (fork *Fork) GetLeaf() (phase0.Slot, phase0.Root) {
	return fork.leafSlot, fork.leafRoot
}

func (fork *Fork) GetParent() ForkKey {
	return fork.parentFork
}
