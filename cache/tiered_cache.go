package cache

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/allegro/bigcache/v3"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/utils"
)

var modelDynSsz *dynssz.DynSsz = dynssz.NewDynSsz(map[string]any{}, dynssz.WithExtendedTypes())

// Tiered cache is a cache implementation combining a local & remote cache
type TieredCache struct {
	localGoCache   *bigcache.BigCache
	remoteCache    RemoteCache
	logger         logrus.FieldLogger
	sszCompatMap   map[reflect.Type]bool
	sszCompatMutex sync.RWMutex
}

var ErrCacheMiss error = errors.New("cache miss")

type RemoteCache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	SetBytes(ctx context.Context, key string, value []byte, expiration time.Duration) error
	SetString(ctx context.Context, key, value string, expiration time.Duration) error
	SetUint64(ctx context.Context, key string, value uint64, expiration time.Duration) error
	SetBool(ctx context.Context, key string, value bool, expiration time.Duration) error

	Get(ctx context.Context, key string, returnValue any) (any, error)
	GetBytes(ctx context.Context, key string) ([]byte, error)
	GetString(ctx context.Context, key string) (string, error)
	GetUint64(ctx context.Context, key string) (uint64, error)
	GetBool(ctx context.Context, key string) (bool, error)
}

func NewTieredCache(cacheSize int, redisAddress string, redisPrefix string, logger logrus.FieldLogger) (*TieredCache, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	var remoteCache RemoteCache
	if redisAddress != "" {
		var err error
		remoteCache, err = InitRedisCache(ctx, redisAddress, redisPrefix)
		if err != nil {
			logrus.WithError(err).Errorf("error initializing remote redis cache. address: %v", redisAddress)
			return nil, err
		}
	}

	cacheConfig := bigcache.DefaultConfig(24 * time.Hour)
	cacheConfig.HardMaxCacheSize = cacheSize
	cacheConfig.StatsEnabled = true
	cacheConfig.CleanWindow = 5 * time.Minute
	cacheConfig.Shards = 8
	cacheConfig.MaxEntrySize = 100 * 1024 // 100KB

	localCache, err := bigcache.New(context.Background(), cacheConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating local cache: %w", err)
	}

	return &TieredCache{
		remoteCache:  remoteCache,
		localGoCache: localCache,
		logger:       logger,
		sszCompatMap: make(map[reflect.Type]bool),
	}, nil
}

// pageKeyPrefix extracts the page type from a cache key by returning
// the portion before the first ':' separator (e.g. "slots" from "slots:123:456").
func pageKeyPrefix(key string) string {
	if before, _, ok := strings.Cut(key, ":"); ok {
		return before
	}
	return key
}

func (cache *TieredCache) isSszCompatible(value interface{}, key string) bool {
	if utils.Config.KillSwitch.DisableSSZPageCache {
		return false
	}

	valType := reflect.TypeOf(value)

	cache.sszCompatMutex.RLock()
	compat, ok := cache.sszCompatMap[valType]
	cache.sszCompatMutex.RUnlock()
	if ok {
		return compat
	}

	cache.sszCompatMutex.Lock()
	defer cache.sszCompatMutex.Unlock()

	// Re-check after acquiring write lock to avoid redundant validation.
	if compat, ok = cache.sszCompatMap[valType]; ok {
		return compat
	}

	err := modelDynSsz.ValidateType(valType)
	compat = err == nil
	cache.sszCompatMap[valType] = compat

	if err != nil {
		cache.logger.WithError(err).Warnf("page model not ssz compatible: %v (%v)", pageKeyPrefix(key), valType.Name())
	}

	return compat
}

func (cache *TieredCache) marshalValue(value *cachedValue, key string) ([]byte, error) {
	if cache.isSszCompatible(value.Value, key) {
		return value.MarshalSSZ()
	}
	return json.Marshal(value)
}

func (cache *TieredCache) unmarshalValue(data []byte, value *cachedValue, key string) error {
	if cache.isSszCompatible(value.Value, key) {
		err := value.UnmarshalSSZ(data)
		if err == nil {
			return nil
		}
	}
	return json.Unmarshal(data, value)
}

type cachedValue struct {
	Version uint64 `json:"i"`
	Timeout uint64 `json:"t"`
	Value   any    `json:"v" ssz-type:"custom"`
}

func (cv *cachedValue) MarshalSSZ() ([]byte, error) {
	size, err := modelDynSsz.SizeSSZ(cv.Value)
	if err != nil {
		return nil, err
	}

	dst := make([]byte, 0, 16+size)
	dst = binary.LittleEndian.AppendUint64(dst, cv.Version)
	dst = binary.LittleEndian.AppendUint64(dst, cv.Timeout)
	return modelDynSsz.MarshalSSZTo(cv.Value, dst)
}

func (cv *cachedValue) UnmarshalSSZ(buf []byte) error {
	cv.Version = binary.LittleEndian.Uint64(buf[0:8])
	cv.Timeout = binary.LittleEndian.Uint64(buf[8:16])
	return modelDynSsz.UnmarshalSSZ(cv.Value, buf[16:])
}

func (cache *TieredCache) Set(key string, value interface{}, expiration time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	cacheValue := cachedValue{
		Version: 1,
		Value:   value,
	}
	if expiration > 0 {
		cacheValue.Timeout = uint64(time.Now().Add(expiration).Unix())
	}

	cacheValueBytes, err := cache.marshalValue(&cacheValue, key)
	if err != nil {
		cacheType := reflect.TypeOf(value)
		cache.sszCompatMutex.Lock()
		cache.sszCompatMap[cacheType] = false
		cache.sszCompatMutex.Unlock()
		cache.logger.WithError(err).Warnf("page model not ssz compatible: %v (%v)", pageKeyPrefix(key), cacheType.Name())
		return err
	}

	cache.localGoCache.Set(key, cacheValueBytes)
	if cache.remoteCache != nil {
		return cache.remoteCache.SetBytes(ctx, key, cacheValueBytes, expiration)
	}
	return nil
}

func (cache *TieredCache) Get(key string, returnValue interface{}) (interface{}, error) {
	cacheValue := &cachedValue{
		Value: returnValue,
	}

	// try to retrieve the key from the local cache
	wanted, err := cache.localGoCache.Get(key)
	if err == nil {
		err = cache.unmarshalValue(wanted, cacheValue, key)
		if err != nil {
			utils.LogError(err, "error unmarshalling data for key", 0, map[string]interface{}{"key": key})
			return nil, err
		}

		// manual TTL check since bigcache uses a global LifeWindow
		if cacheValue.Timeout > 0 && cacheValue.Timeout < uint64(time.Now().Unix()) {
			cache.localGoCache.Delete(key)
		} else {
			return cacheValue.Value, nil
		}
	}

	if cache.remoteCache == nil {
		return nil, ErrCacheMiss
	}

	// retrieve the key from the remote cache
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	_, err = cache.remoteCache.Get(ctx, key, cacheValue)
	if err != nil {
		return nil, err
	}

	if cacheValue.Timeout == 0 || cacheValue.Timeout > uint64(time.Now().Add(2*time.Second).Unix()) {
		valueMarshal, err := cache.marshalValue(cacheValue, key)
		if err != nil {
			return nil, err
		}
		cache.localGoCache.Set(key, valueMarshal)
	}
	return cacheValue.Value, nil
}

// TieredCacheStats holds statistics about the tiered cache.
type TieredCacheStats struct {
	LocalEntryCount  int64
	LocalHitCount    int64
	LocalMissCount   int64
	LocalLookupCount int64
	LocalHitRate     float64
	RemoteEnabled    bool
	SSZEnabled       bool
	SSZCompatTypes   int
	SSZIncompatTypes int
}

// GetStats returns cache statistics.
func (cache *TieredCache) GetStats() *TieredCacheStats {
	bcStats := cache.localGoCache.Stats()
	lookups := bcStats.Hits + bcStats.Misses

	var hitRate float64
	if lookups > 0 {
		hitRate = float64(bcStats.Hits) / float64(lookups)
	}

	stats := &TieredCacheStats{
		LocalEntryCount:  int64(cache.localGoCache.Len()),
		LocalHitCount:    int64(bcStats.Hits),
		LocalMissCount:   int64(bcStats.Misses),
		LocalLookupCount: int64(lookups),
		LocalHitRate:     hitRate,
		RemoteEnabled:    cache.remoteCache != nil,
		SSZEnabled:       !utils.Config.KillSwitch.DisableSSZPageCache,
	}

	cache.sszCompatMutex.RLock()
	for _, compat := range cache.sszCompatMap {
		if compat {
			stats.SSZCompatTypes++
		} else {
			stats.SSZIncompatTypes++
		}
	}
	cache.sszCompatMutex.RUnlock()

	return stats
}

// PageTypeStats holds per-page-type cache statistics.
type PageTypeStats struct {
	PageType  string
	Count     int64
	TotalSize int64
	AvgSize   int64
}

// GetPageTypeStats iterates over all local cache entries and returns
// per-page-type (key prefix before first ':') statistics.
func (cache *TieredCache) GetPageTypeStats() []*PageTypeStats {
	type accumulator struct {
		count     int64
		totalSize int64
	}

	byType := make(map[string]*accumulator, 64)
	it := cache.localGoCache.Iterator()

	for it.SetNext() {
		entry, err := it.Value()
		if err != nil {
			continue
		}

		key := entry.Key()
		pageType := key
		if idx := strings.IndexByte(key, ':'); idx >= 0 {
			pageType = key[:idx]
		}

		acc, ok := byType[pageType]
		if !ok {
			acc = &accumulator{}
			byType[pageType] = acc
		}

		acc.count++
		acc.totalSize += int64(len(entry.Value()))
	}

	results := make([]*PageTypeStats, 0, len(byType))
	for pt, acc := range byType {
		avg := int64(0)
		if acc.count > 0 {
			avg = acc.totalSize / acc.count
		}
		results = append(results, &PageTypeStats{
			PageType:  pt,
			Count:     acc.count,
			TotalSize: acc.totalSize,
			AvgSize:   avg,
		})
	}

	return results
}
