package tiered

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb/pebble"
	"github.com/ethpandaops/dora/blockdb/s3"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

// TieredEngine combines Pebble (cache) and S3 (primary storage) in a tiered architecture.
// Reads check cache first, then fall back to S3.
// Writes go to both (write-through).
type TieredEngine struct {
	cache   *pebble.PebbleEngine
	primary *s3.S3Engine
	cleanup *pebble.CacheCleanup
	logger  logrus.FieldLogger

	// Tier-level read counters: how often an object read was served from the
	// Pebble cache vs. fell through to S3.
	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
}

// GetCache returns the Pebble cache tier (for metrics/debug).
func (e *TieredEngine) GetCache() *pebble.PebbleEngine { return e.cache }

// GetPrimary returns the S3 primary tier (for metrics/debug).
func (e *TieredEngine) GetPrimary() *s3.S3Engine { return e.primary }

// TierStats returns the tier-level cache hit/miss counts.
func (e *TieredEngine) TierStats() (hits, misses int64) {
	return e.cacheHits.Load(), e.cacheMisses.Load()
}

func (e *TieredEngine) recordTierRead(hit bool) {
	if hit {
		e.cacheHits.Add(1)
	} else {
		e.cacheMisses.Add(1)
	}
}

// Compile-time guarantees that the tiered engine exposes every capability of
// its underlying tiers (block data, exec data, duties).
var (
	_ types.BlockDbEngine  = (*TieredEngine)(nil)
	_ types.ExecDataEngine = (*TieredEngine)(nil)
	_ types.DutiesEngine   = (*TieredEngine)(nil)
	_ types.TxHashIndex    = (*TieredEngine)(nil)
)

// SetTimeToSlotFn installs the time->slot resolver used by the Pebble cache's
// age-based eviction of slot-keyed namespaces (exec data, duties).
func (e *TieredEngine) SetTimeToSlotFn(fn func(t time.Time) uint64) {
	e.cleanup.SetTimeToSlotFn(fn)
}

// NewTieredEngine creates a new tiered storage engine from the shared pebble
// (cache) and s3 (primary) configs.
func NewTieredEngine(pebbleConfig dtypes.PebbleBlockDBConfig, s3Config dtypes.S3BlockDBConfig, logger logrus.FieldLogger) (types.BlockDbEngine, error) {
	// Initialize Pebble cache
	cacheEngine, err := pebble.NewPebbleEngine(pebbleConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize pebble cache: %w", err)
	}

	pebbleEngine, ok := cacheEngine.(*pebble.PebbleEngine)
	if !ok {
		return nil, fmt.Errorf("unexpected pebble engine type")
	}

	// Initialize S3 primary storage
	primaryEngine, err := s3.NewS3Engine(s3Config)
	if err != nil {
		cacheEngine.Close()
		return nil, fmt.Errorf("failed to initialize s3 primary storage: %w", err)
	}

	s3Engine, ok := primaryEngine.(*s3.S3Engine)
	if !ok {
		cacheEngine.Close()
		return nil, fmt.Errorf("unexpected s3 engine type")
	}

	// Initialize cache cleanup (cache mode: Pebble is a cache backed by S3)
	cleanup := pebble.NewCacheCleanup(pebbleEngine, logger, true)
	cleanup.Start()

	return &TieredEngine{
		cache:   pebbleEngine,
		primary: s3Engine,
		cleanup: cleanup,
		logger:  logger.WithField("component", "tiered-blockdb"),
	}, nil
}

// Close closes both storage engines.
func (e *TieredEngine) Close() error {
	if e.cleanup != nil {
		e.cleanup.Stop()
	}

	var errs []error
	if err := e.cache.Close(); err != nil {
		errs = append(errs, fmt.Errorf("cache close: %w", err))
	}
	if err := e.primary.Close(); err != nil {
		errs = append(errs, fmt.Errorf("primary close: %w", err))
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// GetStoredComponents returns which components exist for a block.
// Checks cache first, then S3.
func (e *TieredEngine) GetStoredComponents(ctx context.Context, slot uint64, root []byte) (types.BlockDataFlags, error) {
	// Check cache first
	cacheFlags, err := e.cache.GetStoredComponents(ctx, slot, root)
	if err != nil {
		e.logger.Debugf("cache GetStoredComponents error: %v", err)
	}

	// If cache has all components, return early
	if cacheFlags == types.BlockDataFlagAll {
		return cacheFlags, nil
	}

	// Check S3 for additional components
	s3Flags, err := e.primary.GetStoredComponents(ctx, slot, root)
	if err != nil {
		return cacheFlags, nil // Return cache result on S3 error
	}

	return cacheFlags | s3Flags, nil
}

// GetBlock retrieves block data with selective loading.
// Checks cache first, fetches missing components from S3.
func (e *TieredEngine) GetBlock(
	ctx context.Context,
	slot uint64,
	root []byte,
	flags types.BlockDataFlags,
	parseBlock func(uint64, []byte) (any, error),
	parsePayload func(uint64, []byte) (any, error),
) (*types.BlockData, error) {
	// Check what's in cache
	cacheFlags, _ := e.cache.GetStoredComponents(ctx, slot, root)

	// Determine what we can get from cache vs S3
	cacheRequestFlags := flags & cacheFlags
	s3RequestFlags := flags &^ cacheFlags

	if flags != 0 {
		e.recordTierRead(s3RequestFlags == 0) // hit only if everything requested was cached
	}

	result := &types.BlockData{}

	// Get from cache
	if cacheRequestFlags != 0 {
		cacheData, err := e.cache.GetBlock(ctx, slot, root, cacheRequestFlags, parseBlock, parsePayload)
		if err != nil {
			e.logger.Debugf("cache GetBlock error: %v", err)
		} else if cacheData != nil {
			mergeBlockDataInto(result, cacheData)

			// Record LRU access
			if e.cleanup != nil {
				e.cleanup.RecordAccess(root, cacheRequestFlags)
			}
		}
	}

	// Get missing components from S3
	if s3RequestFlags != 0 {
		s3Data, err := e.primary.GetBlock(ctx, slot, root, s3RequestFlags, parseBlock, parsePayload)
		if err != nil {
			e.logger.Debugf("s3 GetBlock error: %v", err)
		} else if s3Data != nil {
			mergeBlockDataInto(result, s3Data)

			// Cache the S3 data for future reads
			e.cacheS3Data(ctx, slot, root, s3Data, s3RequestFlags)
		}
	}

	return result, nil
}

// cacheS3Data stores S3 data in the cache for future reads.
func (e *TieredEngine) cacheS3Data(ctx context.Context, slot uint64, root []byte, data *types.BlockData, flags types.BlockDataFlags) {
	// Build cache data with only the components we fetched from S3
	cacheData := &types.BlockData{}

	if flags.Has(types.BlockDataFlagHeader) && len(data.HeaderData) > 0 {
		cacheData.HeaderVersion = data.HeaderVersion
		cacheData.HeaderData = data.HeaderData
	}
	if flags.Has(types.BlockDataFlagBody) && len(data.BodyData) > 0 {
		cacheData.BodyVersion = data.BodyVersion
		cacheData.BodyData = data.BodyData
	}
	if flags.Has(types.BlockDataFlagPayload) && len(data.PayloadData) > 0 {
		cacheData.PayloadVersion = data.PayloadVersion
		cacheData.PayloadData = data.PayloadData
	}
	if flags.Has(types.BlockDataFlagBal) && len(data.BalData) > 0 {
		cacheData.BalVersion = data.BalVersion
		cacheData.BalData = data.BalData
	}

	// Add to cache (ignore errors - caching is best effort)
	_, _, err := e.cache.AddBlock(ctx, slot, root, func() (*types.BlockData, error) {
		return cacheData, nil
	})
	if err != nil {
		e.logger.Debugf("failed to cache S3 data: %v", err)
	}

	// Flush LRU updates since we did a write
	if e.cleanup != nil {
		e.cleanup.FlushLRU()
	}
}

// AddBlock stores block data using write-through to both cache and S3.
// Returns (added, updated, error).
func (e *TieredEngine) AddBlock(
	ctx context.Context,
	slot uint64,
	root []byte,
	dataCb func() (*types.BlockData, error),
) (bool, bool, error) {
	// Get the data once
	data, err := dataCb()
	if err != nil {
		return false, false, err
	}

	// Check what components already exist (in cache or S3)
	existingFlags, _ := e.GetStoredComponents(ctx, slot, root)

	// Determine what new data provides
	newFlags := types.StoredFlagsFromBlockData(data)

	// Check if we need to update
	needsUpdate := (newFlags &^ existingFlags) != 0
	isNew := existingFlags == 0

	if !isNew && !needsUpdate {
		return false, false, nil
	}

	// Write-through: write to S3 first (primary), then cache
	// S3 handles merging with existing data
	s3Added, s3Updated, err := e.primary.AddBlock(ctx, slot, root, func() (*types.BlockData, error) {
		return data, nil
	})
	if err != nil {
		return false, false, fmt.Errorf("failed to write to S3: %w", err)
	}

	// Write to cache
	_, _, err = e.cache.AddBlock(ctx, slot, root, func() (*types.BlockData, error) {
		return data, nil
	})
	if err != nil {
		e.logger.Warnf("failed to write to cache: %v", err)
		// Don't fail - S3 write succeeded
	}

	// Flush LRU updates after write
	if e.cleanup != nil {
		e.cleanup.FlushLRU()
	}

	return s3Added, s3Updated, nil
}

// mergeBlockDataInto merges source data into target (source values take precedence for non-empty fields).
func mergeBlockDataInto(target, source *types.BlockData) {
	if source.HeaderVersion != 0 || len(source.HeaderData) > 0 {
		target.HeaderVersion = source.HeaderVersion
		target.HeaderData = source.HeaderData
	}
	if source.BodyVersion != 0 || len(source.BodyData) > 0 {
		target.BodyVersion = source.BodyVersion
		target.BodyData = source.BodyData
		target.Body = source.Body
	}
	if source.PayloadVersion != 0 || len(source.PayloadData) > 0 {
		target.PayloadVersion = source.PayloadVersion
		target.PayloadData = source.PayloadData
		target.Payload = source.Payload
	}
	if source.BalVersion != 0 || len(source.BalData) > 0 {
		target.BalVersion = source.BalVersion
		target.BalData = source.BalData
	}
}
