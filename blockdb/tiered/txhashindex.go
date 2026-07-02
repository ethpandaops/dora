package tiered

import (
	"context"

	"github.com/ethpandaops/dora/blockdb/types"
)

// The tx-hash index lives in the Pebble cache (namespaces 4/5), pinned: the
// cache cleanup never evicts those namespaces, and it is pruned only by the
// details retention via PruneTxHashBefore. All operations delegate to the cache.

func (e *TieredEngine) PutTxHashes(ctx context.Context, entries []types.TxHashEntry) error {
	return e.cache.PutTxHashes(ctx, entries)
}

func (e *TieredEngine) LookupTxHash(ctx context.Context, prefix []byte) ([]uint64, error) {
	return e.cache.LookupTxHash(ctx, prefix)
}

func (e *TieredEngine) LookupTxHashRange(ctx context.Context, lo, hi []byte) ([]uint64, error) {
	return e.cache.LookupTxHashRange(ctx, lo, hi)
}

func (e *TieredEngine) PruneTxHashBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	return e.cache.PruneTxHashBefore(ctx, maxSlot)
}

// EstimateTxHashIndexSize returns the index's approximate on-disk size in the
// pebble cache (where it is pinned).
func (e *TieredEngine) EstimateTxHashIndexSize() int64 {
	return e.cache.EstimateTxHashIndexSize()
}
