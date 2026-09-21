package handlers

import (
	"bytes"
	"context"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// bidSeenKey identifies a bid by the tuple the slots tables know: slot,
// parent root and builder index. The block hash is kept separately because
// the slots table only has it as eth_block_hash (missing for missed payloads).
type bidSeenKey struct {
	slot         uint64
	parentRoot   string
	builderIndex int64
}

type bidSeenEntry struct {
	blockHash []byte
	seenCount uint32
	seenTotal uint32
}

// getBidSeenInfo merges the gossip observation counters of all bids in the given
// slot window (cached not-yet-flushed bids + persisted rows) into a lookup keyed
// by (slot, parentRoot, builderIndex). Used to classify a slot's winning bid as
// in-protocol (observed on gossip) vs out-of-protocol (builder API/relay only).
func getBidSeenInfo(ctx context.Context, firstSlot uint64, pageSize uint64) map[bidSeenKey][]*bidSeenEntry {
	indexer := services.GlobalBeaconService.GetBeaconIndexer()
	if indexer == nil {
		return nil
	}

	lastSlot := firstSlot
	minSlot := uint64(0)
	if firstSlot+1 > pageSize {
		minSlot = firstSlot - pageSize + 1
	}

	bids := indexer.GetCachedBidsForSlotRange(phase0.Slot(minSlot), phase0.Slot(lastSlot))
	bids = append(bids, db.GetBidsForSlotRange(ctx, minSlot, lastSlot)...)

	return mergeBidSeenBids(bids)
}

// getBidSeenInfoForSlots merges gossip observation counters for an arbitrary set of slots
// (e.g. the non-contiguous result of a filtered slots query).
func getBidSeenInfoForSlots(ctx context.Context, slots []uint64) map[bidSeenKey][]*bidSeenEntry {
	indexer := services.GlobalBeaconService.GetBeaconIndexer()
	if indexer == nil {
		return nil
	}

	var bids []*dbtypes.BlockBid
	for _, slot := range slots {
		bids = append(bids, indexer.GetCachedBidsForSlotRange(phase0.Slot(slot), phase0.Slot(slot))...)
	}
	bids = append(bids, db.GetBidsForSlots(ctx, slots)...)

	return mergeBidSeenBids(bids)
}

// mergeBidSeenBids builds the bidSeenKey lookup, max-merging duplicate bid entries
// (same bid can come from the cache and the DB).
func mergeBidSeenBids(bids []*dbtypes.BlockBid) map[bidSeenKey][]*bidSeenEntry {
	bidMap := make(map[bidSeenKey][]*bidSeenEntry)
	for _, bid := range bids {
		key := bidSeenKey{
			slot:         bid.Slot,
			parentRoot:   string(bid.ParentRoot),
			builderIndex: bid.BuilderIndex,
		}

		entries := bidMap[key]
		duplicate := false
		for _, entry := range entries {
			if bytes.Equal(entry.blockHash, bid.BlockHash) {
				// same bid from cache and DB: keep the higher seen counters
				if bid.SeenCount > entry.seenCount {
					entry.seenCount = bid.SeenCount
				}
				if bid.SeenTotal > entry.seenTotal {
					entry.seenTotal = bid.SeenTotal
				}
				duplicate = true
				break
			}
		}
		if !duplicate {
			bidMap[key] = append(entries, &bidSeenEntry{
				blockHash: bid.BlockHash,
				seenCount: bid.SeenCount,
				seenTotal: bid.SeenTotal,
			})
		}
	}

	return bidMap
}

// matchBidSeen returns the gossip observation counters of the bid matching the
// given slot tuple, preferring an exact execution block hash match.
func matchBidSeen(bidSeenMap map[bidSeenKey][]*bidSeenEntry, slot uint64, parentRoot []byte, builderIndex int64, blockHash []byte) (uint32, uint32) {
	entries := bidSeenMap[bidSeenKey{slot, string(parentRoot), builderIndex}]
	var fallback *bidSeenEntry
	for _, entry := range entries {
		if blockHash != nil && len(entry.blockHash) > 0 && bytes.Equal(entry.blockHash, blockHash) {
			return entry.seenCount, entry.seenTotal
		}
		if fallback == nil {
			fallback = entry
		}
	}
	if fallback != nil {
		return fallback.seenCount, fallback.seenTotal
	}
	return 0, 0
}
