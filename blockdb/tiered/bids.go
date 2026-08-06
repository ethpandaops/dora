package tiered

import (
	"context"

	"github.com/ethpandaops/dora/blockdb/types"
)

// Bids objects are small and rarely read (only when a user expands the
// seen-by details of a bid), so they are not cached in the Pebble tier -
// all operations go straight to the S3 primary.

// AddSlotBids stores the bids object for a slot in S3.
func (e *TieredEngine) AddSlotBids(ctx context.Context, bids *types.SlotBids) (int64, error) {
	return e.primary.AddSlotBids(ctx, bids)
}

// GetSlotBids retrieves the bids object for a slot from S3.
func (e *TieredEngine) GetSlotBids(ctx context.Context, slot uint64) (*types.SlotBids, error) {
	return e.primary.GetSlotBids(ctx, slot)
}

// PruneSlotBidsBefore prunes bids objects from S3.
func (e *TieredEngine) PruneSlotBidsBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	return e.primary.PruneSlotBidsBefore(ctx, maxSlot)
}
