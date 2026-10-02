package tiered

import (
	"context"

	"github.com/ethpandaops/dora/blockdb/types"
)

// Slot meta objects are rarely read (only when a user expands the seen-by details
// of a bid or opens the inclusion lists of a slot) and readers only fetch the
// sections they need, so they are not cached in the Pebble tier - all
// operations go straight to the S3 primary.

// AddSlotMeta stores the meta object for a slot in S3.
func (e *TieredEngine) AddSlotMeta(ctx context.Context, meta *types.SlotMeta) (int64, error) {
	return e.primary.AddSlotMeta(ctx, meta)
}

// GetSlotMeta retrieves the selected parts of the meta object for a slot from S3.
func (e *TieredEngine) GetSlotMeta(ctx context.Context, slot uint64, flags types.SlotMetaFlags) (*types.SlotMeta, error) {
	return e.primary.GetSlotMeta(ctx, slot, flags)
}

// PruneSlotMetaBefore prunes meta objects from S3.
func (e *TieredEngine) PruneSlotMetaBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	return e.primary.PruneSlotMetaBefore(ctx, maxSlot)
}
