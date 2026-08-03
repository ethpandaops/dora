package services

import (
	"bytes"
	"context"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/dbtypes"
)

// BidParentClass classifies which chain position an execution payload bid targets,
// relative to the parent chain of a reference block (usually the block that was
// actually proposed in the bid's slot).
type BidParentClass uint8

const (
	// BidParentClassUnknown means the bid's parent root could not be resolved to a known block.
	BidParentClassUnknown BidParentClass = iota
	// BidParentClassParent means the bid targets the reference block's beacon parent.
	BidParentClassParent
	// BidParentClassReorg means the bid targets a deeper ancestor of the reference block,
	// i.e. it proposes to orphan the block(s) between that ancestor and the reference block.
	BidParentClassReorg
	// BidParentClassOrphaned means the bid targets a known block outside the reference
	// block's parent chain (a competing fork that ended up orphaned).
	BidParentClassOrphaned
)

// String returns the wire/display identifier of the bid parent class.
func (c BidParentClass) String() string {
	switch c {
	case BidParentClassParent:
		return "parent"
	case BidParentClassReorg:
		return "reorg"
	case BidParentClassOrphaned:
		return "orphaned"
	default:
		return "unknown"
	}
}

// bidClassifyWindow is the number of slots preceding the bid slot that are inspected
// to resolve bid parent roots and parent payload hashes.
const bidClassifyWindow = 16

// ClassifiedBlockBid is a BlockBid annotated with the chain position its parent tuple
// (parent_block_root, parent_block_hash) targets.
type ClassifiedBlockBid struct {
	*dbtypes.BlockBid
	ParentClass BidParentClass
	// ReorgDepth is the number of blocks on the reference chain the bid would orphan
	// (0 for parent bids, 1 for grandparent/"reorg n-1" bids, ...).
	ReorgDepth uint64
	// ParentSlot is the slot of the targeted beacon parent block (valid if ParentKnown).
	ParentSlot  uint64
	ParentKnown bool
	// ParentFull indicates the bid builds on the targeted block's own payload (full parent).
	// False means it builds on an earlier payload, i.e. it treats the target's payload as
	// withheld/empty.
	ParentFull bool
	// ElParentSlot is the slot of the block whose payload block hash matches the bid's
	// parent hash, i.e. the EL head the bid builds on (valid if ElParentKnown).
	ElParentSlot  uint64
	ElParentKnown bool
	// ElParentUnrevealed indicates the bid's parent hash matches a committed payload hash
	// that was never revealed on the reference chain - such a bid can never become valid.
	ElParentUnrevealed bool
}

// GetSlotBidsClassified returns all execution payload bids for the given slot (regardless
// of their parent root) and classifies each bid's parent tuple against the parent chain of
// the reference block identified by parentRoot (the reference block's parent root).
// Bids targeting deeper ancestors (reorg bids) or competing forks are included and marked.
func (bs *ChainService) GetSlotBidsClassified(ctx context.Context, slot phase0.Slot, parentRoot phase0.Root) []*ClassifiedBlockBid {
	bids := bs.beaconIndexer.GetBlockBidsForSlot(slot)
	if len(bids) == 0 {
		return []*ClassifiedBlockBid{}
	}

	// Load all blocks (canonical + orphaned) in the window preceding the bid slot to
	// resolve the bids' parent roots and payload hashes against.
	firstSlot := uint64(0)
	if uint64(slot) > 0 {
		firstSlot = uint64(slot) - 1
	}
	ctxBlocks := bs.GetDbBlocksForSlots(ctx, firstSlot, bidClassifyWindow, false, true)

	blockByRoot := make(map[phase0.Root]*dbtypes.Slot, len(ctxBlocks))
	for _, block := range ctxBlocks {
		if len(block.Root) != 32 {
			continue
		}
		blockByRoot[phase0.Root(block.Root)] = block
	}

	// Walk the reference block's parent chain; index = reorg depth (0 = parent).
	ancestors := make([]*dbtypes.Slot, 0, bidClassifyWindow)
	ancestorDepth := make(map[phase0.Root]int, bidClassifyWindow)
	ancestorRoot := parentRoot
	for range bidClassifyWindow {
		block := blockByRoot[ancestorRoot]
		if block == nil {
			break
		}

		ancestorDepth[ancestorRoot] = len(ancestors)
		ancestors = append(ancestors, block)

		if len(block.ParentRoot) != 32 {
			break
		}
		ancestorRoot = phase0.Root(block.ParentRoot)
	}

	classifiedBids := make([]*ClassifiedBlockBid, 0, len(bids))
	for _, bid := range bids {
		classifiedBid := &ClassifiedBlockBid{BlockBid: bid}

		// Resolve the targeted beacon parent block.
		var targetBlock *dbtypes.Slot
		if len(bid.ParentRoot) == 32 {
			bidParentRoot := phase0.Root(bid.ParentRoot)
			if depth, isAncestor := ancestorDepth[bidParentRoot]; isAncestor {
				targetBlock = ancestors[depth]
				if depth == 0 {
					classifiedBid.ParentClass = BidParentClassParent
				} else {
					classifiedBid.ParentClass = BidParentClassReorg
					classifiedBid.ReorgDepth = uint64(depth)
				}
			} else if forkBlock := blockByRoot[bidParentRoot]; forkBlock != nil {
				targetBlock = forkBlock
				classifiedBid.ParentClass = BidParentClassOrphaned
			}
		}

		if targetBlock != nil {
			classifiedBid.ParentSlot = targetBlock.Slot
			classifiedBid.ParentKnown = true
			classifiedBid.ParentFull = len(targetBlock.EthBlockHash) > 0 &&
				bytes.Equal(bid.ParentHash, targetBlock.EthBlockHash)
		}

		// Resolve the EL parent: the reference-chain block whose payload block hash the
		// bid builds on. For payload-less blocks EthBlockHash holds the committed (never
		// revealed) bid hash, so a match there flags an invalid parent hash.
		for _, ancestor := range ancestors {
			if len(ancestor.EthBlockHash) == 0 || !bytes.Equal(bid.ParentHash, ancestor.EthBlockHash) {
				continue
			}

			classifiedBid.ElParentSlot = ancestor.Slot
			classifiedBid.ElParentKnown = true
			classifiedBid.ElParentUnrevealed = ancestor.PayloadStatus == dbtypes.PayloadStatusMissing
			break
		}
		if !classifiedBid.ElParentKnown && classifiedBid.ParentFull {
			// Fork-target bids build on the fork block's own payload.
			classifiedBid.ElParentSlot = targetBlock.Slot
			classifiedBid.ElParentKnown = true
			classifiedBid.ElParentUnrevealed = targetBlock.PayloadStatus == dbtypes.PayloadStatusMissing
		}

		classifiedBids = append(classifiedBids, classifiedBid)
	}

	return classifiedBids
}
