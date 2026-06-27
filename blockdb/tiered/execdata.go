package tiered

import (
	"context"

	"github.com/ethpandaops/dora/blockdb/types"
)

// AddExecData stores execution data write-through: S3 (primary, authoritative)
// then the Pebble cache (best-effort). Returns the authoritative stored size.
func (e *TieredEngine) AddExecData(ctx context.Context, slot uint64, blockRoot []byte, data []byte) (int64, error) {
	size, err := e.primary.AddExecData(ctx, slot, blockRoot, data)
	if err != nil {
		return 0, err
	}

	if _, cerr := e.cache.AddExecData(ctx, slot, blockRoot, data); cerr != nil {
		e.logger.Debugf("failed to cache exec data: %v", cerr)
	} else {
		e.cleanup.RecordExecAccess(slot, blockRoot)
	}

	return size, nil
}

// GetExecData returns the full DXTX blob, checking the cache first and
// populating it from S3 on a miss.
func (e *TieredEngine) GetExecData(ctx context.Context, slot uint64, blockRoot []byte) ([]byte, error) {
	if cached, err := e.cache.HasExecData(ctx, slot, blockRoot); err == nil && cached {
		e.recordTierRead(true)
		e.cleanup.RecordExecAccess(slot, blockRoot)
		return e.cache.GetExecData(ctx, slot, blockRoot)
	}
	e.recordTierRead(false)

	data, err := e.primary.GetExecData(ctx, slot, blockRoot)
	if err != nil {
		return nil, err
	}
	if data != nil {
		if _, cerr := e.cache.AddExecData(ctx, slot, blockRoot, data); cerr != nil {
			e.logger.Debugf("failed to cache exec data: %v", cerr)
		} else {
			e.cleanup.RecordExecAccess(slot, blockRoot)
		}
	}
	return data, nil
}

// GetExecDataRange returns a byte range of the DXTX blob, serving from the cache
// when the object is cached and falling back to S3 range reads otherwise.
func (e *TieredEngine) GetExecDataRange(ctx context.Context, slot uint64, blockRoot []byte, offset int64, length int64) ([]byte, error) {
	if cached, err := e.cache.HasExecData(ctx, slot, blockRoot); err == nil && cached {
		e.recordTierRead(true)
		e.cleanup.RecordExecAccess(slot, blockRoot)
		return e.cache.GetExecDataRange(ctx, slot, blockRoot, offset, length)
	}
	e.recordTierRead(false)
	return e.primary.GetExecDataRange(ctx, slot, blockRoot, offset, length)
}

// GetExecDataTxSections returns the requested per-tx sections, serving from the
// cache when the object is cached and falling back to S3's efficient range
// reads otherwise. A single-tx view is not worth pulling the full blob to cache.
func (e *TieredEngine) GetExecDataTxSections(ctx context.Context, slot uint64, blockRoot []byte, txHash []byte, sections uint32) (*types.ExecDataTxSections, error) {
	if cached, err := e.cache.HasExecData(ctx, slot, blockRoot); err == nil && cached {
		e.recordTierRead(true)
		e.cleanup.RecordExecAccess(slot, blockRoot)
		return e.cache.GetExecDataTxSections(ctx, slot, blockRoot, txHash, sections)
	}
	e.recordTierRead(false)
	return e.primary.GetExecDataTxSections(ctx, slot, blockRoot, txHash, sections)
}

// HasExecData checks the cache first, then S3.
func (e *TieredEngine) HasExecData(ctx context.Context, slot uint64, blockRoot []byte) (bool, error) {
	if ok, err := e.cache.HasExecData(ctx, slot, blockRoot); err == nil && ok {
		return true, nil
	}
	return e.primary.HasExecData(ctx, slot, blockRoot)
}

// DeleteExecData removes execution data from both tiers.
func (e *TieredEngine) DeleteExecData(ctx context.Context, slot uint64, blockRoot []byte) error {
	if cerr := e.cache.DeleteExecData(ctx, slot, blockRoot); cerr != nil {
		e.logger.Debugf("failed to delete cached exec data: %v", cerr)
	}
	return e.primary.DeleteExecData(ctx, slot, blockRoot)
}

// PruneExecDataBefore prunes execution data from both tiers (authoritative
// retention, distinct from cache eviction). Returns the primary's count.
func (e *TieredEngine) PruneExecDataBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	count, err := e.primary.PruneExecDataBefore(ctx, maxSlot)
	if _, cerr := e.cache.PruneExecDataBefore(ctx, maxSlot); cerr != nil {
		e.logger.Debugf("failed to prune cached exec data: %v", cerr)
	}
	return count, err
}
