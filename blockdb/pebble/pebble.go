package pebble

import (
	"encoding/binary"

	"github.com/cockroachdb/pebble"

	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

// Key namespaces. Every Pebble key begins with a 2-byte big-endian namespace
// prefix. Namespace 2 is shared by exec-data objects and the block-component LRU
// records; they never collide because their keys differ in length.
const (
	KeyNamespaceBlock       uint16 = 1 // block components: [ns1][root][blockType]
	KeyNamespaceExecData    uint16 = 2 // execution data (DXTX blobs): [ns2][slot][hash4]
	KeyNamespaceLRU         uint16 = 2 // block-component LRU records: [ns2][root]
	KeyNamespaceDuties      uint16 = 3 // per-epoch duties: [ns3][firstSlot][type][slotIdx]
	KeyNamespaceTxHash      uint16 = 4 // tx-hash index lookup: [ns4][prefix10][tx_uid8]
	KeyNamespaceTxHashPrune uint16 = 5 // tx-hash index prune-order: [ns5][tx_uid8]
	KeyNamespaceAccessTrack uint16 = 6 // exec/duties access records: [ns6][entityNs][slot][tail]
)

type PebbleEngine struct {
	db     *pebble.DB
	config dtypes.PebbleBlockDBConfig
}

func NewPebbleEngine(config dtypes.PebbleBlockDBConfig) (types.BlockDbEngine, error) {
	cache := pebble.NewCache(int64(config.CacheSize * 1024 * 1024))
	defer cache.Unref()

	db, err := pebble.Open(config.Path, &pebble.Options{
		Cache: cache,
	})
	if err != nil {
		return nil, err
	}

	return &PebbleEngine{
		db:     db,
		config: config,
	}, nil
}

// GetDB returns the underlying pebble database for metrics collection.
func (e *PebbleEngine) GetDB() *pebble.DB {
	return e.db
}

func (e *PebbleEngine) Close() error {
	return e.db.Close()
}

// GetConfig returns the engine configuration.
func (e *PebbleEngine) GetConfig() dtypes.PebbleBlockDBConfig {
	return e.config
}

// makeNamespaceRangeStart returns the range start key for a namespace.
func makeNamespaceRangeStart(ns uint16) []byte {
	key := make([]byte, 2)
	binary.BigEndian.PutUint16(key[0:2], ns)
	return key
}

// makeNamespaceSlotKey returns [namespace:2][slot:8] for range operations.
func makeNamespaceSlotKey(ns uint16, slot uint64) []byte {
	key := make([]byte, 10)
	binary.BigEndian.PutUint16(key[0:2], ns)
	binary.BigEndian.PutUint64(key[2:10], slot)
	return key
}

// copyHashPrefix copies up to n bytes from src to dst.
func copyHashPrefix(dst []byte, src []byte, n int) {
	l := min(len(src), n)
	copy(dst[:l], src[:l])
}
