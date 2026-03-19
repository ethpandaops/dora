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

	"github.com/coocood/freecache"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/utils"
)

var modelDynSsz *dynssz.DynSsz = dynssz.NewDynSsz(map[string]any{}, dynssz.WithExtendedTypes())

// Tiered cache is a cache implementation combining a local & remote cache
type TieredCache struct {
	localGoCache   *freecache.Cache
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

	return &TieredCache{
		remoteCache:  remoteCache,
		localGoCache: freecache.NewCache(cacheSize * 1024 * 1024), // 100 MB
		logger:       logger,
		sszCompatMap: make(map[reflect.Type]bool),
	}, nil
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

	err := modelDynSsz.ValidateType(valType)
	compat = err == nil
	cache.sszCompatMap[valType] = compat

	if err != nil {
		keySplit := strings.Split(key, ":")
		cache.logger.WithError(err).Warnf("page model not ssz compatible: %v (%v)", keySplit[0], valType.Name())
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
		return err
	}

	cache.localGoCache.Set([]byte(key), cacheValueBytes, int(expiration.Seconds()))
	fmt.Println("set local cache", key, expiration.Seconds())
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
	wanted, err := cache.localGoCache.Get([]byte(key))
	fmt.Println("get local cache", key, err, len(wanted))
	if err == nil {
		err = cache.unmarshalValue(wanted, cacheValue, key)
		if err != nil {
			utils.LogError(err, "error unmarshalling data for key", 0, map[string]interface{}{"key": key})
			return nil, err
		}

		return cacheValue.Value, nil
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
		var timeout uint64
		if cacheValue.Timeout == 0 {
			timeout = 0
		} else {
			timeout = cacheValue.Timeout - uint64(time.Now().Unix())
		}
		cache.localGoCache.Set([]byte(key), valueMarshal, int(timeout))
	}
	return cacheValue.Value, nil
}

// TieredCacheStats holds statistics about the tiered cache.
type TieredCacheStats struct {
	LocalEntryCount        int64
	LocalHitCount          int64
	LocalMissCount         int64
	LocalLookupCount       int64
	LocalHitRate           float64
	LocalEvacuateCount     int64
	LocalExpiredCount      int64
	LocalOverwriteCount    int64
	LocalAverageAccessTime int64
	RemoteEnabled          bool
	SSZEnabled             bool
	SSZCompatTypes         int
	SSZIncompatTypes       int
}

// GetStats returns cache statistics.
func (cache *TieredCache) GetStats() *TieredCacheStats {
	stats := &TieredCacheStats{
		LocalEntryCount:        cache.localGoCache.EntryCount(),
		LocalHitCount:          cache.localGoCache.HitCount(),
		LocalMissCount:         cache.localGoCache.MissCount(),
		LocalLookupCount:       cache.localGoCache.LookupCount(),
		LocalHitRate:           cache.localGoCache.HitRate(),
		LocalEvacuateCount:     cache.localGoCache.EvacuateCount(),
		LocalExpiredCount:      cache.localGoCache.ExpiredCount(),
		LocalOverwriteCount:    cache.localGoCache.OverwriteCount(),
		LocalAverageAccessTime: cache.localGoCache.AverageAccessTime(),
		RemoteEnabled:          cache.remoteCache != nil,
		SSZEnabled:             !utils.Config.KillSwitch.DisableSSZPageCache,
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
	it := cache.localGoCache.NewIterator()

	for {
		entry := it.Next()
		if entry == nil {
			break
		}

		key := string(entry.Key)
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
		acc.totalSize += int64(len(entry.Value))
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
