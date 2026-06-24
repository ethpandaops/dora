package tiered

import (
	"context"

	"github.com/ethpandaops/dora/blockdb/types"
)

// AddEpochDuties stores duties write-through to S3 (primary) and the Pebble cache.
func (e *TieredEngine) AddEpochDuties(ctx context.Context, duties *types.EpochDuties) (int64, error) {
	size, err := e.primary.AddEpochDuties(ctx, duties)
	if err != nil {
		return 0, err
	}

	if _, cerr := e.cache.AddEpochDuties(ctx, duties); cerr != nil {
		e.logger.Debugf("failed to cache duties: %v", cerr)
	}

	return size, nil
}

// GetEpochDuties retrieves the full duties, checking the cache first.
func (e *TieredEngine) GetEpochDuties(ctx context.Context, firstSlot uint64) (*types.EpochDuties, error) {
	if d, err := e.cache.GetEpochDuties(ctx, firstSlot); err == nil && d != nil {
		return d, nil
	}

	d, err := e.primary.GetEpochDuties(ctx, firstSlot)
	if err != nil {
		return nil, err
	}
	if d != nil {
		if _, cerr := e.cache.AddEpochDuties(ctx, d); cerr != nil {
			e.logger.Debugf("failed to cache duties: %v", cerr)
		}
	}
	return d, nil
}

// GetSlotCommittees reads a slot's committees, checking the cache first and
// populating it from S3 on a miss.
func (e *TieredEngine) GetSlotCommittees(ctx context.Context, firstSlot uint64, slot uint64) ([][]uint64, error) {
	if c, err := e.cache.GetSlotCommittees(ctx, firstSlot, slot); err == nil && c != nil {
		return c, nil
	}

	c, err := e.primary.GetSlotCommittees(ctx, firstSlot, slot)
	if err != nil {
		return nil, err
	}
	if c != nil {
		e.populateCache(ctx, firstSlot)
	}
	return c, nil
}

// GetSlotPtc reads a slot's PTC, checking the cache first.
func (e *TieredEngine) GetSlotPtc(ctx context.Context, firstSlot uint64, slot uint64) ([]uint64, error) {
	if p, err := e.cache.GetSlotPtc(ctx, firstSlot, slot); err == nil && p != nil {
		return p, nil
	}
	return e.primary.GetSlotPtc(ctx, firstSlot, slot)
}

// populateCache fetches the full epoch duties from S3 and stores them in the
// cache so subsequent per-slot reads are served locally.
func (e *TieredEngine) populateCache(ctx context.Context, firstSlot uint64) {
	if ok, _ := e.cache.HasEpochDuties(ctx, firstSlot); ok {
		return
	}
	d, err := e.primary.GetEpochDuties(ctx, firstSlot)
	if err != nil || d == nil {
		return
	}
	if _, cerr := e.cache.AddEpochDuties(ctx, d); cerr != nil {
		e.logger.Debugf("failed to cache duties: %v", cerr)
	}
}

// HasEpochDuties checks the cache first, then S3.
func (e *TieredEngine) HasEpochDuties(ctx context.Context, firstSlot uint64) (bool, error) {
	if ok, err := e.cache.HasEpochDuties(ctx, firstSlot); err == nil && ok {
		return true, nil
	}
	return e.primary.HasEpochDuties(ctx, firstSlot)
}

// PruneEpochDutiesBefore prunes duties from both tiers.
func (e *TieredEngine) PruneEpochDutiesBefore(ctx context.Context, maxFirstSlot uint64) (int64, error) {
	count, err := e.primary.PruneEpochDutiesBefore(ctx, maxFirstSlot)
	if _, cerr := e.cache.PruneEpochDutiesBefore(ctx, maxFirstSlot); cerr != nil {
		e.logger.Debugf("failed to prune cached duties: %v", cerr)
	}
	return count, err
}
