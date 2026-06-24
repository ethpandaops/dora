package beacon

import (
	"strconv"
	"sync"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/syndtr/goleveldb/leveldb"
)

// pubkeyCacheBuilderPrefix namespaces builder pubkey keys in the shared leveldb store so
// they do not collide with validator keys (which are stored unprefixed for backwards compat).
const pubkeyCacheBuilderPrefix = "b:"

type pubkeyCache struct {
	pubkeyDb    *leveldb.DB
	pubkeyMap   map[phase0.BLSPubKey]phase0.ValidatorIndex
	pubkeyMutex sync.RWMutex // mutex to protect pubkeyMap for concurrent access
	keyPrefix   []byte       // optional prefix for leveldb keys to namespace entries (e.g. builders)
	ownsDb      bool         // whether this cache owns (and should close) the leveldb handle
}

// newPubkeyCache creates a new cache for validator public keys.
func newPubkeyCache(indexer *Indexer, cacheFile string) *pubkeyCache {
	cache := &pubkeyCache{}

	if cacheFile != "" {
		db, err := leveldb.OpenFile(cacheFile, nil)
		if err != nil {
			indexer.logger.WithError(err).Error("failed to open pubkey cache")
		} else {
			cache.pubkeyDb = db
			cache.ownsDb = true
		}
	}

	if cache.pubkeyDb == nil {
		cache.pubkeyMap = make(map[phase0.BLSPubKey]phase0.ValidatorIndex)
	}

	return cache
}

// newPrefixedCache returns a pubkey cache that shares this cache's leveldb store (if any)
// but namespaces its keys with the given prefix, so a separate index space (e.g. builders,
// EIP-8282) can coexist with validators in the same file without key collisions. When there
// is no backing file it gets its own in-memory map instead.
func (c *pubkeyCache) newPrefixedCache(prefix []byte) *pubkeyCache {
	sub := &pubkeyCache{
		pubkeyDb:  c.pubkeyDb,
		keyPrefix: prefix,
	}
	if sub.pubkeyDb == nil {
		sub.pubkeyMap = make(map[phase0.BLSPubKey]phase0.ValidatorIndex)
	}
	return sub
}

// dbKey builds the leveldb key for a pubkey, applying the optional namespace prefix.
func (c *pubkeyCache) dbKey(pubkey phase0.BLSPubKey) []byte {
	if len(c.keyPrefix) == 0 {
		return pubkey[:]
	}
	key := make([]byte, 0, len(c.keyPrefix)+len(pubkey))
	key = append(key, c.keyPrefix...)
	key = append(key, pubkey[:]...)
	return key
}

func (c *pubkeyCache) Add(pubkey phase0.BLSPubKey, index phase0.ValidatorIndex) error {
	c.pubkeyMutex.Lock()
	defer c.pubkeyMutex.Unlock()

	if c.pubkeyDb != nil {
		indexStr := strconv.FormatUint(uint64(index), 10)
		err := c.pubkeyDb.Put(c.dbKey(pubkey), []byte(indexStr), nil)
		if err != nil {
			return err
		}
	} else {
		c.pubkeyMap[pubkey] = index
	}

	return nil
}

func (c *pubkeyCache) Get(pubkey phase0.BLSPubKey) (phase0.ValidatorIndex, bool) {
	if c.pubkeyDb != nil {
		data, err := c.pubkeyDb.Get(c.dbKey(pubkey), nil)
		if err != nil {
			return 0, false
		}

		index, err := strconv.ParseUint(string(data), 10, 64)
		if err != nil {
			return 0, false
		}

		return phase0.ValidatorIndex(index), true
	} else {
		c.pubkeyMutex.RLock()
		defer c.pubkeyMutex.RUnlock()

		index, exists := c.pubkeyMap[pubkey]
		return index, exists
	}
}

func (c *pubkeyCache) Close() error {
	if c.pubkeyDb != nil && c.ownsDb {
		return c.pubkeyDb.Close()
	}
	return nil
}
